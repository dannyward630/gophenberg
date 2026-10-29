// SPDX-License-Identifier: Apache-2.0

package updater_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/gopherium/gophenberg/internal/updater"
)

// entry is one entry of a test archive.
type entry struct {
	name     string
	body     string
	mode     int64
	typeflag byte
	linkname string
}

// file returns a regular file entry of name holding body.
func file(name, body string) entry {
	return entry{name: name, body: body, mode: 0o644, typeflag: tar.TypeReg}
}

// binary returns the entry of the Gophenberg binary, executable.
func binary() entry {
	return entry{name: "gophenberg", body: "the Gophenberg 0.21.0 binary", mode: 0o755, typeflag: tar.TypeReg}
}

// releaseEntries returns the entries of a release archive, as make release-archives packs them.
func releaseEntries() []entry {
	return []entry{
		file("LICENSE", "the Apache License 2.0"),
		{name: "LICENSES/", mode: 0o755, typeflag: tar.TypeDir},
		file("LICENSES/GPL-3.0.txt", "the GNU General Public License 3.0"),
		file("NOTICE", "the notice of Gophenberg"),
		binary(),
		file("release.json", `{"version":"0.21.0"}`),
	}
}

// writeHeaders writes the header and the body of each entry into archive, failing the test when it cannot.
func writeHeaders(t *testing.T, archive *tar.Writer, entries []entry) {
	t.Helper()
	for _, e := range entries {
		header := &tar.Header{
			Name: e.name, Mode: e.mode, Typeflag: e.typeflag, Linkname: e.linkname, Size: int64(len(e.body)),
		}
		if err := archive.WriteHeader(header); err != nil {
			t.Fatalf("writing the header of %s: %v", e.name, err)
		}
		if _, err := archive.Write([]byte(e.body)); err != nil {
			t.Fatalf("writing the body of %s: %v", e.name, err)
		}
	}
}

// gzipped returns body compressed with gzip.
func gzipped(t *testing.T, body []byte) []byte {
	t.Helper()
	var held bytes.Buffer
	zipped := gzip.NewWriter(&held)
	if _, err := zipped.Write(body); err != nil {
		t.Fatalf("compressing the archive: %v", err)
	}
	if err := zipped.Close(); err != nil {
		t.Fatalf("closing the compressed archive: %v", err)
	}
	return held.Bytes()
}

// packed returns the tar.gz of entries.
func packed(t *testing.T, entries ...entry) []byte {
	t.Helper()
	var held bytes.Buffer
	archive := tar.NewWriter(&held)
	writeHeaders(t, archive, entries)
	if err := archive.Close(); err != nil {
		t.Fatalf("closing the archive: %v", err)
	}
	return gzipped(t, held.Bytes())
}

// buildFolder returns the path of a build folder that is not there yet, in a new folder.
func buildFolder(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "0.21.0")
}

// wantFile fails t unless the file at path holds body.
func wantFile(t *testing.T, path, body string) {
	t.Helper()
	held, err := os.ReadFile(path)
	if err != nil || string(held) != body {
		t.Errorf("%s holds %q, %v, want %q", path, held, err, body)
	}
}

// executable reports whether the owner may run the file at path, failing t when it cannot be read.
func executable(t *testing.T, path string) bool {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return info.Mode().Perm()&0o100 != 0
}

func TestUnpackWritesTheFilesOfARelease(t *testing.T) {
	t.Parallel()

	dir := buildFolder(t)

	if err := updater.Unpack(bytes.NewReader(packed(t, releaseEntries()...)), dir); err != nil {
		t.Fatalf("Unpack() = %v, want nil", err)
	}

	for _, e := range releaseEntries() {
		if e.typeflag == tar.TypeReg {
			wantFile(t, filepath.Join(dir, filepath.FromSlash(e.name)), e.body)
		}
	}
	if !executable(t, filepath.Join(dir, "gophenberg")) {
		t.Errorf("gophenberg is not executable, want it run as the next build")
	}
}

func TestUnpackWritesEveryLicenseTextOfTheLicensesFolder(t *testing.T) {
	t.Parallel()

	dir := buildFolder(t)
	added := file("LICENSES/MIT.txt", "the MIT License")
	added.mode = 0o755

	if err := updater.Unpack(bytes.NewReader(packed(t, append(releaseEntries(), added)...)), dir); err != nil {
		t.Fatalf("Unpack() of an archive with a second license text = %v, want nil", err)
	}

	wantFile(t, filepath.Join(dir, "LICENSES", "MIT.txt"), "the MIT License")
	if executable(t, filepath.Join(dir, "LICENSES", "MIT.txt")) {
		t.Errorf("LICENSES/MIT.txt packed as 0755 is executable, want it written as a plain file")
	}
}

func TestUnpackKeepsOnlyTheBinaryExecutableWhateverTheArchiveSays(t *testing.T) {
	t.Parallel()

	dir := buildFolder(t)
	plain := binary()
	plain.mode = 0o644
	runnable := file("release.json", `{"version":"0.21.0"}`)
	runnable.mode = 0o755

	if err := updater.Unpack(bytes.NewReader(packed(t, plain, runnable)), dir); err != nil {
		t.Fatalf("Unpack() = %v, want nil", err)
	}

	if !executable(t, filepath.Join(dir, "gophenberg")) {
		t.Errorf("gophenberg packed as 0644 is not executable, want it executable")
	}
	if executable(t, filepath.Join(dir, "release.json")) {
		t.Errorf("release.json packed as 0755 is executable, want it written as a plain file")
	}
}

func TestUnpackTakesNamesThatStartAtTheArchiveFolder(t *testing.T) {
	t.Parallel()

	dir := buildFolder(t)
	named := binary()
	named.name = "./gophenberg"

	if err := updater.Unpack(bytes.NewReader(packed(t, named, file("./LICENSES/GPL-3.0.txt", "GPL"))), dir); err != nil {
		t.Fatalf("Unpack() = %v, want nil", err)
	}

	wantFile(t, filepath.Join(dir, "gophenberg"), named.body)
	wantFile(t, filepath.Join(dir, "LICENSES", "GPL-3.0.txt"), "GPL")
}

func TestUnpackRefusesAnEntryOutsideTheBuildFolder(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"../gophenberg", "/gophenberg", "LICENSES/../../gophenberg", ""} {
		dir := buildFolder(t)
		escaping := binary()
		escaping.name = name

		err := updater.Unpack(bytes.NewReader(packed(t, escaping)), dir)

		wantRefused(t, err, "refused the archive, its entry \""+name+"\" is outside the build folder")
		wantMissing(t, dir)
		wantMissing(t, filepath.Join(filepath.Dir(dir), "gophenberg"))
	}
}

func TestUnpackRefusesALink(t *testing.T) {
	t.Parallel()

	for _, link := range []entry{
		{name: "gophenberg", mode: 0o777, typeflag: tar.TypeSymlink, linkname: "/bin/sh"},
		{name: "NOTICE", mode: 0o644, typeflag: tar.TypeLink, linkname: "LICENSE"},
	} {
		dir := buildFolder(t)

		err := updater.Unpack(bytes.NewReader(packed(t, file("LICENSE", "the license"), link, binary())), dir)

		wantRefused(t, err, "refused the archive, its entry \""+link.name+"\" is a link")
		wantMissing(t, dir)
	}
}

func TestUnpackRefusesAnEntryAReleaseDoesNotHold(t *testing.T) {
	t.Parallel()

	for _, unknown := range []entry{
		file("plugins.so", "a plugin"),
		file("LICENSES", "a license"),
		file("licenses/GPL-3.0.txt", "GPL"),
		file("LICENSES/more/MIT.txt", "MIT"),
		{name: "LICENSES/more/", mode: 0o755, typeflag: tar.TypeDir},
		{name: "LICENSES/MIT.txt", mode: 0o644, typeflag: tar.TypeFifo},
		{name: "themes/", mode: 0o755, typeflag: tar.TypeDir},
		{name: "gophenberg/", mode: 0o755, typeflag: tar.TypeDir},
		{name: "NOTICE", mode: 0o644, typeflag: tar.TypeFifo},
	} {
		dir := buildFolder(t)

		err := updater.Unpack(bytes.NewReader(packed(t, binary(), unknown)), dir)

		wantRefused(t, err, "refused the archive, its entry \""+unknown.name+"\" is not a file a release holds")
		wantMissing(t, dir)
	}
}

func TestUnpackRefusesAnArchiveWithNoBinary(t *testing.T) {
	t.Parallel()

	dir := buildFolder(t)

	err := updater.Unpack(bytes.NewReader(packed(t, file("LICENSE", "the license"), file("NOTICE", "the notice"))), dir)

	wantRefused(t, err, "refused the archive, which holds no gophenberg binary")
	wantMissing(t, dir)
}

func TestUnpackRefusesAnEntryTwice(t *testing.T) {
	t.Parallel()

	dir := buildFolder(t)

	err := updater.Unpack(bytes.NewReader(packed(t, binary(), binary())), dir)

	wantRefused(t, err, "file exists")
	wantMissing(t, dir)
}

func TestUnpackRefusesAnArchiveItCannotRead(t *testing.T) {
	t.Parallel()

	var cut bytes.Buffer
	archive := tar.NewWriter(&cut)
	if err := archive.WriteHeader(&tar.Header{Name: "gophenberg", Mode: 0o755, Size: 100}); err != nil {
		t.Fatalf("writing the header: %v", err)
	}
	if _, err := archive.Write([]byte("short")); err != nil {
		t.Fatalf("writing the body: %v", err)
	}
	for text, body := range map[string][]byte{
		"refused the archive, which is not gzip":    []byte("not an archive"),
		"refused the archive, which cannot be read": gzipped(t, []byte(strings.Repeat("x", 1024))),
		"unpack gophenberg: unexpected EOF":         gzipped(t, cut.Bytes()),
	} {
		dir := buildFolder(t)

		err := updater.Unpack(bytes.NewReader(body), dir)

		wantRefused(t, err, text)
		wantMissing(t, dir)
	}
}

func TestUnpackRefusesABuildFolderWhoseParentItCannotOpen(t *testing.T) {
	t.Parallel()

	for name, parent := range map[string]func(t *testing.T) string{
		"a parent that is not there": func(t *testing.T) string { return filepath.Join(t.TempDir(), "versions") },
		"a parent it may not read": func(t *testing.T) string {
			versions := t.TempDir()
			denied(t, versions, 0o300)
			return versions
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := filepath.Join(parent(t), "0.21.0")

			err := updater.Unpack(bytes.NewReader(packed(t, releaseEntries()...)), dir)

			wantRefused(t, err, "updater: make the build folder")
			wantMissing(t, dir)
		})
	}
}

func TestUnpackLeavesAFolderThatIsThereAlone(t *testing.T) {
	t.Parallel()

	dir := buildFolder(t)
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatalf("making the folder: %v", err)
	}
	kept := filepath.Join(dir, "gophenberg")
	if err := os.WriteFile(kept, []byte("the running binary"), 0o755); err != nil {
		t.Fatalf("writing the running binary: %v", err)
	}

	err := updater.Unpack(bytes.NewReader(packed(t, releaseEntries()...)), dir)

	wantRefused(t, err, "file exists")
	wantFile(t, kept, "the running binary")
}

// openTree opens the folder at path as a tree that closes once t ends, failing t when it cannot.
func openTree(t *testing.T, path string) *os.Root {
	t.Helper()
	tree, err := os.OpenRoot(path)
	if err != nil {
		t.Fatalf("opening %s as a tree: %v", path, err)
	}
	t.Cleanup(func() { _ = tree.Close() })
	return tree
}

// installTree returns the path of a new install root holding an empty versions folder, and the tree open on it.
func installTree(t *testing.T) (string, *os.Root) {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "versions"), 0o755); err != nil {
		t.Fatalf("making the versions folder: %v", err)
	}
	return root, openTree(t, root)
}

// wantEntries fails t unless the folder at path holds the entries called names, in order.
func wantEntries(t *testing.T, path string, names ...string) {
	t.Helper()
	entries, err := os.ReadDir(path)
	held := make([]string, 0, len(entries))
	for _, entry := range entries {
		held = append(held, entry.Name())
	}
	if err != nil || !slices.Equal(held, names) {
		t.Errorf("%s holds %v, %v, want %v", path, held, err, names)
	}
}

func TestUnpackInWritesTheFilesOfAReleaseThroughTheTree(t *testing.T) {
	t.Parallel()

	root, tree := installTree(t)

	if err := updater.UnpackIn(tree, bytes.NewReader(packed(t, releaseEntries()...)), "versions/0.21.0"); err != nil {
		t.Fatalf("UnpackIn() = %v, want nil", err)
	}

	dir := filepath.Join(root, "versions", "0.21.0")
	for _, e := range releaseEntries() {
		if e.typeflag == tar.TypeReg {
			wantFile(t, filepath.Join(dir, filepath.FromSlash(e.name)), e.body)
		}
	}
	if !executable(t, filepath.Join(dir, "gophenberg")) {
		t.Errorf("gophenberg is not executable, want it run as the next build")
	}
}

func TestUnpackInRefusesABuildFolderWhoseParentIsALinkOutOfTheTree(t *testing.T) {
	t.Parallel()

	root, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "versions")); err != nil {
		t.Fatalf("linking versions out of the tree: %v", err)
	}

	err := updater.UnpackIn(openTree(t, root), bytes.NewReader(packed(t, releaseEntries()...)), "versions/0.21.0")

	wantRefused(t, err, "updater: make the build folder")
	wantEntries(t, outside)
}

func TestUnpackInDeletesTheBuildFolderWhenAnEntryFails(t *testing.T) {
	t.Parallel()

	root, tree := installTree(t)

	err := updater.UnpackIn(tree, bytes.NewReader(packed(t, file("LICENSE", "the license"), binary(), binary())),
		"versions/0.21.0")

	wantRefused(t, err, "file exists")
	wantEntries(t, filepath.Join(root, "versions"))
}

func TestUnpackInLeavesAFolderThatIsThereAlone(t *testing.T) {
	t.Parallel()

	root, tree := installTree(t)
	kept := filepath.Join(root, "versions", "0.21.0", "gophenberg")
	if err := os.Mkdir(filepath.Dir(kept), 0o755); err != nil {
		t.Fatalf("making the folder: %v", err)
	}
	if err := os.WriteFile(kept, []byte("the running binary"), 0o755); err != nil {
		t.Fatalf("writing the running binary: %v", err)
	}

	err := updater.UnpackIn(tree, bytes.NewReader(packed(t, releaseEntries()...)), "versions/0.21.0")

	wantRefused(t, err, "file exists")
	wantFile(t, kept, "the running binary")
}

func TestUnpackInRefusesABuildItCannotSaveToDisk(t *testing.T) {
	t.Parallel()

	versions := t.TempDir()
	tree := openTree(t, versions)
	denied(t, versions, 0o300)

	err := updater.UnpackIn(tree, bytes.NewReader(packed(t, releaseEntries()...)), "0.21.0")

	wantRefused(t, err, "updater: save the build to disk")
	wantMissing(t, filepath.Join(versions, "0.21.0"))
}
