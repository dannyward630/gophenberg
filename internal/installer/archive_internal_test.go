// SPDX-License-Identifier: Apache-2.0

package installer

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"runtime"
	"testing"

	"github.com/gopherium/gophenberg/internal/version"
)

// entry is one file of a release archive a test packs: its name and what it holds.
type entry struct {
	name, body string
}

// packed returns a gzip tar archive that holds entries in order, as make release-archives packs a release.
func packed(t *testing.T, entries ...entry) string {
	t.Helper()
	var archive bytes.Buffer
	zipped := gzip.NewWriter(&archive)
	files := tar.NewWriter(zipped)
	for _, packed := range entries {
		header := &tar.Header{Name: packed.name, Mode: 0o644, Size: int64(len(packed.body)), Typeflag: tar.TypeReg}
		if err := files.WriteHeader(header); err != nil {
			t.Fatalf("packing %s: %v", packed.name, err)
		}
		if _, err := files.Write([]byte(packed.body)); err != nil {
			t.Fatalf("packing %s: %v", packed.name, err)
		}
	}
	if err := files.Close(); err != nil {
		t.Fatalf("closing the tar archive: %v", err)
	}
	if err := zipped.Close(); err != nil {
		t.Fatalf("closing the gzip stream: %v", err)
	}
	return archive.String()
}

// releaseArchiveOf returns a release archive whose release.json names version and platform, its files in tar order.
func releaseArchiveOf(t *testing.T, version, platform string) string {
	t.Helper()
	return packed(t,
		entry{"LICENSE", "Apache License\n"},
		entry{"NOTICE", "Gophenberg\n"},
		entry{"gophenberg", "#!/bin/sh\n"},
		entry{"release.json", releaseJSONOf(version, platform)},
	)
}

// releaseJSONOf returns the release.json of a release of version built for platform.
func releaseJSONOf(version, platform string) string {
	return `{"schema": 1, "name": "gophenberg", "version": "` + version + `", "platform": "` + platform + `"}` + "\n"
}

// archivePath is where the tests put the release archive, from the top of the server.
const archivePath = "/var/lib/gophenberg-installer/release.tar.gz"

// archiveChecked opens the archive at path on server as a first run on example.com does, returning what it opens.
func archiveChecked(server *fakeServer, path string) (*os.File, error) {
	return server.check().openArchive(path)
}

func TestOpenArchiveOpensTheReleaseArchiveOfThisInstallerForThisServer(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	server.write(archivePath, releaseArchiveOf(t, version.Version(), "linux/"+runtime.GOARCH))

	archive, err := archiveChecked(server, archivePath)
	if err != nil {
		t.Fatalf("openArchive() = %v, want the archive open", err)
	}
	defer closeArchive(archive)

	if archive.Name() != server.at(archivePath) {
		t.Errorf("openArchive() opened %s, want %s", archive.Name(), server.at(archivePath))
	}
}

func TestOpenArchiveReadsAReleaseJSONItsArchiveNamesFromTheFolderItIsIn(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	server.write(archivePath, packed(t, entry{"./release.json", releaseJSONOf(version.Version(),
		"linux/"+runtime.GOARCH)}))

	archive, err := archiveChecked(server, archivePath)
	defer closeArchive(archive)

	if err != nil {
		t.Errorf("openArchive() = %v, want the archive open", err)
	}
}

func TestOpenArchiveNeedsNoArchiveOnceCurrentNamesABuild(t *testing.T) {
	t.Parallel()

	check := newFakeServer(t).check()
	check.found.current = earlierBuild

	archive, err := check.openArchive("")

	if archive != nil || err != nil {
		t.Errorf("openArchive() = %v, %v, want no archive needed", archive, err)
	}
}

func TestOpenArchiveRefusesAFirstRunWithoutAnArchive(t *testing.T) {
	t.Parallel()

	archive, err := archiveChecked(newFakeServer(t), "")

	if archive != nil {
		t.Errorf("openArchive() opened %s, want nothing open", archive.Name())
	}
	wantRefused(t, err, "-archive is required while /opt/gophenberg/current names no build, such as the archive "+
		"install.sh downloads into /var/lib/gophenberg-installer, so nothing was changed")
}

func TestOpenArchiveRefusesAnArchiveItCannotOpen(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)

	_, err := archiveChecked(server, archivePath)

	wantRefused(t, err, "installer: open "+server.at(archivePath)+": no such file or directory")
}

func TestOpenArchiveRefusesAnArchiveThatIsNoReleaseOfThisInstallerForThisServer(t *testing.T) {
	t.Parallel()

	for _, refused := range []struct {
		archive func(t *testing.T) string
		want    string
	}{
		{func(*testing.T) string { return "a release note, which is no gzip stream" },
			"is not a gzip file: gzip: invalid header"},
		{func(t *testing.T) string { return gzipped(t, "not a tar archive") }, "cannot be read: unexpected EOF"},
		{func(t *testing.T) string { return packed(t, entry{"gophenberg", "#!/bin/sh\n"}) }, "holds no release.json"},
		{func(t *testing.T) string { return packed(t, entry{"release.json", "not JSON"}) },
			"holds a release.json that cannot be read: invalid character 'o' in literal null (expecting 'u')"},
		{func(t *testing.T) string { return releaseArchiveOf(t, "0.0.1", "linux/"+runtime.GOARCH) },
			"holds Gophenberg 0.0.1, not this installer's " + version.Version() + ", and server:install installs " +
				"its own release only"},
		{func(t *testing.T) string { return releaseArchiveOf(t, version.Version(), "windows/arm64") },
			"is built for windows/arm64, not for this server's linux/" + runtime.GOARCH},
	} {
		server := newFakeServer(t)
		server.write(archivePath, refused.archive(t))

		archive, err := archiveChecked(server, archivePath)

		if archive != nil {
			t.Errorf("openArchive() opened %s, want nothing open", archive.Name())
		}
		wantRefused(t, err, "the archive "+archivePath+" "+refused.want+", so nothing was changed")
	}
}

// gzipped returns body compressed with gzip and nothing else around it.
func gzipped(t *testing.T, body string) string {
	t.Helper()
	var archive bytes.Buffer
	zipped := gzip.NewWriter(&archive)
	if _, err := zipped.Write([]byte(body)); err != nil {
		t.Fatalf("compressing: %v", err)
	}
	if err := zipped.Close(); err != nil {
		t.Fatalf("closing the gzip stream: %v", err)
	}
	return archive.String()
}

func TestCloseArchiveTakesNoArchive(t *testing.T) {
	t.Parallel()

	closeArchive(nil)
}
