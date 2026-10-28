// SPDX-License-Identifier: Apache-2.0

package installer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/gopherium/framework/gonsole"
)

// stepCaddy is the name the install record keeps the step that writes Caddy's files under.
const stepCaddy = "caddy"

// stepStart is the name the install record keeps the step that starts the site under.
const stepStart = "start"

// stepAdmin is the name the install record keeps the step that creates the first administrator under.
const stepAdmin = "admin"

// stepEnded is how the install record marks a step that ended.
const stepEnded = "ended"

// unattendedUpgrade is the program of unattended-upgrades, there while that package is installed.
const unattendedUpgrade = "/usr/bin/unattended-upgrade"

// keptValue is what a dry run prints for the value of a line the person added to the site's .env.
const keptValue = "(kept as it is)"

// dryRunLead is the first line a dry run prints, once every check passed.
const dryRunLead = "Every check passed, and nothing was changed. Without -dry-run, server:install would take these " +
	"steps.\n"

// step is one step server:install takes, with the files it writes, the keys a dry run hides, its text and its change.
type step struct {
	id     string
	does   string
	files  []file
	hides  []string
	prints string
	apply  func(*installing, context.Context, step) error
}

// file is one file a step writes: its path from the top of the server, the user that owns it, its mode and its text.
type file struct {
	path  string
	owner string
	mode  fs.FileMode
	text  string
}

// site is what the steps of one run write their files and print their text from.
type site struct {
	settings    Settings
	record      Record
	env         gonsole.Env
	systemd     int
	systemctl   string
	runuser     string
	unattended  bool
	current     bool
	missing     []string
	dotEnv      map[string]string
	graces      []time.Duration
	uploadCapMB int
}

// siteOn returns the site the steps of the run check checked write their files from, as machine holds it.
func siteOn(machine Machine, check *machineCheck) (site, error) {
	systemctl, err := machine.program("systemctl")
	if err != nil {
		return site{}, err
	}
	runuser, err := machine.program("runuser")
	if err != nil {
		return site{}, err
	}
	root := check.settings.Root
	dotEnv, err := machine.siteEnv(root)
	if err != nil {
		return site{}, err
	}
	graces, uploadCap, err := siteLimits(gonsole.Env{Prefix: check.env.Prefix, Getenv: lookup(dotEnv)}, check.settings)
	if err != nil {
		return site{}, err
	}
	return site{
		settings: check.settings, record: check.record, env: check.env, systemd: check.found.systemd,
		systemctl: systemctl, runuser: runuser, unattended: machine.there(unattendedUpgrade),
		current: check.found.current != "", missing: slices.DeleteFunc(parentsOf(root), machine.there), dotEnv: dotEnv,
		graces: graces, uploadCapMB: uploadCap,
	}, nil
}

// siteLimits returns the stop graces and, for the person's own web server, the upload cap the site's .env names.
func siteLimits(dotEnv gonsole.Env, settings Settings) ([]time.Duration, int, error) {
	graces, gracesErr := StopGraces(dotEnv)
	uploadCap, uploadCapErr := ownUploadCap(dotEnv, settings)
	if err := errors.Join(gracesErr, uploadCapErr); err != nil {
		return nil, 0, fmt.Errorf("%s: %w", filepath.Join(settings.Root, ".env"), err)
	}
	return graces, uploadCap, nil
}

// ownUploadCap returns the upload cap dotEnv names for the lines of the person's web server, none with Caddy.
func ownUploadCap(dotEnv gonsole.Env, settings Settings) (int, error) {
	if settings.withCaddy() {
		return 0, nil
	}
	return UploadCapMB(dotEnv)
}

// steps returns every step a run takes once the checks pass, in order, each with what it writes and prints.
func (s site) steps() []step {
	planned := s.setupSteps()
	if s.settings.withCaddy() {
		planned = append(planned, s.caddyStep())
	}
	planned = append(planned, s.startStep())
	if !s.record.ended(stepAdmin) {
		planned = append(planned, step{
			id: stepAdmin, does: "create the first administrator with account:create-admin as gophenberg",
			apply: (*installing).createAdmin,
		})
	}
	return append(planned, s.closingSteps()...)
}

// setupSteps returns the steps from the server settings to the command on the path, which come before Caddy's.
func (s site) setupSteps() []step {
	root := s.settings.Root
	return []step{
		{id: "server-env", does: "start the install record " + recordPath + " and write the server settings",
			files: []file{{path: serverEnvPath, owner: "root", mode: 0o600, text: s.settings.serverEnv}},
			apply: (*installing).writeOnly},
		s.packagesStep(),
		{id: "user", does: "make the system user gophenberg, which runs the site and signs in to PostgreSQL",
			apply: (*installing).user},
		{id: "install-root", does: s.installRootDoes(), apply: (*installing).installRoot},
		{id: "database", does: "make the database role gophenberg and its database gophenberg as the postgres user",
			apply: (*installing).database},
		s.buildStep(),
		{id: "installer-copy", does: "keep a copy of this installer at " + installerCopy + ", the one root runs for " +
			"server:install and server:uninstall", apply: (*installing).keepCopy},
		{id: "site-env", does: "write the site's settings, then run check as gophenberg in " + root,
			files: []file{s.siteEnvFile()}, hides: s.keptKeys(), apply: (*installing).writeSiteEnv},
		s.unitsStep(),
		{id: "command", does: "write the gophenberg command on the path", files: []file{s.commandFile()},
			apply: (*installing).writeOnly},
	}
}

// packagesStep returns the step that installs PostgreSQL, and Caddy from its own repository when the run sets it up.
func (s site) packagesStep() step {
	if !s.settings.withCaddy() {
		return step{id: "packages", does: "install PostgreSQL with apt-get", apply: (*installing).packages}
	}
	return step{id: "packages", does: "install PostgreSQL and Caddy with apt-get, Caddy from its own apt repository",
		files: s.aptFiles(), apply: (*installing).packages}
}

// unitsStep returns the step that writes the systemd units, and Caddy's drop-in when the run sets Caddy up.
func (s site) unitsStep() step {
	does := "write the systemd units and Caddy's drop-in, then reload systemd and enable gophenberg.service"
	if !s.settings.withCaddy() {
		does = "write the systemd units, then reload systemd and enable gophenberg.service"
	}
	return step{id: "units", does: does, files: s.unitFiles(), apply: (*installing).units}
}

// closingSteps returns the steps after the site starts: the HTTPS check with Caddy, else the person's web server lines.
func (s site) closingSteps() []step {
	summary := step{id: "summary", does: "print the summary and end the install record", apply: (*installing).summary}
	if !s.settings.withCaddy() {
		return []step{summary, s.webServerStep()}
	}
	https := step{id: "https", does: fmt.Sprintf("check that https://%s/admin/ answers within %s", s.settings.Domain,
		s.settings.Checks.HTTPSWait), apply: (*installing).https}
	return []step{https, summary}
}

// webServerStep returns the step that prints the lines the person's web server needs to send the domain to the site.
func (s site) webServerStep() step {
	return step{id: "web-server", does: fmt.Sprintf("print the lines for nginx, Caddy, Traefik and Apache that send %s "+
		"to the site at %s", s.settings.Domain, s.settings.Addr), prints: render("web-server.txt", s.data()),
		apply: (*installing).printWebServer}
}

// buildStep returns the step that unpacks the release archive, or keeps the build current names once there is one.
func (s site) buildStep() step {
	current := s.settings.Root + "/current"
	if s.current {
		return step{id: "build", does: "keep the build " + current + " names, since updates go through update:apply",
			apply: (*installing).build}
	}
	return step{id: "build", does: "unpack the release archive into " + s.settings.Root + "/versions and point " +
		current + " at it", apply: (*installing).build}
}

// caddyStep returns the step that writes Caddy's files and restarts Caddy, or reloads it once the record holds the sum.
func (s site) caddyStep() step {
	does := "write Caddy's files, keep the package's Caddyfile as " + keptCaddyfilePath + ", check them with caddy " +
		"validate and restart Caddy"
	if s.caddyfileWritten() {
		does = "write Caddy's files, check them with caddy validate and reload Caddy"
	}
	return step{id: stepCaddy, does: does, files: s.caddyFiles(), apply: (*installing).caddy}
}

// caddyfileWritten reports whether the record holds the first run's Caddyfile sum, cleared by server:uninstall alone.
func (s site) caddyfileWritten() bool {
	return s.record.Caddyfile != ""
}

// startStep returns the step that starts the site and waits for its pages, or restarts it once the record owns it.
func (s site) startStep() step {
	return step{id: stepStart, does: s.startVerb() + " gophenberg.service and wait until /admin/, /api/locale and / " +
		"answer on " + s.settings.Addr.String(), apply: (*installing).start}
}

// startVerb returns how the run starts the site: start, or restart once the record owns gophenberg.service.
func (s site) startVerb() string {
	if s.record.owns(pieceService) {
		return "restart"
	}
	return "start"
}

// printSteps prints each of steps, the text of every file it writes with the values it hides left out, and its text.
func printSteps(w io.Writer, steps []step) error {
	var answer strings.Builder
	answer.WriteString(dryRunLead)
	for n, planned := range steps {
		fmt.Fprintf(&answer, "\nStep %d: %s\n", n+1, planned.does)
		for _, written := range planned.files {
			fmt.Fprintf(&answer, "  writes %s, owned by %s with mode %04o:\n", written.path, written.owner,
				written.mode)
			writeIndented(&answer, hidden(written.text, planned.hides))
		}
		if planned.prints != "" {
			answer.WriteString("  prints:\n")
			writeIndented(&answer, planned.prints)
		}
	}
	_, err := io.WriteString(w, answer.String())
	return err
}

// hidden returns text with keptValue in place of the value of each line whose key is one of keys.
func hidden(text string, keys []string) string {
	var shown strings.Builder
	for line := range strings.Lines(text) {
		if key, _, _ := strings.Cut(line, "="); slices.Contains(keys, key) {
			line = key + "=" + keptValue + "\n"
		}
		shown.WriteString(line)
	}
	return shown.String()
}

// writeIndented writes text to answer with every line that is not empty indented by four spaces.
func writeIndented(answer *strings.Builder, text string) {
	for line := range strings.Lines(text) {
		if line != "\n" {
			answer.WriteString("    ")
		}
		answer.WriteString(line)
	}
}
