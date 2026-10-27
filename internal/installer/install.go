// SPDX-License-Identifier: Apache-2.0

package installer

import (
	"context"
	"errors"
	"fmt"
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

// Install checks machine for server:install as request asks, then prints the steps of a dry run or checks the rest.
func Install(ctx context.Context, machine Machine, call gonsole.Call, request Request) error {
	check, err := checkFor(ctx, machine, call, request)
	if err != nil {
		return err
	}
	if !request.DryRun {
		return install(ctx, call, request, check)
	}
	planned, err := siteOn(machine, check)
	if err != nil {
		return err
	}
	return printSteps(call.Stdout, planned.steps())
}

// checkFor reads the settings and the install record of the run request asks for, then checks machine against them.
func checkFor(ctx context.Context, machine Machine, call gonsole.Call, request Request) (*machineCheck, error) {
	settings, err := settingsOn(machine, call.Env, request)
	if err != nil {
		return nil, rootOr(machine, installCommand, err)
	}
	record, err := readRecord(machine)
	if err != nil {
		return nil, rootOr(machine, installCommand, err)
	}
	if err := record.allows(version.Version()); err != nil {
		return nil, err
	}
	if err := record.allowsWebServer(settings, call.Env); err != nil {
		return nil, err
	}
	check := &machineCheck{
		machine: machine, settings: settings, request: request, record: record, env: call.Env, notes: call.Stderr,
	}
	return check, check.run(ctx)
}

// install opens the release archive and asks for the first administrator, then stops, as this build changes nothing.
func install(ctx context.Context, call gonsole.Call, request Request, check *machineCheck) error {
	archive, err := check.openArchive(request.Archive)
	if err != nil {
		return err
	}
	defer closeArchive(archive)
	if !check.record.ended(stepAdmin) {
		answered, err := askAdmin(ctx, check.machine, request, check.typedEmailRule())
		if err != nil {
			return err
		}
		request.AdminEmail = answered.email
		if check.settings, err = settingsOn(check.machine, call.Env, request); err != nil {
			return err
		}
	}
	return stop("this build of server:install only checks the server, and -dry-run prints the steps it would take")
}

// typedEmailRule returns the rule a typed email keeps, the certificate email's too while no setting gives one.
func (c *machineCheck) typedEmailRule() func(string) (string, error) {
	if c.settings.CertificateEmail != "" {
		return checkEmail
	}
	return func(typed string) (string, error) {
		email, err := checkEmail(typed)
		if err != nil {
			return "", err
		}
		if _, err := certificateEmail(email); err != nil {
			return "", fmt.Errorf("the email also becomes %s, which %w", c.env.Key("INSTALL_CERTIFICATE_EMAIL"), err)
		}
		return email, nil
	}
}

// rootOr returns the stop of a run of command that is not root's for err on a file root alone may read, else err.
func rootOr(machine Machine, command string, err error) error {
	if errors.Is(err, fs.ErrPermission) && machine.Euid() != 0 {
		return notRoot(command)
	}
	return err
}
