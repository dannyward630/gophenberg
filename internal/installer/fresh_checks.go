// SPDX-License-Identifier: Apache-2.0

package installer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/gopherium/gophenberg/internal/updater"
)

// port is one port a check listens on briefly, what it is for, and the piece of the install record that holds it.
type port struct {
	network, address, what, piece string
}

// piece is something server:install installs, by its name in the record, what a person calls it and its paths.
type piece struct {
	name, what string
	paths      []string
}

// pieces are what server:install installs itself, each found on a server by any one of its paths.
var pieces = []piece{
	{piecePostgreSQL, "PostgreSQL", []string{"/etc/postgresql", "/usr/lib/postgresql", "/var/lib/postgresql"}},
	{pieceCaddy, "Caddy", []string{"/etc/caddy", "/usr/bin/caddy"}},
	{pieceService, "a gophenberg service", []string{
		"/etc/systemd/system/gophenberg.service", "/etc/systemd/system/gophenberg-rollback.service",
	}},
}

// trustedFolders hold the files root writes and the programs it runs, so the site's user must never own one.
var trustedFolders = append([]string{
	filepath.Dir(serverEnvPath), installerFolder, filepath.Dir(caddyKeyPath), "/etc/apt/sources.list.d",
	"/etc/apt/preferences.d", "/etc/apt/apt.conf.d", "/etc/systemd/system", "/etc/systemd/system/caddy.service.d",
	filepath.Dir(caddySitePath), filepath.Dir(installerCopy), maintenanceFolder, filepath.Dir(commandPath),
	"/usr/local/sbin",
}, searchFolders...)

// caddyPorts are the ports Caddy answers on, which a fresh server must leave free when the install sets Caddy up.
var caddyPorts = []port{
	{"tcp", ":80", "TCP port 80, which Caddy answers on", pieceCaddy},
	{"tcp", ":443", "TCP port 443, which Caddy answers on", pieceCaddy},
	{"udp", ":443", "UDP port 443, which Caddy answers HTTP/3 on", pieceCaddy},
}

// ports returns every port a fresh server must leave free for the site, PostgreSQL and, when set up, Caddy.
func (c *machineCheck) ports() []port {
	own := []port{
		{"tcp", c.settings.Addr.String(), c.siteAddress(), pieceService},
		{"tcp", ":5432", "TCP port 5432, which PostgreSQL answers on", piecePostgreSQL},
	}
	if !c.settings.withCaddy() {
		return own
	}
	return append(slices.Clone(caddyPorts), own...)
}

// siteAddress returns how a check names the address the site listens on and the setting that gives it.
func (c *machineCheck) siteAddress() string {
	return fmt.Sprintf("%s, the %s the site listens on", c.settings.Addr, c.env.Key("ADDR"))
}

// checkSitePort stops when the site's address takes the TCP port of another piece the install sets up, on any run.
func checkSitePort(_ context.Context, c *machineCheck) error {
	site := portNumber(c.settings.Addr.String())
	for _, other := range c.ports() {
		if other.network != "tcp" || other.piece == pieceService {
			continue
		}
		if portNumber(other.address) == site {
			return stop("%s, takes %s, and %s must name another port, in the environment or in its line in %s",
				c.siteAddress(), other.what, c.env.Key("ADDR"), serverEnvPath)
		}
	}
	return nil
}

// portNumber returns the port number of address, a host and a port such as 127.0.0.1:8081 or :80.
func portNumber(address string) string {
	_, number, _ := net.SplitHostPort(address)
	return number
}

// checkPorts stops when something listens on a port the install needs, skipping the ports of a piece the record owns.
func checkPorts(_ context.Context, c *machineCheck) error {
	for _, wanted := range c.ports() {
		if c.record.owns(wanted.piece) {
			continue
		}
		if err := c.machine.checkPort(wanted); err != nil {
			return err
		}
	}
	return nil
}

// checkPort stops when a brief listen on wanted fails, because something else holds it or the machine refuses it.
func (m Machine) checkPort(wanted port) error {
	listener, err := m.listenBriefly(wanted.network, wanted.address)
	if errors.Is(err, syscall.EADDRINUSE) {
		return stop("something already listens on %s", wanted.what)
	}
	if err != nil {
		return stop("cannot listen on %s, to check that it is free: %v", wanted.what, err)
	}
	return listener.Close()
}

// listenBriefly listens on address over network, udp or tcp, and returns what stops the listening.
func (m Machine) listenBriefly(network, address string) (io.Closer, error) {
	if network == "udp" {
		return m.ListenPacket(network, address)
	}
	return m.Listen(network, address)
}

// freshPieces returns the pieces a fresh server must not hold, Caddy left out when the person keeps a web server.
func (c *machineCheck) freshPieces() []piece {
	if c.settings.withCaddy() {
		return pieces
	}
	return slices.DeleteFunc(slices.Clone(pieces), func(held piece) bool { return held.name == pieceCaddy })
}

// checkPieces stops on a server that holds PostgreSQL, Caddy or a gophenberg service the record does not own.
func checkPieces(_ context.Context, c *machineCheck) error {
	for _, held := range c.freshPieces() {
		if c.record.owns(held.name) {
			continue
		}
		if found := slices.IndexFunc(held.paths, c.machine.there); found >= 0 {
			return stop("this server already has %s at %s, and server:install sets up a fresh server only", held.what,
				held.paths[found])
		}
	}
	return nil
}

// checkDatabaseURL stops a run given a database address of its own, since server:install sets up its own PostgreSQL.
func checkDatabaseURL(_ context.Context, c *machineCheck) error {
	if c.env.Value("DATABASE_URL") != "" {
		return stop("%s is set, and server:install sets up its own PostgreSQL on a fresh server",
			c.env.Key("DATABASE_URL"))
	}
	return nil
}

// checkInstallRoot stops unless the install root is a new or empty folder of its own under parents root alone writes.
func checkInstallRoot(_ context.Context, c *machineCheck) error {
	for _, parent := range parentsOf(c.settings.Root) {
		if err := c.checkParent(parent); err != nil {
			return err
		}
	}
	if err := c.checkRootFolder(); err != nil {
		return err
	}
	return c.checkTrusted()
}

// checkTrusted stops when the install root is or holds a trusted folder, which the site's user would then own.
func (c *machineCheck) checkTrusted() error {
	root := c.settings.Root
	found := slices.IndexFunc(trustedFolders, func(folder string) bool { return holds(root, folder) })
	if found < 0 {
		return nil
	}
	return stop("the install root %s would hand %s, whose files root runs or reads, to the site's user, and %s must "+
		"name another folder", root, trustedFolders[found], c.env.Key("INSTALL_ROOT"))
}

// holds reports whether folder is root itself or a folder under it, both clean absolute paths.
func holds(root, folder string) bool {
	return folder == root || strings.HasPrefix(folder, strings.TrimSuffix(root, "/")+"/")
}

// parentsOf returns every parent of path, named from the top of the server, the top first.
func parentsOf(path string) []string {
	var parents []string
	for path != "/" {
		path = filepath.Dir(path)
		parents = append(parents, path)
	}
	slices.Reverse(parents)
	return parents
}

// checkParent stops when parent of the install root is a link, a file or a folder a user other than root may write.
func (c *machineCheck) checkParent(parent string) error {
	info, err := c.machine.lstat(parent)
	if err != nil || info == nil {
		return err
	}
	if !info.IsDir() {
		return stop("%s, a parent of the install root %s, is not a real folder", parent, c.settings.Root)
	}
	if !ownedByRoot(info) || info.Mode().Perm()&0o022 != 0 {
		return stop("%s, a parent of the install root %s, must be owned by root and writable by root alone", parent,
			c.settings.Root)
	}
	return nil
}

// checkInstallerFolder stops when the folder of the install record is a link, a file or others may write it.
func checkInstallerFolder(machine Machine) error {
	info, err := machine.lstat(installerFolder)
	if err != nil || info == nil {
		return err
	}
	if info.IsDir() && rootAloneWrites(info) {
		return nil
	}
	return stop("%s, which holds the install record, must be a real folder owned by root and writable by root alone",
		installerFolder)
}

// rootAloneWrites reports whether root owns the file info describes and no other user may write it.
func rootAloneWrites(info fs.FileInfo) bool {
	return ownedByRoot(info) && info.Mode().Perm()&0o022 == 0
}

// ownedByRoot reports whether root owns the file info describes.
func ownedByRoot(info fs.FileInfo) bool {
	stat, described := info.Sys().(*syscall.Stat_t)
	return described && stat.Uid == 0
}

// checkCurrent finds the build the current link of the install root names, stopping at a link that names none.
func checkCurrent(_ context.Context, c *machineCheck) error {
	layout := updater.Layout{Root: filepath.Join(c.machine.Root, c.settings.Root)}
	current, err := layout.Named(updater.Current)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return stop("cannot tell which build %s/current names: %v", c.settings.Root, err)
	}
	if err := c.checkProgram(current); err != nil {
		return err
	}
	c.found.current = current
	return nil
}

// checkProgram stops when the build called current has no program anyone may run, as after a delete by hand.
func (c *machineCheck) checkProgram(current string) error {
	program := updater.Layout{Root: c.settings.Root}.Binary(current)
	info, err := c.machine.lstat(program)
	switch {
	case err != nil:
		return err
	case info == nil:
		return stop("cannot tell which build %s/current names: %s is not there", c.settings.Root, program)
	case !info.Mode().IsRegular():
		return stop("cannot tell which build %s/current names: %s is not a file", c.settings.Root, program)
	case info.Mode().Perm()&0o111 == 0:
		return stop("cannot tell which build %s/current names: %s is not executable", c.settings.Root, program)
	}
	return nil
}

// checkRootFolder stops when the install root is a link, a file or a folder holding files the record does not own.
func (c *machineCheck) checkRootFolder() error {
	root := c.settings.Root
	info, err := c.machine.lstat(root)
	if err != nil || info == nil {
		return err
	}
	if !info.IsDir() {
		return stop("the install root %s is not a real folder", root)
	}
	if c.record.owns(root) {
		return nil
	}
	empty, err := c.machine.empty(root)
	if err != nil {
		return err
	}
	if !empty {
		return stop("the install root %s already holds files that server:install did not put there, and %s must "+
			"name an empty or a new folder", root, c.env.Key("INSTALL_ROOT"))
	}
	return nil
}
