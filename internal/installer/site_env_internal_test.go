// SPDX-License-Identifier: Apache-2.0

package installer

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

// wantedSiteEnv is the site's .env a first run writes for example.com with every default.
const wantedSiteEnv = `# Written by gophenberg server:install.
GOPHENBERG_DATABASE_URL=postgres:///gophenberg?host=/var/run/postgresql&user=gophenberg
GOPHENBERG_SUPERUSER_POLICY=refuse
GOPHENBERG_INSTALL_ROOT=/opt/gophenberg
GOPHENBERG_ADDR=127.0.0.1:8081
GOPHENBERG_PUBLIC_URL=https://example.com
GOPHENBERG_TRUSTED_PROXIES=127.0.0.1/32,::1/128
GOPHENBERG_THEMES_DIR=/opt/gophenberg/themes
GOPHENBERG_MEDIA_DIR=/opt/gophenberg/media
GOPHENBERG_DATABASE_WAIT=60s
`

// siteEnvAt is where the site's .env sits on a fresh server with every default, from the top of the server.
const siteEnvAt = "/opt/gophenberg/.env"

func TestTheSiteEnvOfAFirstRunNamesThePeerAddressTheSuperuserPolicyAndTheServerSettings(t *testing.T) {
	t.Parallel()

	got := firstSite(t, "example.com", 255).siteEnvFile()

	wantFiles(t, []file{got}, []file{{siteEnvAt, "gophenberg", 0o600, wantedSiteEnv}})
	if !strings.Contains(got.text, "GOPHENBERG_DATABASE_URL="+DatabaseAddress+"\n") {
		t.Errorf("the site's .env names no database address %s:\n%s", DatabaseAddress, got.text)
	}
}

func TestTheSiteEnvOfALaterRunWritesTheServerSettingsAgainAndKeepsEveryOtherLine(t *testing.T) {
	t.Parallel()

	running := firstSite(t, "blog.example.com", 255)
	running.settings.Root = "/srv/gophenberg"
	running.dotEnv = map[string]string{
		"GOPHENBERG_INSTALL_ROOT": "/etc", "GOPHENBERG_DATABASE_URL": "postgres://elsewhere.example.com/gophenberg",
		"GOPHENBERG_SUPERUSER_POLICY": "warn", "GOPHENBERG_DATABASE_WAIT": "10s",
		"GOPHENBERG_SITE_TITLE": "Maria Perez's $site", "GOPHENBERG_THEME": "starter",
	}

	got := running.siteEnvFile()

	want := `# Written by gophenberg server:install.
GOPHENBERG_DATABASE_URL=postgres:///gophenberg?host=/var/run/postgresql&user=gophenberg
GOPHENBERG_SUPERUSER_POLICY=refuse
GOPHENBERG_INSTALL_ROOT=/srv/gophenberg
GOPHENBERG_ADDR=127.0.0.1:8081
GOPHENBERG_PUBLIC_URL=https://blog.example.com
GOPHENBERG_TRUSTED_PROXIES=127.0.0.1/32,::1/128
GOPHENBERG_THEMES_DIR=/srv/gophenberg/themes
GOPHENBERG_MEDIA_DIR=/srv/gophenberg/media
GOPHENBERG_DATABASE_WAIT=10s
GOPHENBERG_SITE_TITLE="Maria Perez's \$site"
GOPHENBERG_THEME=starter
`
	wantFiles(t, []file{got}, []file{{"/srv/gophenberg/.env", "gophenberg", 0o600, want}})
}

func TestStopGracesReadTheFiveGracesServeStopsWithinInTheOrderItWaitsThroughThem(t *testing.T) {
	t.Parallel()

	for _, stopping := range []struct {
		values map[string]string
		want   []time.Duration
	}{
		{nil, []time.Duration{10 * time.Second, 5 * time.Second, 2 * time.Second, 5 * time.Second, 3 * time.Second}},
		{map[string]string{
			"GOPHENBERG_SHUTDOWN_GRACE": "4m", "GOPHENBERG_SHUTDOWN_CANCEL_GRACE": "7s",
			"GOPHENBERG_CRON_STOP_GRACE": "1500ms", "GOPHENBERG_SHUTDOWN_STOP_GRACE": "9s",
			"GOPHENBERG_THEME_STOP_GRACE": "11s", "GOPHENBERG_HTTP_READ_TIMEOUT": "soon",
		}, []time.Duration{4 * time.Minute, 7 * time.Second, 1500 * time.Millisecond, 9 * time.Second, 11 * time.Second}},
	} {
		got, err := StopGraces(envOf(stopping.values))

		if err != nil || !slices.Equal(got, stopping.want) {
			t.Errorf("StopGraces(%v) = %v, %v, want %v", stopping.values, got, err, stopping.want)
		}
	}
}

func TestStopGracesRefuseEveryGraceServeCannotRead(t *testing.T) {
	t.Parallel()

	_, err := StopGraces(envOf(map[string]string{
		"GOPHENBERG_SHUTDOWN_GRACE": "soon", "GOPHENBERG_THEME_STOP_GRACE": "0s",
	}))

	wantRefused(t, err, `GOPHENBERG_SHUTDOWN_GRACE: must be a duration like 30s, got "soon"`+"\n"+
		`GOPHENBERG_THEME_STOP_GRACE: must stand above zero, got "0s"`)
}

// rootWith returns a machine whose install root /opt/gophenberg holds a .env file holding body.
func rootWith(t *testing.T, body string) Machine {
	t.Helper()
	return serverWith(t, map[string]string{siteEnvAt: body})
}

func TestSiteEnvReadsTheSettingsTheSitesDotEnvHolds(t *testing.T) {
	t.Parallel()

	machine := rootWith(t, "# Maria Perez's note\nGOPHENBERG_SHUTDOWN_GRACE=4m\nGOPHENBERG_SITE_TITLE='Maria Perez'\n")

	got, err := machine.siteEnv("/opt/gophenberg")

	want := map[string]string{"GOPHENBERG_SHUTDOWN_GRACE": "4m", "GOPHENBERG_SITE_TITLE": "Maria Perez"}
	if err != nil || !maps.Equal(got, want) {
		t.Errorf("siteEnv() = %v, %v, want %v", got, err, want)
	}
}

func TestSiteEnvReadsNothingBeforeTheInstallRootOrItsDotEnvIsThere(t *testing.T) {
	t.Parallel()

	empty := serverWith(t, nil)
	if err := os.MkdirAll(filepath.Join(empty.Root, "opt", "gophenberg"), 0o750); err != nil {
		t.Fatalf("making the install root: %v", err)
	}
	for _, machine := range []Machine{serverWith(t, nil), empty} {
		got, err := machine.siteEnv("/opt/gophenberg")

		if err != nil || got != nil {
			t.Errorf("siteEnv() = %v, %v, want nothing read", got, err)
		}
	}
}

func TestSiteEnvRefusesALinkThatLeadsOutOfTheInstallRoot(t *testing.T) {
	t.Parallel()

	machine := serverWith(t, map[string]string{"/etc/shadow": "root:secret:20000::::::\n"})
	if err := os.MkdirAll(filepath.Join(machine.Root, "opt", "gophenberg"), 0o750); err != nil {
		t.Fatalf("making the install root: %v", err)
	}
	if err := os.Symlink("../../etc/shadow", filepath.Join(machine.Root, siteEnvAt)); err != nil {
		t.Fatalf("planting the link: %v", err)
	}

	got, err := machine.siteEnv("/opt/gophenberg")

	wantRefused(t, err, "installer: /opt/gophenberg/.env: openat .env: path escapes from parent")
	if got != nil {
		t.Errorf("siteEnv() read %v through a link out of the install root, want nothing", got)
	}
}

func TestSiteEnvFollowsALinkThatStaysInTheInstallRoot(t *testing.T) {
	t.Parallel()

	machine := serverWith(t, map[string]string{"/opt/gophenberg/settings/site.env": "GOPHENBERG_THEME=starter\n"})
	if err := os.Symlink("settings/site.env", filepath.Join(machine.Root, siteEnvAt)); err != nil {
		t.Fatalf("planting the link: %v", err)
	}

	got, err := machine.siteEnv("/opt/gophenberg")

	if err != nil || got["GOPHENBERG_THEME"] != "starter" {
		t.Errorf("siteEnv() = %v, %v, want the file the link names inside the install root", got, err)
	}
}

func TestSiteEnvRefusesAnInstallRootItCannotOpen(t *testing.T) {
	t.Parallel()

	machine := serverWith(t, map[string]string{"/opt/gophenberg": "a file where the install root goes\n"})

	_, err := machine.siteEnv("/opt/gophenberg")

	wantRefused(t, err, "installer: open "+filepath.Join(machine.Root, "/opt/gophenberg")+": not a directory")
}

func TestSiteEnvRefusesAFolderAtDotEnv(t *testing.T) {
	t.Parallel()

	machine := serverWith(t, nil)
	if err := os.MkdirAll(filepath.Join(machine.Root, siteEnvAt), 0o750); err != nil {
		t.Fatalf("making a folder where .env goes: %v", err)
	}

	_, err := machine.siteEnv("/opt/gophenberg")

	wantRefused(t, err, "installer: /opt/gophenberg/.env: it is not a regular file")
}

func TestSiteEnvRefusesAPipeAtDotEnvWithoutWaitingOnIt(t *testing.T) {
	t.Parallel()

	machine := serverWith(t, nil)
	if err := os.MkdirAll(filepath.Join(machine.Root, "opt", "gophenberg"), 0o750); err != nil {
		t.Fatalf("making the install root: %v", err)
	}
	pipe := filepath.Join(machine.Root, siteEnvAt)
	if err := syscall.Mkfifo(pipe, 0o600); err != nil {
		t.Fatalf("planting the pipe: %v", err)
	}
	writer, err := os.OpenFile(pipe, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("holding the pipe open: %v", err)
	}
	defer func() { _ = writer.Close() }()
	read := make(chan error, 1)

	go func() {
		_, err := machine.siteEnv("/opt/gophenberg")
		read <- err
	}()

	select {
	case err := <-read:
		wantRefused(t, err, "installer: /opt/gophenberg/.env: it is not a regular file")
	case <-time.After(2 * time.Second):
		t.Error("siteEnv() still waits on a pipe held open after 2s, want the pipe refused at once")
	}
}

func TestSiteEnvRefusesADotEnvThatIsAHardLinkToAnotherFile(t *testing.T) {
	t.Parallel()

	machine := serverWith(t, map[string]string{"/etc/gophenberg/server.env": "GOPHENBERG_INSTALL_ROOT=/opt/gophenberg\n"})
	if err := os.MkdirAll(filepath.Join(machine.Root, "opt", "gophenberg"), 0o750); err != nil {
		t.Fatalf("making the install root: %v", err)
	}
	err := os.Link(filepath.Join(machine.Root, serverEnvPath), filepath.Join(machine.Root, siteEnvAt))
	if err != nil {
		t.Fatalf("planting the hard link: %v", err)
	}

	got, err := machine.siteEnv("/opt/gophenberg")

	wantRefused(t, err, "installer: /opt/gophenberg/.env: it has 2 hard links, and the site's .env must have one")
	if got != nil {
		t.Errorf("siteEnv() read %v through a hard link, want nothing", got)
	}
}

func TestSoleFileRefusesADotEnvItCannotDescribe(t *testing.T) {
	t.Parallel()

	err := soleFile(nil, syscall.EIO)

	wantRefused(t, err, "input/output error")
}

func TestSiteEnvRefusesADotEnvOverAMebibyte(t *testing.T) {
	t.Parallel()

	setting := "GOPHENBERG_SITE_TITLE="
	fits := rootWith(t, setting+strings.Repeat("a", 1<<20-len(setting)-1)+"\n")
	over := rootWith(t, setting+strings.Repeat("a", 1<<20-len(setting))+"\n")

	if _, err := fits.siteEnv("/opt/gophenberg"); err != nil {
		t.Errorf("siteEnv() of a .env of a mebibyte = %v, want it read", err)
	}
	_, err := over.siteEnv("/opt/gophenberg")

	wantRefused(t, err, "installer: /opt/gophenberg/.env: it holds more than 1048576 bytes, more than any site needs")
}

func TestSiteEnvRefusesASettingThatHoldsAControlCharacterOrABrokenByte(t *testing.T) {
	t.Parallel()

	for body, key := range map[string]string{
		"GOPHENBERG_SITE_TITLE=\"Maria\x1b]52;c;bWFyaWE=\x07\"\n": "GOPHENBERG_SITE_TITLE",
		"GOPHENBERG_SITE_TITLE=Maria\x1b[2JPerez\n":               "GOPHENBERG_SITE_TITLE",
		"GOPHENBERG_SITE_TITLE=\"Maria\\nPerez\"\n":               "GOPHENBERG_SITE_TITLE",
		"GOPHENBERG_SITE_TITLE=\"Maria\u009bPerez\"\n":            "GOPHENBERG_SITE_TITLE",
		"GOPHENBERG_SITE_TITLE=\"Maria\xffPerez\"\n":              "GOPHENBERG_SITE_TITLE",
		"GOPHENBERG_SITE\vTITLE=Maria\n":                          "GOPHENBERG_SITE\vTITLE",
	} {
		machine := rootWith(t, "GOPHENBERG_THEME=starter\n"+body)

		got, err := machine.siteEnv("/opt/gophenberg")

		wantRefused(t, err, fmt.Sprintf("installer: /opt/gophenberg/.env: %q holds a control character or a byte "+
			"that is not UTF-8, which no setting needs", key))
		if got != nil {
			t.Errorf("siteEnv() of %q read %v, want nothing", body, got)
		}
	}
}

func TestSiteEnvKeepsATabInAValue(t *testing.T) {
	t.Parallel()

	machine := rootWith(t, "GOPHENBERG_SITE_TITLE=\"Maria\tPerez\"\n")

	got, err := machine.siteEnv("/opt/gophenberg")

	if err != nil || got["GOPHENBERG_SITE_TITLE"] != "Maria\tPerez" {
		t.Errorf("siteEnv() = %q, %v, want the value with its tab", got, err)
	}
}

func TestSiteEnvRefusesADotEnvItCannotParse(t *testing.T) {
	t.Parallel()

	machine := rootWith(t, "not a setting line\n")

	_, err := machine.siteEnv("/opt/gophenberg")

	wantRefused(t, err, `installer: /opt/gophenberg/.env: unexpected character "\n" in variable name near `+
		`"not a setting line\n"`)
}
