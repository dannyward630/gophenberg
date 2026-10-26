// SPDX-License-Identifier: Apache-2.0

package installer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path"
	"regexp"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"testing/iotest"
	"time"

	"github.com/gopherium/framework/gonsole"
)

// wantedFirewallNote is the note of every domain check that looks at the domain.
const wantedFirewallNote = "the hosting provider's firewall must let TCP 80, TCP 443 and UDP 443 in for HTTPS to " +
	"work, and this check cannot see it\n"

// unreachedCauses is how a domain check that did not reach this server names the likely causes and the skip flag.
const unreachedCauses = "likely because example.com points at another server or because the network does not " +
	"route a request from this server to its own public address back to it, and -skip-domain-check skips this " +
	"check for a server behind a proxy it cannot cross, so nothing was changed"

// tokenPage matches the page a domain check fetches, through the address it names, under the host name example.com.
var tokenPage = regexp.MustCompile(`^example\.com http://(.+):80/\.well-known/gophenberg-install/([A-Z2-7]{26})$`)

// portListens are the brief listens of the port checks on a fresh server, in the order they run.
var portListens = []string{"tcp :80", "tcp :443", "udp :443", "tcp 127.0.0.1:8081", "tcp :5432"}

// pipeListener is a TCP listener in memory that takes each connection a fake fetch dials through a pipe.
type pipeListener struct {
	conns  chan net.Conn
	closed chan struct{}
	once   sync.Once
}

// newPipeListener returns a pipe listener that takes connections until it is closed.
func newPipeListener() *pipeListener {
	return &pipeListener{conns: make(chan net.Conn), closed: make(chan struct{})}
}

// Accept returns the next connection a fake fetch dialed, failing once the listener is closed.
func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.conns:
		return conn, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

// Close stops the listener taking connections.
func (l *pipeListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

// Addr returns TCP port 80, which the listener stands for.
func (l *pipeListener) Addr() net.Addr {
	return &net.TCPAddr{Port: 80}
}

// closedYet reports whether the listener was closed.
func (l *pipeListener) closedYet() bool {
	select {
	case <-l.closed:
		return true
	default:
		return false
	}
}

// dial hands the listener one end of a new pipe and returns the other, failing once the listener is closed.
func (l *pipeListener) dial(ctx context.Context, _, _ string) (net.Conn, error) {
	client, server := net.Pipe()
	select {
	case l.conns <- server:
		return client, nil
	case <-l.closed:
		return nil, net.ErrClosed
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// fetch asks the listener for request through a pipe in memory.
func (l *pipeListener) fetch(request *http.Request) (*http.Response, error) {
	transport := &http.Transport{DialContext: l.dial}
	defer transport.CloseIdleConnections()
	return (&http.Client{Transport: transport}).Do(request)
}

// packetPort is a UDP port the fake server holds for a brief listen, which only lets go of it.
type packetPort struct {
	net.PacketConn
}

// Close lets go of the port.
func (packetPort) Close() error {
	return nil
}

// cardAddress returns the address of a network card with the size of its network, such as 203.0.113.7/24.
func cardAddress(prefix string) net.Addr {
	parsed := netip.MustParsePrefix(prefix)
	return &net.IPNet{IP: parsed.Addr().AsSlice(), Mask: net.CIDRMask(parsed.Bits(), parsed.Addr().BitLen())}
}

// addresses returns the addresses each of listed names.
func addresses(listed ...string) []netip.Addr {
	parsed := make([]netip.Addr, 0, len(listed))
	for _, address := range listed {
		parsed = append(parsed, netip.MustParseAddr(address))
	}
	return parsed
}

// answered returns a fetch that answers with status and body.
func answered(code int, body string) func(*http.Request) (*http.Response, error) {
	return func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: code, Status: fmt.Sprintf("%d %s", code, http.StatusText(code)),
			Body: io.NopCloser(strings.NewReader(body)),
		}, nil
	}
}

// domainCheck returns the machine check of this fake server for domain, with no install record.
func (s *fakeServer) domainCheck(domain string) *machineCheck {
	check := s.check()
	check.settings.Domain = domain
	return check
}

// wantDomainRefused fails t unless the checks stopped with want and noted the firewall they cannot see.
func (s *fakeServer) wantDomainRefused(err error, want string) {
	s.t.Helper()
	wantRefused(s.t, err, want)
	if s.notes.String() != wantedFirewallNote {
		s.t.Errorf("the checks noted %q, want %q", s.notes.String(), wantedFirewallNote)
	}
}

// tokensOf returns the address and the token of each page the fake server was asked for, each a token page.
func (s *fakeServer) tokensOf() (through, tokens []string) {
	s.t.Helper()
	for _, fetched := range s.fetched {
		match := tokenPage.FindStringSubmatch(fetched)
		if match == nil {
			s.t.Fatalf("the check fetched %q, want a token page of example.com", fetched)
		}
		through, tokens = append(through, match[1]), append(tokens, match[2])
	}
	return through, tokens
}

func TestTheDomainCheckPassesADomainWhoseEveryAddressIsTheMachinesOwn(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	server.addresses = addresses("::ffff:203.0.113.7", "2001:db8::7")
	server.own = append(server.own, cardAddress("2001:db8::7/64"))

	err := server.domainCheck("example.com").run(t.Context())

	server.wantPassed(err, wantedFirewallNote)
	if want := []string{"ip example.com"}; !slices.Equal(server.resolved, want) {
		t.Errorf("the check resolved %q, want %q", server.resolved, want)
	}
	if len(server.fetched) != 0 || !slices.Equal(server.listened, portListens) {
		t.Errorf("the check fetched %q and listened on %q, want no fetch and only the port checks", server.fetched,
			server.listened)
	}
}

func TestTheDomainCheckIsLeftOutWithTheirOwnWebServer(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	server.unresolved = &net.DNSError{Err: "no such host", Name: "example.com", IsNotFound: true}

	err := server.ownWebServerCheck().run(t.Context())

	server.wantPassed(err, "")
	if len(server.resolved) != 0 || len(server.fetched) != 0 {
		t.Errorf("the checks with the person's own web server resolved %q and fetched %q, want neither",
			server.resolved, server.fetched)
	}
}

func TestTheDomainCheckFetchesItsTokenThroughEveryAddressThatIsNotTheMachinesOwn(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	server.addresses = addresses("::ffff:203.0.113.7", "::ffff:198.51.100.7", "2001:db8::7")

	err := server.domainCheck("example.com").run(t.Context())

	server.wantPassed(err, wantedFirewallNote)
	through, tokens := server.tokensOf()
	if want := []string{"198.51.100.7", "[2001:db8::7]"}; !slices.Equal(through, want) {
		t.Errorf("the check fetched its token through %q, want %q", through, want)
	}
	if len(tokens) != 2 || tokens[0] != tokens[1] {
		t.Errorf("the check fetched the tokens %q, want one token through both addresses", tokens)
	}
	if want := append(slices.Clone(portListens), "tcp :80"); !slices.Equal(server.listened, want) {
		t.Errorf("the check listened on %q, want %q", server.listened, want)
	}
	for _, listener := range server.listeners {
		if !listener.closedYet() {
			t.Errorf("the check left a listener open, want every listener closed")
		}
	}
}

func TestTheDomainCheckServesANewRandomTokenOnEachRun(t *testing.T) {
	t.Parallel()

	var tokens []string
	for range 2 {
		server := newFakeServer(t)
		server.addresses = addresses("198.51.100.7")

		err := server.domainCheck("example.com").run(t.Context())

		server.wantPassed(err, wantedFirewallNote)
		_, served := server.tokensOf()
		tokens = append(tokens, served...)
	}
	if len(tokens) != 2 || tokens[0] == tokens[1] {
		t.Errorf("two runs served the tokens %q, want two different tokens", tokens)
	}
}

func TestTheDomainCheckStopsWhenAnAddressAnswersAsAnotherServer(t *testing.T) {
	t.Parallel()

	for _, elsewhere := range []struct {
		reason string
		answer func(*http.Request) (*http.Response, error)
	}{
		{"it answered 404 Not Found", answered(http.StatusNotFound, "404 page not found\n")},
		{"it answered 301 Moved Permanently", answered(http.StatusMovedPermanently, "")},
		{"it answered a page without the check's token", answered(http.StatusOK, "Welcome to nginx!")},
		{"it answered a page without the check's token", func(request *http.Request) (*http.Response, error) {
			return answered(http.StatusOK, path.Base(request.URL.Path)+"0")(request)
		}},
		{"read tcp 198.51.100.7:80: connection reset by peer", func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Body: io.NopCloser(iotest.ErrReader(
				errors.New("read tcp 198.51.100.7:80: connection reset by peer")))}, nil
		}},
		{"dial tcp 198.51.100.7:80: connect: connection refused", func(*http.Request) (*http.Response, error) {
			return nil, errors.New("dial tcp 198.51.100.7:80: connect: connection refused")
		}},
	} {
		server := newFakeServer(t)
		server.addresses = addresses("198.51.100.7")
		server.answerFetch = elsewhere.answer

		err := server.domainCheck("example.com").run(t.Context())

		server.wantDomainRefused(err, "example.com points at 198.51.100.7, which did not answer the check as this "+
			"server would ("+elsewhere.reason+"), "+unreachedCauses)
	}
}

func TestTheDomainCheckStopsAtTheFirstAddressThatDoesNotAnswer(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	server.addresses = addresses("2001:db8::7", "198.51.100.7")
	server.answerFetch = answered(http.StatusNotFound, "")

	err := server.domainCheck("example.com").run(t.Context())

	server.wantDomainRefused(err, "example.com points at 2001:db8::7, which did not answer the check as this "+
		"server would (it answered 404 Not Found), "+unreachedCauses)
	if len(server.fetched) != 1 {
		t.Errorf("the check fetched %q, want one fetch", server.fetched)
	}
}

func TestTheDomainCheckGivesUpOnAFetchOnceTheDomainCheckTimeoutPasses(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	server.addresses = addresses("198.51.100.7")
	var left time.Duration
	server.answerFetch = func(request *http.Request) (*http.Response, error) {
		deadline, _ := request.Context().Deadline()
		left = time.Until(deadline)
		<-request.Context().Done()
		return nil, request.Context().Err()
	}
	check := server.domainCheck("example.com")
	check.settings.Checks.DomainCheckTimeout = 20 * time.Millisecond

	err := check.run(t.Context())

	server.wantDomainRefused(err, "example.com points at 198.51.100.7, which did not answer the check as this "+
		"server would (context deadline exceeded), "+unreachedCauses)
	if left <= 0 || left > 20*time.Millisecond {
		t.Errorf("the fetch had %v left, want at most the 20ms of the domain check timeout", left)
	}
}

func TestTheDomainCheckStopsWhenTheCaddyItSetUpHoldsPort80(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	server.addresses = addresses("198.51.100.7")
	server.taken["tcp :80"] = inUse("tcp", ":80")
	check := server.domainCheck("example.com")
	check.record.Installed, check.record.Domain = []string{pieceCaddy}, "old.example.com"

	err := check.run(t.Context())

	server.wantDomainRefused(err, "the Caddy this installer set up holds TCP port 80, so the check that example.com "+
		"points at this server cannot run, and -skip-domain-check skips it once the domain points here, so "+
		"nothing was changed")
	if len(server.fetched) != 0 {
		t.Errorf("the check fetched %q, want nothing fetched", server.fetched)
	}
}

func TestTheDomainCheckStopsWhenItCannotListenOnPort80(t *testing.T) {
	t.Parallel()

	for _, unlistened := range []struct {
		installed []string
		failed    error
		why       string
	}{
		{nil, inUse("tcp", ":80"), "address already in use"},
		{[]string{piecePostgreSQL, pieceService}, inUse("tcp", ":80"), "address already in use"},
		{[]string{pieceCaddy}, fmt.Errorf("listen tcp :80: bind: %w", syscall.EACCES), "permission denied"},
	} {
		server := newFakeServer(t)
		server.addresses = addresses("198.51.100.7")
		server.taken["tcp :80"] = unlistened.failed
		check := server.domainCheck("example.com")
		check.record.Installed = unlistened.installed

		err := checkDomain(t.Context(), check)

		server.wantDomainRefused(err, "cannot listen on TCP port 80 to check that example.com points at this "+
			"server: listen tcp :80: bind: "+unlistened.why+", and -skip-domain-check skips this check, so "+
			"nothing was changed")
		if len(server.fetched) != 0 {
			t.Errorf("the check fetched %q, want nothing fetched", server.fetched)
		}
	}
}

func TestTheDomainCheckStopsOnADomainThatResolvesToAnAddressThatNeverLeavesTheServer(t *testing.T) {
	t.Parallel()

	for named, shown := range map[string]string{
		"127.0.0.1": "127.0.0.1", "::1": "::1", "::ffff:127.0.1.1": "127.0.1.1", "0.0.0.0": "0.0.0.0", "::": "::",
		"::ffff:0.0.0.0": "0.0.0.0",
	} {
		server := newFakeServer(t)
		server.addresses = addresses("::ffff:203.0.113.7", named)

		err := server.domainCheck("example.com").run(t.Context())

		server.wantDomainRefused(err, "example.com resolves to "+shown+", an address that never leaves this server, "+
			"likely from a line in /etc/hosts, and Let's Encrypt asks for the domain from outside, which needs its "+
			"public A or AAAA record to point at this server, and -skip-domain-check skips this check once it does, "+
			"so nothing was changed")
		if len(server.fetched) != 0 || !slices.Equal(server.listened, portListens) {
			t.Errorf("the check of %s fetched %q and listened on %q, want no fetch and only the port checks", named,
				server.fetched, server.listened)
		}
	}
}

func TestTheDomainCheckStopsOnADomainWithNoAddress(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	server.unresolved = &net.DNSError{Err: "no such host", Name: "example.com", IsNotFound: true}

	err := server.domainCheck("example.com").run(t.Context())

	server.wantDomainRefused(err, "cannot find an A or AAAA record of example.com, which must point at this server: "+
		"lookup example.com: no such host, so nothing was changed")
}

func TestTheDomainCheckRefusesWhenItCannotListTheMachinesOwnAddresses(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	server.unlisted = errors.New("route ip+net: netlinkrib: permission denied")

	err := server.domainCheck("example.com").run(t.Context())

	server.wantDomainRefused(err, "installer: route ip+net: netlinkrib: permission denied")
}

func TestTheDomainCheckReadsAnAddressItCannotParseAsNoneOfTheMachinesOwn(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	server.own = []net.Addr{&net.IPNet{IP: net.IP{203, 0, 113}, Mask: net.CIDRMask(24, 32)}}
	server.answerFetch = answered(http.StatusNotFound, "")

	err := server.domainCheck("example.com").run(t.Context())

	server.wantDomainRefused(err, "example.com points at 203.0.113.7, which did not answer the check as this "+
		"server would (it answered 404 Not Found), "+unreachedCauses)
}

func TestTheDomainCheckTakesTheAddressOfANetworkCardWithoutTheSizeOfItsNetwork(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	server.own = []net.Addr{&net.IPAddr{IP: net.ParseIP("203.0.113.7")}}

	err := server.domainCheck("example.com").run(t.Context())

	server.wantPassed(err, wantedFirewallNote)
	if len(server.fetched) != 0 {
		t.Errorf("the check fetched %q, want no fetch", server.fetched)
	}
}

func TestTheDomainCheckIsSkippedForLocalhostForTheFlagAndForTheDomainTheRecordNames(t *testing.T) {
	t.Parallel()

	localhost := newFakeServer(t)
	flagged := newFakeServer(t)
	recorded := newFakeServer(t)
	checks := map[*fakeServer]*machineCheck{
		localhost: localhost.domainCheck("localhost"),
		flagged:   flagged.domainCheck("example.com"),
		recorded:  recorded.domainCheck("example.com"),
	}
	checks[flagged].request.SkipDomainCheck = true
	checks[recorded].record.Domain = "example.com"

	for server, check := range checks {
		server.unresolved = errors.New("lookup example.com: no such host")

		err := check.run(t.Context())

		server.wantPassed(err, "")
		if len(server.resolved) != 0 || !slices.Equal(server.listened, portListens) {
			t.Errorf("a skipped check resolved %q and listened on %q, want no domain asked for", server.resolved,
				server.listened)
		}
	}
}

func TestTheDomainCheckRunsForADomainOtherThanTheOneTheRecordNames(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	check := server.domainCheck("example.com")
	check.record.Domain = "old.example.com"

	err := check.run(t.Context())

	server.wantPassed(err, wantedFirewallNote)
	if want := []string{"ip example.com"}; !slices.Equal(server.resolved, want) {
		t.Errorf("the check resolved %q, want %q", server.resolved, want)
	}
}

// onRealPortZero returns a domain check of example.com that listens for real on 127.0.0.1 and fetches through reach.
func onRealPortZero(server *fakeServer, reach func(listened string) string) (*machineCheck, *net.Listener) {
	machine := RealMachine()
	server.addresses = addresses("198.51.100.7")
	check := server.domainCheck("example.com")
	opened := new(net.Listener)
	check.machine.Listen = func(network, _ string) (net.Listener, error) {
		listener, err := machine.Listen(network, "127.0.0.1:0")
		if err != nil {
			return nil, err
		}
		*opened = listener
		return listener, nil
	}
	check.machine.Fetch = func(request *http.Request) (*http.Response, error) {
		request.URL.Host = reach((*opened).Addr().String())
		return machine.Fetch(request)
	}
	return check, opened
}

func TestTheDomainCheckFetchesItsTokenFromARealListenerOnPortZero(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	check, opened := onRealPortZero(server, func(listened string) string { return listened })

	err := checkDomain(t.Context(), check)

	server.wantPassed(err, wantedFirewallNote)
	if err := (*opened).Close(); !errors.Is(err, net.ErrClosed) {
		t.Errorf("closing the token listener after the check = %v, want it closed already", err)
	}
}

func TestTheDomainCheckStopsWhenARealFetchReachesAnotherServer(t *testing.T) {
	t.Parallel()

	elsewhere := httptest.NewServer(http.NotFoundHandler())
	defer elsewhere.Close()
	server := newFakeServer(t)
	check, _ := onRealPortZero(server, func(string) string { return elsewhere.Listener.Addr().String() })

	err := checkDomain(t.Context(), check)

	server.wantDomainRefused(err, "example.com points at 198.51.100.7, which did not answer the check as this "+
		"server would (it answered 404 Not Found), "+unreachedCauses)
}

func TestInstallHandsTheDomainCheckTheRequestToSkipIt(t *testing.T) {
	t.Parallel()

	for skip, want := range map[bool]string{
		true: "", false: "cannot find an A or AAAA record of example.com, which must point at this server: lookup " +
			"example.com: no such host, so nothing was changed",
	} {
		server := newFakeServer(t)
		server.unresolved = errors.New("lookup example.com: no such host")
		request := firstRun
		request.SkipDomainCheck = skip

		err := Install(t.Context(), server.machine(), gonsole.Call{
			Env: envOf(nil), Stdout: io.Discard, Stderr: &server.notes,
		}, request)

		if got := errText(err); got != want {
			t.Errorf("Install() with -skip-domain-check %v = %q, want %q", skip, got, want)
		}
	}
}

// errText returns the text of err, empty for no error.
func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
