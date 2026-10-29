// SPDX-License-Identifier: Apache-2.0

package installer

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/joho/godotenv"

	"github.com/gopherium/framework/gonsole"
)

// DatabaseAddress is the address the site's .env names, the gophenberg role signing in by peer over the local socket.
const DatabaseAddress = "postgres:///gophenberg?host=/var/run/postgresql&user=gophenberg"

// siteEnvHeader is the first line of the site's .env, naming what writes it.
const siteEnvHeader = "# Written by gophenberg server:install."

// DatabaseWait is the database wait the site's .env gets while it names none, the one serve waits by default.
const DatabaseWait = "60s"

// maxSiteEnvBytes is the largest .env the installer reads, far above what any site needs.
const maxSiteEnvBytes = 1 << 20

// maxUploadCapMB is the most megabytes an upload cap may name and still be counted in bytes, as serve allows.
const maxUploadCapMB = math.MaxInt64 >> 20

// UploadCapMB returns the most megabytes one upload may hold under env, read as serve reads it.
func UploadCapMB(env gonsole.Env) (int, error) {
	return env.Count("MEDIA_UPLOAD_CAP_MB", 128, gonsole.AtMost(maxUploadCapMB))
}

// stopGraces are the five graces serve waits through as it stops, in order, each by its setting and its default.
var stopGraces = []struct {
	name     string
	fallback time.Duration
}{
	{"SHUTDOWN_GRACE", 10 * time.Second},
	{"SHUTDOWN_CANCEL_GRACE", 5 * time.Second},
	{"CRON_STOP_GRACE", 2 * time.Second},
	{"SHUTDOWN_STOP_GRACE", 5 * time.Second},
	{"THEME_STOP_GRACE", 3 * time.Second},
}

// StopGraces returns the five graces serve waits through as it stops under env, read as serve reads them.
func StopGraces(env gonsole.Env) ([]time.Duration, error) {
	graces := make([]time.Duration, 0, len(stopGraces))
	var refused []error
	for _, grace := range stopGraces {
		read, err := env.Duration(grace.name, grace.fallback)
		graces, refused = append(graces, read), append(refused, err)
	}
	if err := errors.Join(refused...); err != nil {
		return nil, err
	}
	return graces, nil
}

// envSetting is one line of the site's .env, by its key and its value.
type envSetting struct {
	key, value string
}

// siteEnvFile returns the site's .env: its header, the lines the server decides, then every other line the site holds.
func (s site) siteEnvFile() file {
	lines := []string{siteEnvHeader}
	for _, decided := range s.decidedSettings() {
		lines = append(lines, envLine(decided.key, decided.value))
	}
	for _, key := range s.keptKeys() {
		lines = append(lines, envLine(key, s.dotEnv[key]))
	}
	text := strings.Join(lines, "\n") + "\n"
	return file{path: filepath.Join(s.settings.Root, ".env"), owner: "gophenberg", mode: 0o600, text: text}
}

// decidedSettings returns the lines of the site's .env the server decides, in order, keeping a database wait it sets.
func (s site) decidedSettings() []envSetting {
	root, wait := s.settings.Root, s.env.Key("DATABASE_WAIT")
	return []envSetting{
		{s.env.Key("DATABASE_URL"), DatabaseAddress},
		{s.env.Key("SUPERUSER_POLICY"), "refuse"},
		{s.env.Key("INSTALL_ROOT"), root},
		{s.env.Key("ADDR"), s.settings.Addr.String()},
		{s.env.Key("PUBLIC_URL"), "https://" + s.settings.Domain},
		{s.env.Key("TRUSTED_PROXIES"), "127.0.0.1/32,::1/128"},
		{s.env.Key("THEMES_DIR"), root + "/themes"},
		{s.env.Key("MEDIA_DIR"), root + "/media"},
		{wait, cmp.Or(s.dotEnv[wait], DatabaseWait)},
	}
}

// keptKeys returns the sorted keys of the lines the person added to the site's .env, which a run keeps as they are.
func (s site) keptKeys() []string {
	kept := maps.Clone(s.dotEnv)
	for _, decided := range s.decidedSettings() {
		delete(kept, decided.key)
	}
	return slices.Sorted(maps.Keys(kept))
}

// siteEnv returns the settings of the site's .env under root, read through root itself, none before one is there.
func (m Machine) siteEnv(root string) (map[string]string, error) {
	tree, err := os.OpenRoot(filepath.Join(m.Root, root))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("installer: %w", err)
	}
	defer func() { _ = tree.Close() }()
	settings, err := readSiteEnv(tree)
	if err != nil {
		return nil, fmt.Errorf("installer: %s/.env: %w", root, err)
	}
	return settings, nil
}

// readSiteEnv returns the settings of the .env in tree, none when it is not there.
func readSiteEnv(tree *os.Root) (map[string]string, error) {
	body, err := dotEnvBody(tree)
	if err != nil || body == nil {
		return nil, err
	}
	if len(body) > maxSiteEnvBytes {
		return nil, fmt.Errorf("it holds more than %d bytes, more than any site needs", maxSiteEnvBytes)
	}
	parsed, err := godotenv.Parse(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	return parsed, unprintable(parsed)
}

// unprintable refuses a setting whose name or value is not printable.
func unprintable(settings map[string]string) error {
	for _, key := range slices.Sorted(maps.Keys(settings)) {
		if !printable(key) || !printable(settings[key]) {
			return fmt.Errorf("%q holds a control character or a byte that is not UTF-8, which no setting needs", key)
		}
	}
	return nil
}

// printable reports whether text is UTF-8 that holds no control character but a tab.
func printable(text string) bool {
	return utf8.ValidString(text) && !strings.ContainsFunc(text, control)
}

// control reports whether r is a control character other than a tab.
func control(r rune) bool {
	return r != '\t' && unicode.IsControl(r)
}

// dotEnvBody returns at most one byte over maxSiteEnvBytes of the .env in tree, opened without waiting on a pipe.
func dotEnvBody(tree *os.Root) ([]byte, error) {
	file, err := tree.OpenFile(".env", os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	if err := soleFile(file.Stat()); err != nil {
		return nil, err
	}
	return io.ReadAll(io.LimitReader(file, maxSiteEnvBytes+1))
}

// soleFile refuses a .env that info, or the err of describing it, does not show as a regular file with one name.
func soleFile(info fs.FileInfo, err error) error {
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("it is not a regular file")
	}
	if stat, described := info.Sys().(*syscall.Stat_t); described && stat.Nlink > 1 {
		return fmt.Errorf("it has %d hard links, and the site's .env must have one", stat.Nlink)
	}
	return nil
}

// program returns where the runner finds the program called name, named from the top of the server.
func (m Machine) program(name string) (string, error) {
	for _, folder := range searchFolders {
		if path := filepath.Join(folder, name); executable(filepath.Join(m.Root, path)) {
			return path, nil
		}
	}
	return "", fmt.Errorf("installer: no program %s in %s", name, searchPath)
}
