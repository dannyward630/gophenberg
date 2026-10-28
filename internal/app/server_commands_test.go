// SPDX-License-Identifier: Apache-2.0

package app

import (
	"bytes"
	"errors"
	"flag"
	"maps"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gopherium/framework/gonsole"
	"github.com/gopherium/framework/gonsole/testkit"

	"github.com/gopherium/gophenberg/internal/installer"
)

// onMachine returns the gophenberg command line over env, its server commands reaching machine in place of this one.
func onMachine(env map[string]string, machine installer.Machine) gonsole.Program {
	program := Program(testkit.Getenv(env), noPlugins)
	kept := slices.DeleteFunc(slices.Clone(program.Commands), func(command gonsole.Command) bool {
		return strings.HasPrefix(command.Name, "server:")
	})
	program.Commands = append(kept, serverCommands(machine)...)
	return program
}

// stderrOf runs the built binary with args in folder under env and returns its error stream and its exit code.
func stderrOf(t *testing.T, binary string, env []string, folder string, args ...string) (string, int) {
	t.Helper()
	var stderr bytes.Buffer
	cmd := exec.CommandContext(t.Context(), binary, args...)
	cmd.Dir, cmd.Env, cmd.Stderr = folder, env, &stderr
	var exited *exec.ExitError
	if err := cmd.Run(); err != nil && !errors.As(err, &exited) {
		t.Fatalf("gophenberg %q: %v", args, err)
	}
	return stderr.String(), cmd.ProcessState.ExitCode()
}

func TestServerInstallOffersEveryFlagOfTheInstallLine(t *testing.T) {
	t.Parallel()

	got := testkit.Run(t, Program(testkit.Getenv(nil), noPlugins), "", "help", "server:install")

	want := strings.Join([]string{
		"install Gophenberg on this Linux server with PostgreSQL, Caddy and a systemd service",
		"",
		"Usage:",
		"  gophenberg server:install [flags]",
		"",
		"Flags:",
		"  -admin-email email",
		"    \tthe email of the first administrator, asked on the terminal when missing",
		"  -admin-name name",
		"    \tthe name of the first administrator, asked on the terminal when missing",
		"  -admin-password-file file",
		"    \tthe file whose first line is the first administrator's password, asked on the terminal when missing",
		"  -archive file",
		"    \tthe checked release file install.sh downloaded into /var/lib/gophenberg-installer, " +
			"not needed once current exists",
		"  -domain host",
		"    \tthe host name the site answers at, needed on the first run and kept in /etc/gophenberg/server.env",
		"  -dry-run",
		"    \trun every check, then print each step and every file it would write, and change nothing",
		"  -skip-domain-check",
		"    \tskip the check that the domain points at this server, for a server behind a proxy the check cannot cross",
		"  -web-server name",
		"    \tthe name of the web server in front of the site, caddy to have the install set Caddy up or none to keep " +
			"your own. Left out, a first run takes caddy and a later run keeps the one in /etc/gophenberg/server.env",
		"",
	}, "\n")
	if got.Code != gonsole.ExitDone || got.Stdout != want || got.Stderr != "" {
		t.Errorf("help server:install = %d, stdout %q, stderr %q, want 0 and\n%s", got.Code, got.Stdout, got.Stderr,
			want)
	}
}

func TestServerUninstallOffersDeleteDataAndYes(t *testing.T) {
	t.Parallel()

	got := testkit.Run(t, Program(testkit.Getenv(nil), noPlugins), "", "help", "server:uninstall")

	want := strings.Join([]string{
		"remove what server:install set up on this server, keeping the site's data unless -delete-data",
		"",
		"Usage:",
		"  gophenberg server:uninstall [flags]",
		"",
		"Flags:",
		"  -delete-data",
		"    \talso drop the database and its role, and remove the install root, /etc/gophenberg/server.env and the " +
			"system user gophenberg",
		"  -yes",
		"    \tapply the change, a dry run without it",
		"",
	}, "\n")
	if got.Code != gonsole.ExitDone || got.Stdout != want {
		t.Errorf("help server:uninstall = %d, stdout %q, want 0 and\n%s", got.Code, got.Stdout, want)
	}
	if got.Stderr != "" {
		t.Errorf("help server:uninstall noted %q, want nothing", got.Stderr)
	}
}

// recordedMachine returns a server root runs on whose install record names the PostgreSQL it installed and the user.
func recordedMachine(t *testing.T) installer.Machine {
	t.Helper()
	root := t.TempDir()
	folder := filepath.Join(root, "var/lib/gophenberg-installer")
	if err := os.MkdirAll(folder, 0o700); err != nil {
		t.Fatalf("making the installer's folder: %v", err)
	}
	record := []byte(`{"installer": "0.19.0", "installed": ["postgresql"]}` + "\n")
	if err := os.WriteFile(filepath.Join(folder, "install.json"), record, 0o600); err != nil {
		t.Fatalf("writing the install record: %v", err)
	}
	return installer.Machine{
		Root: root, Lstat: os.Lstat, Euid: func() int { return 0 },
		LookupUser: func(name string) (*user.User, error) { return &user.User{Username: name}, nil },
	}
}

func TestServerUninstallListsWhatItKeepsAndEndsWithTheDryRunLineWithoutYes(t *testing.T) {
	t.Parallel()

	got := testkit.Run(t, onMachine(nil, recordedMachine(t)), "", "server:uninstall")

	want := "server:uninstall finds nothing left to remove.\nserver:uninstall keeps:\n  the system user gophenberg\n" +
		"  /var/lib/gophenberg-installer/install.json, so a later install picks up what is kept\n" +
		"  the packages server:install installed, which sudo apt-get purge postgresql removes\n"
	if got.Code != gonsole.ExitDone || got.Stdout != want {
		t.Errorf("server:uninstall = %d, stdout %q, stderr %q, want 0 and\n%s", got.Code, got.Stdout, got.Stderr, want)
	}
	if line := "gophenberg: dry run, nothing changed, pass -yes to apply\n"; got.Stderr != line {
		t.Errorf("server:uninstall noted %q, want gonsole's dry run line %q", got.Stderr, line)
	}
}

func TestServerUninstallHandsDeleteDataToTheUninstall(t *testing.T) {
	t.Parallel()

	got := testkit.Run(t, onMachine(nil, recordedMachine(t)), "", "server:uninstall", "-delete-data")

	want := "server:uninstall removes:\n  the system user gophenberg\nserver:uninstall keeps:\n"
	if got.Code != gonsole.ExitDone || !strings.HasPrefix(got.Stdout, want) {
		t.Errorf("server:uninstall -delete-data = %d, stdout %q, stderr %q, want 0 and the user removed", got.Code,
			got.Stdout, got.Stderr)
	}
}

func TestServerUninstallRefusesAPlainUser(t *testing.T) {
	t.Parallel()

	machine := recordedMachine(t)
	machine.Euid = func() int { return 1000 }

	got := testkit.Run(t, onMachine(nil, machine), "", "server:uninstall", "-yes")

	wantAnswer(t, got, gonsole.ExitFailed,
		"gophenberg: server:uninstall must run as root, such as with sudo, so nothing was changed")
}

func TestServerInstallHandsTheInstallerEveryFlagTheLineSets(t *testing.T) {
	t.Parallel()

	flags := flag.NewFlagSet("server:install", flag.ContinueOnError)
	installFlags(flags)
	err := flags.Parse([]string{
		"-domain", "example.com", "-archive", "/var/lib/gophenberg-installer/gophenberg_0.20.0_linux_amd64.tar.gz",
		"-admin-email", "admin@example.com", "-admin-name", "Maria Perez", "-admin-password-file", "/root/password",
		"-web-server", "none", "-skip-domain-check", "-dry-run",
	})
	if err != nil {
		t.Fatalf("parsing the install line: %v", err)
	}
	given := map[string]string{}
	flags.Visit(func(set *flag.Flag) { given[set.Name] = set.Value.String() })

	request := requestOf(given)

	want := installer.Request{
		Domain: "example.com", Archive: "/var/lib/gophenberg-installer/gophenberg_0.20.0_linux_amd64.tar.gz",
		AdminEmail: "admin@example.com", AdminName: "Maria Perez", AdminPasswordFile: "/root/password",
		WebServer: "none", SkipDomainCheck: true, DryRun: true,
	}
	if request != want {
		t.Errorf("requestOf() = %+v, want %+v", request, want)
	}
}

func TestServerInstallLeavesTheWebServerToTheInstallerWhenTheLineNamesNone(t *testing.T) {
	t.Parallel()

	if request := requestOf(map[string]string{"domain": "example.com"}); request.WebServer != "" {
		t.Errorf("requestOf() names the web server %q, want none, so server.env keeps the one it names",
			request.WebServer)
	}
}

func TestServerInstallRefusesAnInstallRootThatIsNotAbsolute(t *testing.T) {
	t.Parallel()

	env := map[string]string{"GOPHENBERG_INSTALL_ROOT": "planted/root"}

	got := testkit.Run(t, onMachine(env, installer.Machine{Root: t.TempDir()}), "", "server:install", "-dry-run",
		"-domain", "example.com")

	wantAnswer(t, got, gonsole.ExitFailed,
		`gophenberg: GOPHENBERG_INSTALL_ROOT: must be an absolute path, got "planted/root"`)
}

func TestServerInstallReadsTheStopGracesServeWaitsThroughAsServeReadsThem(t *testing.T) {
	t.Parallel()

	for _, values := range []map[string]string{
		{},
		{
			"GOPHENBERG_SHUTDOWN_GRACE": "4m", "GOPHENBERG_SHUTDOWN_CANCEL_GRACE": "7s",
			"GOPHENBERG_CRON_STOP_GRACE": "1500ms", "GOPHENBERG_SHUTDOWN_STOP_GRACE": "9s",
			"GOPHENBERG_THEME_STOP_GRACE": "11s",
		},
	} {
		served := maps.Clone(values)
		served["GOPHENBERG_DATABASE_URL"] = "postgres://gophenberg@example.com/gophenberg"
		settings, err := loadRunConfig(testkit.Getenv(served))
		if err != nil {
			t.Fatalf("loadRunConfig(%v) = %v, want the settings serve runs under", values, err)
		}

		got, err := installer.StopGraces(settingsEnv(testkit.Getenv(values)))

		want := []time.Duration{
			settings.serving.Grace, settings.serving.CancelGrace, settings.cron.stopGrace, settings.serving.StopGrace,
			settings.themeStopGrace,
		}
		if err != nil || !slices.Equal(got, want) {
			t.Errorf("installer.StopGraces(%v) = %v, %v, want the graces serve stops within %v", values, got, err, want)
		}
	}
}

func TestServerInstallReadsTheUploadCapAsServeReadsIt(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"", "512", strconv.FormatInt(maxUploadCapMB, 10)} {
		values := map[string]string{"GOPHENBERG_MEDIA_UPLOAD_CAP_MB": value}
		served := maps.Clone(values)
		served["GOPHENBERG_DATABASE_URL"] = "postgres://gophenberg@example.com/gophenberg"
		settings, err := loadRunConfig(testkit.Getenv(served))
		if err != nil {
			t.Fatalf("loadRunConfig(%v) = %v, want the settings serve runs under", values, err)
		}

		got, err := installer.UploadCapMB(settingsEnv(testkit.Getenv(values)))

		if err != nil || int64(got)<<20 != settings.mediaUploadCap {
			t.Errorf("installer.UploadCapMB(%q) = %d, %v, want the %d bytes serve takes", value, got, err,
				settings.mediaUploadCap)
		}
	}
}

func TestServerInstallRefusesTheUploadCapsServeRefuses(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"big", "0", strconv.FormatInt(maxUploadCapMB+1, 10)} {
		values := map[string]string{
			"GOPHENBERG_MEDIA_UPLOAD_CAP_MB": value, "GOPHENBERG_DATABASE_URL": "postgres://example.com/gophenberg",
		}
		_, serveErr := loadRunConfig(testkit.Getenv(values))

		_, err := installer.UploadCapMB(settingsEnv(testkit.Getenv(values)))

		if err == nil || serveErr == nil || err.Error() != serveErr.Error() {
			t.Errorf("installer.UploadCapMB(%q) = %v, want the error serve refuses it with, %v", value, err, serveErr)
		}
	}
}

func TestServerInstallWritesTheDatabaseWaitServeWaitsByDefault(t *testing.T) {
	t.Parallel()

	wait, err := databaseWaitFrom(settingsEnv(testkit.Getenv(nil)))
	if err != nil {
		t.Fatalf("databaseWaitFrom() = %v, want the default", err)
	}
	written, err := time.ParseDuration(installer.DatabaseWait)

	if err != nil || written != wait.limit {
		t.Errorf("server:install writes the database wait %q, read as %v with %v, want the %v serve waits by default",
			installer.DatabaseWait, written, err, wait.limit)
	}
}

func TestOnlyTheServerCommandsLeaveTheDotEnvFileUnread(t *testing.T) {
	t.Parallel()

	for line, reads := range map[string]bool{
		"server:install -dry-run": false, "server:uninstall -yes": false, "update:apply": true, "serve": true,
		"help server:install": true, "": true,
	} {
		if got := ReadsDotEnv(strings.Fields(line)); got != reads {
			t.Errorf("ReadsDotEnv(%q) = %t, want %t", line, got, reads)
		}
	}
}

func TestMainBinaryLeavesTheDotEnvFileUnreadForTheServerCommands(t *testing.T) {
	t.Parallel()

	binary, env := testkit.CoverBinary(t, "GOPHENBERG_", "gophenberg")
	folder := t.TempDir()
	planted := []byte("GOPHENBERG_INSTALL_ROOT=planted/root\n")
	if err := os.WriteFile(filepath.Join(folder, ".env"), planted, 0o600); err != nil {
		t.Fatalf("writing the .env file: %v", err)
	}

	install, code := stderrOf(t, binary, append(slices.Clone(env), "GOPHENBERG_INSTALL_MEMORY_FLOOR_MB=0"), folder,
		"server:install", "-dry-run", "-domain", "example.com")
	status, _ := stderrOf(t, binary, env, folder, "update:status")

	want := "gophenberg: GOPHENBERG_INSTALL_MEMORY_FLOOR_MB: must stand above zero, got \"0\"\n"
	if install != want || code != gonsole.ExitFailed {
		t.Errorf("server:install = %d, printing %q, want %d and only the memory floor of its environment %q, the "+
			".env file never read", code, install, gonsole.ExitFailed, want)
	}
	if want := `GOPHENBERG_INSTALL_ROOT: must be an absolute path, got "planted/root"`; !strings.Contains(status, want) {
		t.Errorf("update:status printed %q, want the .env file read and %q", status, want)
	}
}
