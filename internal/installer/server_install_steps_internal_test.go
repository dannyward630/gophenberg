// SPDX-License-Identifier: Apache-2.0

package installer

import (
	"flag"
	"fmt"
	"io/fs"
	"maps"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/gopherium/gophenberg/internal/version"
)

// serverInstallFeature is the feature server:install answers to on a fake server.
const serverInstallFeature = "../../test/features/features/server-install.feature"

// earlierRecord is the install record of an earlier run for example.com that ended every step.
const earlierRecord = `{"installer": "0.0.1", "domain": "example.com",
"installed": ["postgresql", "caddy", "gophenberg.service", "/opt/gophenberg"],
"steps": {"server-env": "ended", "packages": "ended", "user": "ended", "install-root": "ended", "database": "ended",
"build": "ended", "installer-copy": "ended", "site-env": "ended", "units": "ended", "command": "ended",
"caddy": "ended", "start": "ended", "admin": "ended", "https": "ended", "summary": "ended"}}
`

// probes are the programs the checks run, none of which changes a server.
var probes = []string{"systemctl --version", sandboxLine, "systemd-detect-virt", clockLine}

// printedFile is one file a dry run prints: the line that names it, and its text.
type printedFile struct {
	header, text string
}

// installScenario is what one server:install scenario keeps between its steps.
type installScenario struct {
	t       *testing.T
	server  *fakeServer
	env     map[string]string
	before  map[string]string
	stdout  string
	stderr  string
	err     error
	checked map[string]bool
}

// initializeServerInstall returns the binding of the server:install steps, t holding every fake server.
func initializeServerInstall(t *testing.T) func(*godog.ScenarioContext) {
	return func(sc *godog.ScenarioContext) {
		s := &installScenario{t: t, env: map[string]string{}, checked: map[string]bool{}}
		sc.Given(`^a fresh Ubuntu 24.04 server run by systemd 255$`, s.freshServer)
		sc.Given(`^the operator is not root$`, s.notRoot)
		sc.Given(`^the server runs Ubuntu 22.04$`, s.runUbuntu2204)
		sc.Given(`^the server runs systemd (\d+)$`, s.runSystemd)
		sc.Given(`^the server runs (Ubuntu 24.04|Debian 12) with systemd (\d+)$`, s.runSystemWithSystemd)
		sc.Given(`^systemd cannot set up a sandbox on the server, a container systemd-detect-virt reports as lxc$`,
			s.failSandbox)
		sc.Given(`^the server has (\d+) MB of memory$`, s.holdMemory)
		sc.Given(`^something listens on TCP port 80$`, s.takePort80)
		sc.Given(`^example.com has no A or AAAA record$`, s.resolveNothing)
		sc.Given(`^example.com points at 198.51.100.7, where another server answers every page with 404$`,
			s.pointElsewhere)
		sc.Given(`^example.com resolves to 127.0.1.1, as a line in /etc/hosts makes it$`, s.pointAtHostsLine)
		sc.Given(`^PostgreSQL is installed on the server$`, s.installPostgreSQL)
		sc.Given(`^Caddy is installed on the server$`, s.installCaddy)
		sc.Given(`^another user can write /opt$`, s.openOpt)
		sc.Given(`^the install record names the installer "([^"]*)"$`, s.recordInstaller)
		sc.Given(`^an earlier run installed Gophenberg for example.com on the server$`, s.installEarlier)
		sc.Given(`^only root may read the server settings and the install record$`, s.lockRootFiles)
		sc.Given(`^the web server line of the server settings was changed by hand to none$`, s.keepOwnWebServer)
		sc.Given(`^the site's .env holds the line "([^"]*)"$`, s.addSiteEnvLine)
		sc.Given(`^the site's .env holds the line "([^"]*)", <ESC> standing for the escape character$`,
			s.addEscapedSiteEnvLine)
		sc.Given(`^unattended-upgrades is installed on the server$`, s.installUnattended)
		sc.Given(`^the environment sets (GOPHENBERG_[A-Z_]+) to (\S+)$`, s.setEnv)
		sc.When(`^the operator runs "([^"]*)"$`, s.runLine)
		initializeServerInstallOutcomes(sc, s)
	}
}

// initializeServerInstallOutcomes binds the steps that check what a server:install run did to s.
func initializeServerInstallOutcomes(sc *godog.ScenarioContext, s *installScenario) {
	sc.Then(`^the run stops with "(.*)"$`, s.stopsWith)
	sc.Then(`^the run stops because this installer is older than the ([0-9.]+) installer$`, s.stopsAsOlder)
	sc.Then(`^the run succeeds$`, s.succeeds)
	sc.Then(`^the run notes "([^"]*)"$`, s.notes)
	sc.Then(`^the run notes nothing$`, s.notesNothing)
	sc.Then(`^the run prints nothing$`, s.printsNothing)
	sc.Then(`^nothing on the server changed$`, s.changedNothing)
	sc.Then(`^the checks listened on no port$`, s.listenedNowhere)
	sc.Then(`^the checks fetched no page$`, s.fetchedNothing)
	sc.Then(`^the checks listened only on 127.0.0.1:8081 and TCP port 5432$`, s.listenedOnSiteAndDatabase)
	sc.Then(`^the checks looked up no domain$`, s.resolvedNothing)
	sc.Then(`^the dry run writes only these files:$`, s.writesOnly)
	sc.Then(`^the dry run prints for the person's web server:$`, s.printsForWebServer)
	sc.Then(`^the dry run lists these steps:$`, s.listsSteps)
	sc.Then(`^the dry run writes "([^"]*)", owned by (root|gophenberg) with mode (0[0-7]{3}):$`, s.writes)
	sc.Then(`^the dry run writes "([^"]*)", owned by root with mode 0644, holding Caddy's repository key$`,
		s.writesCaddyKey)
	sc.Then(`^the dry run writes no other file$`, s.writesNoOtherFile)
	sc.Then(`^the dry run writes every other file as it does for example.com$`, s.writesAsForExample)
	sc.Then(`^the site's unit (holds|lacks) the line "([^"]*)"$`, s.unitHolds)
}

// freshServer gives the scenario a fresh Ubuntu 24.04 server run by systemd 255 that every check passes on.
func (s *installScenario) freshServer() {
	s.server = newFakeServer(s.t)
}

// notRoot has the run start as a plain user.
func (s *installScenario) notRoot() {
	s.server.euid = 1000
}

// runUbuntu2204 has the server run Ubuntu 22.04.
func (s *installScenario) runUbuntu2204() {
	s.server.write(osReleasePath, "PRETTY_NAME=\"Ubuntu 22.04.5 LTS\"\nVERSION_ID=\"22.04\"\nID=ubuntu\n")
}

// runSystemd has the server run systemd major.
func (s *installScenario) runSystemd(major int) {
	s.server.answers["systemctl"] = answer{printed: fmt.Sprintf("systemd %d (%d.4-1)\n+PAM +AUDIT\n", major, major)}
}

// runSystemWithSystemd has the server run system, Ubuntu 24.04 or Debian 12, with systemd major.
func (s *installScenario) runSystemWithSystemd(system string, major int) {
	if system == "Debian 12" {
		s.server.write(osReleasePath, debianRelease(12, "bookworm"))
	}
	s.runSystemd(major)
}

// failSandbox has systemd fail to set up the sandbox of the site's service in a container reported as lxc.
func (s *installScenario) failSandbox() {
	s.server.answers["systemd-run"] = answer{
		printed: "Job for run-u7.service failed.\n", err: fmt.Errorf("installer: run systemd-run: exit status 1"),
	}
	s.server.answers["systemd-detect-virt"] = answer{printed: "lxc\n"}
}

// holdMemory gives the server megabytes of memory.
func (s *installScenario) holdMemory(megabytes int) {
	s.server.write(meminfoPath, meminfoOf(megabytes*1024))
}

// takePort80 has something else listen on TCP port 80.
func (s *installScenario) takePort80() {
	s.server.taken["tcp :80"] = inUse("tcp", ":80")
}

// resolveNothing has every domain resolve to no address.
func (s *installScenario) resolveNothing() {
	s.server.unresolved = &net.DNSError{Err: "no such host", Name: "example.com", IsNotFound: true}
}

// pointElsewhere has every domain point at 198.51.100.7, where another server answers every page with 404.
func (s *installScenario) pointElsewhere() {
	s.server.addresses = addresses("198.51.100.7")
	s.server.answerFetch = answered(http.StatusNotFound, "404 page not found\n")
}

// pointAtHostsLine has every domain resolve to 127.0.1.1, as Go reads a line of /etc/hosts.
func (s *installScenario) pointAtHostsLine() {
	s.server.addresses = addresses("::ffff:127.0.1.1")
}

// installPostgreSQL puts PostgreSQL's settings on the server, as its package does.
func (s *installScenario) installPostgreSQL() {
	s.server.folder("/etc/postgresql/16/main")
}

// installCaddy puts Caddy's settings and program on the server, as its package does.
func (s *installScenario) installCaddy() {
	s.server.folder("/etc/caddy")
	s.server.program("/usr/bin/caddy")
}

// openOpt lets every user write /opt.
func (s *installScenario) openOpt() {
	s.server.chmod("/opt", 0o777)
}

// recordInstaller has the install record name installer as the newest installer that ran on the server.
func (s *installScenario) recordInstaller(installer string) {
	s.server.write(recordPath, `{"installer": "`+installer+`"}`)
}

// installEarlier sets the server up as an earlier run for example.com left it, every port held by what it installed.
func (s *installScenario) installEarlier() {
	server := s.server
	server.write(serverEnvPath, firstServerEnv)
	server.write(recordPath, earlierRecord)
	server.folder("/etc/postgresql/16/main", "/etc/caddy", "/opt/gophenberg/versions/0.19.0")
	server.write("/etc/systemd/system/gophenberg.service", wantedSiteUnit)
	server.write(siteEnvAt, wantedSiteEnv)
	server.link("/opt/gophenberg/current", "/opt/gophenberg/versions/0.19.0")
	for _, held := range portListens {
		network, address, _ := strings.Cut(held, " ")
		server.taken[held] = inUse(network, address)
	}
}

// lockRootFiles takes away every right on server.env and the install record, as a plain user meets them.
func (s *installScenario) lockRootFiles() {
	s.server.chmod(serverEnvPath, 0)
	s.server.chmod(recordPath, 0)
}

// keepOwnWebServer changes the web server line of server.env from caddy to none, as a hand edit does.
func (s *installScenario) keepOwnWebServer() error {
	held, err := os.ReadFile(s.server.at(serverEnvPath))
	if err != nil {
		return err
	}
	s.server.write(serverEnvPath, strings.Replace(string(held), "WEB_SERVER=caddy", "WEB_SERVER=none", 1))
	return nil
}

// addSiteEnvLine adds line to the site's .env.
func (s *installScenario) addSiteEnvLine(line string) error {
	held, err := os.ReadFile(s.server.at(siteEnvAt))
	if err != nil {
		return err
	}
	s.server.write(siteEnvAt, string(held)+line+"\n")
	return nil
}

// addEscapedSiteEnvLine adds line to the site's .env with the escape character in place of each <ESC>.
func (s *installScenario) addEscapedSiteEnvLine(line string) error {
	return s.addSiteEnvLine(strings.ReplaceAll(line, "<ESC>", "\x1b"))
}

// installUnattended puts the program of unattended-upgrades on the server.
func (s *installScenario) installUnattended() {
	s.server.program(unattendedUpgrade)
}

// setEnv sets the setting called key to value in the environment of the run.
func (s *installScenario) setEnv(key, value string) {
	s.env[key] = value
}

// runLine runs the server:install line on the scenario's server, noting what was on the server before.
func (s *installScenario) runLine(line string) error {
	request, err := requestOf(line)
	if err != nil {
		return err
	}
	s.before = s.server.snapshot()
	s.stdout, s.stderr, s.err = dryRunUnder(s.server, s.env, request)
	return nil
}

// requestOf returns what the server:install line asks for.
func requestOf(line string) (Request, error) {
	fields := strings.Fields(line)
	if len(fields) == 0 || fields[0] != "server:install" {
		return Request{}, fmt.Errorf("the line %q runs no server:install", line)
	}
	flags := flag.NewFlagSet("server:install", flag.ContinueOnError)
	var request Request
	flags.StringVar(&request.Domain, "domain", "", "")
	flags.StringVar(&request.Archive, "archive", "", "")
	flags.StringVar(&request.AdminEmail, "admin-email", "", "")
	flags.StringVar(&request.WebServer, "web-server", "", "")
	flags.BoolVar(&request.SkipDomainCheck, "skip-domain-check", false, "")
	flags.BoolVar(&request.DryRun, "dry-run", false, "")
	return request, flags.Parse(fields[1:])
}

// snapshot returns every file, folder and link on the fake server by its path, with its mode and what it holds.
func (s *fakeServer) snapshot() map[string]string {
	held := map[string]string{}
	err := filepath.WalkDir(s.root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		held[s.serverPath(path)] = info.Mode().String() + " " + contentOf(path, info)
		return nil
	})
	if err != nil {
		s.t.Errorf("reading the fake server: %v", err)
	}
	return held
}

// contentOf returns the text of the file at path, the path a link leads to, or nothing for a folder.
func contentOf(path string, info fs.FileInfo) string {
	if info.Mode()&fs.ModeSymlink != 0 {
		target, _ := os.Readlink(path)
		return target
	}
	if info.IsDir() {
		return ""
	}
	held, _ := os.ReadFile(path)
	return string(held)
}

// stopsWith fails unless the run stopped with want.
func (s *installScenario) stopsWith(want string) error {
	if s.err == nil || s.err.Error() != want {
		return fmt.Errorf("the run ended with %v, want %q", s.err, want)
	}
	return nil
}

// stopsAsOlder fails unless the run stopped because this installer is older than the one the record names.
func (s *installScenario) stopsAsOlder(recorded string) error {
	return s.stopsWith("this installer is " + version.Version() + ", and this server was set up by the " + recorded +
		" installer, so run the install.sh line again")
}

// succeeds fails unless the run passed every check.
func (s *installScenario) succeeds() error {
	if s.err != nil {
		return fmt.Errorf("the run stopped with %q, want every check passed", s.err)
	}
	return nil
}

// notes fails unless the run noted the line on its error stream.
func (s *installScenario) notes(line string) error {
	if !slices.Contains(strings.Split(s.stderr, "\n"), line) {
		return fmt.Errorf("the run noted %q, want the line %q", s.stderr, line)
	}
	return nil
}

// notesNothing fails when the run noted anything on its error stream.
func (s *installScenario) notesNothing() error {
	if s.stderr != "" {
		return fmt.Errorf("the run noted %q, want nothing", s.stderr)
	}
	return nil
}

// printsNothing fails when the run printed anything on its answer stream.
func (s *installScenario) printsNothing() error {
	if s.stdout != "" {
		return fmt.Errorf("the run printed %q, want nothing", s.stdout)
	}
	return nil
}

// changedNothing fails when a file, folder or link on the server changed, or a program ran that is not a check's.
func (s *installScenario) changedNothing() error {
	if after := s.server.snapshot(); !maps.Equal(after, s.before) {
		return fmt.Errorf("the server held\n%v\nbefore the run and\n%v\nafter it", s.before, after)
	}
	for _, ran := range s.server.ran {
		if !slices.Contains(probes, ran) {
			return fmt.Errorf("the run ran %q, which is no check", ran)
		}
	}
	return nil
}

// listenedNowhere fails when the checks listened on any port.
func (s *installScenario) listenedNowhere() error {
	if len(s.server.listened) != 0 {
		return fmt.Errorf("the checks listened on %q, want no port", s.server.listened)
	}
	return nil
}

// fetchedNothing fails when the checks fetched any page.
func (s *installScenario) fetchedNothing() error {
	if len(s.server.fetched) != 0 {
		return fmt.Errorf("the checks fetched %q, want no page", s.server.fetched)
	}
	return nil
}

// listenedOnSiteAndDatabase fails unless the checks listened on the site's own port and PostgreSQL's alone, in order.
func (s *installScenario) listenedOnSiteAndDatabase() error {
	if want := []string{"tcp 127.0.0.1:8081", "tcp :5432"}; !slices.Equal(s.server.listened, want) {
		return fmt.Errorf("the checks listened on %q, want %q", s.server.listened, want)
	}
	return nil
}

// resolvedNothing fails when the checks looked up any domain.
func (s *installScenario) resolvedNothing() error {
	if len(s.server.resolved) != 0 {
		return fmt.Errorf("the checks looked up %q, want no domain", s.server.resolved)
	}
	return nil
}

// writesOnly fails unless the files the dry run prints are those want names, one path a line, in order.
func (s *installScenario) writesOnly(want *godog.DocString) error {
	var paths []string
	for line := range strings.Lines(s.stdout) {
		if named, isHeader := strings.CutPrefix(line, "  writes "); isHeader {
			path, _, _ := strings.Cut(named, ", owned by ")
			paths = append(paths, path)
		}
	}
	if got := strings.Join(paths, "\n"); got != want.Content {
		return fmt.Errorf("the dry run writes\n%s\nwant\n%s", got, want.Content)
	}
	return nil
}

// printsForWebServer fails unless the text a step of the dry run prints is want, line for line.
func (s *installScenario) printsForWebServer(want *godog.DocString) error {
	_, after, found := strings.Cut(s.stdout, "\n  prints:\n")
	if !found {
		return fmt.Errorf("the dry run printed\n%s\nwith no step that prints a text", s.stdout)
	}
	block, _, _ := strings.Cut(after, "\nStep ")
	var printed strings.Builder
	for line := range strings.Lines(block) {
		printed.WriteString(strings.TrimPrefix(line, "    "))
	}
	if got := printed.String(); got != want.Content+"\n" {
		return fmt.Errorf("the dry run prints\n%s\nwant\n%s", got, want.Content)
	}
	return nil
}

// listsSteps fails unless the steps the dry run lists are want, line for line.
func (s *installScenario) listsSteps(want *godog.DocString) error {
	var listed []string
	for line := range strings.Lines(s.stdout) {
		if strings.HasPrefix(line, "Step ") {
			listed = append(listed, strings.TrimSuffix(line, "\n"))
		}
	}
	if got := strings.Join(listed, "\n"); got != want.Content {
		return fmt.Errorf("the dry run lists\n%s\nwant\n%s", got, want.Content)
	}
	return nil
}

// printedFiles returns every file the dry run prints, by its path.
func (s *installScenario) printedFiles() map[string]printedFile {
	printed := map[string]printedFile{}
	var path string
	var text strings.Builder
	keep := func() {
		if path != "" {
			held := printed[path]
			held.text = strings.TrimRight(text.String(), "\n") + "\n"
			printed[path] = held
		}
	}
	for line := range strings.Lines(s.stdout) {
		named, isHeader := strings.CutPrefix(line, "  writes ")
		switch {
		case isHeader:
			keep()
			path, _, _ = strings.Cut(named, ", owned by ")
			printed[path] = printedFile{header: strings.TrimSuffix(line, "\n")}
			text.Reset()
		case strings.HasPrefix(line, "Step "):
			keep()
			path = ""
		case path != "":
			text.WriteString(strings.TrimPrefix(line, "    "))
		}
	}
	keep()
	return printed
}

// writes fails unless the dry run prints the file at path, owned by owner with mode, holding want line for line.
func (s *installScenario) writes(path, owner, mode string, want *godog.DocString) error {
	return s.writesText(path, "owned by "+owner+" with mode "+mode, want.Content+"\n")
}

// writesText fails unless the dry run prints the file at path with the owner and mode named, holding want.
func (s *installScenario) writesText(path, owned, want string) error {
	s.checked[path] = true
	printed, found := s.printedFiles()[path]
	if !found {
		return fmt.Errorf("the dry run printed\n%s\nwith no file %s", s.stdout, path)
	}
	if header := "  writes " + path + ", " + owned + ":"; printed.header != header {
		return fmt.Errorf("the dry run names the file with %q, want %q", printed.header, header)
	}
	if printed.text != want {
		return fmt.Errorf("the dry run writes %s holding\n%s\nwant\n%s", path, printed.text, want)
	}
	return nil
}

// writesCaddyKey fails unless the dry run prints the key of Caddy's own repository, as the binary carries it.
func (s *installScenario) writesCaddyKey() error {
	return s.writesText("/etc/apt/keyrings/caddy.asc", "owned by root with mode 0644", caddyKey)
}

// writesNoOtherFile fails when the dry run prints a file no step of the scenario checked.
func (s *installScenario) writesNoOtherFile() error {
	for path := range s.printedFiles() {
		if !s.checked[path] {
			return fmt.Errorf("the dry run writes %s, which the scenario does not check", path)
		}
	}
	return nil
}

// writesAsForExample fails unless every file the scenario did not check is as a dry run for example.com prints it.
func (s *installScenario) writesAsForExample() error {
	example := newFakeServer(s.t)
	example.program(unattendedUpgrade)
	stdout, _, err := dryRun(example, Request{Domain: "example.com", AdminEmail: "admin@example.com", DryRun: true})
	if err != nil {
		return fmt.Errorf("the dry run for example.com stopped with %w", err)
	}
	wanted := (&installScenario{stdout: stdout}).printedFiles()
	got := s.printedFiles()
	if gotPaths, wantedPaths := slices.Sorted(maps.Keys(got)), slices.Sorted(maps.Keys(wanted)); !slices.Equal(
		gotPaths, wantedPaths) {
		return fmt.Errorf("the dry run writes %v, want %v", gotPaths, wantedPaths)
	}
	for path, file := range wanted {
		if !s.checked[path] && got[path] != file {
			return fmt.Errorf("the dry run writes %s as\n%s\nwant it as for example.com\n%s", path, got[path].text,
				file.text)
		}
	}
	return nil
}

// unitHolds fails unless the site's unit the dry run prints holds the line, or lacks it when holds says so.
func (s *installScenario) unitHolds(holds, line string) error {
	unit := s.printedFiles()["/etc/systemd/system/gophenberg.service"].text
	held := slices.Contains(strings.Split(unit, "\n"), line)
	if held != (holds == "holds") {
		return fmt.Errorf("the site's unit holding the line %q is %s, want the unit that %s it:\n%s", line,
			strconv.FormatBool(held), holds, unit)
	}
	return nil
}

func TestServerInstall(t *testing.T) {
	t.Parallel()

	tags := "~@wip"
	if os.Getenv("GOPHENBERG_BDD_WIP") != "" {
		tags = ""
	}
	suite := godog.TestSuite{
		ScenarioInitializer: initializeServerInstall(t),
		Options: &godog.Options{
			Format: "pretty", Paths: []string{serverInstallFeature}, Tags: tags, Strict: true, TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Error("the feature did not pass")
	}
}
