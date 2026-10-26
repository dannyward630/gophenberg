// SPDX-License-Identifier: Apache-2.0

package installer

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/gopherium/framework/gonsole"
)

// ubuntuRelease is the os-release of Ubuntu 24.04.
const ubuntuRelease = `PRETTY_NAME="Ubuntu 24.04.3 LTS"
NAME="Ubuntu"
VERSION_ID="24.04"
VERSION="24.04.3 LTS (Noble Numbat)"
VERSION_CODENAME=noble
ID=ubuntu
ID_LIKE=debian
`

// sandboxLine is the systemd-run line the sandbox check runs, as the fake server records it.
const sandboxLine = "systemd-run --wait --collect -p ProtectSystem=strict -p PrivateTmp=yes -p NoNewPrivileges=yes " +
	"/bin/true"

// clockLine is the timedatectl line the clock check runs, as the fake server records it.
const clockLine = "timedatectl show -p NTPSynchronized --value"

// checkedSettings are the settings every machine check passes with on a fake server, for localhost.
var checkedSettings = Settings{
	Root: "/opt/gophenberg", Domain: "localhost", Addr: netip.MustParseAddrPort("127.0.0.1:8081"), WebServer: "caddy",
	Checks: Checks{MemoryFloorMB: 900, DiskFloorMB: 1024, DomainCheckTimeout: 10 * time.Second},
}

// meminfoOf returns a /proc/meminfo whose MemTotal is kB.
func meminfoOf(kB int) string {
	return fmt.Sprintf("MemTotal:        %d kB\nMemFree:          812345 kB\nMemAvailable:    1523456 kB\n", kB)
}

// answer is what a fake program prints and how it ends.
type answer struct {
	printed string
	err     error
}

// ownedInfo describes a file as owned by uid, whatever user the test runs as.
type ownedInfo struct {
	fs.FileInfo
	uid uint32
}

// Sys returns the system description of the file with uid as its owner.
func (i ownedInfo) Sys() any {
	stat := *i.FileInfo.Sys().(*syscall.Stat_t)
	stat.Uid = i.uid
	return &stat
}

// fakeServer is a server every machine check passes on, which records what the checks ask of it.
type fakeServer struct {
	t           *testing.T
	root        string
	euid        int
	answers     map[string]answer
	taken       map[string]error
	freeMB      map[string]uint64
	owners      map[string]uint32
	broken      map[string]error
	described   map[string]fs.FileInfo
	unmeasured  map[string]error
	addresses   []netip.Addr
	unresolved  error
	own         []net.Addr
	unlisted    error
	answerFetch func(request *http.Request) (*http.Response, error)
	notes       strings.Builder
	ran         []string
	listened    []string
	listeners   []*pipeListener
	measured    []string
	resolved    []string
	fetched     []string
}

// newFakeServer returns a fake server every machine check passes on.
func newFakeServer(t *testing.T) *fakeServer {
	t.Helper()
	server := &fakeServer{
		t: t, root: t.TempDir(),
		answers: map[string]answer{
			"systemctl":   {printed: "systemd 255 (255.4-1ubuntu8.6)\n+PAM +AUDIT +SELINUX +APPARMOR\n"},
			"systemd-run": {printed: "Running as unit: run-u7.service\nFinished with result: success\n"},
			"timedatectl": {printed: "yes\n"},
		},
		taken: map[string]error{}, freeMB: map[string]uint64{}, owners: map[string]uint32{}, broken: map[string]error{},
		described: map[string]fs.FileInfo{}, unmeasured: map[string]error{},
		addresses: addresses("::ffff:203.0.113.7"),
		own:       []net.Addr{cardAddress("127.0.0.1/8"), cardAddress("203.0.113.7/24"), cardAddress("::1/128")},
	}
	server.write("/etc/os-release", ubuntuRelease)
	server.write("/proc/meminfo", meminfoOf(2048000))
	server.folder("/run/systemd/system", "/opt", "/var/lib")
	server.program("/usr/bin/systemctl")
	server.program("/usr/sbin/runuser")
	return server
}

// program puts a program anybody may run at path, named from the top of the server.
func (s *fakeServer) program(path string) {
	s.t.Helper()
	s.write(path, "#!/bin/sh\n")
	s.chmod(path, 0o755)
}

// at returns the path on the test's own disk of path, named from the top of the server.
func (s *fakeServer) at(path string) string {
	return filepath.Join(s.root, path)
}

// write puts a file holding body at path, named from the top of the server.
func (s *fakeServer) write(path, body string) {
	s.t.Helper()
	if err := os.MkdirAll(filepath.Dir(s.at(path)), 0o755); err != nil {
		s.t.Fatalf("making the folder of %s: %v", path, err)
	}
	if err := os.WriteFile(s.at(path), []byte(body), 0o644); err != nil {
		s.t.Fatalf("writing %s: %v", path, err)
	}
}

// folder makes a folder at each of paths, named from the top of the server.
func (s *fakeServer) folder(paths ...string) {
	s.t.Helper()
	for _, path := range paths {
		if err := os.MkdirAll(s.at(path), 0o755); err != nil {
			s.t.Fatalf("making the folder %s: %v", path, err)
		}
	}
}

// link puts a link at path that points at target, both named from the top of the server.
func (s *fakeServer) link(path, target string) {
	s.t.Helper()
	if err := os.MkdirAll(filepath.Dir(s.at(path)), 0o755); err != nil {
		s.t.Fatalf("making the folder of %s: %v", path, err)
	}
	if err := os.Symlink(s.at(target), s.at(path)); err != nil {
		s.t.Fatalf("linking %s to %s: %v", path, target, err)
	}
}

// remove takes the file or folder at path away, named from the top of the server.
func (s *fakeServer) remove(path string) {
	s.t.Helper()
	if err := os.RemoveAll(s.at(path)); err != nil {
		s.t.Fatalf("removing %s: %v", path, err)
	}
}

// chmod gives the file at path, named from the top of the server, mode.
func (s *fakeServer) chmod(path string, mode fs.FileMode) {
	s.t.Helper()
	if err := os.Chmod(s.at(path), mode); err != nil {
		s.t.Fatalf("changing the mode of %s: %v", path, err)
	}
}

// serverPath returns the path from the top of the server of path on the test's own disk.
func (s *fakeServer) serverPath(path string) string {
	return "/" + strings.TrimPrefix(strings.TrimPrefix(path, s.root), "/")
}

// machine returns the machine the checks reach this fake server through.
func (s *fakeServer) machine() Machine {
	return Machine{
		Run: s.run, Root: s.root, Lstat: s.lstat, Free: s.free, Listen: s.listen, ListenPacket: s.listenPacket,
		Fetch: s.fetch, Resolve: s.resolve, Addresses: s.ownAddresses, Euid: func() int { return s.euid },
	}
}

// run answers program as the answers say, failing the test for a program no answer covers.
func (s *fakeServer) run(_ context.Context, program Program) (string, error) {
	s.ran = append(s.ran, strings.Join(append([]string{program.Name}, program.Args...), " "))
	reply, found := s.answers[program.Name]
	if !found {
		s.t.Errorf("the checks ran %s, which the fake server does not answer", program.Name)
	}
	return reply.printed, reply.err
}

// lstat describes the file at path on the test's own disk as owned by root, or by the owner a test gave it.
func (s *fakeServer) lstat(path string) (fs.FileInfo, error) {
	server := s.serverPath(path)
	if err := s.broken[server]; err != nil {
		return nil, err
	}
	if info, found := s.described[server]; found {
		return info, nil
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	return ownedInfo{FileInfo: info, uid: s.owners[server]}, nil
}

// free returns 10 GB for a folder on the test's own disk that exists, or the megabytes a test gave it.
func (s *fakeServer) free(path string) (uint64, error) {
	server := s.serverPath(path)
	s.measured = append(s.measured, server)
	if err := s.unmeasured[server]; err != nil {
		return 0, err
	}
	if _, err := os.Stat(path); err != nil {
		return 0, err
	}
	if megabytes, found := s.freeMB[server]; found {
		return megabytes << 20, nil
	}
	return 10240 << 20, nil
}

// listen listens in memory on a TCP port, failing with the error a test gave the port.
func (s *fakeServer) listen(network, address string) (net.Listener, error) {
	s.listened = append(s.listened, network+" "+address)
	if err := s.taken[network+" "+address]; err != nil {
		return nil, err
	}
	listener := newPipeListener()
	s.listeners = append(s.listeners, listener)
	return listener, nil
}

// listenPacket holds a UDP port for nothing, failing with the error a test gave the port.
func (s *fakeServer) listenPacket(network, address string) (net.PacketConn, error) {
	s.listened = append(s.listened, network+" "+address)
	if err := s.taken[network+" "+address]; err != nil {
		return nil, err
	}
	return packetPort{}, nil
}

// resolve records the network and the domain asked for and returns the addresses a test gave every domain.
func (s *fakeServer) resolve(_ context.Context, network, host string) ([]netip.Addr, error) {
	s.resolved = append(s.resolved, network+" "+host)
	return s.addresses, s.unresolved
}

// ownAddresses returns the addresses a test gave the network cards of the server.
func (s *fakeServer) ownAddresses() ([]net.Addr, error) {
	return s.own, s.unlisted
}

// fetch records the page asked for, then answers as the test says or else through the newest TCP listener.
func (s *fakeServer) fetch(request *http.Request) (*http.Response, error) {
	s.fetched = append(s.fetched, request.Host+" "+request.URL.String())
	if s.answerFetch != nil {
		return s.answerFetch(request)
	}
	return s.listeners[len(s.listeners)-1].fetch(request)
}

// check returns the machine check of this fake server with checkedSettings, no install record and no environment.
func (s *fakeServer) check() *machineCheck {
	return &machineCheck{machine: s.machine(), settings: checkedSettings, env: envOf(nil), notes: &s.notes}
}

// ownWebServerCheck returns the machine check of this fake server for example.com, its web server the person's own.
func (s *fakeServer) ownWebServerCheck() *machineCheck {
	check := s.check()
	check.settings.Domain, check.settings.WebServer = "example.com", "none"
	return check
}

// wantPassed fails t unless the checks passed with notes printed.
func (s *fakeServer) wantPassed(err error, notes string) {
	s.t.Helper()
	if err != nil || s.notes.String() != notes {
		s.t.Errorf("the checks = %v, noting %q, want them passed noting %q", err, s.notes.String(), notes)
	}
}

func TestTheMachineChecksPassAFreshUbuntuServerAndNoteNothing(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	check := server.check()

	err := check.run(t.Context())

	server.wantPassed(err, "")
	if check.found.systemd != 255 {
		t.Errorf("the checks found systemd %d, want 255", check.found.systemd)
	}
	if want := []string{"systemctl --version", sandboxLine, clockLine}; !slices.Equal(server.ran, want) {
		t.Errorf("the checks ran %q, want %q", server.ran, want)
	}
	wantListened := []string{"tcp :80", "tcp :443", "udp :443", "tcp 127.0.0.1:8081", "tcp :5432"}
	if !slices.Equal(server.listened, wantListened) {
		t.Errorf("the checks listened on %q, want %q", server.listened, wantListened)
	}
	if want := []string{"/opt/gophenberg", "/opt", "/var/lib"}; !slices.Equal(server.measured, want) {
		t.Errorf("the checks measured the disk under %q, want %q", server.measured, want)
	}
}

func TestTheMachineChecksStopARunThatIsNotRoots(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	server.euid = 1000

	err := server.check().run(t.Context())

	wantRefused(t, err, "server:install must run as root, such as with sudo, so nothing was changed")
	if len(server.ran) != 0 {
		t.Errorf("the checks ran %q as a plain user, want nothing run", server.ran)
	}
}

func TestTheMachineChecksStopWhereSystemdCannotSetUpTheSandboxOfTheSite(t *testing.T) {
	t.Parallel()

	for printed, reported := range map[string]string{"lxc\n": "lxc", "": "nothing"} {
		server := newFakeServer(t)
		server.answers["systemd-run"] = answer{
			printed: "Job for run-u7.service failed.\n", err: errors.New("installer: run systemd-run: exit status 1"),
		}
		server.answers["systemd-detect-virt"] = answer{printed: printed}

		err := server.check().run(t.Context())

		wantRefused(t, err, "systemd cannot set up the sandbox the site's service runs in on this server, as on a "+
			"container plan without nesting, and systemd-detect-virt reports "+reported+", so nothing was changed")
	}
}

func TestTheMachineChecksTakeMemoryRightAtTheFloor(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	server.write("/proc/meminfo", meminfoOf(900*1024))

	err := server.check().run(t.Context())

	server.wantPassed(err, "")
}

func TestTheMachineChecksStopOnLessMemoryThanTheFloor(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	server.write("/proc/meminfo", meminfoOf(900*1024-1))

	err := server.check().run(t.Context())

	wantRefused(t, err, "this server has 899 MB of memory, less than the 900 MB GOPHENBERG_INSTALL_MEMORY_FLOOR_MB "+
		"asks, so nothing was changed")
}

func TestTheMachineChecksStopWhenTheyCannotTellTheMemory(t *testing.T) {
	t.Parallel()

	missing := newFakeServer(t)
	missing.remove("/proc/meminfo")
	unnamed := newFakeServer(t)
	unnamed.write("/proc/meminfo", "MemFree:          812345 kB\n")

	for _, server := range []*fakeServer{missing, unnamed} {
		err := server.check().run(t.Context())

		wantRefused(t, err, "cannot tell how much memory this server has: /proc/meminfo has no MemTotal line in kB, "+
			"so nothing was changed")
	}
}

func TestTheMachineChecksRefuseAMemoryReportTheyCannotRead(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	server.remove("/proc/meminfo")
	server.folder("/proc/meminfo")

	err := server.check().run(t.Context())

	wantRefused(t, err, "installer: read "+server.at("/proc/meminfo")+": is a directory")
}

func TestTheMachineChecksTakeFreeDiskRightAtTheFloor(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	server.freeMB["/opt"], server.freeMB["/var/lib"] = 1024, 1024

	err := server.check().run(t.Context())

	server.wantPassed(err, "")
}

func TestTheMachineChecksStopOnLessFreeDiskThanTheFloor(t *testing.T) {
	t.Parallel()

	for _, folder := range []string{"/opt", "/var/lib"} {
		server := newFakeServer(t)
		server.freeMB[folder] = 1023

		err := server.check().run(t.Context())

		wantRefused(t, err, "this server has 1023 MB free under "+folder+", less than the 1024 MB "+
			"GOPHENBERG_INSTALL_DISK_FLOOR_MB asks, so nothing was changed")
	}
}

func TestTheMachineChecksMeasureTheDiskAtTheNearestFolderThatExists(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	server.remove("/opt")
	server.remove("/var")
	check := server.check()
	check.settings.Root = "/srv/sites/gophenberg"
	server.folder("/srv")

	err := check.run(t.Context())

	server.wantPassed(err, "")
	want := []string{"/srv/sites/gophenberg", "/srv/sites", "/srv", "/var/lib", "/var", "/"}
	if !slices.Equal(server.measured, want) {
		t.Errorf("the checks measured the disk under %q, want %q", server.measured, want)
	}
}

func TestTheMachineChecksRefuseADiskTheyCannotMeasure(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	server.unmeasured["/opt"] = &fs.PathError{Op: "statfs", Path: server.at("/opt"), Err: syscall.EIO}

	err := server.check().run(t.Context())

	wantRefused(t, err, "installer: statfs "+server.at("/opt")+": input/output error")
}

func TestTheMachineChecksWarnWhenTheClockIsNotSynchronized(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	server.answers["timedatectl"] = answer{printed: "no\n"}

	err := server.check().run(t.Context())

	server.wantPassed(err, "warning: the clock of this server is not synchronized, and HTTPS and the signed update "+
		"index both need the right time, so turn on NTP with sudo timedatectl set-ntp true\n")
}

func TestTheMachineChecksSayNothingOfAClockTheyCannotRead(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	server.answers["timedatectl"] = answer{err: errors.New("installer: no program timedatectl in /usr/sbin:/usr/bin")}

	err := server.check().run(t.Context())

	server.wantPassed(err, "")
}

func TestInstallRunsTheMachineChecksAndPrintsTheirNotesOnTheErrorStream(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	server.write("/etc/os-release", debianRelease(12, "bookworm"))
	server.answers["timedatectl"] = answer{printed: "no\n"}
	var stdout, stderr strings.Builder

	err := Install(t.Context(), server.machine(), gonsole.Call{Env: envOf(nil), Stdout: &stdout, Stderr: &stderr},
		firstRun)

	want := "Debian GNU/Linux 12 (bookworm) is not yet tested in Gophenberg's CI, which tests Ubuntu 24.04\n" +
		wantedFirewallNote + "warning: the clock of this server is not synchronized, and HTTPS and the signed update " +
		"index both need the right time, so turn on NTP with sudo timedatectl set-ntp true\n"
	if err != nil || stderr.String() != want {
		t.Errorf("Install() = %v, noting %q, want nil and the notes\n%s", err, stderr.String(), want)
	}
	if !strings.HasPrefix(stdout.String(), wantedLead) {
		t.Errorf("Install() printed %q, want its answer to hold the steps alone", stdout.String())
	}
}
