// SPDX-License-Identifier: Apache-2.0

package installer

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/gopherium/framework/gonsole"
)

// nothingChanged ends every message a machine check stops the run with.
const nothingChanged = ", so nothing was changed"

// varLib is the folder PostgreSQL, Caddy and the install record keep their data under, from the top of the server.
const varLib = "/var/lib"

// sandboxProbe runs /bin/true in the sandbox of the site's unit, which only a server that can run the unit passes.
var sandboxProbe = Program{Name: "systemd-run", Args: []string{
	"--wait", "--collect", "-p", "ProtectSystem=strict", "-p", "PrivateTmp=yes", "-p", "NoNewPrivileges=yes",
	"/bin/true",
}}

// clockProbe asks whether the clock of the server is synchronized, which it answers with yes or no.
var clockProbe = Program{Name: "timedatectl", Args: []string{"show", "-p", "NTPSynchronized", "--value"}}

// machineChecks are the checks of the machine in the order server:install runs them, before anything changes.
var machineChecks = []func(context.Context, *machineCheck) error{
	checkRoot, checkSystem, checkSystemd, checkSandbox, checkMemory, checkInstallRoot, checkDisk, checkPorts,
	checkPieces, checkDatabaseURL, checkDomain, checkClock,
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

// facts is what the machine checks find that the files server:install writes depend on.
type facts struct {
	systemd int
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
		return notRoot()
	}
	return nil
}

// notRoot returns the stop of a run that is not root's.
func notRoot() error {
	return stop("server:install must run as root, such as with sudo")
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
