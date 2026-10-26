// SPDX-License-Identifier: Apache-2.0

package installer

import (
	"bytes"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"net/netip"
	"strings"
	"testing"
	"time"
)

// wantedSiteUnit is the site's unit a first run writes on systemd 255 with every default.
const wantedSiteUnit = `# Written by gophenberg server:install.
[Unit]
Description=Gophenberg
Documentation=https://docs.gophenberg.org/self-hosting/server-install/
After=network-online.target postgresql.service
Wants=network-online.target
StartLimitIntervalSec=300
StartLimitBurst=10
OnFailure=gophenberg-rollback.service

[Service]
Type=exec
User=gophenberg
Group=gophenberg
WorkingDirectory=/opt/gophenberg
ExecStart=/opt/gophenberg/current/gophenberg serve
Restart=on-failure
RestartMode=direct
RestartSec=5s
SuccessExitStatus=75
RestartForceExitStatus=75
RestartPreventExitStatus=76
TimeoutStopSec=30s
KillMode=control-group
NoNewPrivileges=true
ProtectSystem=strict
ReadWritePaths=/opt/gophenberg
PrivateTmp=yes

[Install]
WantedBy=multi-user.target
`

// wantedRollbackUnit is the rollback unit a first run writes with every default.
const wantedRollbackUnit = `# Written by gophenberg server:install.
[Unit]
Description=Roll Gophenberg back after a failed update
Documentation=https://docs.gophenberg.org/self-hosting/updates-and-backups/#a-version-that-fails-its-check
ConditionPathExists=/opt/gophenberg/previous
StartLimitIntervalSec=0

[Service]
Type=oneshot
User=gophenberg
Group=gophenberg
WorkingDirectory=/opt/gophenberg
ExecStart=/opt/gophenberg/previous/gophenberg update:rollback -after-failure
ExecStartPost=+/usr/bin/systemctl reset-failed gophenberg.service
ExecStartPost=+/usr/bin/systemctl start gophenberg.service
NoNewPrivileges=true
ProtectSystem=strict
ReadWritePaths=/opt/gophenberg
PrivateTmp=yes
`

// wantedCaddyDropIn is the drop-in that gives Caddy a folder for its admin socket only Caddy and root open.
const wantedCaddyDropIn = `# Written by gophenberg server:install.
[Service]
RuntimeDirectory=caddy
RuntimeDirectoryMode=0700
`

// wantedCaddyfile is the Caddyfile a first run writes for example.com with the first administrator's email.
const wantedCaddyfile = `# Written by gophenberg server:install on its first run.
{
	email admin@example.com
	admin unix//run/caddy/admin.sock
}

import /etc/caddy/gophenberg.caddy
`

// wantedLocalCaddyfile is the Caddyfile a first run writes for localhost or with no certificate email.
const wantedLocalCaddyfile = `# Written by gophenberg server:install on its first run.
{
	admin unix//run/caddy/admin.sock
}

import /etc/caddy/gophenberg.caddy
`

// wantedCaddySite is Gophenberg's Caddy site for example.com with every default.
const wantedCaddySite = `# Written by gophenberg server:install.
example.com {
	reverse_proxy 127.0.0.1:8081 {
		lb_try_duration 10s
	}
	handle_errors 502 503 {
		root * /usr/local/share/gophenberg/maintenance
		rewrite * /index.html
		header Retry-After 600
		file_server {
			status 503
		}
	}
}
`

// wantedMaintenancePage is the page Caddy shows while the site restarts, one message in a white card on a light page.
const wantedMaintenancePage = `<!DOCTYPE html>
<html lang="en">
<head>
	<meta charset="utf-8">
	<meta name="viewport" content="width=device-width, initial-scale=1">
	<title>Briefly unavailable</title>
	<style>
		body {
			margin: 0;
			padding: 48px 16px;
			background: #fcfcfc;
			color: #1e1e1e;
			font-family: system-ui, sans-serif;
		}
		main {
			box-sizing: border-box;
			max-width: 700px;
			margin: 0 auto;
			padding: 24px 32px;
			border: 1px solid #dbdbdb;
			border-radius: 8px;
			background: #fff;
		}
		p {
			margin: 0;
			font-size: 16px;
			line-height: 1.5;
		}
	</style>
</head>
<body>
	<main>
		<p>This site is briefly unavailable. Check back in a minute.</p>
	</main>
</body>
</html>
`

// wantedCommand is the gophenberg command on the path a first run writes with every default.
const wantedCommand = `#!/bin/sh
# Written by gophenberg server:install.
if [ "$(id -u)" -ne 0 ]; then
	echo "gophenberg: run it with sudo, such as sudo gophenberg update:status" >&2
	exit 1
fi
case "${1-}" in
server:install | server:uninstall)
	cd / || exit 1
	exec /usr/local/lib/gophenberg/gophenberg "$@"
	;;
esac
cd /opt/gophenberg || exit 1
/usr/sbin/runuser --pty -u gophenberg -- /opt/gophenberg/current/gophenberg "$@"
code=$?
if [ "$code" -ne 75 ]; then
	exit "$code"
fi
/usr/bin/systemctl reset-failed gophenberg.service
/usr/bin/systemctl restart gophenberg.service || exit 1
echo "gophenberg: restarted the site to start the new build"
`

// wantedCaddySource is the apt source of Caddy's own repository, signed by its key alone.
const wantedCaddySource = `# Written by gophenberg server:install.
Types: deb
URIs: https://dl.cloudsmith.io/public/caddy/stable/deb/debian
Suites: any-version
Components: main
Signed-By: /etc/apt/keyrings/caddy.asc
`

// wantedCaddyPin is the apt pin that lets Caddy's own repository install caddy and nothing else.
const wantedCaddyPin = `Explanation: Written by gophenberg server:install.
Package: caddy
Pin: origin dl.cloudsmith.io
Pin-Priority: 500

Package: *
Pin: origin dl.cloudsmith.io
Pin-Priority: -1
`

// wantedCaddyUpgrades is the line that lets unattended-upgrades install Caddy's fixes from its own repository.
const wantedCaddyUpgrades = `# Written by gophenberg server:install.
Unattended-Upgrade::Origins-Pattern {
	"origin=cloudsmith/caddy/stable,site=dl.cloudsmith.io";
};
`

// caddyKeyFingerprint is the fingerprint of the key that signs Caddy's own apt repository.
const caddyKeyFingerprint = "65760C51EDEA2017CEA2CA15155B6D79CA56EA34"

// firstSite returns the site a first run for domain finds on a fresh server run by systemd major, with every default.
func firstSite(t *testing.T, domain string, major int) site {
	t.Helper()
	return siteOf(t, Request{Domain: domain, AdminEmail: "admin@example.com", DryRun: true}, major)
}

// siteOf returns the site a first run as request asks finds on a fresh server run by systemd major, with every default.
func siteOf(t *testing.T, request Request, major int) site {
	t.Helper()
	settings, err := settingsOn(serverWith(t, nil), envOf(nil), request)
	if err != nil {
		t.Fatalf("settingsOn(%+v) = %v, want the defaults", request, err)
	}
	graces, err := StopGraces(envOf(nil))
	if err != nil {
		t.Fatalf("StopGraces() = %v, want the defaults", err)
	}
	return site{
		settings: settings, env: envOf(nil), systemd: major, systemctl: "/usr/bin/systemctl",
		runuser: "/usr/sbin/runuser", graces: graces, uploadCapMB: 128,
	}
}

// wantFiles fails t unless got are the files want, path, owner, mode and text alike.
func wantFiles(t *testing.T, got, want []file) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("got %d files %v, want %d", len(got), pathsOf(got), len(want))
		return
	}
	for n := range want {
		if got[n] != want[n] {
			t.Errorf("file %d is %s, owned by %s with mode %04o:\n%s\nwant %s, owned by %s with mode %04o:\n%s", n,
				got[n].path, got[n].owner, got[n].mode, got[n].text, want[n].path, want[n].owner, want[n].mode,
				want[n].text)
		}
	}
}

// pathsOf returns the path of each of files.
func pathsOf(files []file) []string {
	paths := make([]string, 0, len(files))
	for _, written := range files {
		paths = append(paths, written.path)
	}
	return paths
}

// unitsOf returns the units and the drop-in a site with these units writes, the site's unit holding siteUnit.
func unitsOf(siteUnit string) []file {
	return []file{
		{"/etc/systemd/system/gophenberg.service", "root", 0o644, siteUnit},
		{"/etc/systemd/system/gophenberg-rollback.service", "root", 0o644, wantedRollbackUnit},
		{"/etc/systemd/system/caddy.service.d/gophenberg.conf", "root", 0o644, wantedCaddyDropIn},
	}
}

func TestTheUnitsOnSystemd255RestartTheSiteDirectlyAndRollItBackWithNoStartLimit(t *testing.T) {
	t.Parallel()

	got := firstSite(t, "example.com", 255).unitFiles()

	wantFiles(t, got, unitsOf(wantedSiteUnit))
}

func TestTheUnitsWriteRestartModeFromSystemd254On(t *testing.T) {
	t.Parallel()

	without := strings.Replace(wantedSiteUnit, "RestartMode=direct\n", "", 1)
	for major, want := range map[int]string{254: wantedSiteUnit, 253: without, 252: without, 251: without} {
		got := firstSite(t, "example.com", major).unitFiles()

		wantFiles(t, got, unitsOf(want))
	}
}

func TestTheSiteUnitStopsWithinEveryGraceServeWaitsThroughAndTheMargin(t *testing.T) {
	t.Parallel()

	for _, stopping := range []struct {
		graces map[string]string
		margin time.Duration
		want   string
	}{
		{nil, 5 * time.Second, "30s"},
		{map[string]string{"GOPHENBERG_SHUTDOWN_GRACE": "4m"}, 5 * time.Second, "260s"},
		{map[string]string{"GOPHENBERG_CRON_STOP_GRACE": "1500ms", "GOPHENBERG_THEME_STOP_GRACE": "1500ms"},
			5 * time.Second, "28s"},
		{map[string]string{"GOPHENBERG_SHUTDOWN_GRACE": "10100ms"}, 5 * time.Second, "31s"},
		{nil, 2500 * time.Millisecond, "28s"},
		{map[string]string{
			"GOPHENBERG_SHUTDOWN_GRACE": "1s", "GOPHENBERG_SHUTDOWN_CANCEL_GRACE": "1s",
			"GOPHENBERG_CRON_STOP_GRACE": "1s", "GOPHENBERG_SHUTDOWN_STOP_GRACE": "1s",
			"GOPHENBERG_THEME_STOP_GRACE": "1ns",
		}, time.Nanosecond, "5s"},
		{map[string]string{
			"GOPHENBERG_SHUTDOWN_GRACE": "2562047h", "GOPHENBERG_SHUTDOWN_CANCEL_GRACE": "2562047h",
			"GOPHENBERG_CRON_STOP_GRACE": "2562047h", "GOPHENBERG_SHUTDOWN_STOP_GRACE": "2562047h",
			"GOPHENBERG_THEME_STOP_GRACE": "2562047h",
		}, 2562047 * time.Hour, "55340215200s"},
	} {
		running := firstSite(t, "example.com", 255)
		graces, err := StopGraces(envOf(stopping.graces))
		if err != nil {
			t.Fatalf("StopGraces(%v) = %v, want the graces read", stopping.graces, err)
		}
		running.graces, running.settings.Restarts.StopMargin = graces, stopping.margin

		got := running.unitFiles()

		want := strings.Replace(wantedSiteUnit, "TimeoutStopSec=30s", "TimeoutStopSec="+stopping.want, 1)
		wantFiles(t, got, unitsOf(want))
	}
}

func TestTheUnitsFollowTheInstallRootTheStartLimitTheRestartDelayAndSystemctl(t *testing.T) {
	t.Parallel()

	running := firstSite(t, "example.com", 255)
	running.settings.Root, running.systemctl = "/srv/gophenberg", "/bin/systemctl"
	running.settings.Restarts.LimitInterval, running.settings.Restarts.LimitBurst = time.Minute, 20
	running.settings.Restarts.Delay = 2 * time.Second

	got := running.unitFiles()

	siteUnit := strings.NewReplacer("/opt/gophenberg", "/srv/gophenberg", "StartLimitIntervalSec=300",
		"StartLimitIntervalSec=60", "StartLimitBurst=10", "StartLimitBurst=20", "RestartSec=5s", "RestartSec=2s",
	).Replace(wantedSiteUnit)
	rollbackUnit := strings.NewReplacer("/opt/gophenberg", "/srv/gophenberg", "/usr/bin/systemctl", "/bin/systemctl").
		Replace(wantedRollbackUnit)
	wantFiles(t, got, []file{
		{"/etc/systemd/system/gophenberg.service", "root", 0o644, siteUnit},
		{"/etc/systemd/system/gophenberg-rollback.service", "root", 0o644, rollbackUnit},
		{"/etc/systemd/system/caddy.service.d/gophenberg.conf", "root", 0o644, wantedCaddyDropIn},
	})
}

// caddyFilesOf returns the Caddy files a first run writes, holding caddySite and caddyfile.
func caddyFilesOf(caddySite, caddyfile string) []file {
	return []file{
		{"/usr/local/share/gophenberg/maintenance/index.html", "root", 0o644, wantedMaintenancePage},
		{"/etc/caddy/gophenberg.caddy", "root", 0o644, caddySite},
		{"/etc/caddy/Caddyfile", "root", 0o644, caddyfile},
	}
}

func TestTheCaddyFilesOfARealDomainNameTheCertificateEmailTheAdminSocketAndTheImport(t *testing.T) {
	t.Parallel()

	got := firstSite(t, "example.com", 255).caddyFiles()

	wantFiles(t, got, caddyFilesOf(wantedCaddySite, wantedCaddyfile))
}

func TestTheCaddyFilesOfLocalhostNameNoCertificateEmail(t *testing.T) {
	t.Parallel()

	got := firstSite(t, "localhost", 255).caddyFiles()

	wantFiles(t, got, caddyFilesOf(strings.Replace(wantedCaddySite, "example.com {", "localhost {", 1),
		wantedLocalCaddyfile))
}

func TestTheCaddyFilesNameNoCertificateEmailWhenTheRunKnowsNone(t *testing.T) {
	t.Parallel()

	running := firstSite(t, "example.com", 255)
	running.settings.CertificateEmail = ""

	got := running.caddyFiles()

	wantFiles(t, got, caddyFilesOf(wantedCaddySite, wantedLocalCaddyfile))
}

func TestTheCaddySiteFollowsTheSitesAddressTheProxyRetryAndTheRetryAfter(t *testing.T) {
	t.Parallel()

	running := firstSite(t, "blog.example.com", 255)
	running.settings.Addr = netip.MustParseAddrPort("[::1]:8090")
	running.settings.Restarts.ProxyRetry, running.settings.Restarts.RetryAfter = 1500*time.Millisecond, 2*time.Minute
	running.settings.CertificateEmail = "maria@example.com"

	got := running.caddyFiles()

	caddySite := strings.NewReplacer("example.com {", "blog.example.com {", "127.0.0.1:8081", "[::1]:8090",
		"lb_try_duration 10s", "lb_try_duration 1.5s", "Retry-After 600", "Retry-After 120").Replace(wantedCaddySite)
	caddyfile := strings.Replace(wantedCaddyfile, "admin@example.com", "maria@example.com", 1)
	wantFiles(t, got, caddyFilesOf(caddySite, caddyfile))
}

func TestTheCaddyFilesOfALaterRunLeaveTheCaddyfileAlone(t *testing.T) {
	t.Parallel()

	running := firstSite(t, "example.com", 255)
	running.record.Steps = map[string]string{stepCaddy: stepEnded}

	got := running.caddyFiles()

	wantFiles(t, got, caddyFilesOf(wantedCaddySite, wantedCaddyfile)[:2])
}

func TestTheCaddyFilesOfARunCutOffBeforeItsCaddyStepEndedWriteTheCaddyfile(t *testing.T) {
	t.Parallel()

	running := firstSite(t, "example.com", 255)
	running.record.Steps = map[string]string{stepCaddy: "started"}

	got := running.caddyFiles()

	wantFiles(t, got, caddyFilesOf(wantedCaddySite, wantedCaddyfile))
}

func TestTheCommandOnThePathRunsTheInstallersCopyAsRootAndEveryOtherCommandAsTheSite(t *testing.T) {
	t.Parallel()

	got := firstSite(t, "example.com", 255).commandFile()

	wantFiles(t, []file{got}, []file{{"/usr/local/bin/gophenberg", "root", 0o755, wantedCommand}})
}

func TestTheCommandOnThePathFollowsTheInstallRootRunuserAndSystemctl(t *testing.T) {
	t.Parallel()

	running := firstSite(t, "example.com", 255)
	running.settings.Root, running.runuser, running.systemctl = "/srv/gophenberg", "/sbin/runuser", "/bin/systemctl"

	got := running.commandFile()

	want := strings.NewReplacer("/opt/gophenberg", "/srv/gophenberg", "/usr/sbin/runuser", "/sbin/runuser",
		"/usr/bin/systemctl", "/bin/systemctl").Replace(wantedCommand)
	wantFiles(t, []file{got}, []file{{"/usr/local/bin/gophenberg", "root", 0o755, want}})
}

// aptFilesOf returns the apt files that bring Caddy from its own repository, the unattended-upgrades line last.
func aptFilesOf() []file {
	return []file{
		{"/etc/apt/keyrings/caddy.asc", "root", 0o644, caddyKey},
		{"/etc/apt/sources.list.d/caddy.sources", "root", 0o644, wantedCaddySource},
		{"/etc/apt/preferences.d/gophenberg-caddy", "root", 0o644, wantedCaddyPin},
		{"/etc/apt/apt.conf.d/52gophenberg-caddy", "root", 0o644, wantedCaddyUpgrades},
	}
}

func TestTheAptFilesLetCaddysOwnRepositoryInstallCaddyAloneAndUnattendedUpgradesItsFixes(t *testing.T) {
	t.Parallel()

	running := firstSite(t, "example.com", 255)
	running.unattended = true

	got := running.aptFiles()

	wantFiles(t, got, aptFilesOf())
}

func TestTheAptFilesLeaveUnattendedUpgradesAloneWhereItIsNotInstalled(t *testing.T) {
	t.Parallel()

	got := firstSite(t, "example.com", 255).aptFiles()

	wantFiles(t, got, aptFilesOf()[:3])
}

// fingerprintOf returns the fingerprint of the first public key of armored, an OpenPGP public key block.
func fingerprintOf(t *testing.T, armored string) string {
	t.Helper()
	header, rest, _ := strings.Cut(armored, "\n\n")
	body, _, _ := strings.Cut(rest, "\n=")
	if header != "-----BEGIN PGP PUBLIC KEY BLOCK-----" {
		t.Fatalf("the key is no armored OpenPGP public key block:\n%s", armored)
	}
	packets, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(body, "\n", ""))
	if err != nil {
		t.Fatalf("the key's armor holds no packets: %v", err)
	}
	if !bytes.HasPrefix(packets, []byte{0x99}) {
		t.Fatalf("the key's packets start with %x, want a public key packet of a two-byte length", packets[:1])
	}
	length := int(packets[1])<<8 | int(packets[2])
	sum := sha1.Sum(packets[:3+length])
	return strings.ToUpper(hex.EncodeToString(sum[:]))
}

func TestTheCaddyKeyIsTheOneThatSignsCaddysOwnRepository(t *testing.T) {
	t.Parallel()

	if got := fingerprintOf(t, caddyKey); got != caddyKeyFingerprint {
		t.Errorf("the Caddy key has the fingerprint %s, want %s", got, caddyKeyFingerprint)
	}
	if !strings.HasSuffix(caddyKey, "\n-----END PGP PUBLIC KEY BLOCK-----\n") {
		t.Errorf("the Caddy key does not end its block with a new line:\n%s", caddyKey)
	}
}

func TestRenderPanicsOnATemplateItDoesNotHold(t *testing.T) {
	t.Parallel()

	defer func() {
		if recover() == nil {
			t.Error("render() of a template it does not hold returned, want a panic")
		}
	}()

	render("missing.conf", fileData{})
}
