// SPDX-License-Identifier: Apache-2.0

package installsh_test

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/gopherium/gophenberg/internal/signature"
)

// publishScriptPath is publish-install.sh as the repository holds it.
const publishScriptPath = "../../deploy/publish-install.sh"

// recordDownloads is the address a test record lists its archives under, before the version.
const recordDownloads = "https://downloads.example.com/v"

// unrelatedFile is a file of the update site that publish-install.sh never touches.
const unrelatedFile = "v1/core/stable.json"

// placeholdersLine is the stop line for an install.sh without its three unstamped lines, after the script's path.
const placeholdersLine = " must hold each of version=unstamped, sha256_amd64=unstamped and sha256_arm64=unstamped once"

// notStable ends the stop line for a version that is not a stable release version.
const notStable = ", which is not a stable release version of three numbers"

// siteStays ends the stop line for a releasetool step that failed.
const siteStays = ", so the update site stays as it is"

// releasetoolBinary is the releasetool TestMain builds for the publish tests.
var releasetoolBinary string

// releaseRecord is the part of a release record a test changes before it signs the record.
type releaseRecord struct {
	Version   string `json:"version"`
	Artifacts struct {
		Package []map[string]string `json:"package"`
	} `json:"artifacts"`
}

// publishSite is one publish test: its test root, the release key and the standby, and the dash that runs the script.
type publishSite struct {
	t       *testing.T
	dash    string
	root    string
	keys    map[string]ed25519.PrivateKey
	publics map[string]ed25519.PublicKey
}

// publishRun is how one run of publish-install.sh ended and what it printed.
type publishRun struct {
	code   int
	stdout string
	stderr string
}

func TestMain(m *testing.M) {
	os.Exit(runWithReleasetool(m))
}

func TestPublishInstallWritesARecordIntoAnEmptySite(t *testing.T) {
	t.Parallel()

	p := newPublishSite(t)
	before := p.siteFiles()
	p.publish(p.writeRecord(releaseVersion, "release", nil)).published(t, releaseVersion)
	p.checkPublished(releaseVersion, before)
}

func TestPublishInstallSignsANewerReleaseOverAnOlderCopy(t *testing.T) {
	t.Parallel()

	p := newPublishSite(t)
	p.publishedCopy("0.19.0")
	before := p.siteFiles()
	p.publish(p.writeRecord(releaseVersion, "release", nil)).published(t, releaseVersion)
	p.checkPublished(releaseVersion, before)
}

func TestPublishInstallLeavesTheSiteAloneForAnOlderOrTheSameRelease(t *testing.T) {
	t.Parallel()

	cases := map[string]struct{ published, release string }{
		"an older release":                 {published: "0.20.0", release: "0.19.0"},
		"the same release":                 {published: "0.20.0", release: "0.20.0"},
		"a release older by its numbers":   {published: "0.10.0", release: "0.9.0"},
		"a release older by its last part": {published: "0.20.10", release: "0.20.9"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := newPublishSite(t)
			p.publishedCopy(tc.published)
			before := p.siteFiles()
			p.publish(p.writeRecord(tc.release, "release", nil)).stayed(t, tc.published, tc.release)
			p.unchanged(before)
		})
	}
}

func TestPublishInstallComparesVersionsAsNumbers(t *testing.T) {
	t.Parallel()

	cases := map[string]struct{ published, release string }{
		"a minor of two digits":  {published: "0.9.0", release: "0.10.0"},
		"a patch of two digits":  {published: "0.20.9", release: "0.20.10"},
		"a major of two digits":  {published: "9.0.0", release: "10.0.0"},
		"a minor over its patch": {published: "0.19.10", release: "0.20.0"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := newPublishSite(t)
			p.publishedCopy(tc.published)
			before := p.siteFiles()
			p.publish(p.writeRecord(tc.release, "release", nil)).published(t, tc.release)
			p.checkPublished(tc.release, before)
		})
	}
}

func TestPublishInstallRefusesARecordItCannotStampFrom(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		version string
		change  func(*releaseRecord)
		line    string
	}{
		"a prerelease": {
			version: "0.20.0-rc.1",
			line:    "the release record names 0.20.0-rc.1" + notStable,
		},
		"a version of two numbers": {
			version: "0.20",
			line:    "the release record names 0.20" + notStable,
		},
		"no linux/arm64 archive": {
			version: releaseVersion, change: dropLinuxArm64,
			line: "the release record lists no single sha256 checksum of the linux/arm64 archive",
		},
		"two linux/amd64 archives": {
			version: releaseVersion, change: doubleLinuxAmd64,
			line: "the release record lists no single sha256 checksum of the linux/amd64 archive",
		},
		"a checksum without sha256:": {
			version: releaseVersion, change: unprefixLinuxAmd64,
			line: "the release record lists no single sha256 checksum of the linux/amd64 archive",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := newPublishSite(t)
			p.publishedCopy("0.19.0")
			before := p.siteFiles()
			p.publish(p.writeRecord(tc.version, "release", tc.change)).refused(t, tc.line)
			p.unchanged(before)
		})
	}
}

func TestPublishInstallRefusesARecordWhoseSignatureDoesNotHold(t *testing.T) {
	t.Parallel()

	cases := map[string]func(p *publishSite) string{
		"a record the standby key signed": func(p *publishSite) string {
			return p.writeRecord(releaseVersion, "standby", nil)
		},
		"a record changed after it was signed": func(p *publishSite) string {
			record := p.writeRecord(releaseVersion, "release", nil)
			p.changeRecord(record, swapChecksums)
			return record
		},
	}
	for name, record := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := newPublishSite(t)
			p.publishedCopy("0.19.0")
			before := p.siteFiles()
			p.publish(record(p)).refusedAfterReleasetool(t,
				"releasetool could not verify the release record under the public key"+siteStays)
			p.unchanged(before)
		})
	}
}

func TestPublishInstallRefusesASiteCopyThatNamesNoRelease(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		text func(t *testing.T) []byte
		line string
	}{
		"a copy still unstamped": {
			text: repositoryScript,
			line: "the update site's install.sh names unstamped" + notStable,
		},
		"a copy naming a prerelease": {
			text: func(t *testing.T) []byte { return stampedScript(t, "0.21.0-rc.1") },
			line: "the update site's install.sh names 0.21.0-rc.1" + notStable,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := newPublishSite(t)
			p.siteCopy(tc.text(t))
			before := p.siteFiles()
			p.publish(p.writeRecord(releaseVersion, "release", nil)).refused(t, tc.line)
			p.unchanged(before)
		})
	}
}

func TestPublishInstallRefusesASourceWithoutItsPlaceholders(t *testing.T) {
	t.Parallel()

	cases := map[string]struct{ line, replacement string }{
		"no sha256_arm64 line": {line: "sha256_arm64=unstamped\n", replacement: ""},
		"version=unstamped twice": {
			line:        "version=unstamped\n",
			replacement: "version=unstamped\nversion=unstamped\n",
		},
		"a version stamped already": {line: "version=unstamped\n", replacement: "version=0.19.0\n"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := newPublishSite(t)
			p.publishedCopy("0.19.0")
			before := p.siteFiles()
			source := string(repositoryScript(t))
			if strings.Count(source, tc.line) != 1 {
				t.Fatalf("deploy/install.sh holds %q %d times, want once", tc.line, strings.Count(source, tc.line))
			}
			script := p.besideScript(strings.Replace(source, tc.line, tc.replacement, 1))
			run := p.run(script, p.arguments(p.writeRecord(releaseVersion, "release", nil), "release"))
			run.refused(t, p.path("deploy", "install.sh")+placeholdersLine)
			p.unchanged(before)
		})
	}
}

func TestPublishInstallRefusesAWrongPublicKey(t *testing.T) {
	t.Parallel()

	p := newPublishSite(t)
	p.publishedCopy("0.19.0")
	before := p.siteFiles()
	run := p.run(publishScriptPath, p.arguments(p.writeRecord(releaseVersion, "release", nil), "standby"))
	run.refusedAfterReleasetool(t, "releasetool could not verify the signed install.sh under the public key"+siteStays)
	p.unchanged(before)
}

func TestPublishInstallRefusesASigningKeyItCannotRead(t *testing.T) {
	t.Parallel()

	p := newPublishSite(t)
	p.publishedCopy("0.19.0")
	before := p.siteFiles()
	arguments := p.arguments(p.writeRecord(releaseVersion, "release", nil), "release")
	arguments[3] = p.path("keys", "missing.pem")
	p.run(publishScriptPath, arguments).refusedAfterReleasetool(t, "releasetool could not sign install.sh"+siteStays)
	p.unchanged(before)
}

func TestPublishInstallNamesItsArguments(t *testing.T) {
	t.Parallel()

	cases := map[string]func(arguments []string) []string{
		"none": func([]string) []string { return nil },
		"four": func(arguments []string) []string { return arguments[:4] },
		"six":  func(arguments []string) []string { return append(arguments, "extra") },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := newPublishSite(t)
			p.publishedCopy("0.19.0")
			before := p.siteFiles()
			arguments := p.arguments(p.writeRecord(releaseVersion, "release", nil), "release")
			p.run(publishScriptPath, change(arguments)).refused(t, "usage: publish-install.sh <releasetool>"+
				" <release record> <update site folder> <signing key file> <public key>")
			p.unchanged(before)
		})
	}
}

// runWithReleasetool builds releasetool into a temporary folder, runs the tests and removes the folder again.
func runWithReleasetool(m *testing.M) int {
	folder, err := os.MkdirTemp("", "installsh-")
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "making a folder for releasetool:", err)
		return 1
	}
	defer func() { _ = os.RemoveAll(folder) }()
	releasetoolBinary = filepath.Join(folder, "releasetool")
	build := exec.Command("go", "build", "-o", releasetoolBinary, "../../cmd/releasetool")
	build.Env = append(os.Environ(), "GOWORK=off")
	if out, err := build.CombinedOutput(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "building releasetool: %v\n%s", err, out)
		return 1
	}
	return m.Run()
}

// needPublishTools returns the dash that runs publish-install.sh, skipping when a tool the script runs is missing.
func needPublishTools(t *testing.T) string {
	t.Helper()
	dash := needDash(t)
	for _, tool := range []string{"jq", "awk", "sort", "tail", "sed", "mv", "dirname"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not on PATH, so publish-install.sh cannot run here", tool)
		}
	}
	return dash
}

// newPublishSite makes a test root whose update site holds an unrelated file, and the release key and the standby.
func newPublishSite(t *testing.T) *publishSite {
	t.Helper()
	p := &publishSite{
		t: t, dash: needPublishTools(t), root: t.TempDir(),
		keys: map[string]ed25519.PrivateKey{}, publics: map[string]ed25519.PublicKey{},
	}
	if err := os.MkdirAll(p.path("temp"), 0o700); err != nil {
		t.Fatalf("making the temporary folder: %v", err)
	}
	p.write(p.path("site", unrelatedFile), []byte("{}\n"))
	for _, name := range []string{"release", "standby"} {
		public, private, err := ed25519.GenerateKey(nil)
		if err != nil {
			t.Fatalf("making the %s key: %v", name, err)
		}
		der, err := x509.MarshalPKCS8PrivateKey(private)
		if err != nil {
			t.Fatalf("writing the %s key: %v", name, err)
		}
		p.write(p.path("keys", name+".pem"), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
		p.keys[name], p.publics[name] = private, public
	}
	return p
}

// path joins the parts under the test root.
func (p *publishSite) path(parts ...string) string {
	return filepath.Join(append([]string{p.root}, parts...)...)
}

// write writes the data to the path, making its folders first.
func (p *publishSite) write(path string, data []byte) {
	p.t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		p.t.Fatalf("making the folder of %s: %v", path, err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		p.t.Fatalf("writing %s: %v", path, err)
	}
}

// runReleasetool runs the built releasetool with the arguments and returns what it printed on standard output.
func (p *publishSite) runReleasetool(args ...string) []byte {
	p.t.Helper()
	var stderr bytes.Buffer
	command := exec.Command(releasetoolBinary, args...)
	command.Stderr = &stderr
	out, err := command.Output()
	if err != nil {
		p.t.Fatalf("releasetool %s: %v\n%s", strings.Join(args, " "), err, stderr.String())
	}
	return out
}

// archiveBytes is what the small stand-in archive of the version for the Linux processor holds.
func archiveBytes(version, arch string) []byte {
	return []byte("the linux/" + arch + " archive of Gophenberg " + version + "\n")
}

// checksumOf returns the SHA-256 in hex of the stand-in archive of the version for the Linux processor.
func checksumOf(version, arch string) string {
	sum := sha256.Sum256(archiveBytes(version, arch))
	return hex.EncodeToString(sum[:])
}

// writeRecord signs stand-in Linux archives of the version and a record of them, which change edits and signer signs.
func (p *publishSite) writeRecord(version, signer string, change func(*releaseRecord)) string {
	p.t.Helper()
	folder := p.path("release", version)
	archives := make([]string, 0, 2)
	for _, arch := range []string{"amd64", "arm64"} {
		archive := filepath.Join(folder, "gophenberg_"+version+"_linux_"+arch+".tar.gz")
		p.write(archive, archiveBytes(version, arch))
		archives = append(archives, archive)
	}
	p.runReleasetool(append([]string{"sign", "-key", p.path("keys", "release.pem")}, archives...)...)
	record := filepath.Join(folder, "gophenberg_"+version+"_release-record.json")
	p.write(record, p.runReleasetool(append([]string{"record", "-version", version, "-download-url",
		recordDownloads + version}, archives...)...))
	if change != nil {
		p.changeRecord(record, change)
	}
	p.runReleasetool("sign", "-key", p.path("keys", signer+".pem"), record)
	return record
}

// changeRecord writes the record file again with the change made.
func (p *publishSite) changeRecord(path string, change func(*releaseRecord)) {
	p.t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		p.t.Fatalf("reading the record: %v", err)
	}
	var record releaseRecord
	if err := json.Unmarshal(data, &record); err != nil {
		p.t.Fatalf("reading the record: %v", err)
	}
	change(&record)
	changed, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		p.t.Fatalf("writing the record: %v", err)
	}
	p.write(path, append(changed, '\n'))
}

// dropLinuxArm64 leaves the linux/arm64 archive out of the record.
func dropLinuxArm64(record *releaseRecord) {
	record.Artifacts.Package = slices.DeleteFunc(record.Artifacts.Package, func(entry map[string]string) bool {
		return entry["id"] == "linux-arm64"
	})
}

// doubleLinuxAmd64 lists the linux/amd64 archive twice in the record.
func doubleLinuxAmd64(record *releaseRecord) {
	for _, entry := range record.Artifacts.Package {
		if entry["id"] == "linux-amd64" {
			record.Artifacts.Package = append(record.Artifacts.Package, maps.Clone(entry))
			return
		}
	}
}

// unprefixLinuxAmd64 takes sha256: off the checksum of the linux/amd64 archive.
func unprefixLinuxAmd64(record *releaseRecord) {
	for _, entry := range record.Artifacts.Package {
		if entry["id"] == "linux-amd64" {
			entry["checksum"] = strings.TrimPrefix(entry["checksum"], "sha256:")
		}
	}
}

// swapChecksums gives the linux/amd64 archive the checksum of the linux/arm64 one, as a swapped record would.
func swapChecksums(record *releaseRecord) {
	sums := map[string]string{}
	for _, entry := range record.Artifacts.Package {
		sums[entry["id"]] = entry["checksum"]
	}
	for _, entry := range record.Artifacts.Package {
		if entry["id"] == "linux-amd64" {
			entry["checksum"] = sums["linux-arm64"]
		}
	}
}

// repositoryScript returns deploy/install.sh as the repository holds it.
func repositoryScript(t *testing.T) []byte {
	t.Helper()
	source, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatalf("reading install.sh: %v", err)
	}
	return source
}

// stampedScript returns deploy/install.sh with its three unstamped lines naming the version and its two checksums.
func stampedScript(t *testing.T, version string) []byte {
	t.Helper()
	stamps := map[string]string{
		"version=unstamped\n":      "version=" + version + "\n",
		"sha256_amd64=unstamped\n": "sha256_amd64=" + checksumOf(version, "amd64") + "\n",
		"sha256_arm64=unstamped\n": "sha256_arm64=" + checksumOf(version, "arm64") + "\n",
	}
	lines := strings.SplitAfter(string(repositoryScript(t)), "\n")
	seen := stampLines(lines, stamps)
	for line := range stamps {
		if seen[line] != 1 {
			t.Fatalf("deploy/install.sh holds %q on %d lines, want 1", line, seen[line])
		}
	}
	return []byte(strings.Join(lines, ""))
}

// stampLines replaces each of lines the stamps name with its stamp and returns how many times it found each.
func stampLines(lines []string, stamps map[string]string) map[string]int {
	seen := map[string]int{}
	for i, line := range lines {
		if stamped, found := stamps[line]; found {
			lines[i] = stamped
			seen[line]++
		}
	}
	return seen
}

// publishedCopy puts install.sh stamped for the version into the update site, with a .sig the release key made.
func (p *publishSite) publishedCopy(version string) {
	p.t.Helper()
	p.siteCopy(stampedScript(p.t, version))
}

// siteCopy puts the text into the update site as install.sh, with a .sig the release key made.
func (p *publishSite) siteCopy(text []byte) {
	p.t.Helper()
	digest, err := signature.Digest(bytes.NewReader(text))
	if err != nil {
		p.t.Fatalf("digesting the site's install.sh: %v", err)
	}
	p.write(p.path("site", "install.sh"), text)
	p.write(p.path("site", "install.sh.sig"), []byte(signature.Sign(p.keys["release"], digest)+"\n"))
}

// besideScript copies publish-install.sh beside the text, as install.sh, into the test root and returns its path.
func (p *publishSite) besideScript(text string) string {
	p.t.Helper()
	script, err := os.ReadFile(publishScriptPath)
	if err != nil {
		p.t.Fatalf("reading publish-install.sh: %v", err)
	}
	p.write(p.path("deploy", "install.sh"), []byte(text))
	p.write(p.path("deploy", "publish-install.sh"), script)
	return p.path("deploy", "publish-install.sh")
}

// arguments returns the five arguments for the record, the named signer's key file and the release key's Multikey.
func (p *publishSite) arguments(record, signer string) []string {
	p.t.Helper()
	multikey, err := signature.Multikey(p.publics["release"])
	if err != nil {
		p.t.Fatalf("writing the release key as a Multikey: %v", err)
	}
	return []string{releasetoolBinary, record, p.path("site"), p.path("keys", signer+".pem"), multikey}
}

// publish runs publish-install.sh on the record, signing with the release key and checking under its Multikey.
func (p *publishSite) publish(record string) publishRun {
	p.t.Helper()
	return p.run(publishScriptPath, p.arguments(record, "release"))
}

// run runs the script under dash with the arguments and checks that its temporary folder is empty again.
func (p *publishSite) run(script string, args []string) publishRun {
	p.t.Helper()
	var stdout, stderr bytes.Buffer
	command := exec.Command(p.dash, append([]string{script}, args...)...)
	command.Stdout, command.Stderr = &stdout, &stderr
	command.Env = []string{"PATH=" + os.Getenv("PATH"), "TMPDIR=" + p.path("temp")}
	err := command.Run()
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		p.t.Fatalf("running publish-install.sh: %v", err)
	}
	left, err := os.ReadDir(p.path("temp"))
	if err != nil {
		p.t.Fatalf("reading the temporary folder: %v", err)
	}
	if len(left) != 0 {
		p.t.Errorf("publish-install.sh left %s in its temporary folder", left[0].Name())
	}
	return publishRun{code: command.ProcessState.ExitCode(), stdout: stdout.String(), stderr: stderr.String()}
}

// siteFiles returns what each path under the update site holds, keyed by its path in the site, a folder ending in /.
func (p *publishSite) siteFiles() map[string]string {
	p.t.Helper()
	site := p.path("site")
	files := map[string]string{}
	err := filepath.WalkDir(site, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name, err := filepath.Rel(site, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			files[name+"/"] = ""
			return nil
		}
		data, err := os.ReadFile(path)
		files[name] = string(data)
		return err
	})
	if err != nil {
		p.t.Fatalf("reading the update site: %v", err)
	}
	return files
}

// changedPaths returns each path whose content differs between before and after, or that only one of them holds.
func changedPaths(before, after map[string]string) []string {
	changed := differing(before, after)
	for path := range after {
		if _, found := before[path]; !found {
			changed = append(changed, path)
		}
	}
	slices.Sort(changed)
	return changed
}

// differing returns each path of before that after does not hold with the same content.
func differing(before, after map[string]string) []string {
	var changed []string
	for path, held := range before {
		if now, found := after[path]; !found || now != held {
			changed = append(changed, path)
		}
	}
	return changed
}

// unchanged checks that the update site holds byte for byte what it held before, and nothing more.
func (p *publishSite) unchanged(before map[string]string) {
	p.t.Helper()
	if changed := changedPaths(before, p.siteFiles()); len(changed) != 0 {
		p.t.Errorf("the run changed %q in the update site, want nothing changed", changed)
	}
}

// checkPublished checks that only install.sh, stamped for the version, and its .sig under the release key changed.
func (p *publishSite) checkPublished(version string, before map[string]string) {
	p.t.Helper()
	after := p.siteFiles()
	if changed := changedPaths(before, after); !slices.Equal(changed, []string{"install.sh", "install.sh.sig"}) {
		p.t.Errorf("the run changed %q in the update site, want install.sh and install.sh.sig", changed)
	}
	if want := string(stampedScript(p.t, version)); after["install.sh"] != want {
		p.t.Errorf("the update site's install.sh is not deploy/install.sh stamped for %s: %s", version,
			firstDifference(after["install.sh"], want))
	}
	digest, err := signature.Digest(strings.NewReader(after["install.sh"]))
	if err != nil {
		p.t.Fatalf("digesting the update site's install.sh: %v", err)
	}
	line := strings.TrimSuffix(after["install.sh.sig"], "\n")
	if err := signature.Verify(p.publics["release"], digest, line); err != nil {
		p.t.Errorf("the update site's install.sh.sig %q does not hold under the release key: %v", line, err)
	}
}

// firstDifference names the first line where got and want differ.
func firstDifference(got, want string) string {
	gotLines, wantLines := strings.Split(got, "\n"), strings.Split(want, "\n")
	for i := range min(len(gotLines), len(wantLines)) {
		if gotLines[i] != wantLines[i] {
			return fmt.Sprintf("line %d is %q, want %q", i+1, gotLines[i], wantLines[i])
		}
	}
	return fmt.Sprintf("it has %d lines, want %d", len(gotLines), len(wantLines))
}

// published checks that the run ended with exit code 0, printing only that install.sh now names the version.
func (r publishRun) published(t *testing.T, version string) {
	t.Helper()
	r.ended(t, 0, "install.sh now names Gophenberg "+version+"\n", "")
}

// stayed checks that the run ended with exit code 0, printing only that the site's copy naming published stays.
func (r publishRun) stayed(t *testing.T, published, release string) {
	t.Helper()
	r.ended(t, 0, "the update site's install.sh names "+published+", which is not older than "+release+
		", so it stays as it is\n", "")
}

// refused checks that the run ended with exit code 1, printing only the stop line on standard error.
func (r publishRun) refused(t *testing.T, line string) {
	t.Helper()
	r.ended(t, 1, "", "publish-install.sh: "+line+"\n")
}

// ended checks the run's exit code and what it printed on standard output and on standard error.
func (r publishRun) ended(t *testing.T, code int, stdout, stderr string) {
	t.Helper()
	if r.code != code {
		t.Errorf("publish-install.sh ended with exit code %d, want %d", r.code, code)
	}
	if r.stdout != stdout {
		t.Errorf("publish-install.sh printed %q, want %q", r.stdout, stdout)
	}
	if r.stderr != stderr {
		t.Errorf("publish-install.sh printed %q on standard error, want %q", r.stderr, stderr)
	}
}

// refusedAfterReleasetool checks that the run ended with exit code 1 and the stop line, after releasetool's own lines.
func (r publishRun) refusedAfterReleasetool(t *testing.T, line string) {
	t.Helper()
	if r.code != 1 {
		t.Errorf("publish-install.sh ended with exit code %d and printed %q, want 1", r.code, r.stderr)
	}
	if r.stdout != "" {
		t.Errorf("publish-install.sh printed %q, want nothing", r.stdout)
	}
	lines := strings.Split(strings.TrimSuffix(r.stderr, "\n"), "\n")
	last := len(lines) - 1
	if want := "publish-install.sh: " + line; lines[last] != want {
		t.Errorf("publish-install.sh ended its standard error with %q, want %q", lines[last], want)
	}
	if last == 0 {
		t.Errorf("publish-install.sh printed no line of releasetool's own before it stopped")
	}
	for _, own := range lines[:last] {
		if !strings.HasPrefix(own, "releasetool: ") {
			t.Errorf("publish-install.sh printed %q before it stopped, want only releasetool's own lines", own)
		}
	}
}
