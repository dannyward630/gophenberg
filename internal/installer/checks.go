// SPDX-License-Identifier: Apache-2.0

package installer

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/gopherium/framework/gonsole"
)

// nothingChanged ends every message a machine check stops the run with.
const nothingChanged = ", so nothing was changed"

// installCommand is the name of the command that installs Gophenberg on a server.
const installCommand = "server:install"

// varLib is the folder PostgreSQL, Caddy and the install record keep their data under, from the top of the server.
const varLib = "/var/lib"

// caddyfilePath is Caddy's own settings file, which the first run writes and no later run writes again.
const caddyfilePath = "/etc/caddy/Caddyfile"

// caddyfileLines are the lines of the Caddyfile the first run writes that every later run needs, each with why.
var caddyfileLines = []struct{ line, why string }{
	{"import " + caddySitePath, "which has Caddy serve the site"},
	{"admin unix//run/caddy/admin.sock", "which keeps Caddy's admin address off the loopback port any local user can " +
		"reach"},
}

// commandHeader is how the gophenberg command server:install writes begins, which shows that it wrote the file.
var commandHeader = strings.Join(strings.SplitAfter(render("gophenberg", fileData{}), "\n")[:2], "")

// sandboxProbe runs /bin/true in the sandbox of the site's unit, which only a server that can run the unit passes.
var sandboxProbe = Program{Name: "systemd-run", Args: []string{
	"--wait", "--collect", "-p", "ProtectSystem=strict", "-p", "PrivateTmp=yes", "-p", "NoNewPrivileges=yes",
	"/bin/true",
}}

// clockProbe asks whether the clock of the server is synchronized, which it answers with yes or no.
var clockProbe = Program{Name: "timedatectl", Args: []string{"show", "-p", "NTPSynchronized", "--value"}}

// machineChecks are the checks of the machine in the order server:install runs them, before anything changes.
var machineChecks = []func(context.Context, *machineCheck) error{
	checkRoot, checkSystem, checkSystemd, checkSandbox, checkMemory, checkInstallRoot, checkCurrent, checkDisk,
	checkSitePort, checkPorts, checkPieces, checkCommand, checkCaddyfile, checkDatabaseURL, checkDomain, checkClock,
}

// machineCheck is one run of the machine checks, with what they read, where their notes go and what they find.
type machineCheck struct {
	machine  Machine
	settings Settings
	request  Request
	record   Record
	env      gonsole.Env
	notes    io.Writer
	found    facts
}

// facts is what the machine checks find that the steps of server:install depend on.
type facts struct {
	systemd int
	current string
}

// run runs every machine check in turn and stops at the first one that fails.
func (c *machineCheck) run(ctx context.Context) error {
	for _, check := range machineChecks {
		if err := check(ctx, c); err != nil {
			return err
		}
	}
	return nil
}

// stop returns the error a check stops the run with, format and args saying why, followed by nothing changed.
func stop(format string, args ...any) error {
	return fmt.Errorf(format+nothingChanged, args...)
}

// checkRoot stops a run that is not root's, since the steps after the checks change the whole server.
func checkRoot(_ context.Context, c *machineCheck) error {
	if c.machine.Euid() != 0 {
		return notRoot(installCommand)
	}
	return nil
}

// notRoot returns the stop of a run of command that is not root's.
func notRoot(command string) error {
	return stop("%s must run as root, such as with sudo", command)
}

// checkCommand stops where a gophenberg command server:install did not write sits on the path, which it would replace.
func checkCommand(_ context.Context, c *machineCheck) error {
	info, err := c.machine.lstat(commandPath)
	if err != nil || info == nil {
		return err
	}
	head := ""
	if info.Mode().IsRegular() {
		if head, err = c.machine.head(commandPath, len(commandHeader)); err != nil {
			return fmt.Errorf("installer: %w", err)
		}
	}
	if head != commandHeader {
		return stop("this server already has a %s that server:install did not write, and server:install puts the "+
			"gophenberg command there", commandPath)
	}
	return nil
}

// checkCaddyfile stops a later run whose Caddyfile lost a line the first run wrote, since no later run writes it again.
func checkCaddyfile(_ context.Context, c *machineCheck) error {
	if !c.settings.withCaddy() || !c.record.ended(stepCaddy) {
		return nil
	}
	body, err := c.machine.read(caddyfilePath)
	if err != nil {
		return err
	}
	held := trimmedLines(string(body))
	for _, needed := range caddyfileLines {
		if !slices.Contains(held, needed.line) {
			return stop("%s lacks the line %q that an earlier run wrote, %s, and it must go back in before "+
				"server:install runs again", caddyfilePath, needed.line, needed.why)
		}
	}
	return nil
}

// head returns at most n bytes from the start of the file at path, named from the top of the server.
func (m Machine) head(path string, n int) (string, error) {
	file, err := os.Open(filepath.Join(m.Root, path))
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	read, err := io.ReadAll(io.LimitReader(file, int64(n)))
	return string(read), err
}

// trimmedLines returns each line of text without the spaces and tabs around it.
func trimmedLines(text string) []string {
	var lines []string
	for line := range strings.Lines(text) {
		lines = append(lines, strings.TrimSpace(line))
	}
	return lines
}

// checkSandbox stops where systemd cannot set up the site's sandbox, naming what systemd-detect-virt reports.
func checkSandbox(ctx context.Context, c *machineCheck) error {
	if _, err := c.machine.Run(ctx, sandboxProbe); err == nil {
		return nil
	}
	virtual, _ := c.machine.Run(ctx, Program{Name: "systemd-detect-virt"})
	return stop("systemd cannot set up the sandbox the site's service runs in on this server, as on a container plan "+
		"without nesting, and systemd-detect-virt reports %s", cmp.Or(strings.TrimSpace(virtual), "nothing"))
}

// checkDisk stops on less free disk than GOPHENBERG_INSTALL_DISK_FLOOR_MB under the install root or /var/lib.
func checkDisk(_ context.Context, c *machineCheck) error {
	for _, path := range []string{c.settings.Root, varLib} {
		if err := c.checkFree(path); err != nil {
			return err
		}
	}
	return nil
}

// checkFree stops when the file system that holds path has less free disk than GOPHENBERG_INSTALL_DISK_FLOOR_MB.
func (c *machineCheck) checkFree(path string) error {
	under, available, err := c.machine.freeUnder(path)
	if err != nil {
		return fmt.Errorf("installer: %w", err)
	}
	floor := c.settings.Checks.DiskFloorMB
	if available < uint64(floor)<<20 {
		return stop("this server has %d MB free under %s, less than the %d MB %s asks", available>>20, under, floor,
			c.env.Key("INSTALL_DISK_FLOOR_MB"))
	}
	return nil
}

// checkClock warns when the clock of the server is not synchronized, which HTTPS and the signed update index need.
func checkClock(ctx context.Context, c *machineCheck) error {
	synchronized, _ := c.machine.Run(ctx, clockProbe)
	if strings.TrimSpace(synchronized) != "no" {
		return nil
	}
	_, err := fmt.Fprintln(c.notes, "warning: the clock of this server is not synchronized, and HTTPS and the signed "+
		"update index both need the right time, so turn on NTP with sudo timedatectl set-ntp true")
	return err
}
