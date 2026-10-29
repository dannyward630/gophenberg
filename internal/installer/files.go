// SPDX-License-Identifier: Apache-2.0

package installer

import (
	"embed"
	"io/fs"
	"strings"
	"text/template"
	"time"

	"github.com/gopherium/gophenberg/internal/server"
	"github.com/gopherium/gophenberg/internal/themehost"
)

// caddyKeyPath is where apt keeps the key of Caddy's own repository, which only that repository's source names.
const caddyKeyPath = "/etc/apt/keyrings/caddy.asc"

// caddySitePath is Gophenberg's Caddy site, which every run writes again and the Caddyfile imports.
const caddySitePath = "/etc/caddy/gophenberg.caddy"

// installerCopy is the installer's own copy, the binary root runs for server:install and server:uninstall.
const installerCopy = "/usr/local/lib/gophenberg/gophenberg"

// commandPath is the gophenberg command on the path, which root runs for every command a person types.
const commandPath = "/usr/local/bin/gophenberg"

// maintenanceFolder is the root-owned folder of the page Caddy shows while the site restarts, outside the site's tree.
const maintenanceFolder = "/usr/local/share/gophenberg/maintenance"

// restartModeSystemd is the oldest systemd that knows RestartMode=direct.
const restartModeSystemd = 254

// templateFiles holds the template of every file server:install writes.
//
//go:embed templates
var templateFiles embed.FS

// templates are the templates of templateFiles, each named by its file.
var templates = template.Must(template.New("").ParseFS(templateFiles, "templates/*"))

// caddyKey is the armored public key that signs Caddy's own apt repository, as Caddy publishes it.
var caddyKey = render("caddy.asc", fileData{})

// fileData is what the templates write their files and lines from, each timing of the units in whole seconds.
type fileData struct {
	Root, Domain, Email, Addr                          string
	RestartMode                                        bool
	LimitInterval, LimitBurst, RestartSec, TimeoutStop int64
	ProxyRetry                                         string
	RetryAfter                                         int64
	Systemctl, Runuser, Installer                      string
	Maintenance, CaddySite, Key                        string
	UploadMB                                           int
	BodyKB, BodyBytes, ThemeMB, EnvelopeKB             int64
	UploadSetting, UploadTimeout, TimeoutSetting       string
}

// render returns the text the template called name writes from data, panicking on a template the package lacks.
func render(name string, data fileData) string {
	var text strings.Builder
	if err := templates.ExecuteTemplate(&text, name, data); err != nil {
		panic(err)
	}
	return text.String()
}

// data returns what the templates write the files of this site from.
func (s site) data() fileData {
	restarts, body := s.settings.Restarts, bodyLimit(s.uploadCapMB)
	return fileData{
		Root: s.settings.Root, Domain: s.settings.Domain, Email: s.certificateEmail(), Addr: s.settings.Addr.String(),
		RestartMode: s.systemd >= restartModeSystemd, LimitInterval: inSeconds(restarts.LimitInterval),
		LimitBurst: int64(restarts.LimitBurst), RestartSec: inSeconds(restarts.Delay),
		TimeoutStop: wholeSeconds(append(s.graces, restarts.StopMargin)...), ProxyRetry: restarts.ProxyRetry.String(),
		RetryAfter: inSeconds(restarts.RetryAfter), Systemctl: s.systemctl, Runuser: s.runuser, Installer: installerCopy,
		Maintenance: maintenanceFolder, CaddySite: caddySitePath, Key: caddyKeyPath, UploadMB: s.uploadCapMB,
		BodyKB: body >> 10, BodyBytes: body, ThemeMB: themehost.MaxSize >> 20, EnvelopeKB: server.UploadEnvelope >> 10,
		UploadSetting: s.env.Key("MEDIA_UPLOAD_CAP_MB"), UploadTimeout: server.DefaultUploadTimeout.String(),
		TimeoutSetting: s.env.Key("UPLOAD_TIMEOUT"),
	}
}

// bodyLimit returns the largest request body the site takes, a media upload under capMB or a theme, framing included.
func bodyLimit(capMB int) int64 {
	return max(int64(capMB)<<20, themehost.MaxSize) + server.UploadEnvelope
}

// certificateEmail returns the email of Caddy's certificate account, none for localhost, which Caddy signs itself.
func (s site) certificateEmail() string {
	if s.settings.Domain == "localhost" {
		return ""
	}
	return s.settings.CertificateEmail
}

// inSeconds returns duration in whole seconds, which the settings already ask it to be.
func inSeconds(duration time.Duration) int64 {
	return int64(duration / time.Second)
}

// wholeSeconds returns durations added up in whole seconds, a leftover part of a second counted as one.
func wholeSeconds(durations ...time.Duration) int64 {
	var whole, part int64
	for _, duration := range durations {
		whole += int64(duration / time.Second)
		part += int64(duration % time.Second)
	}
	return whole + (part+int64(time.Second)-1)/int64(time.Second)
}

// rootFile returns the file at path that root owns with mode, its text written by the template called name.
func (s site) rootFile(path string, mode fs.FileMode, name string) file {
	return file{path: path, owner: "root", mode: mode, text: render(name, s.data())}
}

// unitFiles returns the site's unit, the rollback unit and, with Caddy, the drop-in that gives Caddy its socket folder.
func (s site) unitFiles() []file {
	units := []file{
		s.rootFile("/etc/systemd/system/gophenberg.service", 0o644, "gophenberg.service"),
		s.rootFile("/etc/systemd/system/gophenberg-rollback.service", 0o644, "gophenberg-rollback.service"),
	}
	if !s.settings.withCaddy() {
		return units
	}
	return append(units, s.rootFile("/etc/systemd/system/caddy.service.d/gophenberg.conf", 0o644,
		"caddy-gophenberg.conf"))
}

// caddyFiles returns the maintenance page, Gophenberg's Caddy site and, until the record holds its sum, the Caddyfile.
func (s site) caddyFiles() []file {
	files := []file{
		s.rootFile(maintenanceFolder+"/index.html", 0o644, "maintenance.html"),
		s.rootFile(caddySitePath, 0o644, "gophenberg.caddy"),
	}
	if s.record.Caddyfile != "" {
		return files
	}
	return append(files, s.rootFile(caddyfilePath, 0o644, "Caddyfile"))
}

// commandFile returns the gophenberg command on the path.
func (s site) commandFile() file {
	return s.rootFile(commandPath, 0o755, "gophenberg")
}

// aptFiles returns the files that bring Caddy alone from its own repository, and its unattended-upgrades line.
func (s site) aptFiles() []file {
	files := []file{
		s.rootFile(caddyKeyPath, 0o644, "caddy.asc"),
		s.rootFile("/etc/apt/sources.list.d/caddy.sources", 0o644, "caddy.sources"),
		s.rootFile("/etc/apt/preferences.d/gophenberg-caddy", 0o644, "gophenberg-caddy"),
	}
	if !s.unattended {
		return files
	}
	return append(files, s.rootFile("/etc/apt/apt.conf.d/52gophenberg-caddy", 0o644, "52gophenberg-caddy"))
}
