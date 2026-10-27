// SPDX-License-Identifier: Apache-2.0

package installer

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"

	"github.com/gopherium/gophenberg/internal/version"
)

// serverPlatform is the platform of this server as the release.json of its archive names it.
const serverPlatform = "linux/" + runtime.GOARCH

// maxReleaseJSONBytes is the largest release.json the installer reads, far above what a release writes.
const maxReleaseJSONBytes = 1 << 16

// releaseFacts is the part of the release.json of an archive that must match this installer and this server.
type releaseFacts struct {
	Version  string `json:"version"`
	Platform string `json:"platform"`
}

// openArchive opens the release archive at path and checks its release.json, none while the run needs no archive.
func (c *machineCheck) openArchive(path string) (*os.File, error) {
	if path == "" {
		return nil, c.archiveNeeded()
	}
	archive, err := os.Open(filepath.Join(c.machine.Root, path))
	if err != nil {
		return nil, fmt.Errorf("installer: %w", err)
	}
	if err := checkArchive(archive, filepath.Clean(path)); err != nil {
		_ = archive.Close()
		return nil, err
	}
	return archive, nil
}

// archiveNeeded refuses a run without an archive while the current link of the install root names no build.
func (c *machineCheck) archiveNeeded() error {
	if c.found.current != "" {
		return nil
	}
	return stop("-archive is required while %s/current names no build, such as the archive install.sh downloads "+
		"into %s", c.settings.Root, installerFolder)
}

// checkArchive refuses an archive at path whose release.json names another version or platform than this run's.
func checkArchive(archive io.Reader, path string) error {
	facts, err := releaseFactsOf(archive)
	if err != nil {
		return stop("the archive %s %v", path, err)
	}
	if facts.Version != version.Version() {
		return stop("the archive %s holds Gophenberg %s, not this installer's %s, and server:install installs its "+
			"own release only", path, facts.Version, version.Version())
	}
	if facts.Platform != serverPlatform {
		return stop("the archive %s is built for %s, not for this server's %s", path, facts.Platform, serverPlatform)
	}
	return nil
}

// releaseFactsOf reads the release.json of a release archive, saying what the archive is when it holds none.
func releaseFactsOf(archive io.Reader) (releaseFacts, error) {
	unzipped, err := gzip.NewReader(archive)
	if err != nil {
		return releaseFacts{}, fmt.Errorf("is not a gzip file: %w", err)
	}
	entries := tar.NewReader(unzipped)
	for {
		header, err := entries.Next()
		if errors.Is(err, io.EOF) {
			return releaseFacts{}, errors.New("holds no release.json")
		}
		if err != nil {
			return releaseFacts{}, fmt.Errorf("cannot be read: %w", err)
		}
		if path.Clean(header.Name) == "release.json" {
			return decodedFacts(entries)
		}
	}
}

// decodedFacts decodes the release.json release holds, reading no more of it than any release writes.
func decodedFacts(release io.Reader) (releaseFacts, error) {
	var facts releaseFacts
	if err := json.NewDecoder(io.LimitReader(release, maxReleaseJSONBytes)).Decode(&facts); err != nil {
		return releaseFacts{}, fmt.Errorf("holds a release.json that cannot be read: %w", err)
	}
	return facts, nil
}

// closeArchive closes archive, when the run opened one.
func closeArchive(archive *os.File) {
	if archive != nil {
		_ = archive.Close()
	}
}
