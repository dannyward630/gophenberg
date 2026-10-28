// SPDX-License-Identifier: Apache-2.0

package installer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/gopherium/framework/gonsole"

	"github.com/gopherium/gophenberg/internal/version"
)

// installerFolder is root's own folder of the install record and of the archives install.sh downloads.
const installerFolder = "/var/lib/gophenberg-installer"

// recordPath is where server:install keeps its install record, from the top of the server.
const recordPath = installerFolder + "/install.json"

// piecePostgreSQL is how the install record names the PostgreSQL server:install installed.
const piecePostgreSQL = "postgresql"

// pieceCaddy is how the install record names the Caddy server:install installed.
const pieceCaddy = "caddy"

// pieceService is how the install record names the gophenberg.service unit server:install wrote.
const pieceService = "gophenberg.service"

// stepStarted is how the install record marks a step a run started and has not ended.
const stepStarted = "started"

// Record is the install record of the newest installer: its steps, the pieces it installed, its domain and Caddyfile.
type Record struct {
	Installer string            `json:"installer"`
	Steps     map[string]string `json:"steps,omitempty"`
	Installed []string          `json:"installed,omitempty"`
	Domain    string            `json:"domain,omitempty"`
	Caddyfile string            `json:"caddyfile,omitempty"`
}

// caddyfileSum returns the SHA-256 of the Caddyfile text in hex, as the record keeps the one the first run wrote.
func caddyfileSum(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// readRecord returns the install record on machine, empty before a first run, refusing one that is not a record.
func readRecord(machine Machine) (Record, error) {
	body, err := machine.read(recordPath)
	if err != nil || body == nil {
		return Record{}, err
	}
	var record Record
	if err := json.Unmarshal(body, &record); err != nil {
		return Record{}, fmt.Errorf("installer: %s is not an install record: %w", recordPath, err)
	}
	if !version.Valid(record.Installer) {
		return Record{}, fmt.Errorf("installer: %s names the installer %q, which is not a version", recordPath,
			record.Installer)
	}
	return record, nil
}

// ended reports whether the record says the step it keeps under id ended.
func (r Record) ended(id string) bool {
	return r.Steps[id] == stepEnded
}

// started reports whether the record says a run started the step it keeps under id and did not end it.
func (r Record) started(id string) bool {
	return r.Steps[id] == stepStarted
}

// own adds piece to what the record says server:install installed, once.
func (r *Record) own(piece string) {
	if !r.owns(piece) {
		r.Installed = append(r.Installed, piece)
	}
}

// owns reports whether the record says server:install installed piece itself, the install root named by its path.
func (r Record) owns(piece string) bool {
	return slices.Contains(r.Installed, piece)
}

// allowsWebServer refuses a run without Caddy while Gophenberg's Caddy site is on machine, whatever packages it owns.
func allowsWebServer(machine Machine, settings Settings, env gonsole.Env) error {
	if settings.withCaddy() || !machine.there(caddySitePath) {
		return nil
	}
	return webServerMoved(env, webServerCaddy, settings.WebServer)
}

// allows refuses an installer older than the one the record names, which would write its older files over newer ones.
func (r Record) allows(running string) error {
	if version.Before(running, r.Installer) {
		return fmt.Errorf("this installer is %s, and this server was set up by the %s installer, so run the install.sh "+
			"line again", running, r.Installer)
	}
	return nil
}
