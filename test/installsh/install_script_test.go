// SPDX-License-Identifier: Apache-2.0

package installsh_test

import (
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/gopherium/gophenberg/internal/signature"
)

// installScriptFeature is the feature that describes install.sh.
const installScriptFeature = "../features/features/install-script.feature"

// installDocs is the docs page that publishes the release key and the standby.
const installDocs = "../../docs/src/content/docs/self-hosting/install.md"

// downloadFolder is the name mktemp gives install.sh's download folder.
var downloadFolder = regexp.MustCompile(`^download\.[A-Za-z0-9]{10}$`)

// scriptSteps binds the install.sh steps of one scenario to its state and the dash that runs it.
type scriptSteps struct {
	t    *testing.T
	dash string
	s    *scriptScenario
}

func TestInstallScript(t *testing.T) {
	t.Parallel()

	dash := needDash(t)
	tags := "~@wip"
	if os.Getenv("GOPHENBERG_BDD_WIP") != "" {
		tags = ""
	}
	suite := godog.TestSuite{
		ScenarioInitializer: initializeInstallScript(t, dash),
		Options: &godog.Options{
			Format: "pretty", Paths: []string{installScriptFeature}, Tags: tags, Strict: true, TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Error("the feature did not pass")
	}
}

func TestInstallScriptTrustsTheKeysTheDocsPublish(t *testing.T) {
	t.Parallel()

	script, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatalf("reading install.sh: %v", err)
	}
	docs, err := os.ReadFile(installDocs)
	if err != nil {
		t.Fatalf("reading the docs page: %v", err)
	}
	bodies := regexp.MustCompile(`-----BEGIN PUBLIC KEY-----\n(\S+)\n-----END PUBLIC KEY-----`).FindAllStringSubmatch(
		string(docs), -1)
	multikeys := regexp.MustCompile(`z6Mk[1-9A-HJ-NP-Za-km-z]+`).FindAllString(string(docs), -1)
	if len(bodies) != 2 || len(multikeys) != 2 {
		t.Fatalf("the docs page publishes %d PEM keys and %d Multikeys, want the release key and the standby",
			len(bodies), len(multikeys))
	}
	for i, name := range []string{"release_key", "standby_key"} {
		body := scriptValue(t, string(script), name)
		if body != bodies[i][1] {
			t.Errorf("install.sh's %s is %s, want %s, as the docs page publishes it", name, body, bodies[i][1])
		}
		if multikey := multikeyOf(t, body); multikey != multikeys[i] {
			t.Errorf("install.sh's %s is the Multikey %s, want %s, as the docs page publishes it", name, multikey,
				multikeys[i])
		}
	}
}

// needDash returns the dash that runs install.sh, skipping when it is missing or this is not Linux.
func needDash(t *testing.T) string {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("install.sh runs on Linux servers alone")
	}
	dash, err := exec.LookPath("dash")
	if err != nil {
		t.Skip("dash is not on PATH, so install.sh cannot run as curl | sh runs it")
	}
	for _, tool := range realTools {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not on PATH, so install.sh cannot run here", tool)
		}
	}
	return dash
}

// scriptValue returns the value of the one line of install.sh that sets name.
func scriptValue(t *testing.T, script, name string) string {
	t.Helper()
	var values []string
	for line := range strings.Lines(script) {
		if value, found := strings.CutPrefix(strings.TrimSuffix(line, "\n"), name+"="); found {
			values = append(values, value)
		}
	}
	if len(values) != 1 {
		t.Fatalf("install.sh sets %s on %d lines, want 1", name, len(values))
	}
	return values[0]
}

// multikeyOf returns the Multikey of the Ed25519 key one base64 PEM line holds.
func multikeyOf(t *testing.T, body string) string {
	t.Helper()
	der, err := base64.StdEncoding.DecodeString(body)
	if err != nil {
		t.Fatalf("decoding %s: %v", body, err)
	}
	key, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		t.Fatalf("parsing %s: %v", body, err)
	}
	if _, ok := key.(ed25519.PublicKey); !ok {
		t.Fatalf("%s holds a %T, want an Ed25519 key", body, key)
	}
	multikey, err := signature.Multikey(key)
	if err != nil {
		t.Fatalf("writing %s as a Multikey: %v", body, err)
	}
	return multikey
}

// initializeInstallScript returns the binding of the install.sh steps, each scenario on a test root of its own.
func initializeInstallScript(t *testing.T, dash string) func(*godog.ScenarioContext) {
	return func(sc *godog.ScenarioContext) {
		steps := &scriptSteps{t: t, dash: dash, s: &scriptScenario{}}
		sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
			return ctx, steps.s.setUp(t.TempDir())
		})
		initializeRelease(sc, steps)
		initializeServer(sc, steps)
		sc.When(`^the operator runs install\.sh as root with these arguments:$`, steps.runWithTable)
		sc.When(`^the operator runs install\.sh as root with "([^"]*)"$`, steps.runAsRoot)
		sc.When(`^the operator runs install\.sh as a plain user with "([^"]*)"$`, steps.runAsPlainUser)
		sc.When(`^the operator runs install\.sh as root with "([^"]*)" and presses Ctrl-C while server:install runs$`,
			steps.runAndInterrupt)
		initializeHandover(sc, steps)
		initializeOutcomes(sc, steps)
	}
}

// initializeRelease binds the steps that set what install.sh names and what the download address serves.
func initializeRelease(sc *godog.ScenarioContext, steps *scriptSteps) {
	sc.Given(`^install\.sh names Gophenberg 0\.20\.0 and the checksum of each Linux archive$`, steps.nameRelease)
	sc.Given(`^install\.sh still names no release$`, steps.nameNoRelease)
	sc.Given(`^the copy of install\.sh ends right after the word main of its last line$`, steps.cutOff)
	sc.Given(`^the download address serves the linux/(amd64|arm64) archive the (release|standby) key signed$`,
		steps.serveSigned)
	sc.Given(`^the download address serves the linux/amd64 archive signed by a key that is neither release key$`,
		steps.serveSignedByNeither)
	sc.Given(`^the download address serves another linux/amd64 archive the release key signed$`, steps.serveAnother)
	sc.Given(`^the download address serves a linux/amd64 archive without the gophenberg binary, which install\.sh`+
		` names and the release key signed$`, steps.serveWithoutBinary)
	sc.Given(`^the download address serves no (\S+)$`, steps.serveNo)
	sc.Given(`^the archive's \.sig is empty$`, steps.emptySignature)
	sc.Given(`^the archive's \.sig holds the text "([^"]*)"$`, steps.textSignature)
	sc.Given(`^curl cannot reach the download address and exits with code (\d+)$`, steps.unreachable)
}

// initializeServer binds the steps that set up the server install.sh runs on.
func initializeServer(sc *godog.ScenarioContext, steps *scriptSteps) {
	sc.Given(`^the server is an? (x86_64|aarch64|riscv64) machine$`, steps.machine)
	sc.Given(`^the server has no (curl|openssl|tar)$`, steps.withoutTool)
	sc.Given(`^openssl version prints "([^"]*)"$`, steps.opensslVersion)
	sc.Given(`^/var/lib/gophenberg-installer does not exist yet$`, steps.noInstallerFolder)
	sc.Given(`^/var/lib/gophenberg-installer is (a link to another folder|a folder another user owns|`+
		`a folder with mode 0770|a folder with mode 0707|a plain file)$`, steps.installerFolderIs)
	sc.Given(`^server:install exits with code (\d+)$`, steps.handoverExits)
}

// initializeHandover binds the steps that check how install.sh ran server:install.
func initializeHandover(sc *godog.ScenarioContext, steps *scriptSteps) {
	sc.Then(`^server:install ran from /, from a download folder inside /var/lib/gophenberg-installer, with these`+
		` arguments:$`, steps.ranWith)
	sc.Then(`^server:install ran on the archive (\S+)$`, steps.ranOn)
	sc.Then(`^server:install read its standard input from /dev/null$`, steps.readNothing)
	sc.Then(`^server:install never ran$`, steps.neverRan)
	sc.Then(`^curl downloaded each file with these arguments:$`, steps.downloadedWith)
	sc.Then(`^nothing was downloaded$`, steps.nothingDownloaded)
}

// initializeOutcomes binds the steps that check what install.sh printed, how it ended and what it left.
func initializeOutcomes(sc *godog.ScenarioContext, steps *scriptSteps) {
	sc.Then(`^install\.sh printed:$`, steps.printed)
	sc.Then(`^install\.sh ended with exit code (\d+)$`, steps.endedWith)
	sc.Then(`^install\.sh stops with "([^"]*)"$`, steps.stopsWith)
	sc.Then(`^install\.sh stops with "([^"]*)" after (curl|tar)'s own error$`, steps.stopsAfter)
	sc.Then(`^no download folder is left in /var/lib/gophenberg-installer$`, steps.noDownloadFolder)
	sc.Then(`^install\.sh made nothing outside /var/lib/gophenberg-installer$`, steps.madeNothingOutside)
	sc.Then(`^/var/lib/gophenberg-installer is a folder with mode (0[0-7]{3})$`, steps.installerFolderMode)
}

// nameRelease packs and stamps an archive for each Linux processor, the version line naming the release.
func (steps *scriptSteps) nameRelease() error {
	steps.s.lines["version"] = "version=" + releaseVersion
	for _, arch := range []string{"amd64", "arm64"} {
		archive, err := steps.s.packArchive(releaseVersion, arch, true)
		if err != nil {
			return err
		}
		steps.s.archives[arch] = archive
		steps.s.stamp(arch, archive)
	}
	return nil
}

// nameNoRelease leaves install.sh's version line as the repository holds it.
func (steps *scriptSteps) nameNoRelease() error {
	delete(steps.s.lines, "version")
	return nil
}

// cutOff makes the test copy end right after the word main of its last line.
func (steps *scriptSteps) cutOff() error {
	steps.s.cutOff = true
	return nil
}

// serveSigned serves the release's archive for the processor with a .sig the named key made.
func (steps *scriptSteps) serveSigned(arch, key string) error {
	return steps.s.serve(arch, steps.s.archives[arch], key)
}

// serveSignedByNeither serves the release's linux/amd64 archive with a .sig a third key made.
func (steps *scriptSteps) serveSignedByNeither() error {
	return steps.s.serve("amd64", steps.s.archives["amd64"], "neither")
}

// serveAnother serves an older release's linux/amd64 archive the release key signed, under this release's name.
func (steps *scriptSteps) serveAnother() error {
	archive, err := steps.s.packArchive("0.19.0", "amd64", true)
	if err != nil {
		return err
	}
	return steps.s.serve("amd64", archive, "release")
}

// serveWithoutBinary serves and stamps a linux/amd64 archive that holds no gophenberg, the release key signing it.
func (steps *scriptSteps) serveWithoutBinary() error {
	archive, err := steps.s.packArchive(releaseVersion, "amd64", false)
	if err != nil {
		return err
	}
	steps.s.stamp("amd64", archive)
	return steps.s.serve("amd64", archive, "release")
}

// serveNo removes the named file from what the download address serves.
func (steps *scriptSteps) serveNo(name string) error {
	return os.Remove(filepath.Join(steps.s.served(), name))
}

// emptySignature empties the served .sig of the linux/amd64 archive.
func (steps *scriptSteps) emptySignature() error {
	return steps.textSignature("")
}

// textSignature writes the text as the served .sig of the linux/amd64 archive.
func (steps *scriptSteps) textSignature(text string) error {
	return os.WriteFile(filepath.Join(steps.s.served(), archiveName("amd64")+".sig"), []byte(text), 0o644)
}

// unreachable makes the fake curl fail every download with the exit code.
func (steps *scriptSteps) unreachable(code string) error {
	return os.WriteFile(steps.s.path("logs", "curl.fails"), []byte(code+"\n"), 0o644)
}

// machine makes uname -m name the machine.
func (steps *scriptSteps) machine(name string) error {
	return steps.s.writeAnswering("uname", "-m", name)
}

// withoutTool leaves the tool off the test's PATH, skipping when the folders install.sh adds to PATH hold it.
func (steps *scriptSteps) withoutTool(tool string) error {
	for _, folder := range []string{"/usr/sbin", "/sbin"} {
		if _, err := os.Stat(filepath.Join(folder, tool)); err == nil {
			steps.t.Logf("%s holds %s, which install.sh finds through the PATH it extends", folder, tool)
			return godog.ErrSkip
		}
	}
	if err := os.Remove(steps.s.path("fakes", tool)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := os.Remove(steps.s.path("tools", tool)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// opensslVersion makes openssl version print the line, every other openssl command staying the real one.
func (steps *scriptSteps) opensslVersion(line string) error {
	return steps.s.writeAnswering("openssl", "version", line)
}

// noInstallerFolder removes the installer folder.
func (steps *scriptSteps) noInstallerFolder() error {
	return os.Remove(steps.s.installer())
}

// installerFolderIs puts what the step names at the installer folder's path.
func (steps *scriptSteps) installerFolderIs(what string) error {
	folder := steps.s.installer()
	switch what {
	case "a link to another folder":
		return errors.Join(os.Remove(folder), os.Symlink(steps.s.path("elsewhere"), folder))
	case "a folder another user owns":
		return steps.rootIsAnotherUser()
	case "a plain file":
		return errors.Join(os.Remove(folder), os.WriteFile(folder, nil, 0o600))
	}
	mode, err := strconv.ParseUint(strings.TrimPrefix(what, "a folder with mode "), 8, 32)
	if err != nil {
		return err
	}
	return os.Chmod(folder, fs.FileMode(mode))
}

// rootIsAnotherUser makes root's id one more than the test's own, so the folders the test makes belong to another.
func (steps *scriptSteps) rootIsAnotherUser() error {
	other := strconv.Itoa(os.Getuid() + 1)
	steps.s.lines["root_id"] = "root_id=" + other
	return steps.s.writeAnswering("id", "-u", other)
}

// handoverExits makes the fake binary exit with the code.
func (steps *scriptSteps) handoverExits(code string) error {
	return os.WriteFile(steps.s.path("logs", "handover.exit"), []byte(code+"\n"), 0o644)
}

// runWithTable runs install.sh as root with one argument for each row of the table.
func (steps *scriptSteps) runWithTable(table *godog.Table) error {
	args := make([]string, 0, len(table.Rows))
	for _, row := range table.Rows {
		args = append(args, row.Cells[0].Value)
	}
	return steps.run(args)
}

// runAsRoot runs install.sh as root with the arguments, split at spaces.
func (steps *scriptSteps) runAsRoot(arguments string) error {
	return steps.run(strings.Fields(arguments))
}

// runAsPlainUser runs install.sh with the arguments while id -u names a user other than root.
func (steps *scriptSteps) runAsPlainUser(arguments string) error {
	if err := steps.s.writeAnswering("id", "-u", strconv.Itoa(os.Getuid()+1)); err != nil {
		return err
	}
	return steps.run(strings.Fields(arguments))
}

// run runs install.sh to its end with the arguments.
func (steps *scriptSteps) run(args []string) error {
	command, err := steps.s.start(steps.dash, args)
	if err != nil {
		return err
	}
	return steps.s.finish(command, command.Wait())
}

// runAndInterrupt runs install.sh as root and sends Ctrl-C to its process group once server:install is running.
func (steps *scriptSteps) runAndInterrupt(arguments string) error {
	if err := os.WriteFile(steps.s.path("logs", "handover.wait"), nil, 0o644); err != nil {
		return err
	}
	command, err := steps.s.start(steps.dash, strings.Fields(arguments))
	if err != nil {
		return err
	}
	ended := make(chan error, 1)
	go func() { ended <- command.Wait() }()
	if err := steps.awaitHandover(command, ended); err != nil {
		return err
	}
	if err := syscall.Kill(-command.Process.Pid, syscall.SIGINT); err != nil {
		return err
	}
	return steps.s.finish(command, <-ended)
}

// awaitHandover waits until the fake binary says it started, failing when install.sh ends first or never gets there.
func (steps *scriptSteps) awaitHandover(command *exec.Cmd, ended <-chan error) error {
	deadline := time.After(30 * time.Second)
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		if _, err := os.Stat(steps.s.path("logs", "handover.started")); err == nil {
			return nil
		}
		select {
		case err := <-ended:
			return fmt.Errorf("install.sh ended (%v) before server:install started, printing %q", err,
				steps.s.stderr.String())
		case <-deadline:
			killed := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
			return errors.Join(errors.New("server:install did not start within 30 seconds"), killed, <-ended)
		case <-tick.C:
		}
	}
}

// inDownloadFolder checks that the fake binary ran from a download folder inside the installer folder, and names it.
func (steps *scriptSteps) inDownloadFolder() (handoverRecord, error) {
	record, err := steps.s.handover()
	if err != nil {
		return record, err
	}
	if filepath.Dir(record.folder) != steps.s.installer() || !downloadFolder.MatchString(filepath.Base(record.folder)) {
		return record, fmt.Errorf("server:install ran from %s, want a download.* folder inside %s", record.folder,
			steps.s.installer())
	}
	return record, nil
}

// ranWith checks that server:install ran from / out of a download folder, with the table's arguments.
func (steps *scriptSteps) ranWith(table *godog.Table) error {
	record, err := steps.inDownloadFolder()
	if err != nil {
		return err
	}
	if record.dir != "/" {
		return fmt.Errorf("server:install ran in %s, want /", record.dir)
	}
	want := make([]string, 0, len(table.Rows))
	for _, row := range table.Rows {
		want = append(want, strings.ReplaceAll(row.Cells[0].Value, "<download folder>", record.folder))
	}
	if !slices.Equal(record.args, want) {
		return fmt.Errorf("server:install got %q, want %q", record.args, want)
	}
	return nil
}

// ranOn checks that server:install ran on the named archive in its download folder.
func (steps *scriptSteps) ranOn(name string) error {
	record, err := steps.inDownloadFolder()
	if err != nil {
		return err
	}
	want := []string{"server:install", "-archive", record.folder + "/" + name}
	if len(record.args) < len(want) || !slices.Equal(record.args[:len(want)], want) {
		return fmt.Errorf("server:install got %q, want it to start with %q", record.args, want)
	}
	return nil
}

// readNothing checks that server:install's standard input was /dev/null.
func (steps *scriptSteps) readNothing() error {
	record, err := steps.s.handover()
	if err != nil {
		return err
	}
	if record.stdin != "/dev/null" {
		return fmt.Errorf("server:install read its standard input from %s, want /dev/null", record.stdin)
	}
	return nil
}

// neverRan checks that install.sh never ran the archive's binary.
func (steps *scriptSteps) neverRan() error {
	return steps.absent("handover.log", "server:install ran, want it never run")
}

// absent checks that the fakes never wrote the named log, failing with the complaint when they did.
func (steps *scriptSteps) absent(log, complaint string) error {
	_, err := os.Stat(steps.s.path("logs", log))
	if err == nil {
		return errors.New(complaint)
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// downloadedWith checks that curl ran once for each row of the table, with its cells as the arguments.
func (steps *scriptSteps) downloadedWith(table *godog.Table) error {
	record, err := steps.s.handover()
	if err != nil {
		return err
	}
	logged, err := os.ReadFile(steps.s.path("logs", "curl.log"))
	if err != nil {
		return err
	}
	var want strings.Builder
	for _, row := range table.Rows {
		for _, cell := range row.Cells {
			want.WriteString(strings.ReplaceAll(steps.s.asRun(cell.Value), "<download folder>", record.folder) + "\n")
		}
		want.WriteString("\n")
	}
	if string(logged) != want.String() {
		return fmt.Errorf("curl got %q, want %q", logged, want.String())
	}
	return nil
}

// nothingDownloaded checks that install.sh never ran curl.
func (steps *scriptSteps) nothingDownloaded() error {
	return steps.absent("curl.log", "curl ran, want nothing downloaded")
}

// printed checks what install.sh printed on standard output.
func (steps *scriptSteps) printed(want *godog.DocString) error {
	if got := steps.s.stdout.String(); got != want.Content+"\n" {
		return fmt.Errorf("install.sh printed %q, want %q", got, want.Content+"\n")
	}
	return nil
}

// endedWith checks install.sh's exit code.
func (steps *scriptSteps) endedWith(code int) error {
	if steps.s.exitCode != code {
		return fmt.Errorf("install.sh ended with exit code %d and printed %q, want %d", steps.s.exitCode,
			steps.s.stderr.String(), code)
	}
	return nil
}

// stopsWith checks that install.sh ended with exit code 1 and printed the stop line alone on standard error.
func (steps *scriptSteps) stopsWith(line string) error {
	if err := steps.endedWith(1); err != nil {
		return err
	}
	if got, want := steps.s.stderr.String(), "install.sh: "+steps.s.asRun(line)+"\n"; got != want {
		return fmt.Errorf("install.sh printed %q on standard error, want %q", got, want)
	}
	return nil
}

// stopsAfter checks that install.sh ended with exit code 1 and the stop line, after the tool's own error lines.
func (steps *scriptSteps) stopsAfter(line, tool string) error {
	if err := steps.endedWith(1); err != nil {
		return err
	}
	lines := strings.Split(strings.TrimSuffix(steps.s.stderr.String(), "\n"), "\n")
	last := len(lines) - 1
	if want := "install.sh: " + steps.s.asRun(line); lines[last] != want {
		return fmt.Errorf("install.sh ended its standard error with %q, want %q", lines[last], want)
	}
	if last == 0 {
		return fmt.Errorf("install.sh printed no line of %s's own before it stopped", tool)
	}
	for _, own := range lines[:last] {
		if !strings.HasPrefix(own, tool+": ") {
			return fmt.Errorf("install.sh printed %q before it stopped, want only %s's own lines", own, tool)
		}
	}
	return nil
}

// noDownloadFolder checks that the installer folder holds no download folder.
func (steps *scriptSteps) noDownloadFolder() error {
	entries, err := os.ReadDir(steps.s.installer())
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "download.") {
			return fmt.Errorf("install.sh left %s in the installer folder", entry.Name())
		}
	}
	return nil
}

// madeNothingOutside checks that every path outside the installer folder is as it was before the run.
func (steps *scriptSteps) madeNothingOutside() error {
	after, err := steps.s.outsideInstaller()
	if err != nil {
		return err
	}
	if !maps.Equal(steps.s.before, after) {
		return fmt.Errorf("the paths outside the installer folder were %v before the run and %v after it",
			steps.s.before, after)
	}
	return nil
}

// installerFolderMode checks that the installer folder is a real folder with the mode.
func (steps *scriptSteps) installerFolderMode(mode string) error {
	info, err := os.Lstat(steps.s.installer())
	if err != nil {
		return err
	}
	if got := fmt.Sprintf("%04o", info.Mode().Perm()); !info.IsDir() || got != mode {
		return fmt.Errorf("the installer folder is %v, want a folder with mode %s", info.Mode(), mode)
	}
	return nil
}
