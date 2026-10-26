// SPDX-License-Identifier: Apache-2.0

package installer

import (
	"cmp"
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strings"
)

// httpPort is the port Let's Encrypt asks a domain on over plain HTTP, which the domain check serves its token on.
const httpPort = 80

// tokenFolder is the folder of the page the domain check serves its token at.
const tokenFolder = "/.well-known/gophenberg-install/"

// firewallNote is what each domain check that looks at the domain notes, since no check sees the provider's firewall.
const firewallNote = "the hosting provider's firewall must let TCP 80, TCP 443 and UDP 443 in for HTTPS to work, " +
	"and this check cannot see it"

// checkDomain stops unless the domain points at this server, noting the firewall it cannot see, when not skipped.
func checkDomain(ctx context.Context, c *machineCheck) error {
	if c.skipsDomain() {
		return nil
	}
	err := c.proveDomain(ctx)
	_, noteErr := fmt.Fprintln(c.notes, firewallNote)
	return cmp.Or(err, noteErr)
}

// skipsDomain reports whether the person's own web server, -skip-domain-check, localhost or the record skips the check.
func (c *machineCheck) skipsDomain() bool {
	if !c.settings.withCaddy() {
		return true
	}
	domain := c.settings.Domain
	if c.request.SkipDomainCheck || domain == "localhost" {
		return true
	}
	return domain == c.record.Domain
}

// proveDomain stops unless every address the domain names leaves the server and is its own or answers the token.
func (c *machineCheck) proveDomain(ctx context.Context) error {
	domain := c.settings.Domain
	named, err := c.machine.Resolve(ctx, "ip", domain)
	if err != nil {
		return stop("cannot find an A or AAAA record of %s, which must point at this server: %v", domain, err)
	}
	if found := slices.IndexFunc(named, staysHere); found >= 0 {
		return stop("%s resolves to %s, an address that never leaves this server, likely from a line in /etc/hosts, "+
			"and Let's Encrypt asks for the domain from outside, which needs its public A or AAAA record to point at "+
			"this server, and -skip-domain-check skips this check once it does", domain, named[found].Unmap())
	}
	own, err := ownAddresses(c.machine)
	if err != nil {
		return err
	}
	foreign := foreignOf(named, own)
	if len(foreign) == 0 {
		return nil
	}
	return c.serveToken(ctx, foreign)
}

// staysHere reports whether address is a loopback or an unspecified address, which never leaves the server.
func staysHere(address netip.Addr) bool {
	unmapped := address.Unmap()
	return unmapped.IsLoopback() || unmapped.IsUnspecified()
}

// ownAddresses returns the addresses of the machine's network cards, one it cannot read as the zero address.
func ownAddresses(machine Machine) (map[netip.Addr]bool, error) {
	listed, err := machine.Addresses()
	if err != nil {
		return nil, fmt.Errorf("installer: %w", err)
	}
	own := make(map[netip.Addr]bool, len(listed))
	for _, address := range listed {
		host, _, _ := strings.Cut(address.String(), "/")
		parsed, _ := netip.ParseAddr(host)
		own[parsed.Unmap()] = true
	}
	return own, nil
}

// foreignOf returns the addresses of named that are not one of own, each IPv4 address read as itself.
func foreignOf(named []netip.Addr, own map[netip.Addr]bool) []netip.Addr {
	var foreign []netip.Addr
	for _, address := range named {
		if !own[address.Unmap()] {
			foreign = append(foreign, address.Unmap())
		}
	}
	return foreign
}

// serveToken answers a random token on TCP port 80 for a moment and fetches it through each of addresses in turn.
func (c *machineCheck) serveToken(ctx context.Context, addresses []netip.Addr) error {
	listener, err := c.machine.Listen("tcp", fmt.Sprint(":", httpPort))
	if err != nil {
		return stop("cannot listen on TCP port %d to check that %s points at this server: %v, and "+
			"-skip-domain-check skips this check", httpPort, c.settings.Domain, err)
	}
	token := rand.Text()
	page := http.NewServeMux()
	page.HandleFunc("GET "+tokenFolder+token, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, token)
	})
	server := &http.Server{
		Handler: page, ReadHeaderTimeout: c.settings.Checks.DomainCheckTimeout, ErrorLog: log.New(io.Discard, "", 0),
	}
	served := make(chan struct{})
	go func() {
		defer close(served)
		_ = server.Serve(listener)
	}()
	defer func() {
		_ = server.Close()
		<-served
	}()
	for _, address := range addresses {
		if err := c.fetchToken(ctx, address, token); err != nil {
			return err
		}
	}
	return nil
}

// fetchToken stops unless the token page fetched through address answers token within the domain check timeout.
func (c *machineCheck) fetchToken(ctx context.Context, address netip.Addr, token string) error {
	ctx, cancel := context.WithTimeout(ctx, c.settings.Checks.DomainCheckTimeout)
	defer cancel()
	request := (&http.Request{
		Method: http.MethodGet, Host: c.settings.Domain, Header: http.Header{}, Close: true,
		URL: &url.URL{Scheme: "http", Host: netip.AddrPortFrom(address, httpPort).String(), Path: tokenFolder + token},
	}).WithContext(ctx)
	response, err := c.machine.Fetch(request)
	if err != nil {
		return c.unreached(address, err.Error())
	}
	defer func() { _ = response.Body.Close() }()
	if reason := notToken(response, token); reason != "" {
		return c.unreached(address, reason)
	}
	return nil
}

// notToken returns why response is not the page that answers token, empty when it is.
func notToken(response *http.Response, token string) string {
	if response.StatusCode != http.StatusOK {
		return "it answered " + response.Status
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, int64(len(token))+1))
	if err != nil {
		return err.Error()
	}
	if string(body) != token {
		return "it answered a page without the check's token"
	}
	return ""
}

// unreached returns the stop of a domain check that address did not answer for reason, naming the likely causes.
func (c *machineCheck) unreached(address netip.Addr, reason string) error {
	domain := c.settings.Domain
	return stop("%s points at %s, which did not answer the check as this server would (%s), likely because %s "+
		"points at another server or because the network does not route a request from this server to its own "+
		"public address back to it, and -skip-domain-check skips this check for a server behind a proxy it cannot "+
		"cross", domain, address, reason, domain)
}
