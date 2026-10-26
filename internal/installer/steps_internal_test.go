// SPDX-License-Identifier: Apache-2.0

package installer

import (
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gopherium/framework/gonsole"
)

// wantedLead is the first line a dry run prints once every check passed.
const wantedLead = "Every check passed, and nothing was changed. Without -dry-run, server:install would take these " +
	"steps.\n"

// firstSteps are what each step of a first run for example.com does, in the order it takes them.
var firstSteps = []string{
	"start the install record /var/lib/gophenberg-installer/install.json and write the server settings",
	"install PostgreSQL and Caddy with apt-get, Caddy from its own apt repository",
	"make the system user gophenberg, which runs the site and signs in to PostgreSQL",
	"make the install root /opt/gophenberg and its folders, owned by gophenberg",
	"make the database role gophenberg and its database gophenberg as the postgres user",
	"unpack the release archive into /opt/gophenberg/versions and point /opt/gophenberg/current at it",
	"keep a copy of this installer at /usr/local/lib/gophenberg/gophenberg, the one root runs for server:install " +
		"and server:uninstall",
	"write the site's settings, then run check as gophenberg in /opt/gophenberg",
	"write the systemd units and Caddy's drop-in, then reload systemd and enable gophenberg.service",
	"write the gophenberg command on the path",
	"write Caddy's files, keep the package's Caddyfile as /etc/caddy/Caddyfile.before-gophenberg, check them with " +
		"caddy validate and restart Caddy",
	"start gophenberg.service and wait until /admin/, /api/locale and / answer on 127.0.0.1:8081",
	"create the first administrator with account:create-admin as gophenberg",
	"check that https://example.com/admin/ answers within 2m0s",
	"print the summary and end the install record",
}

// stepIDs are the names the install record keeps the steps of a first run under, in the order the run takes them.
var stepIDs = []string{
	"server-env", "packages", "user", "install-root", "database", "build", "installer-copy", "site-env", "units",
	"command", "caddy", "start", "admin", "https", "summary",
}

// refusingWriter is an answer stream that takes nothing.
type refusingWriter struct{}

// Write takes nothing and says the terminal is gone.
func (refusingWriter) Write([]byte) (int, error) {
	return 0, errors.New("the terminal is gone")
}

// doneOf returns what each of steps does and the name the install record keeps it under.
func doneOf(steps []step) (does, ids []string) {
	for _, planned := range steps {
		does, ids = append(does, planned.does), append(ids, planned.id)
	}
	return does, ids
}

// filesOf returns every file steps write, in the order they write them.
func filesOf(steps []step) []file {
	var files []file
	for _, planned := range steps {
		files = append(files, planned.files...)
	}
	return files
}

func TestTheStepsOfAFirstRunTakeEveryStepWithTheFilesEachWrites(t *testing.T) {
	t.Parallel()

	running := firstSite(t, "example.com", 255)

	steps := running.steps()

	does, ids := doneOf(steps)
	if !slices.Equal(does, firstSteps) {
		t.Errorf("the steps do\n%s\nwant\n%s", strings.Join(does, "\n"), strings.Join(firstSteps, "\n"))
	}
	if !slices.Equal(ids, stepIDs) {
		t.Errorf("the record keeps the steps under %q, want %q", ids, stepIDs)
	}
	want := slices.Concat(
		[]file{{"/etc/gophenberg/server.env", "root", 0o600, firstServerEnv}}, aptFilesOf()[:3],
		[]file{{siteEnvAt, "gophenberg", 0o600, wantedSiteEnv}}, unitsOf(wantedSiteUnit),
		[]file{{"/usr/local/bin/gophenberg", "root", 0o755, wantedCommand}},
		caddyFilesOf(wantedCaddySite, wantedCaddyfile),
	)
	wantFiles(t, filesOf(steps), want)
}

func TestTheStepsOfALaterRunKeepTheBuildReloadCaddyRestartTheSiteAndCreateNoAdministrator(t *testing.T) {
	t.Parallel()

	running := firstSite(t, "example.com", 255)
	running.current = true
	running.record.Steps = map[string]string{stepCaddy: stepEnded, stepStart: stepEnded, stepAdmin: stepEnded}

	does, ids := doneOf(running.steps())

	want := slices.Concat(firstSteps[:5],
		[]string{"keep the build /opt/gophenberg/current names, since updates go through update:apply"},
		firstSteps[6:10], []string{
			"write Caddy's files, check them with caddy validate and reload Caddy",
			"restart gophenberg.service and wait until /admin/, /api/locale and / answer on 127.0.0.1:8081",
		}, firstSteps[13:])
	if !slices.Equal(does, want) {
		t.Errorf("the steps do\n%s\nwant\n%s", strings.Join(does, "\n"), strings.Join(want, "\n"))
	}
	if wantIDs := slices.Delete(slices.Clone(stepIDs), 12, 13); !slices.Equal(ids, wantIDs) {
		t.Errorf("the record keeps the steps under %q, want %q", ids, wantIDs)
	}
}

func TestTheStepsOfARunCutOffBeforeTheAdministratorWasCreatedStillCreateIt(t *testing.T) {
	t.Parallel()

	running := firstSite(t, "example.com", 255)
	running.record.Steps = map[string]string{stepAdmin: "started", stepStart: "started"}

	does, _ := doneOf(running.steps())

	if !slices.Equal(does, firstSteps) {
		t.Errorf("the steps do\n%s\nwant those of a first run", strings.Join(does, "\n"))
	}
}

func TestTheStepsWaitForTheSiteOnItsOwnAddressAndHTTPSWithinTheWaitTheSettingsName(t *testing.T) {
	t.Parallel()

	running := firstSite(t, "blog.example.com", 255)
	running.settings.Root = "/srv/gophenberg"
	running.settings.Checks.HTTPSWait = 90 * time.Second

	does, _ := doneOf(running.steps())

	for _, want := range []string{
		"make the install root /srv/gophenberg and its folders, owned by gophenberg",
		"start gophenberg.service and wait until /admin/, /api/locale and / answer on 127.0.0.1:8081",
		"check that https://blog.example.com/admin/ answers within 1m30s",
	} {
		if !slices.Contains(does, want) {
			t.Errorf("the steps do\n%s\nwant one that does %q", strings.Join(does, "\n"), want)
		}
	}
}

func TestPrintStepsPrintsEachStepAndTheFullTextOfEveryFileItWrites(t *testing.T) {
	t.Parallel()

	var answer strings.Builder

	err := printSteps(&answer, []step{
		{id: "user", does: "make the system user gophenberg"},
		{id: "units", does: "write the units", files: []file{
			{"/etc/systemd/system/a.service", "root", 0o644, "[Unit]\nDescription=A\n\n[Service]\n\tType=exec\n"},
			{"/opt/gophenberg/.env", "gophenberg", 0o600, "GOPHENBERG_THEME=starter\n"},
		}},
	})

	want := wantedLead + `
Step 1: make the system user gophenberg

Step 2: write the units
  writes /etc/systemd/system/a.service, owned by root with mode 0644:
    [Unit]
    Description=A

    [Service]
    	Type=exec
  writes /opt/gophenberg/.env, owned by gophenberg with mode 0600:
    GOPHENBERG_THEME=starter
`
	if err != nil || answer.String() != want {
		t.Errorf("printSteps() = %v, printing\n%s\nwant\n%s", err, answer.String(), want)
	}
}

func TestPrintStepsPrintsTheTextAStepPrintsUnderItsFiles(t *testing.T) {
	t.Parallel()

	var answer strings.Builder

	err := printSteps(&answer, []step{
		{id: "summary", does: "print the summary"},
		{id: "web-server", does: "print the lines", files: []file{
			{"/etc/a.conf", "root", 0o644, "a = 1\n"},
		}, prints: "nginx:\n\nlocation / {\n\tproxy_pass http://127.0.0.1:8081;\n}\n"},
	})

	want := wantedLead + `
Step 1: print the summary

Step 2: print the lines
  writes /etc/a.conf, owned by root with mode 0644:
    a = 1
  prints:
    nginx:

    location / {
    	proxy_pass http://127.0.0.1:8081;
    }
`
	if err != nil || answer.String() != want {
		t.Errorf("printSteps() = %v, printing\n%s\nwant\n%s", err, answer.String(), want)
	}
}

func TestInstallPrintsTheLinesForTheirOwnWebServerWithTheUploadCapOfTheSitesEnv(t *testing.T) {
	t.Parallel()

	server := installedServer(t, wantedSiteEnv+"GOPHENBERG_MEDIA_UPLOAD_CAP_MB=512\n")
	server.write(serverEnvPath, strings.Replace(firstServerEnv, "WEB_SERVER=caddy", "WEB_SERVER=none", 1))

	stdout, stderr, err := dryRun(server, Request{DryRun: true})

	if err != nil || stderr != "" {
		t.Fatalf("Install() = %v, noting %q, want nil and no note", err, stderr)
	}
	want := "  prints:\n" + indentedText(strings.NewReplacer("128 MB", "512 MB", "131136k", "524352k",
		"134283264", "536936448").Replace(wantedWebServerText))
	if !strings.HasSuffix(stdout, want) {
		t.Errorf("the dry run printed\n%s\nwant it to end with\n%s", stdout, want)
	}
}

func TestInstallRefusesASiteEnvWithAnUploadCapServeCannotReadForTheirOwnWebServer(t *testing.T) {
	t.Parallel()

	server := installedServer(t, "GOPHENBERG_MEDIA_UPLOAD_CAP_MB=big\n")
	server.write(serverEnvPath, strings.Replace(firstServerEnv, "WEB_SERVER=caddy", "WEB_SERVER=none", 1))

	stdout, _, err := dryRun(server, Request{DryRun: true})

	wantRefused(t, err, `/opt/gophenberg/.env: GOPHENBERG_MEDIA_UPLOAD_CAP_MB: must be a whole number, got "big"`)
	if stdout != "" {
		t.Errorf("Install() printed %q, want nothing", stdout)
	}
}

func TestInstallLeavesTheUploadCapOfTheSitesEnvUnreadWithCaddy(t *testing.T) {
	t.Parallel()

	server := installedServer(t, "GOPHENBERG_MEDIA_UPLOAD_CAP_MB=big\n")

	stdout, _, err := dryRun(server, Request{DryRun: true})

	if err != nil || !strings.HasPrefix(stdout, wantedLead) {
		t.Errorf("Install() = %v, printing\n%s\nwant every step, the upload cap left unread", err, stdout)
	}
}

func TestPrintStepsReturnsWhatStopsTheAnswer(t *testing.T) {
	t.Parallel()

	err := printSteps(refusingWriter{}, []step{{id: "user", does: "make the system user gophenberg"}})

	wantRefused(t, err, "the terminal is gone")
}

// dryRun runs server:install as request asks on server, returning its answer, its notes and its error.
func dryRun(server *fakeServer, request Request) (stdout, stderr string, err error) {
	return dryRunUnder(server, nil, request)
}

// dryRunUnder runs server:install as request asks on server under the settings env holds, as dryRun returns.
func dryRunUnder(server *fakeServer, env map[string]string, request Request) (stdout, stderr string, err error) {
	var answer, notes strings.Builder
	err = Install(server.t.Context(), server.machine(), gonsole.Call{Env: envOf(env), Stdout: &answer, Stderr: &notes},
		request)
	return answer.String(), notes.String(), err
}

func TestInstallPrintsEveryStepOfADryRunOnceEveryCheckPasses(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)

	stdout, stderr, err := dryRun(server, firstRun)

	if err != nil || stderr != wantedFirewallNote {
		t.Fatalf("Install() = %v, noting %q, want nil and the firewall note", err, stderr)
	}
	if !strings.HasPrefix(stdout, wantedLead) {
		t.Errorf("the dry run printed\n%s\nwant it to start with %q", stdout, wantedLead)
	}
	for n, does := range firstSteps {
		if line := "\nStep " + strconv.Itoa(n+1) + ": " + does + "\n"; !strings.Contains(stdout, line) {
			t.Errorf("the dry run printed\n%s\nwant the line %q", stdout, line)
		}
	}
	if unit := indentedText(wantedSiteUnit); !strings.Contains(stdout, unit) {
		t.Errorf("the dry run printed\n%s\nwant the site's unit\n%s", stdout, unit)
	}
}

// indentedText returns text as a dry run prints it, each line that is not empty indented by four spaces.
func indentedText(text string) string {
	var printed strings.Builder
	for line := range strings.Lines(text) {
		if line != "\n" {
			printed.WriteString("    ")
		}
		printed.WriteString(line)
	}
	return printed.String()
}

func TestInstallStopsARunWithoutDryRunOnceEveryCheckPasses(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)

	stdout, _, err := dryRun(server, Request{Domain: "example.com", AdminEmail: "admin@example.com"})

	wantRefused(t, err, "this build of server:install only checks the server, and -dry-run prints the steps it "+
		"would take, so nothing was changed")
	if stdout != "" {
		t.Errorf("Install() printed %q without -dry-run, want nothing", stdout)
	}
}

func TestInstallRefusesAServerWhereTheRunnerFindsNoSystemctlOrNoRunuser(t *testing.T) {
	t.Parallel()

	for _, missing := range []string{"/usr/bin/systemctl", "/usr/sbin/runuser"} {
		server := newFakeServer(t)
		server.chmod(missing, 0o644)

		_, _, err := dryRun(server, firstRun)

		wantRefused(t, err, "installer: no program "+missing[strings.LastIndex(missing, "/")+1:]+
			" in /usr/sbin:/usr/bin:/sbin:/bin")
	}
}

// installedServer returns a fake server an earlier run for example.com set up, whose site's .env holds siteEnv.
func installedServer(t *testing.T, siteEnv string) *fakeServer {
	t.Helper()
	server := newFakeServer(t)
	server.write(serverEnvPath, firstServerEnv)
	server.write(recordPath, `{"installer": "0.0.1", "domain": "example.com", "installed": ["/opt/gophenberg"]}`)
	server.write(siteEnvAt, siteEnv)
	return server
}

func TestInstallRefusesASiteEnvWithAStopGraceServeCannotRead(t *testing.T) {
	t.Parallel()

	server := installedServer(t, "GOPHENBERG_SHUTDOWN_GRACE=soon\n")

	stdout, _, err := dryRun(server, Request{DryRun: true})

	wantRefused(t, err, `/opt/gophenberg/.env: GOPHENBERG_SHUTDOWN_GRACE: must be a duration like 30s, got "soon"`)
	if stdout != "" {
		t.Errorf("Install() printed %q, want nothing", stdout)
	}
}

func TestInstallRefusesASiteEnvItCannotParse(t *testing.T) {
	t.Parallel()

	server := installedServer(t, "not a setting line\n")

	_, _, err := dryRun(server, Request{DryRun: true})

	wantRefused(t, err, `installer: /opt/gophenberg/.env: unexpected character "\n" in variable name near `+
		`"not a setting line\n"`)
}

func TestInstallWritesTheUnattendedUpgradesLineWhereThatPackageIsInstalled(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	server.program("/usr/bin/unattended-upgrade")

	stdout, _, err := dryRun(server, firstRun)

	header := "  writes /etc/apt/apt.conf.d/52gophenberg-caddy, owned by root with mode 0644:\n"
	if err != nil || !strings.Contains(stdout, header+indentedText(wantedCaddyUpgrades)) {
		t.Errorf("Install() = %v, printing\n%s\nwant the unattended-upgrades line", err, stdout)
	}
}

func TestProgramFindsAProgramWhereTheRunnerLooksForIt(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	server.program("/bin/runuser")
	server.write("/usr/sbin/systemctl", "a file nobody may run\n")

	for name, want := range map[string]string{"systemctl": "/usr/bin/systemctl", "runuser": "/usr/sbin/runuser"} {
		got, err := server.machine().program(name)

		if err != nil || got != want {
			t.Errorf("program(%s) = %q, %v, want %q", name, got, err, want)
		}
	}
	if _, err := server.machine().program("caddy"); err == nil {
		t.Error("program(caddy) found a program the server does not hold, want an error")
	}
}
