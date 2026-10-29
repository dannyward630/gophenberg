// SPDX-License-Identifier: Apache-2.0

package installer_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/gopherium/framework/gonsole"
	"github.com/gopherium/framework/gonsole/testkit"

	"github.com/gopherium/gophenberg/internal/installer"
	"github.com/gopherium/gophenberg/internal/version"
)

// callOf returns the call of a server:install run under the GOPHENBERG_ settings values hold.
func callOf(values map[string]string) gonsole.Call {
	return gonsole.Call{Env: gonsole.Env{Prefix: "GOPHENBERG_", Getenv: testkit.Getenv(values)}}
}

// wantError fails t unless err reads want.
func wantError(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil || err.Error() != want {
		t.Errorf("Install() = %v, want %q", err, want)
	}
}

// rootOwned describes a file as root's, whatever user the test runs as.
type rootOwned struct{ fs.FileInfo }

// Sys returns the system description of the file with root as its owner.
func (i rootOwned) Sys() any {
	stat := *i.FileInfo.Sys().(*syscall.Stat_t)
	stat.Uid = 0
	return &stat
}

// lstatAsRoot describes the file at path as os.Lstat does, with root as its owner.
func lstatAsRoot(path string) (fs.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	return rootOwned{info}, nil
}

// rootRunOn returns the machine whose top is the folder root, run by root and every file in it root's.
func rootRunOn(root string) installer.Machine {
	return installer.Machine{Root: root, Lstat: lstatAsRoot, Euid: func() int { return 0 }}
}

func TestInstallRefusesADomainThatIsNotAHostNameBeforeItReadsAFile(t *testing.T) {
	t.Parallel()

	err := installer.Install(t.Context(), rootRunOn(t.TempDir()), callOf(nil), installer.Request{
		Domain: "not a host name", DryRun: true,
	})

	wantError(t, err, `-domain: must be localhost or a host name such as example.com: labels of 1 to 63 lower-case `+
		`letters, digits and hyphens joined by dots, no hyphen at either end of a label, a letter in the last label, `+
		`253 characters at most, `+
		`got "not a host name"`)
}

func TestInstallRefusesAPasswordFileThatIsNotAnAbsolutePathBeforeItReadsAFile(t *testing.T) {
	t.Parallel()

	err := installer.Install(t.Context(), rootRunOn(t.TempDir()), callOf(nil), installer.Request{
		Domain: "example.com", AdminPasswordFile: "admin-password",
	})

	wantError(t, err, `-admin-password-file: must be an absolute path, got "admin-password"`)
}

func TestInstallRefusesAnInstallRootThatIsNotAbsoluteBeforeItChecksTheMachine(t *testing.T) {
	t.Parallel()

	call := callOf(map[string]string{"GOPHENBERG_INSTALL_ROOT": "planted/root"})

	err := installer.Install(t.Context(), rootRunOn(t.TempDir()), call, installer.Request{
		Domain: "example.com", DryRun: true,
	})

	wantError(t, err, `GOPHENBERG_INSTALL_ROOT: must be an absolute path, got "planted/root"`)
}

// recorded returns a machine whose install record holds body.
func recorded(t *testing.T, body string) installer.Machine {
	t.Helper()
	root := t.TempDir()
	record := filepath.Join(root, "var", "lib", "gophenberg-installer", "install.json")
	if err := os.MkdirAll(filepath.Dir(record), 0o700); err != nil {
		t.Fatalf("making the folder of the install record: %v", err)
	}
	if err := os.WriteFile(record, []byte(body), 0o600); err != nil {
		t.Fatalf("writing the install record: %v", err)
	}
	return rootRunOn(root)
}

func TestInstallRefusesAnInstallerOlderThanTheOneTheRecordNames(t *testing.T) {
	t.Parallel()

	machine := recorded(t, `{"installer": "1000.0.0"}`)

	err := installer.Install(t.Context(), machine, callOf(nil), installer.Request{Domain: "example.com", DryRun: true})

	wantError(t, err, "this installer is "+version.Version()+", and this server was set up by the 1000.0.0 "+
		"installer, so run the install.sh line again")
}

func TestInstallRefusesARecordThatIsNotJSON(t *testing.T) {
	t.Parallel()

	machine := recorded(t, `{"installer": "1000.0.0"`)

	err := installer.Install(t.Context(), machine, callOf(nil), installer.Request{Domain: "example.com", DryRun: true})

	wantError(t, err, "installer: /var/lib/gophenberg-installer/install.json is not an install record: "+
		"unexpected end of JSON input")
}

// lockedOut returns a machine an earlier run set up, the file at path from the top of the server locked by mode 0000.
func lockedOut(t *testing.T, path string) installer.Machine {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root reads a file of mode 0000")
	}
	machine := recorded(t, `{"installer": "0.0.1", "domain": "example.com"}`)
	settings := filepath.Join(machine.Root, "etc", "gophenberg", "server.env")
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatalf("making the folder of server.env: %v", err)
	}
	if err := os.WriteFile(settings, []byte("GOPHENBERG_PUBLIC_URL=https://example.com\n"), 0o600); err != nil {
		t.Fatalf("writing server.env: %v", err)
	}
	if err := os.Chmod(filepath.Join(machine.Root, path), 0); err != nil {
		t.Fatalf("locking %s: %v", path, err)
	}
	return machine
}

func TestInstallRefusesAPlainUserWithTheRootLineWhenAFileRootAloneMayReadIsLocked(t *testing.T) {
	t.Parallel()

	for _, path := range []string{"/etc/gophenberg/server.env", "/var/lib/gophenberg-installer/install.json"} {
		machine := lockedOut(t, path)
		machine.Euid = func() int { return 1000 }

		err := installer.Install(t.Context(), machine, callOf(nil), installer.Request{DryRun: true})

		wantError(t, err, "server:install must run as root, such as with sudo, so nothing was changed")
	}
}

func TestInstallRefusesAFileItMayNotReadAsRootWithTheReason(t *testing.T) {
	t.Parallel()

	for _, path := range []string{"/etc/gophenberg/server.env", "/var/lib/gophenberg-installer/install.json"} {
		machine := lockedOut(t, path)
		machine.Euid = func() int { return 0 }

		err := installer.Install(t.Context(), machine, callOf(nil), installer.Request{DryRun: true})

		wantError(t, err, "installer: open "+filepath.Join(machine.Root, path)+": permission denied")
	}
}

func TestInstallStopsARunThatIsNotRootsBeforeItReadsTheRecord(t *testing.T) {
	t.Parallel()

	machine := recorded(t, `{"installer": "1000.0.0"`)
	machine.Euid = func() int { return 1000 }

	err := installer.Install(t.Context(), machine, callOf(nil), installer.Request{Domain: "example.com", DryRun: true})

	wantError(t, err, "server:install must run as root, such as with sudo, so nothing was changed")
}
