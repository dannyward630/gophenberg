// SPDX-License-Identifier: Apache-2.0

package installer

import (
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"testing"
)

// inUse returns the error a listen on address over network meets when something else holds the port.
func inUse(network, address string) error {
	return fmt.Errorf("listen %s %s: bind: %w", network, address, syscall.EADDRINUSE)
}

func TestTheMachineChecksStopWhenSomethingListensOnAPortTheInstallNeeds(t *testing.T) {
	t.Parallel()

	for held, what := range map[[2]string]string{
		{"tcp", ":80"}:            "TCP port 80, which Caddy answers on",
		{"tcp", ":443"}:           "TCP port 443, which Caddy answers on",
		{"udp", ":443"}:           "UDP port 443, which Caddy answers HTTP/3 on",
		{"tcp", "127.0.0.1:8081"}: "127.0.0.1:8081, the GOPHENBERG_ADDR the site listens on",
		{"tcp", ":5432"}:          "TCP port 5432, which PostgreSQL answers on",
	} {
		server := newFakeServer(t)
		server.taken[held[0]+" "+held[1]] = inUse(held[0], held[1])

		err := server.check().run(t.Context())

		wantRefused(t, err, "something already listens on "+what+", so nothing was changed")
	}
}

func TestTheMachineChecksWithTheirOwnWebServerLeaveOutThePortsCaddyAnswersOn(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	for _, held := range [][2]string{{"tcp", ":80"}, {"tcp", ":443"}, {"udp", ":443"}} {
		server.taken[held[0]+" "+held[1]] = inUse(held[0], held[1])
	}

	err := server.ownWebServerCheck().run(t.Context())

	server.wantPassed(err, "")
	if want := []string{"tcp 127.0.0.1:8081", "tcp :5432"}; !slices.Equal(server.listened, want) {
		t.Errorf("the checks with the person's own web server listened on %q, want %q", server.listened, want)
	}
}

func TestTheMachineChecksWithTheirOwnWebServerStillStopOnTheSitesPortAndPostgreSQLs(t *testing.T) {
	t.Parallel()

	for held, what := range map[string]string{
		"127.0.0.1:8081": "127.0.0.1:8081, the GOPHENBERG_ADDR the site listens on",
		":5432":          "TCP port 5432, which PostgreSQL answers on",
	} {
		server := newFakeServer(t)
		server.taken["tcp "+held] = inUse("tcp", held)

		err := server.ownWebServerCheck().run(t.Context())

		wantRefused(t, err, "something already listens on "+what+", so nothing was changed")
	}
}

func TestTheMachineChecksStopWhenTheyCannotListenOnAPort(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	server.taken["tcp :80"] = fmt.Errorf("listen tcp :80: bind: %w", syscall.EACCES)

	err := server.check().run(t.Context())

	wantRefused(t, err, "cannot listen on TCP port 80, which Caddy answers on, to check that it is free: listen tcp "+
		":80: bind: permission denied, so nothing was changed")
}

func TestTheMachineChecksSkipThePortsOfThePiecesTheRecordOwns(t *testing.T) {
	t.Parallel()

	for owned, listened := range map[string][]string{
		pieceCaddy:      {"tcp 127.0.0.1:8081", "tcp :5432"},
		pieceService:    {"tcp :80", "tcp :443", "udp :443", "tcp :5432"},
		piecePostgreSQL: {"tcp :80", "tcp :443", "udp :443", "tcp 127.0.0.1:8081"},
	} {
		server := newFakeServer(t)
		check := server.check()
		check.record.Installed = []string{owned}

		err := check.run(t.Context())

		server.wantPassed(err, "")
		if !slices.Equal(server.listened, listened) {
			t.Errorf("the checks over a record that owns %s listened on %q, want %q", owned, server.listened, listened)
		}
	}
}

func TestTheMachineChecksPassAServerWhosePortsAreHeldByThePiecesTheRecordOwns(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	for _, held := range [][2]string{
		{"tcp", ":80"}, {"tcp", ":443"}, {"udp", ":443"}, {"tcp", "127.0.0.1:8081"}, {"tcp", ":5432"},
	} {
		server.taken[held[0]+" "+held[1]] = inUse(held[0], held[1])
	}
	check := server.check()
	check.record.Installed = []string{piecePostgreSQL, pieceCaddy, pieceService}

	err := check.run(t.Context())

	server.wantPassed(err, "")
}

// sitePortTaken is how a site address that takes the port of another piece is refused, after the address and the port.
const sitePortTaken = "%s, the GOPHENBERG_ADDR the site listens on, takes %s, and GOPHENBERG_ADDR must name another " +
	"port, in the environment or in its line in /etc/gophenberg/server.env, so nothing was changed"

func TestTheMachineChecksStopASiteAddressThatTakesThePortOfAnotherPiece(t *testing.T) {
	t.Parallel()

	every := []string{piecePostgreSQL, pieceCaddy, pieceService}
	for _, taken := range []struct {
		webServer, addr, what string
		installed             []string
	}{
		{webServerCaddy, "127.0.0.1:80", "TCP port 80, which Caddy answers on", nil},
		{webServerCaddy, "127.0.0.1:443", "TCP port 443, which Caddy answers on", nil},
		{webServerCaddy, "[::1]:443", "TCP port 443, which Caddy answers on", nil},
		{webServerCaddy, "127.0.0.1:5432", "TCP port 5432, which PostgreSQL answers on", nil},
		{webServerNone, "127.0.0.1:5432", "TCP port 5432, which PostgreSQL answers on", nil},
		{webServerCaddy, "127.0.0.1:443", "TCP port 443, which Caddy answers on", every},
		{webServerNone, "[::1]:5432", "TCP port 5432, which PostgreSQL answers on", every},
	} {
		server := newFakeServer(t)
		check := server.check()
		check.settings.WebServer, check.settings.Addr = taken.webServer, netip.MustParseAddrPort(taken.addr)
		check.record.Installed = taken.installed

		err := check.run(t.Context())

		wantRefused(t, err, fmt.Sprintf(sitePortTaken, taken.addr, taken.what))
		if len(server.listened) != 0 {
			t.Errorf("the checks of the site address %s listened on %q, want no port", taken.addr, server.listened)
		}
	}
}

func TestTheMachineChecksTakeASiteAddressWhosePortNoOtherPieceAnswersOn(t *testing.T) {
	t.Parallel()

	for _, free := range []struct{ webServer, addr string }{
		{webServerNone, "127.0.0.1:80"}, {webServerNone, "127.0.0.1:443"}, {webServerNone, "127.0.0.1:8081"},
		{webServerCaddy, "127.0.0.1:8081"}, {webServerCaddy, "[::1]:8443"},
	} {
		server := newFakeServer(t)
		check := server.check()
		check.settings.WebServer, check.settings.Addr = free.webServer, netip.MustParseAddrPort(free.addr)

		err := check.run(t.Context())

		server.wantPassed(err, "")
	}
}

func TestTheMachineChecksStopOnAPieceAFreshServerDoesNotHold(t *testing.T) {
	t.Parallel()

	for path, what := range map[string]string{
		"/etc/postgresql":                                 "PostgreSQL",
		"/usr/lib/postgresql":                             "PostgreSQL",
		"/var/lib/postgresql":                             "PostgreSQL",
		"/etc/caddy":                                      "Caddy",
		"/usr/bin/caddy":                                  "Caddy",
		"/etc/systemd/system/gophenberg.service":          "a gophenberg service",
		"/etc/systemd/system/gophenberg-rollback.service": "a gophenberg service",
	} {
		server := newFakeServer(t)
		server.write(path, "")

		err := server.check().run(t.Context())

		wantRefused(t, err, "this server already has "+what+" at "+path+", and server:install sets up a fresh "+
			"server only, so nothing was changed")
	}
}

func TestTheMachineChecksWithTheirOwnWebServerLeaveOutTheCaddyTheRecordDoesNotOwn(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	server.folder("/etc/caddy")
	server.write("/usr/bin/caddy", "")

	err := server.ownWebServerCheck().run(t.Context())

	server.wantPassed(err, "")
}

func TestTheMachineChecksWithTheirOwnWebServerStillStopOnAnotherPiece(t *testing.T) {
	t.Parallel()

	for path, what := range map[string]string{
		"/etc/postgresql":                        "PostgreSQL",
		"/etc/systemd/system/gophenberg.service": "a gophenberg service",
	} {
		server := newFakeServer(t)
		server.folder("/etc/caddy")
		server.write(path, "")

		err := server.ownWebServerCheck().run(t.Context())

		wantRefused(t, err, "this server already has "+what+" at "+path+", and server:install sets up a fresh "+
			"server only, so nothing was changed")
	}
}

func TestTheMachineChecksFindAPieceByALinkThatLeadsNowhere(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	server.link("/usr/bin/caddy", "/opt/caddy")

	err := server.check().run(t.Context())

	wantRefused(t, err, "this server already has Caddy at /usr/bin/caddy, and server:install sets up a fresh server "+
		"only, so nothing was changed")
}

func TestTheMachineChecksSkipThePiecesTheRecordOwns(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	server.folder("/etc/postgresql", "/usr/lib/postgresql", "/var/lib/postgresql", "/etc/caddy")
	server.write("/usr/bin/caddy", "")
	server.write("/etc/systemd/system/gophenberg.service", "")
	server.write("/etc/systemd/system/gophenberg-rollback.service", "")
	check := server.check()
	check.record.Installed = []string{piecePostgreSQL, pieceCaddy, pieceService}

	err := check.run(t.Context())

	server.wantPassed(err, "")
}

func TestTheMachineChecksStopARunGivenADatabaseAddressOfItsOwn(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	check := server.check()
	check.env = envOf(map[string]string{"GOPHENBERG_DATABASE_URL": "postgres://gophenberg@db.example.com/gophenberg"})

	err := check.run(t.Context())

	wantRefused(t, err, "GOPHENBERG_DATABASE_URL is set, and server:install sets up its own PostgreSQL on a fresh "+
		"server, so nothing was changed")
}

func TestTheMachineChecksStopOnAParentOfTheInstallRootAnotherUserCouldWrite(t *testing.T) {
	t.Parallel()

	owned := newFakeServer(t)
	owned.owners["/opt"] = 1000
	ownedTop := newFakeServer(t)
	ownedTop.owners["/"] = 1000
	group := newFakeServer(t)
	group.chmod("/opt", 0o775)
	others := newFakeServer(t)
	others.chmod("/opt", 0o757)

	for server, parent := range map[*fakeServer]string{owned: "/opt", ownedTop: "/", group: "/opt", others: "/opt"} {
		err := server.check().run(t.Context())

		wantRefused(t, err, parent+", a parent of the install root /opt/gophenberg, must be owned by root and "+
			"writable by root alone, so nothing was changed")
	}
}

func TestTheMachineChecksStopOnAParentOfTheInstallRootThatIsNotARealFolder(t *testing.T) {
	t.Parallel()

	link := newFakeServer(t)
	link.remove("/opt")
	link.folder("/srv")
	link.link("/opt", "/srv")
	file := newFakeServer(t)
	file.remove("/opt")
	file.write("/opt", "")

	for _, server := range []*fakeServer{link, file} {
		err := server.check().run(t.Context())

		wantRefused(t, err, "/opt, a parent of the install root /opt/gophenberg, is not a real folder, so nothing "+
			"was changed")
	}
}

func TestTheMachineChecksTakeAnInstallRootWhoseParentsAreStillMissing(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	server.folder("/srv")
	check := server.check()
	check.settings.Root = "/srv/sites/gophenberg"

	err := check.run(t.Context())

	server.wantPassed(err, "")
}

func TestTheMachineChecksRefuseAParentOfTheInstallRootTheyCannotDescribe(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	server.broken["/opt"] = syscall.EIO

	err := server.check().run(t.Context())

	wantRefused(t, err, "installer: input/output error")
}

func TestTheMachineChecksStopOnAnInstallRootThatIsNotARealFolder(t *testing.T) {
	t.Parallel()

	link := newFakeServer(t)
	link.folder("/srv/gophenberg")
	link.link("/opt/gophenberg", "/srv/gophenberg")
	file := newFakeServer(t)
	file.write("/opt/gophenberg", "")

	for _, server := range []*fakeServer{link, file} {
		err := server.check().run(t.Context())

		wantRefused(t, err, "the install root /opt/gophenberg is not a real folder, so nothing was changed")
	}
}

func TestTheMachineChecksStopOnAnInstallRootThatHoldsFilesTheyDidNotPutThere(t *testing.T) {
	t.Parallel()

	for root, held := range map[string]string{
		"/opt/gophenberg": "/opt/gophenberg/.env", "/etc": "/etc/os-release", "/": "/etc/os-release",
	} {
		server := newFakeServer(t)
		server.write(held, ubuntuRelease)
		check := server.check()
		check.settings.Root = root

		err := check.run(t.Context())

		wantRefused(t, err, "the install root "+root+" already holds files that server:install did not put there, "+
			"and GOPHENBERG_INSTALL_ROOT must name an empty or a new folder, so nothing was changed")
	}
}

func TestTheMachineChecksStopOnAnInstallRootThatIsOrHoldsAFolderRootTrusts(t *testing.T) {
	t.Parallel()

	for root, trusted := range map[string]string{
		"/etc/gophenberg": "/etc/gophenberg", "/var/lib/gophenberg-installer": "/var/lib/gophenberg-installer",
		"/etc/apt/keyrings": "/etc/apt/keyrings", "/etc/apt": "/etc/apt/keyrings",
		"/etc/apt/preferences.d": "/etc/apt/preferences.d", "/usr/local": "/usr/local/lib/gophenberg",
		"/usr/local/share/gophenberg": "/usr/local/share/gophenberg/maintenance", "/usr/local/bin": "/usr/local/bin",
		"/usr/local/sbin": "/usr/local/sbin", "/sbin": "/sbin", "/bin": "/bin",
	} {
		server := newFakeServer(t)
		check := server.check()
		check.settings.Root = root

		err := check.run(t.Context())

		wantRefused(t, err, "the install root "+root+" would hand "+trusted+", whose files root runs or reads, to "+
			"the site's user, and GOPHENBERG_INSTALL_ROOT must name another folder, so nothing was changed")
	}
}

func TestTheMachineChecksStopOnATrustedFolderEvenWhenTheRecordOwnsTheInstallRoot(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	server.folder("/usr/local/bin")
	check := server.check()
	check.settings.Root = "/usr/local/bin"
	check.record.Installed = []string{"/usr/local/bin"}

	err := check.run(t.Context())

	wantRefused(t, err, "the install root /usr/local/bin would hand /usr/local/bin, whose files root runs or reads, "+
		"to the site's user, and GOPHENBERG_INSTALL_ROOT must name another folder, so nothing was changed")
}

func TestTheTrustedFoldersHoldEveryFileRootWrites(t *testing.T) {
	t.Parallel()

	running := firstSite(t, "example.com", 255)
	running.unattended = true

	for _, path := range rootWritten(running.steps()) {
		if folder := filepath.Dir(path); !slices.Contains(trustedFolders, folder) {
			t.Errorf("root writes %s, whose folder %s is no trusted folder, want it among %q", path, folder,
				trustedFolders)
		}
	}
}

// rootWritten returns the path of every file root writes: its settings, record and copy, and the root files of steps.
func rootWritten(steps []step) []string {
	written := []string{serverEnvPath, recordPath, installerCopy}
	for _, file := range filesOf(steps) {
		if file.owner == "root" {
			written = append(written, file.path)
		}
	}
	return written
}

func TestTheMachineChecksTakeAnEmptyInstallRoot(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	server.folder("/opt/gophenberg")

	err := server.check().run(t.Context())

	server.wantPassed(err, "")
}

func TestTheMachineChecksTakeAnInstallRootTheRecordOwns(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	server.write("/opt/gophenberg/.env", "GOPHENBERG_SUPERUSER_POLICY=refuse\n")
	check := server.check()
	check.record.Installed = []string{"/opt/gophenberg"}

	err := check.run(t.Context())

	server.wantPassed(err, "")
}

func TestTheMachineChecksRefuseAnInstallRootTheyCannotDescribe(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	server.broken["/opt/gophenberg"] = syscall.EIO

	err := server.check().run(t.Context())

	wantRefused(t, err, "installer: input/output error")
}

func TestTheMachineChecksRefuseAnInstallRootTheyCannotList(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	folder, err := os.Lstat(server.at("/opt"))
	if err != nil {
		t.Fatalf("describing /opt: %v", err)
	}
	server.described["/opt/gophenberg"] = ownedInfo{FileInfo: folder}

	err = server.check().run(t.Context())

	wantRefused(t, err, "installer: open "+server.at("/opt/gophenberg")+": no such file or directory")
}
