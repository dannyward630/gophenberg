// SPDX-License-Identifier: Apache-2.0

package installer

import (
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// wantedNginxLines are the lines nginx needs to send example.com to the site with every default.
const wantedNginxLines = `client_max_body_size 131136k;

location / {
	proxy_pass http://127.0.0.1:8081;
	proxy_set_header Host $host;
	proxy_set_header X-Forwarded-Host $host;
	proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
	proxy_set_header X-Forwarded-Proto $scheme;
}
`

// wantedCaddyLines are the lines a Caddy of the person's own needs to send example.com to the site with every default.
const wantedCaddyLines = `example.com {
	reverse_proxy 127.0.0.1:8081
}
`

// wantedTraefikLines are the lines Traefik's file provider needs to send example.com to the site with every default.
const wantedTraefikLines = "http:\n" +
	"  routers:\n" +
	"    gophenberg:\n" +
	"      rule: Host(`example.com`)\n" +
	"      entryPoints:\n" +
	"        - websecure\n" +
	"      tls:\n" +
	"        certResolver: letsencrypt\n" +
	"      service: gophenberg\n" +
	"  services:\n" +
	"    gophenberg:\n" +
	"      loadBalancer:\n" +
	"        servers:\n" +
	"          - url: http://127.0.0.1:8081\n"

// wantedApacheLines are the lines Apache needs to send example.com to the site with every default.
const wantedApacheLines = `LimitRequestBody 134283264
ProxyAddHeaders Off
ProxyPreserveHost On
RequestHeader set X-Forwarded-Host expr=%{HTTP_HOST}
RequestHeader set X-Forwarded-For expr=%{REMOTE_ADDR}
RequestHeader set X-Forwarded-Proto expr=%{REQUEST_SCHEME}
ProxyPass / http://127.0.0.1:8081/
ProxyPassReverse / http://127.0.0.1:8081/
`

// wantedWebServerLead opens the text the step for the person's own web server prints, for example.com.
const wantedWebServerLead = `Your own web server provides HTTPS for example.com and must run on this server, since the
site answers on 127.0.0.1:8081 alone. During the restart after an update it shows its own
error page, unless it can hold requests until the site answers again. A web server in a
container needs this server's own network, such as network_mode: host, since a
container's loopback address is its own. The lines below send example.com to the site with
the visitor's host, client address and scheme, and a body limit as large as the largest
upload the site takes: a file of the 128 MB GOPHENBERG_MEDIA_UPLOAD_CAP_MB allows or a
theme of 64 MB, each with 64 KB of form framing around it.
`

// wantedWebServerText is the whole text the step for the person's own web server prints, for example.com.
const wantedWebServerText = wantedWebServerLead + `
nginx, in the server block that serves example.com over HTTPS:

` + wantedNginxLines + `
Caddy, in its Caddyfile, where reverse_proxy sends the visitor's host, client address
and scheme on its own, and Caddy sets no upload limit:

` + wantedCaddyLines + `
Traefik, in a file its file provider reads, where websecure and letsencrypt stand for
your HTTPS entry point and certificate resolver, and Traefik sends the visitor's host,
client address and scheme on its own and sets no size limit:

` + wantedTraefikLines + `
Traefik 3 cuts a request it has not read in full within the entry point's
transport.respondingTimeouts.readTimeout, 60s by default. For large uploads, raise it in
Traefik's install configuration to at least GOPHENBERG_UPLOAD_TIMEOUT, 5m0s unless the
site's .env sets it, or to 0.

Apache, in the virtual host that serves example.com over HTTPS, with its proxy_http and
headers modules enabled:

` + wantedApacheLines

// ownSteps are what each step of a first run for example.com with the person's own web server does, in order.
var ownSteps = []string{
	"start the install record /var/lib/gophenberg-installer/install.json and write the server settings",
	"install PostgreSQL with apt-get",
	"make the system user gophenberg, which runs the site and signs in to PostgreSQL",
	"make the install root /opt/gophenberg and its folders, owned by gophenberg",
	"make the database role gophenberg and its database gophenberg as the postgres user",
	"unpack the release archive into /opt/gophenberg/versions and point /opt/gophenberg/current at it",
	"keep a copy of this installer at /usr/local/lib/gophenberg/gophenberg, the one root runs for server:install " +
		"and server:uninstall",
	"write the site's settings, then run check as gophenberg in /opt/gophenberg",
	"write the systemd units, then reload systemd and enable gophenberg.service",
	"write the gophenberg command on the path",
	"start gophenberg.service and wait until /admin/, /api/locale and / answer on 127.0.0.1:8081",
	"create the first administrator with account:create-admin as gophenberg",
	"print the summary and end the install record",
	"print the lines for nginx, Caddy, Traefik and Apache that send example.com to the site at 127.0.0.1:8081",
}

// ownSite returns the site a first run for domain with the person's own web server finds on a fresh server.
func ownSite(t *testing.T, domain string) site {
	t.Helper()
	return siteOf(t, Request{Domain: domain, AdminEmail: "admin@example.com", WebServer: "none", DryRun: true}, 255)
}

func TestTheLinesForTheirOwnWebServerSendTheDomainToTheSite(t *testing.T) {
	t.Parallel()

	moved := ownSite(t, "blog.example.com")
	moved.settings.Addr, moved.uploadCapMB = netip.MustParseAddrPort("[::1]:8090"), 512
	movedText := strings.NewReplacer("example.com", "blog.example.com", "127.0.0.1:8081", "[::1]:8090",
		"131136k", "524352k", "134283264", "536936448")
	for template, want := range map[string]string{
		"web-server-nginx.conf": wantedNginxLines, "web-server.caddy": wantedCaddyLines,
		"web-server-traefik.yml": wantedTraefikLines, "web-server-apache.conf": wantedApacheLines,
	} {
		if got := render(template, ownSite(t, "example.com").data()); got != want {
			t.Errorf("%s for example.com holds\n%s\nwant\n%s", template, got, want)
		}
		if got, want := render(template, moved.data()), movedText.Replace(want); got != want {
			t.Errorf("%s for blog.example.com on [::1]:8090 with 512 MB holds\n%s\nwant\n%s", template, got, want)
		}
	}
}

func TestTheLinesForTheirOwnWebServerTakeTheLargestBodyTheSiteTakes(t *testing.T) {
	t.Parallel()

	for capMB, limits := range map[int][2]string{
		32: {"65600k", "67174400"}, 64: {"65600k", "67174400"}, 65: {"66624k", "68222976"},
		4096: {"4194368k", "4295032832"}, maxUploadCapMB: {"9007199254740032k", "9223372036853792768"},
	} {
		running := ownSite(t, "example.com")
		running.uploadCapMB = capMB

		nginx, apache := render("web-server-nginx.conf", running.data()), render("web-server-apache.conf", running.data())

		if want := strings.Replace(wantedNginxLines, "131136k", limits[0], 1); nginx != want {
			t.Errorf("the nginx lines for an upload cap of %d MB hold\n%s\nwant\n%s", capMB, nginx, want)
		}
		if want := strings.Replace(wantedApacheLines, "134283264", limits[1], 1); apache != want {
			t.Errorf("the Apache lines for an upload cap of %d MB hold\n%s\nwant\n%s", capMB, apache, want)
		}
	}
}

func TestTheStepsWithTheirOwnWebServerLeaveOutCaddyAndPrintTheLinesForIt(t *testing.T) {
	t.Parallel()

	steps := ownSite(t, "example.com").steps()

	does, ids := doneOf(steps)
	if !slices.Equal(does, ownSteps) {
		t.Errorf("the steps do\n%s\nwant\n%s", strings.Join(does, "\n"), strings.Join(ownSteps, "\n"))
	}
	wantIDs := []string{
		"server-env", "packages", "user", "install-root", "database", "build", "installer-copy", "site-env", "units",
		"command", "start", "admin", "summary", "web-server",
	}
	if !slices.Equal(ids, wantIDs) {
		t.Errorf("the record keeps the steps under %q, want %q", ids, wantIDs)
	}
	ownServerEnv := strings.Replace(firstServerEnv, "WEB_SERVER=caddy", "WEB_SERVER=none", 1)
	wantFiles(t, filesOf(steps), slices.Concat(
		[]file{{"/etc/gophenberg/server.env", "root", 0o600, ownServerEnv}, {siteEnvAt, "gophenberg", 0o600, wantedSiteEnv}},
		unitsOf(wantedSiteUnit)[:2], []file{{"/usr/local/bin/gophenberg", "root", 0o755, wantedCommand}},
	))
	for n, planned := range steps[:len(steps)-1] {
		if planned.prints != "" {
			t.Errorf("step %d prints\n%s\nwant only the last step to print", n+1, planned.prints)
		}
	}
	if got := steps[len(steps)-1].prints; got != wantedWebServerText {
		t.Errorf("the last step prints\n%s\nwant\n%s", got, wantedWebServerText)
	}
}

func TestTheStepsWithTheirOwnWebServerOfALaterRunStillPrintTheLinesForIt(t *testing.T) {
	t.Parallel()

	running := ownSite(t, "example.com")
	running.current = true
	running.record.Steps = map[string]string{stepStart: stepEnded, stepAdmin: stepEnded}

	does, _ := doneOf(running.steps())

	want := slices.Concat(ownSteps[:5],
		[]string{"keep the build /opt/gophenberg/current names, since updates go through update:apply"},
		ownSteps[6:10],
		[]string{"restart gophenberg.service and wait until /admin/, /api/locale and / answer on 127.0.0.1:8081"},
		ownSteps[12:])
	if !slices.Equal(does, want) {
		t.Errorf("the steps do\n%s\nwant\n%s", strings.Join(does, "\n"), strings.Join(want, "\n"))
	}
}

func TestUploadCapMBReadsTheUploadCapAsServeReadsIt(t *testing.T) {
	t.Parallel()

	for value, want := range map[string]int{
		"": 128, "512": 512, " 64 ": 64, strconv.Itoa(maxUploadCapMB): maxUploadCapMB,
	} {
		got, err := UploadCapMB(envOf(map[string]string{"GOPHENBERG_MEDIA_UPLOAD_CAP_MB": value}))

		if err != nil || got != want {
			t.Errorf("UploadCapMB(%q) = %d, %v, want %d", value, got, err, want)
		}
	}
}

func TestUploadCapMBRefusesAnUploadCapServeRefuses(t *testing.T) {
	t.Parallel()

	for value, complaint := range map[string]string{
		"big": `must be a whole number, got "big"`, "0": `must stand above zero, got "0"`,
		strconv.Itoa(maxUploadCapMB + 1): `must stand at or below ` + strconv.Itoa(maxUploadCapMB) + `, got "` +
			strconv.Itoa(maxUploadCapMB+1) + `"`,
	} {
		_, err := UploadCapMB(envOf(map[string]string{"GOPHENBERG_MEDIA_UPLOAD_CAP_MB": value}))

		wantRefused(t, err, "GOPHENBERG_MEDIA_UPLOAD_CAP_MB: "+complaint)
	}
}
