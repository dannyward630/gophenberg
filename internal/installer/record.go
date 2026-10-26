// SPDX-License-Identifier: Apache-2.0

package installer

import (
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

// Record is the install record of the newest installer, its steps, the pieces it installed and the domain it proved.
type Record struct {
	Installer string            `json:"installer"`
	Steps     map[string]string `json:"steps,omitempty"`
	Installed []string          `json:"installed,omitempty"`
	Domain    string            `json:"domain,omitempty"`
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

// owns reports whether the record says server:install installed piece itself, the install root named by its path.
func (r Record) owns(piece string) bool {
	return slices.Contains(r.Installed, piece)
}

// allowsWebServer refuses a run without Caddy on a server where the record says server:install installed Caddy.
func (r Record) allowsWebServer(settings Settings, env gonsole.Env) error {
	if settings.withCaddy() || !r.owns(pieceCaddy) {
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
