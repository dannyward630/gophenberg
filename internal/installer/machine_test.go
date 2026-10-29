// SPDX-License-Identifier: Apache-2.0

package installer_test

import (
	"crypto/x509"
	"errors"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"os/user"
	"path/filepath"
	"slices"
	"sync/atomic"
	"syscall"
	"testing"

	"github.com/gopherium/gophenberg/internal/installer"
)

func TestTheRealMachineIsThisServerAsItsOwnUser(t *testing.T) {
	t.Parallel()

	machine := installer.RealMachine()

	if machine.Root != "/" || machine.Euid() != os.Geteuid() {
		t.Errorf("the real machine has the root %q and the user id %d, want / and %d", machine.Root, machine.Euid(),
			os.Geteuid())
	}
}

func TestTheRealMachineMeasuresTheFreeDiskUnderAFolder(t *testing.T) {
	t.Parallel()

	free, err := installer.RealMachine().Free(t.TempDir())

	if err != nil || free == 0 {
		t.Errorf("measuring the free disk of a temporary folder = %d, %v, want some bytes free", free, err)
	}
}

func TestTheRealMachineCannotMeasureTheFreeDiskUnderAFolderThatIsMissing(t *testing.T) {
	t.Parallel()

	missing := filepath.Join(t.TempDir(), "missing")

	_, err := installer.RealMachine().Free(missing)

	if want := "statfs " + missing + ": no such file or directory"; err == nil || err.Error() != want {
		t.Errorf("measuring a missing folder = %v, want %q", err, want)
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("measuring a missing folder = %v, want it refused as missing", err)
	}
}

func TestTheRealMachineDescribesALinkAndNotWhatItPointsAt(t *testing.T) {
	t.Parallel()

	folder := t.TempDir()
	link := filepath.Join(folder, "link")
	if err := os.Symlink(folder, link); err != nil {
		t.Fatalf("making a link: %v", err)
	}

	info, err := installer.RealMachine().Lstat(link)

	if err != nil || info.Mode()&fs.ModeSymlink == 0 {
		t.Errorf("describing a link = %v, %v, want the link itself", info, err)
	}
}

func TestTheRealMachineListensBrieflyOnATCPAndAUDPPort(t *testing.T) {
	t.Parallel()

	machine := installer.RealMachine()
	listener, err := machine.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening on a free TCP port: %v", err)
	}
	packets, err := machine.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening on a free UDP port: %v", err)
	}

	for network, port := range map[string]io.Closer{"tcp": listener, "udp": packets} {
		if err := port.Close(); err != nil {
			t.Errorf("closing the %s listener: %v", network, err)
		}
	}
}

func TestTheRealMachineCannotListenOnAPortSomethingElseHolds(t *testing.T) {
	t.Parallel()

	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("holding a TCP port: %v", err)
	}
	defer func() { _ = held.Close() }()
	heldPacket, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("holding a UDP port: %v", err)
	}
	defer func() { _ = heldPacket.Close() }()
	machine := installer.RealMachine()

	listener, err := machine.Listen("tcp", held.Addr().String())
	packets, packetErr := machine.ListenPacket("udp", heldPacket.LocalAddr().String())

	if !errors.Is(err, syscall.EADDRINUSE) || listener != nil {
		t.Errorf("listening on the held TCP port = %v, %v, want the address in use", listener, err)
	}
	if !errors.Is(packetErr, syscall.EADDRINUSE) || packets != nil {
		t.Errorf("listening on the held UDP port = %v, %v, want the address in use", packets, packetErr)
	}
}

func TestTheRealMachineFetchesAPageThroughAnAddressUnderTheDomainName(t *testing.T) {
	t.Parallel()

	var asked string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = r.Host
		_, _ = io.WriteString(w, "token")
	}))
	defer server.Close()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+"/", nil)
	if err != nil {
		t.Fatalf("making the request: %v", err)
	}
	request.Host = "example.com"

	response, err := installer.RealMachine().Fetch(request)
	if err != nil {
		t.Fatalf("fetching the page: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)

	if err != nil || string(body) != "token" || asked != "example.com" {
		t.Errorf("the fetch read %q with %v asking for %q, want the token asked for example.com", body, err, asked)
	}
}

func TestTheRealMachineFetchesARedirectAsItIsWithoutFollowingIt(t *testing.T) {
	t.Parallel()

	var followed atomic.Bool
	mux := http.NewServeMux()
	mux.Handle("/elsewhere", http.HandlerFunc(func(http.ResponseWriter, *http.Request) { followed.Store(true) }))
	mux.Handle("/", http.RedirectHandler("/elsewhere", http.StatusMovedPermanently))
	server := httptest.NewServer(mux)
	defer server.Close()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+"/", nil)
	if err != nil {
		t.Fatalf("making the request: %v", err)
	}

	response, err := installer.RealMachine().Fetch(request)
	if err != nil {
		t.Fatalf("fetching the page: %v", err)
	}
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusMovedPermanently || response.Header.Get("Location") != "/elsewhere" {
		t.Errorf("the fetch answered %s to %q, want the redirect itself", response.Status,
			response.Header.Get("Location"))
	}
	if followed.Load() {
		t.Errorf("the fetch followed the redirect, want it returned as it is")
	}
}

func TestTheRealMachineResolvesADomainToItsAddresses(t *testing.T) {
	t.Parallel()

	addresses, err := installer.RealMachine().Resolve(t.Context(), "ip", "localhost")

	loopback := slices.ContainsFunc(addresses, netip.Addr.IsLoopback)
	if err != nil || !loopback {
		t.Errorf("resolving localhost = %v, %v, want a loopback address", addresses, err)
	}
}

func TestTheRealMachineListsItsOwnAddresses(t *testing.T) {
	t.Parallel()

	addresses, err := installer.RealMachine().Addresses()

	loopback := slices.ContainsFunc(addresses, func(address net.Addr) bool {
		network, isNetwork := address.(*net.IPNet)
		return isNetwork && network.IP.IsLoopback()
	})
	if err != nil || !loopback {
		t.Errorf("the machine's own addresses = %v, %v, want its loopback address among them", addresses, err)
	}
}

func TestTheRealMachineOpensTheTerminalOfTheSessionForQuestions(t *testing.T) {
	t.Parallel()

	terminal, err := installer.RealMachine().Terminal()

	var missing *fs.PathError
	switch {
	case err == nil:
		if err := terminal.Close(); err != nil {
			t.Errorf("closing the terminal: %v", err)
		}
	case terminal != nil:
		t.Errorf("opening the terminal outside a session gave %v beside %v, want no terminal", terminal, err)
	case !errors.As(err, &missing) || missing.Path != "/dev/tty":
		t.Errorf("opening the terminal outside a session = %v, want /dev/tty named", err)
	}
}

func TestTheRealMachineNamesTheInstallerItRuns(t *testing.T) {
	t.Parallel()

	want, err := os.Executable()
	if err != nil {
		t.Fatalf("naming the test binary: %v", err)
	}

	got, err := installer.RealMachine().Executable()

	if err != nil || got != want {
		t.Errorf("the running installer = %q, %v, want %q", got, err, want)
	}
}

func TestTheRealMachineLooksUpRoot(t *testing.T) {
	t.Parallel()

	root, err := installer.RealMachine().LookupUser("root")

	if err != nil || [2]string{root.Uid, root.Gid} != [2]string{"0", "0"} {
		t.Errorf("looking up root = %+v, %v, want the user and group id 0", root, err)
	}
}

func TestTheRealMachineCannotLookUpAUserThatIsNotThere(t *testing.T) {
	t.Parallel()

	_, err := installer.RealMachine().LookupUser("gophenberg-missing")

	if !errors.As(err, new(user.UnknownUserError)) {
		t.Errorf("looking up a user that is not there = %v, want the user unknown", err)
	}
}

// rootsOf returns a pool that trusts the root certificate server signs its answers with.
func rootsOf(server *httptest.Server) *x509.CertPool {
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	return roots
}

// fetchedHTTPS asks server for path through the real machine trusting roots, returning the answer it read.
func fetchedHTTPS(t *testing.T, server *httptest.Server, path string, roots *x509.CertPool) (*http.Response, error) {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+path, nil)
	if err != nil {
		t.Fatalf("making the request: %v", err)
	}
	return installer.RealMachine().FetchHTTPS(request, roots)
}

func TestTheRealMachineFetchesHTTPSTrustingTheRootsItIsHanded(t *testing.T) {
	t.Parallel()

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "signed in")
	}))
	defer server.Close()

	response, err := fetchedHTTPS(t, server, "/admin/", rootsOf(server))
	if err != nil {
		t.Fatalf("fetching the page: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)

	if err != nil || [2]any{response.StatusCode, string(body)} != [2]any{http.StatusOK, "signed in"} {
		t.Errorf("the HTTPS fetch answered %s with %q and %v, want 200 and the page", response.Status, body, err)
	}
}

func TestTheRealMachineRefusesAnHTTPSAnswerSignedByARootItWasNotHanded(t *testing.T) {
	t.Parallel()

	server := httptest.NewUnstartedServer(http.NotFoundHandler())
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.StartTLS()
	defer server.Close()

	response, err := fetchedHTTPS(t, server, "/admin/", nil)

	var unknown x509.UnknownAuthorityError
	if response != nil || !errors.As(err, &unknown) {
		t.Errorf("fetching under the system's roots = %v, %v, want the signer unknown", response, err)
	}
}

func TestTheRealMachineFetchesAnHTTPSRedirectAsItIs(t *testing.T) {
	t.Parallel()

	server := httptest.NewTLSServer(http.RedirectHandler("/elsewhere", http.StatusFound))
	defer server.Close()

	response, err := fetchedHTTPS(t, server, "/admin/", rootsOf(server))
	if err != nil {
		t.Fatalf("fetching the page: %v", err)
	}
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusFound || response.Header.Get("Location") != "/elsewhere" {
		t.Errorf("the HTTPS fetch answered %s to %q, want the redirect itself", response.Status,
			response.Header.Get("Location"))
	}
}
