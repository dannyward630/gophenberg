// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"flag"
	"strings"

	"github.com/gopherium/framework/gonsole"

	"github.com/gopherium/gophenberg/internal/installer"
)

// serverNamespace opens the name of every command root runs to set up this server, none of which reads .env.
const serverNamespace = "server:"

// ReadsDotEnv reports whether the command args name reads .env, which no server: command does, since root runs them.
func ReadsDotEnv(args []string) bool {
	return len(args) == 0 || !strings.HasPrefix(args[0], serverNamespace)
}

// serverCommands returns the commands that set up this server, reaching it through machine.
func serverCommands(machine installer.Machine) []gonsole.Command {
	return []gonsole.Command{{
		Name:    serverNamespace + "install",
		Summary: "install Gophenberg on this Linux server with PostgreSQL, Caddy and a systemd service",
		Flags:   installFlags,
		Run:     runInstall(machine),
	}, {
		Name:    serverNamespace + "uninstall",
		Summary: "remove what server:install set up on this server, keeping the site's data unless -delete-data",
		Flags:   uninstallFlags,
		Writes:  true,
		Run:     runUninstall(machine),
	}}
}

// uninstallFlags declares the flags of server:uninstall.
func uninstallFlags(fs *flag.FlagSet) {
	fs.Bool("delete-data", false, "also drop the database and its role, and remove the install root, "+
		"/etc/gophenberg/server.env and the system user gophenberg")
}

// runUninstall returns the run of server:uninstall on machine.
func runUninstall(machine installer.Machine) func(context.Context, gonsole.Call) error {
	return func(ctx context.Context, call gonsole.Call) error {
		return installer.Uninstall(ctx, machine, call, call.Flags["delete-data"] == "true")
	}
}

// installFlags declares the flags of server:install.
func installFlags(fs *flag.FlagSet) {
	fs.String("domain", "",
		"the `host` name the site answers at, needed on the first run and kept in /etc/gophenberg/server.env")
	fs.String("archive", "", "the checked release `file` install.sh downloaded into /var/lib/gophenberg-installer, "+
		"not needed once current exists")
	fs.String("admin-email", "", "the `email` of the first administrator, asked on the terminal when missing")
	fs.String("admin-name", "", "the `name` of the first administrator, asked on the terminal when missing")
	fs.String("admin-password-file", "",
		"the `file` whose first line is the first administrator's password, asked on the terminal when missing")
	fs.String("web-server", "", "the `name` of the web server in front of the site, caddy to have the install set "+
		"Caddy up or none to keep your own. Left out, a first run takes caddy and a later run keeps the one in "+
		"/etc/gophenberg/server.env")
	fs.Bool("skip-domain-check", false,
		"skip the check that the domain points at this server, for a server behind a proxy the check cannot cross")
	fs.Bool("dry-run", false,
		"run every check, then print each step and every file it would write, and change nothing")
}

// runInstall returns the run of server:install on machine.
func runInstall(machine installer.Machine) func(context.Context, gonsole.Call) error {
	return func(ctx context.Context, call gonsole.Call) error {
		return installer.Install(ctx, machine, call, requestOf(call.Flags))
	}
}

// requestOf returns what the flags of one server:install run ask for.
func requestOf(flags map[string]string) installer.Request {
	return installer.Request{
		Domain: flags["domain"], Archive: flags["archive"], AdminEmail: flags["admin-email"],
		AdminName: flags["admin-name"], AdminPasswordFile: flags["admin-password-file"], WebServer: flags["web-server"],
		SkipDomainCheck: flags["skip-domain-check"] == "true", DryRun: flags["dry-run"] == "true",
	}
}
