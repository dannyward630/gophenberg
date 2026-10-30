// SPDX-License-Identifier: Apache-2.0

package installsh_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/gopherium/gophenberg/internal/signature"
)

// scriptPath is install.sh as the repository holds it.
const scriptPath = "../../deploy/install.sh"

// releaseVersion is the release the test copy of install.sh names.
const releaseVersion = "0.20.0"

// realInstallerFolder is the folder install.sh downloads into on a real server.
const realInstallerFolder = "/var/lib/gophenberg-installer"

// realDownloads is the address install.sh downloads a release from on a real server.
const realDownloads = "https://github.com/gopherium/gophenberg/releases/download"

// testDownloads is the address the test copy downloads from, which the fake curl serves from a test folder.
const testDownloads = "https://downloads.example.com/releases/download"

// realTools are the tools install.sh and the fake binary run, linked from this machine into the test's PATH.
var realTools = []string{
	"id", "uname", "mktemp", "mkdir", "rm", "find", "tr", "tar", "gzip", "openssl", "cat", "cp", "readlink", "sleep",
	"touch",
}

// fakeBinary is the gophenberg an archive carries: it records its folders, its standard input and its arguments.
const fakeBinary = `#!/bin/sh
{
	pwd
	printf '%%s\n' "${0%%/*}"
	readlink /proc/self/fd/0
	for argument in "$@"; do
		printf '%%s\n' "$argument"
	done
} > %[1]s/handover.log
if [ -e %[1]s/handover.wait ]; then
	touch %[1]s/handover.started
	sleep 60
fi
code=0
if [ -e %[1]s/handover.exit ]; then
	read -r code < %[1]s/handover.exit
fi
exit "$code"
`

// fakeCurl records its arguments, then copies the file an address names from the served folder, as curl -f would.
const fakeCurl = `#!/bin/sh
for argument in "$@"; do
	printf '%%s\n' "$argument"
done >> %[1]s/curl.log
printf '\n' >> %[1]s/curl.log
if [ -e %[1]s/curl.fails ]; then
	read -r code < %[1]s/curl.fails
	printf 'curl: (%%s) the fake curl cannot reach the address\n' "$code" >&2
	exit "$code"
fi
target=
previous=
for argument in "$@"; do
	if [ "$previous" = -o ]; then
		target=$argument
	fi
	previous=$argument
done
file=%[2]s/${previous#%[3]s/}
if [ ! -f "$file" ]; then
	printf 'curl: (22) The requested URL returned error: 404\n' >&2
	exit 22
fi
cp "$file" "$target"
`

// fakeAnswering answers one first argument with a line and runs the real tool for anything else.
const fakeAnswering = `#!/bin/sh
if [ "$1" = %s ]; then
	printf '%%s\n' %s
	exit 0
fi
exec %s "$@"
`

// pathFacts is what the test compares of one path before and after a run.
type pathFacts struct {
	mode     fs.FileMode
	size     int64
	modified int64
}

// handoverRecord is what the fake binary recorded when install.sh ran it.
type handoverRecord struct {
	dir    string
	folder string
	stdin  string
	args   []string
}

// scriptScenario is what one install.sh scenario keeps: its test root, keys and archives, the test copy and the run.
type scriptScenario struct {
	root     string
	keys     map[string]ed25519.PrivateKey
	archives map[string][]byte
	lines    map[string]string
	cutOff   bool
	before   map[string]pathFacts
	exitCode int
	stdout   bytes.Buffer
	stderr   bytes.Buffer
}

// shellQuote quotes text as one word for sh.
func shellQuote(text string) string {
	return "'" + strings.ReplaceAll(text, "'", `'\''`) + "'"
}

// path joins the parts under the scenario's test root.
func (s *scriptScenario) path(parts ...string) string {
	return filepath.Join(append([]string{s.root}, parts...)...)
}

// installer is the test copy's installer folder.
func (s *scriptScenario) installer() string {
	return s.path("server", "var", "lib", "gophenberg-installer")
}

// served is the folder the fake curl serves the release's files from.
func (s *scriptScenario) served() string {
	return s.path("served", "v"+releaseVersion)
}

// setUp makes the scenario's folders, keys, tool links and fakes, and the test copy's fixed lines.
func (s *scriptScenario) setUp(root string) error {
	s.root, s.keys, s.archives = root, map[string]ed25519.PrivateKey{}, map[string][]byte{}
	s.cutOff, s.before, s.exitCode = false, nil, 0
	s.stdout.Reset()
	s.stderr.Reset()
	for _, folder := range []string{"server/var/lib", "fakes", "tools", "served/v" + releaseVersion, "logs", "elsewhere"} {
		if err := os.MkdirAll(s.path(folder), 0o755); err != nil {
			return err
		}
	}
	if err := makeRootFolder(s.installer()); err != nil {
		return err
	}
	if err := s.linkTools(); err != nil {
		return err
	}
	for _, name := range []string{"release", "standby", "neither"} {
		_, private, err := ed25519.GenerateKey(nil)
		if err != nil {
			return err
		}
		s.keys[name] = private
	}
	return s.fixLines()
}

// makeRootFolder makes a folder with mode 0700, whatever the umask.
func makeRootFolder(path string) error {
	if err := os.Mkdir(path, 0o700); err != nil {
		return err
	}
	return os.Chmod(path, 0o700)
}

// linkTools links each real tool into the tools folder and writes the fake curl.
func (s *scriptScenario) linkTools() error {
	for _, tool := range realTools {
		found, err := exec.LookPath(tool)
		if err != nil {
			return err
		}
		if err := os.Symlink(found, s.path("tools", tool)); err != nil {
			return err
		}
	}
	logs, served := shellQuote(s.path("logs")), shellQuote(s.path("served"))
	return s.writeFake("curl", fmt.Sprintf(fakeCurl, logs, served, shellQuote(testDownloads)))
}

// fixLines sets the test copy's lines for the download address, both keys, the installer folder and root's id.
func (s *scriptScenario) fixLines() error {
	release, err := pemBody(s.keys["release"])
	if err != nil {
		return err
	}
	standby, err := pemBody(s.keys["standby"])
	if err != nil {
		return err
	}
	s.lines = map[string]string{
		"downloads":        "downloads=" + testDownloads,
		"release_key":      "release_key=" + release,
		"standby_key":      "standby_key=" + standby,
		"installer_folder": "installer_folder=" + shellQuote(s.installer()),
		"root_id":          "root_id=" + strconv.Itoa(os.Getuid()),
	}
	return nil
}

// pemBody returns the one base64 line of the PEM that holds the public half of key.
func pemBody(key ed25519.PrivateKey) (string, error) {
	der, err := x509.MarshalPKIXPublicKey(key.Public())
	return base64.StdEncoding.EncodeToString(der), err
}

// writeFake writes an executable fake tool that comes first on the test's PATH.
func (s *scriptScenario) writeFake(tool, text string) error {
	path := s.path("fakes", tool)
	if err := os.WriteFile(path, []byte(text), 0o755); err != nil {
		return err
	}
	return os.Chmod(path, 0o755)
}

// writeAnswering writes a fake tool that prints answer for the first argument given and runs the real tool otherwise.
func (s *scriptScenario) writeAnswering(tool, argument, answer string) error {
	found, err := exec.LookPath(tool)
	if err != nil {
		return err
	}
	return s.writeFake(tool, fmt.Sprintf(fakeAnswering, shellQuote(argument), shellQuote(answer), shellQuote(found)))
}

// archiveName names the release's archive for the processor.
func archiveName(arch string) string {
	return "gophenberg_" + releaseVersion + "_linux_" + arch + ".tar.gz"
}

// packArchive packs a gzipped tar that holds the fake binary, unless left out, beside a release.json.
func (s *scriptScenario) packArchive(version, arch string, withBinary bool) ([]byte, error) {
	var packed bytes.Buffer
	zipped := gzip.NewWriter(&packed)
	archive := tar.NewWriter(zipped)
	entries := []struct {
		name, body string
		mode       int64
	}{
		{"gophenberg", fmt.Sprintf(fakeBinary, shellQuote(s.path("logs"))), 0o755},
		{"release.json", `{"version": "` + version + `", "platform": "linux/` + arch + `"}` + "\n", 0o644},
	}
	if !withBinary {
		entries = entries[1:]
	}
	for _, entry := range entries {
		header := &tar.Header{Name: entry.name, Mode: entry.mode, Size: int64(len(entry.body)), Typeflag: tar.TypeReg}
		if err := archive.WriteHeader(header); err != nil {
			return nil, err
		}
		if _, err := archive.Write([]byte(entry.body)); err != nil {
			return nil, err
		}
	}
	if err := archive.Close(); err != nil {
		return nil, err
	}
	if err := zipped.Close(); err != nil {
		return nil, err
	}
	return packed.Bytes(), nil
}

// stamp sets the test copy's checksum line for the processor to the SHA-256 of the archive.
func (s *scriptScenario) stamp(arch string, archive []byte) {
	sum := sha256.Sum256(archive)
	s.lines["sha256_"+arch] = "sha256_" + arch + "=" + hex.EncodeToString(sum[:])
}

// serve puts the archive under the processor's name in the served folder, with a .sig the named key made.
func (s *scriptScenario) serve(arch string, archive []byte, key string) error {
	path := filepath.Join(s.served(), archiveName(arch))
	if err := os.WriteFile(path, archive, 0o644); err != nil {
		return err
	}
	digest, err := signature.Digest(bytes.NewReader(archive))
	if err != nil {
		return err
	}
	return os.WriteFile(path+".sig", []byte(signature.Sign(s.keys[key], digest)+"\n"), 0o644)
}

// testCopy returns install.sh with each line the scenario sets replaced whole, cut right after main when asked.
func (s *scriptScenario) testCopy() ([]byte, error) {
	source, err := os.ReadFile(scriptPath)
	if err != nil {
		return nil, err
	}
	lines := strings.SplitAfter(string(source), "\n")
	for key, line := range s.lines {
		found := 0
		for i, current := range lines {
			if strings.HasPrefix(current, key+"=") {
				lines[i], found = line+"\n", found+1
			}
		}
		if found != 1 {
			return nil, fmt.Errorf("install.sh sets %s on %d lines, want 1", key, found)
		}
	}
	text := strings.Join(lines, "")
	if !s.cutOff {
		return []byte(text), nil
	}
	if last := "\nmain \"$@\"\n"; !strings.HasSuffix(text, last) {
		return nil, fmt.Errorf("install.sh does not end with %q", last)
	}
	return []byte(strings.TrimSuffix(text, " \"$@\"\n")), nil
}

// start pipes the test copy into dash -s -- with the arguments, the fakes and then the tools alone on PATH.
func (s *scriptScenario) start(dash string, args []string) (*exec.Cmd, error) {
	script, err := s.testCopy()
	if err != nil {
		return nil, err
	}
	if s.before, err = s.outsideInstaller(); err != nil {
		return nil, err
	}
	command := exec.Command(dash, append([]string{"-s", "--"}, args...)...)
	command.Stdin = bytes.NewReader(script)
	command.Stdout, command.Stderr = &s.stdout, &s.stderr
	command.Env = []string{"PATH=" + s.path("fakes") + string(os.PathListSeparator) + s.path("tools")}
	command.Dir = s.path("server")
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.WaitDelay = 10 * time.Second
	return command, command.Start()
}

// finish keeps the exit code of a run that ended, failing only when the run could not be waited for.
func (s *scriptScenario) finish(command *exec.Cmd, err error) error {
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		return err
	}
	s.exitCode = command.ProcessState.ExitCode()
	return nil
}

// outsideInstaller returns the facts of every path under the test root but the logs and the installer folder.
func (s *scriptScenario) outsideInstaller() (map[string]pathFacts, error) {
	facts := map[string]pathFacts{}
	err := filepath.WalkDir(s.root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == s.path("logs") || path == s.installer() {
			return skipped(entry)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		facts[path] = pathFacts{mode: info.Mode(), size: info.Size(), modified: info.ModTime().UnixNano()}
		return nil
	})
	return facts, err
}

// skipped leaves a folder out of a walk, and anything else by returning nothing.
func skipped(entry fs.DirEntry) error {
	if entry.IsDir() {
		return fs.SkipDir
	}
	return nil
}

// handover returns what the fake binary recorded, or an error when install.sh never ran it.
func (s *scriptScenario) handover() (handoverRecord, error) {
	data, err := os.ReadFile(s.path("logs", "handover.log"))
	if errors.Is(err, fs.ErrNotExist) {
		return handoverRecord{}, errors.New("server:install never ran")
	}
	if err != nil {
		return handoverRecord{}, err
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) < 3 {
		return handoverRecord{}, fmt.Errorf("the fake binary recorded %q, want its folders and standard input", data)
	}
	return handoverRecord{dir: lines[0], folder: lines[1], stdin: lines[2], args: lines[3:]}, nil
}

// asRun rewrites the real installer folder and download address in text into the test copy's own.
func (s *scriptScenario) asRun(text string) string {
	return strings.NewReplacer(realInstallerFolder, s.installer(), realDownloads, testDownloads).Replace(text)
}
