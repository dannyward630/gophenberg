// SPDX-License-Identifier: Apache-2.0

package installer

import (
	"errors"
	"flag"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/cucumber/godog"
)

// serverUninstallFeature is the feature server:uninstall answers to on a fake server.
const serverUninstallFeature = "../../test/features/features/server-uninstall.feature"

// personSite is a site a person adds to the Caddyfile, which caddy validate takes.
const personSite = "\nexample.org {\n\trespond \"Maria Perez's own site\"\n}\n"

// ownCaddyfile is the Caddyfile of a Caddy the person set up to send example.com to the site.
const ownCaddyfile = "example.com {\n\treverse_proxy 127.0.0.1:8081\n}\n"

// initializeServerUninstall binds the steps that run server:uninstall and check what it removed and kept.
func initializeServerUninstall(sc *godog.ScenarioContext, s *installScenario) {
	sc.Given(`^server:install installed Gophenberg for example.com( with -web-server none)? on the server$`,
		s.installFirst)
	sc.Given(`^a first run for example.com was cut off in the caddy step once it wrote the Caddyfile$`, s.cutOffInCaddy)
	sc.Given(`^server:uninstall -yes was cut off at its (first|second) "(systemctl [^"]*)"$`, s.cutOffUninstall)
	sc.Given(`^the operator ran "([^"]*)"$`, s.ranEarlier)
	sc.Given(`^the person added the site example.org to /etc/caddy/Caddyfile$`, s.addPersonSite)
	sc.When(`^the person puts the line "([^"]*)" back in /etc/caddy/Caddyfile$`, s.putCaddyfileLineBack)
	sc.Given(`^the person's own Caddy serves example.com from /etc/caddy/Caddyfile$`, s.serveOwnCaddy)
	sc.Then(`^the server holds nothing at these paths:$`, s.holdsNothingAtPaths)
	sc.Then(`^the server still holds these paths:$`, s.stillHoldsPaths)
	sc.Then(`^the server holds /etc/caddy/Caddyfile as the caddy package wrote it$`, s.holdsPackageCaddyfile)
	sc.Then(`^/etc/caddy/Caddyfile lacks the line "([^"]*)"$`, s.caddyfileLacks)
	sc.Then(`^/etc/caddy/Caddyfile is as the person wrote it$`, s.caddyfileIsOwn)
	sc.Then(`^the server still has the system user gophenberg$`, s.keepsSiteUser)
	sc.Then(`^the server has no system user gophenberg$`, s.lostSiteUser)
	sc.Then(`^the install record marks only these steps ended: (.+)$`, s.recordMarksOnly)
	sc.Then(`^the install record marks no step$`, s.recordMarksNothing)
	sc.Then(`^the install record owns only these pieces: (.+)$`, s.recordOwnsOnly)
	sc.Then(`^the install record holds no SHA-256 of a Caddyfile$`, s.recordHoldsNoSum)
	sc.Then(`^the install record still holds the SHA-256 of the Caddyfile server:install wrote$`, s.recordKeepsSum)
}

// installFirst runs a first server:install for example.com that the flags answer, failing unless it went through.
func (s *installScenario) installFirst(withOwnWebServer string) error {
	line := "server:install -domain example.com -archive " + archivePath + " -admin-email admin@example.com " +
		"-admin-name Maria -admin-password-file " + passwordFile
	if withOwnWebServer != "" {
		line += " -web-server none"
	}
	s.putOwnArchive(archivePath)
	s.writeLine(passwordFile, goodPassword)
	return s.ranEarlier(line)
}

// ranEarlier runs line on the scenario's server before the run the scenario checks, failing unless it went through.
func (s *installScenario) ranEarlier(line string) error {
	if err := s.runLine(line); err != nil {
		return err
	}
	if s.err != nil {
		return fmt.Errorf("%s stopped with %w", line, s.err)
	}
	return nil
}

// cutOffInCaddy runs a first install whose restart of Caddy fails, so it stops in the caddy step after the Caddyfile.
func (s *installScenario) cutOffInCaddy() error {
	s.server.effects["systemctl"] = func(program Program) answer {
		if lineOf(program) == "systemctl restart caddy.service" {
			return failed("systemctl", "Job for caddy.service failed.\n")
		}
		return s.server.systemctl(program)
	}
	err := s.installFirst("")
	s.server.effects["systemctl"] = s.server.systemctl
	if err == nil {
		return errors.New("the first server:install went through, want it cut off in the caddy step")
	}
	if err := s.recordMarks(stepCaddy, stepStarted); err != nil {
		return err
	}
	return s.recordHoldsNoSum()
}

// cutOffUninstall runs server:uninstall -yes with the nth run of the systemctl line failing, failing unless it stopped.
func (s *installScenario) cutOffUninstall(nth, line string) error {
	s.server.answerRemovals()
	s.server.cutSystemctl(line, slices.Index([]string{"first", "second"}, nth)+1)
	_, _, err := uninstallOn(s.server, s.env, true, false)
	s.server.answerRemovals()
	if err == nil {
		return fmt.Errorf("server:uninstall -yes went through, want it cut off at its %s %s", nth, line)
	}
	return nil
}

// cutSystemctl has the nth run of the systemctl line fail as a run cut off there stops, every other run answered.
func (s *fakeServer) cutSystemctl(line string, nth int) {
	answering, seen := s.effects["systemctl"], 0
	s.effects["systemctl"] = func(program Program) answer {
		if lineOf(program) != line {
			return answering(program)
		}
		seen++
		if seen != nth {
			return answering(program)
		}
		return failed("systemctl", "Failed to connect to bus: Connection reset by peer\n")
	}
}

// runUninstall runs the server:uninstall line on the scenario's server, its programs answered as a real server does.
func (s *installScenario) runUninstall(line string) error {
	flags := flag.NewFlagSet(uninstallCommand, flag.ContinueOnError)
	apply, deleteData := flags.Bool("yes", false, ""), flags.Bool("delete-data", false, "")
	if err := flags.Parse(strings.Fields(line)[1:]); err != nil {
		return err
	}
	s.server.answerRemovals()
	s.before = s.server.snapshot()
	s.server.forgetRun()
	s.stdout, s.stderr, s.err = uninstallOn(s.server, s.env, *apply, *deleteData)
	return nil
}

// answerRemovals has the fake server answer the programs server:uninstall runs, as a real server does.
func (s *fakeServer) answerRemovals() {
	s.answers["dropdb"], s.answers["dropuser"] = answer{}, answer{}
	s.effects["userdel"], s.effects["systemctl"] = s.userdel, s.removingSystemctl
}

// removingSystemctl stops the unit systemctl disable --now names, then answers as systemctl does.
func (s *fakeServer) removingSystemctl(program Program) answer {
	if slices.Contains(program.Args, "--now") {
		s.running[program.Args[len(program.Args)-1]] = false
	}
	return s.systemctl(program)
}

// userdel removes the OS user its line names, as userdel does.
func (s *fakeServer) userdel(program Program) answer {
	delete(s.users, program.Args[len(program.Args)-1])
	return answer{}
}

// addPersonSite adds a site caddy validate takes to the Caddyfile, as a person does by hand.
func (s *installScenario) addPersonSite() error {
	held, err := os.ReadFile(s.server.at(caddyfilePath))
	if err != nil {
		return err
	}
	s.server.write(caddyfilePath, string(held)+personSite)
	return nil
}

// putCaddyfileLineBack adds line to the end of the Caddyfile, as a person does by hand.
func (s *installScenario) putCaddyfileLineBack(line string) error {
	held, err := os.ReadFile(s.server.at(caddyfilePath))
	if err != nil {
		return err
	}
	s.server.write(caddyfilePath, string(held)+line+"\n")
	return nil
}

// serveOwnCaddy writes the Caddyfile of a Caddy the person set up to send example.com to the site.
func (s *installScenario) serveOwnCaddy() {
	s.server.write(caddyfilePath, ownCaddyfile)
}

// holdsNothingAtPaths fails when the server holds anything at one of the paths listed, one a line.
func (s *installScenario) holdsNothingAtPaths(paths *godog.DocString) error {
	for _, path := range strings.Split(paths.Content, "\n") {
		if err := s.holdsNothingAt(path); err != nil {
			return err
		}
	}
	return nil
}

// stillHoldsPaths fails unless the server holds something at each of the paths listed, one a line.
func (s *installScenario) stillHoldsPaths(paths *godog.DocString) error {
	for _, path := range strings.Split(paths.Content, "\n") {
		if _, err := os.Lstat(s.server.at(path)); err != nil {
			return fmt.Errorf("the server holds nothing at %s, want it kept: %w", path, err)
		}
	}
	return nil
}

// holdsPackageCaddyfile fails unless the Caddyfile on the server is the one the caddy package wrote.
func (s *installScenario) holdsPackageCaddyfile() error {
	return s.holdsFile(caddyfilePath, "root", "0644", packageCaddyfile)
}

// caddyfileLacks fails when the Caddyfile on the server holds line.
func (s *installScenario) caddyfileLacks(line string) error {
	held, err := os.ReadFile(s.server.at(caddyfilePath))
	if err == nil && slices.Contains(trimmedLines(string(held)), line) {
		err = fmt.Errorf("the server holds the Caddyfile\n%s\nwant no line %q", held, line)
	}
	return err
}

// caddyfileIsOwn fails unless the Caddyfile on the server is the one the person wrote.
func (s *installScenario) caddyfileIsOwn() error {
	return s.holdsFile(caddyfilePath, "root", "0644", ownCaddyfile)
}

// keepsSiteUser fails unless the server still has the OS user gophenberg.
func (s *installScenario) keepsSiteUser() error {
	if _, held := s.server.users[siteUser]; !held {
		return errors.New("the server has no user gophenberg, want it kept")
	}
	return nil
}

// lostSiteUser fails when the server still has the OS user gophenberg.
func (s *installScenario) lostSiteUser() error {
	if _, held := s.server.users[siteUser]; held {
		return errors.New("the server still has the user gophenberg, want it removed")
	}
	return nil
}

// recordOnServer returns the install record on the scenario's server.
func (s *installScenario) recordOnServer() (Record, error) {
	return readRecord(s.server.machine())
}

// recordMarksOnly fails unless the install record marks each step the list names ended, and no other step at all.
func (s *installScenario) recordMarksOnly(list string) error {
	record, err := s.recordOnServer()
	if err != nil {
		return err
	}
	want := map[string]string{}
	for _, id := range strings.Split(list, ", ") {
		want[id] = stepEnded
	}
	if !maps.Equal(record.Steps, want) {
		return fmt.Errorf("the install record marks %v, want %v", record.Steps, want)
	}
	return nil
}

// recordMarksNothing fails when the install record marks any step.
func (s *installScenario) recordMarksNothing() error {
	record, err := s.recordOnServer()
	if err == nil && len(record.Steps) != 0 {
		err = fmt.Errorf("the install record marks %v, want no step", record.Steps)
	}
	return err
}

// recordOwnsOnly fails unless the install record owns the pieces the list names, in order, and no other.
func (s *installScenario) recordOwnsOnly(list string) error {
	record, err := s.recordOnServer()
	if err == nil && !slices.Equal(record.Installed, strings.Split(list, ", ")) {
		err = fmt.Errorf("the install record owns %q, want %s", record.Installed, list)
	}
	return err
}

// recordHoldsNoSum fails when the install record holds the SHA-256 of any Caddyfile.
func (s *installScenario) recordHoldsNoSum() error {
	record, err := s.recordOnServer()
	if err == nil && record.Caddyfile != "" {
		err = fmt.Errorf("the install record holds the Caddyfile sum %q, want none", record.Caddyfile)
	}
	return err
}

// recordKeepsSum fails unless the install record holds the SHA-256 of the Caddyfile a first run for example.com writes.
func (s *installScenario) recordKeepsSum() error {
	record, err := s.recordOnServer()
	if err == nil && record.Caddyfile != caddyfileSum(wantedCaddyfile) {
		err = fmt.Errorf("the install record holds the Caddyfile sum %q, want that of\n%s", record.Caddyfile,
			wantedCaddyfile)
	}
	return err
}

func TestServerUninstall(t *testing.T) {
	t.Parallel()

	runFeature(t, serverUninstallFeature)
}
