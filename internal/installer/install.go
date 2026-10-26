// SPDX-License-Identifier: Apache-2.0

package installer

import (
	"context"
	"errors"
	"io/fs"

	"github.com/gopherium/framework/gonsole"

	"github.com/gopherium/gophenberg/internal/version"
)

// Request is what one run of server:install asks for on its command line.
type Request struct {
	Domain            string
	Archive           string
	AdminEmail        string
	AdminName         string
	AdminPasswordFile string
	WebServer         string
	SkipDomainCheck   bool
	DryRun            bool
}

// Install checks machine for server:install as request asks and, with -dry-run, prints every step it would take.
func Install(ctx context.Context, machine Machine, call gonsole.Call, request Request) error {
	settings, err := settingsOn(machine, call.Env, request)
	if err != nil {
		return rootOr(machine, err)
	}
	record, err := readRecord(machine)
	if err != nil {
		return rootOr(machine, err)
	}
	if err := record.allows(version.Version()); err != nil {
		return err
	}
	if err := record.allowsWebServer(settings, call.Env); err != nil {
		return err
	}
	check := machineCheck{
		machine: machine, settings: settings, request: request, record: record, env: call.Env, notes: call.Stderr,
	}
	if err := check.run(ctx); err != nil {
		return err
	}
	if !request.DryRun {
		return stop("this build of server:install only checks the server, and -dry-run prints the steps it would take")
	}
	planned, err := siteOn(machine, &check)
	if err != nil {
		return err
	}
	return printSteps(call.Stdout, planned.steps())
}

// rootOr returns the stop of a run that is not root's for err on a file root alone may read, else err itself.
func rootOr(machine Machine, err error) error {
	if errors.Is(err, fs.ErrPermission) && machine.Euid() != 0 {
		return notRoot()
	}
	return err
}
