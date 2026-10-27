// SPDX-License-Identifier: Apache-2.0

package installer

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/gopherium/framework/gonsole"

	"github.com/gopherium/gophenberg/internal/version"
)

// openTree returns a root on a new folder of the test's own disk, with the path of that folder.
func openTree(t *testing.T) (*os.Root, string) {
	t.Helper()
	folder := t.TempDir()
	tree, err := os.OpenRoot(folder)
	if err != nil {
		t.Fatalf("opening %s as a root: %v", folder, err)
	}
	t.Cleanup(func() { _ = tree.Close() })
	return tree, folder
}

// testIDs returns the ids of the user the test runs as, the only ones a test that is not root may give a file.
func testIDs() *owner {
	return &owner{uid: os.Getuid(), gid: os.Getgid()}
}

// wantEntry fails t unless the entry at path has the type and the mode of mode, belonging to ids or to the test's own.
func wantEntry(t *testing.T, path string, mode fs.FileMode, ids *owner) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("describing %s: %v", path, err)
	}
	if got := info.Mode().Type() | info.Mode().Perm(); got != mode {
		t.Errorf("%s is %s, want %s", path, got, mode)
	}
	want := *testIDs()
	if ids != nil {
		want = *ids
	}
	if stat := info.Sys().(*syscall.Stat_t); int(stat.Uid) != want.uid || int(stat.Gid) != want.gid {
		t.Errorf("%s belongs to %d:%d, want %d:%d", path, stat.Uid, stat.Gid, want.uid, want.gid)
	}
}

// wantText fails t unless the file at path holds text.
func wantText(t *testing.T, path, text string) {
	t.Helper()
	held, err := os.ReadFile(path)
	if err != nil || string(held) != text {
		t.Errorf("reading %s = %q, %v, want %q", path, held, err, text)
	}
}

// wantOnly fails t unless the folder at path holds the entries names alone, in the order of their names.
func wantOnly(t *testing.T, path string, names ...string) {
	t.Helper()
	entries, err := os.ReadDir(path)
	if err != nil {
		t.Fatalf("listing %s: %v", path, err)
	}
	var held []string
	for _, entry := range entries {
		held = append(held, entry.Name())
	}
	if !slices.Equal(held, names) {
		t.Errorf("%s holds %q, want %q", path, held, names)
	}
}

// skipAsRoot skips t when it runs as root, who may do what the test proves a plain user may not.
func skipAsRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root may give a file to any user and write any folder")
	}
}

// serverEnvFile is a server.env a test writes, with a mode a umask of 022 or 077 would narrow.
var serverEnvFile = file{path: "/etc/gophenberg/server.env", owner: "root", mode: 0o664, text: "GOPHENBERG_ADDR=x\n"}

func TestTheMachineOpensAFolderOfTheServerAsATree(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)

	tree, err := server.machine().tree("/opt")
	if err != nil {
		t.Fatalf("tree(/opt) = %v, want the folder open", err)
	}
	defer func() { _ = tree.Close() }()

	if tree.Name() != server.at("/opt") {
		t.Errorf("tree(/opt) opened %s, want %s", tree.Name(), server.at("/opt"))
	}
}

func TestTheMachineRefusesATreeOnAFolderThatIsNotThere(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)

	_, err := server.machine().tree("/srv")

	wantRefused(t, err, "installer: open "+server.at("/srv")+": no such file or directory")
}

func TestWriteFileWritesTheTextWithItsModeAndMakesTheMissingFoldersAt0755WhateverTheUmask(t *testing.T) {
	kept := syscall.Umask(0o077)
	defer syscall.Umask(kept)
	tree, folder := openTree(t)

	err := writeFile(tree, "etc/gophenberg/server.env", serverEnvFile, nil)

	if err != nil {
		t.Fatalf("writeFile() = %v, want the file written", err)
	}
	wantText(t, filepath.Join(folder, "etc/gophenberg/server.env"), serverEnvFile.text)
	wantEntry(t, filepath.Join(folder, "etc/gophenberg/server.env"), 0o664, nil)
	wantEntry(t, filepath.Join(folder, "etc"), fs.ModeDir|0o755, nil)
	wantEntry(t, filepath.Join(folder, "etc/gophenberg"), fs.ModeDir|0o755, nil)
	wantOnly(t, filepath.Join(folder, "etc/gophenberg"), "server.env")
}

func TestWriteFileLeavesAFolderAboveItAsItFoundIt(t *testing.T) {
	t.Parallel()

	tree, folder := openTree(t)
	if err := os.MkdirAll(filepath.Join(folder, "var/lib/gophenberg-installer"), 0o700); err != nil {
		t.Fatalf("making the installer's folder: %v", err)
	}

	err := writeFile(tree, "var/lib/gophenberg-installer/install.json", file{mode: 0o600, text: "{}\n"}, testIDs())

	if err != nil {
		t.Fatalf("writeFile() = %v, want the file written", err)
	}
	wantEntry(t, filepath.Join(folder, "var/lib/gophenberg-installer"), fs.ModeDir|0o700, nil)
}

func TestWriteFileReplacesTheFileThereAndLeavesNoPartialFile(t *testing.T) {
	t.Parallel()

	tree, folder := openTree(t)
	if err := os.WriteFile(filepath.Join(folder, "server.env"), []byte("GOPHENBERG_ADDR=old\n"), 0o600); err != nil {
		t.Fatalf("writing the old file: %v", err)
	}

	err := writeFile(tree, "server.env", serverEnvFile, nil)

	if err != nil {
		t.Fatalf("writeFile() = %v, want the file written", err)
	}
	wantText(t, filepath.Join(folder, "server.env"), serverEnvFile.text)
	wantOnly(t, folder, "server.env")
}

func TestWriteFileGivesTheFileAndTheFoldersItMakesToTheIdsItIsHanded(t *testing.T) {
	t.Parallel()

	tree, folder := openTree(t)

	err := writeFile(tree, "state/.env", file{mode: 0o600, text: "GOPHENBERG_ADDR=x\n"}, testIDs())

	if err != nil {
		t.Fatalf("writeFile() = %v, want the file written", err)
	}
	wantEntry(t, filepath.Join(folder, "state/.env"), 0o600, testIDs())
	wantEntry(t, filepath.Join(folder, "state"), fs.ModeDir|0o755, testIDs())
}

func TestWriteFileRefusesIdsItMayNotGiveAndLeavesNoPartialFile(t *testing.T) {
	t.Parallel()

	skipAsRoot(t)
	tree, folder := openTree(t)

	err := writeFile(tree, "server.env", serverEnvFile, &owner{uid: 0, gid: 0})

	if got := fmt.Sprint(err); !strings.HasPrefix(got, "installer: write /etc/gophenberg/server.env: chown ") ||
		!strings.HasSuffix(got, ": operation not permitted") {
		t.Errorf("writeFile() = %v, want the chown refused", err)
	}
	wantOnly(t, folder)
}

func TestWriteFileRefusesAFolderItMayNotWriteIn(t *testing.T) {
	t.Parallel()

	skipAsRoot(t)
	tree, folder := openTree(t)
	if err := os.Mkdir(filepath.Join(folder, "etc"), 0o555); err != nil {
		t.Fatalf("making a folder nobody may write: %v", err)
	}

	err := writeFile(tree, "etc/server.env", serverEnvFile, nil)

	if got := fmt.Sprint(err); !strings.HasSuffix(got, ": permission denied") {
		t.Errorf("writeFile() = %v, want the write refused", err)
	}
}

func TestWriteFileRefusesAFolderAboveItThatLeadsOutOfTheTree(t *testing.T) {
	t.Parallel()

	tree, folder := openTree(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(folder, "etc")); err != nil {
		t.Fatalf("linking etc out of the tree: %v", err)
	}

	err := writeFile(tree, "etc/server.env", serverEnvFile, nil)

	if got := fmt.Sprint(err); !strings.HasSuffix(got, "path escapes from parent") {
		t.Errorf("writeFile() = %v, want the link out of the tree refused", err)
	}
	wantOnly(t, outside)
}

func TestWriteFileRefusesAFileWhereAFolderAboveItGoes(t *testing.T) {
	t.Parallel()

	tree, folder := openTree(t)
	if err := os.WriteFile(filepath.Join(folder, "etc"), nil, 0o644); err != nil {
		t.Fatalf("writing a file where a folder goes: %v", err)
	}

	err := writeFile(tree, "etc/gophenberg/server.env", serverEnvFile, nil)

	if got := fmt.Sprint(err); !strings.HasSuffix(got, ": not a directory") {
		t.Errorf("writeFile() = %v, want the file in the way refused", err)
	}
}

func TestWriteFileRefusesAFolderAtItsNameAndLeavesNoPartialFile(t *testing.T) {
	t.Parallel()

	tree, folder := openTree(t)
	if err := os.MkdirAll(filepath.Join(folder, "server.env/kept"), 0o755); err != nil {
		t.Fatalf("making a folder where the file goes: %v", err)
	}

	err := writeFile(tree, "server.env", serverEnvFile, nil)

	if got := fmt.Sprint(err); !strings.HasPrefix(got, "installer: write /etc/gophenberg/server.env: renameat ") {
		t.Errorf("writeFile() = %v, want the rename refused", err)
	}
	wantOnly(t, folder, "server.env")
}

func TestWriteFileRefusesAFolderAboveItThatCannotBeMadeTwoLevelsUp(t *testing.T) {
	t.Parallel()

	tree, folder := openTree(t)
	if err := os.Symlink("nowhere/deeper", filepath.Join(folder, "etc")); err != nil {
		t.Fatalf("linking etc to nowhere: %v", err)
	}

	err := writeFile(tree, "etc/apt/keyrings/caddy.asc", file{path: caddyKeyPath, mode: 0o644}, nil)

	wantRefused(t, err, "installer: write /etc/apt/keyrings/caddy.asc: make the folder etc/apt: mkdirat etc/apt: no "+
		"such file or directory")
}

func TestMakeFolderMakesTheFolderWithItsModeWhateverTheUmask(t *testing.T) {
	kept := syscall.Umask(0o077)
	defer syscall.Umask(kept)
	tree, folder := openTree(t)

	err := makeFolder(tree, "media", 0o750, nil)

	if err != nil {
		t.Fatalf("makeFolder() = %v, want the folder made", err)
	}
	wantEntry(t, filepath.Join(folder, "media"), fs.ModeDir|0o750, nil)
}

func TestMakeFolderTakesTheFolderThereAndGivesItItsModeAndOwnerAgain(t *testing.T) {
	t.Parallel()

	tree, folder := openTree(t)
	if err := os.Mkdir(filepath.Join(folder, "versions"), 0o700); err != nil {
		t.Fatalf("making the folder: %v", err)
	}

	err := makeFolder(tree, "versions", 0o755, testIDs())

	if err != nil {
		t.Fatalf("makeFolder() = %v, want the folder taken", err)
	}
	wantEntry(t, filepath.Join(folder, "versions"), fs.ModeDir|0o755, testIDs())
}

func TestMakeFolderTakesTheTopOfTheTree(t *testing.T) {
	t.Parallel()

	tree, folder := openTree(t)

	err := makeFolder(tree, ".", 0o750, testIDs())

	if err != nil {
		t.Fatalf("makeFolder(.) = %v, want the top of the tree taken", err)
	}
	wantEntry(t, folder, fs.ModeDir|0o750, testIDs())
}

func TestMakeFolderRefusesALinkWhetherItLeadsInsideTheTreeOrOut(t *testing.T) {
	t.Parallel()

	tree, folder := openTree(t)
	outside := t.TempDir()
	if err := os.Chmod(outside, 0o700); err != nil {
		t.Fatalf("changing the mode of the folder outside: %v", err)
	}
	if err := os.Mkdir(filepath.Join(folder, "media"), 0o700); err != nil {
		t.Fatalf("making the folder inside: %v", err)
	}
	for name, target := range map[string]string{"themes": "media", "state": outside} {
		if err := os.Symlink(target, filepath.Join(folder, name)); err != nil {
			t.Fatalf("linking %s to %s: %v", name, target, err)
		}

		err := makeFolder(tree, name, 0o755, nil)

		wantRefused(t, err, "make the folder "+name+": it is not a real folder")
	}
	wantEntry(t, filepath.Join(folder, "media"), fs.ModeDir|0o700, nil)
	wantEntry(t, outside, fs.ModeDir|0o700, nil)
}

func TestMakeFolderRefusesAFileAtItsName(t *testing.T) {
	t.Parallel()

	tree, folder := openTree(t)
	if err := os.WriteFile(filepath.Join(folder, "media"), nil, 0o644); err != nil {
		t.Fatalf("writing a file where the folder goes: %v", err)
	}

	err := makeFolder(tree, "media", 0o750, nil)

	wantRefused(t, err, "make the folder media: it is not a real folder")
}

func TestMakeFolderRefusesAFolderItCannotMake(t *testing.T) {
	t.Parallel()

	tree, _ := openTree(t)

	err := makeFolder(tree, "srv/sites", 0o755, nil)

	wantRefused(t, err, "make the folder srv/sites: mkdirat srv/sites: no such file or directory")
}

// runOn returns the run that changes server as a first run for example.com that found the record found, and its answer.
func runOn(t *testing.T, server *fakeServer, found Record) (*installing, *strings.Builder) {
	t.Helper()
	check := server.check()
	check.settings.Domain, check.record = "example.com", found
	answer := &strings.Builder{}
	call := gonsole.Call{Stdout: answer, Stderr: &server.notes}
	running, err := installingOn(check, firstSite(t, "example.com", 255), call)
	if err != nil {
		t.Fatalf("installingOn() = %v, want the run ready", err)
	}
	t.Cleanup(running.close)
	return running, answer
}

// marks returns the marks of the install record on server, failing t when it cannot read it.
func marks(t *testing.T, server *fakeServer) map[string]string {
	t.Helper()
	record, err := readRecord(server.machine())
	if err != nil {
		t.Fatalf("readRecord() = %v, want the record the run saved", err)
	}
	return record.Steps
}

// notApplied is the mark a step's apply notes until the apply runs.
const notApplied = "not applied"

// markedDuring returns a step called id whose apply notes the mark the saved record holds for it, then returns err.
func markedDuring(t *testing.T, server *fakeServer, id string, err error) (step, *string) {
	during := notApplied
	return step{id: id, does: "take the step " + id, apply: func(*installing, context.Context, step) error {
		during = marks(t, server)[id]
		return err
	}}, &during
}

func TestTakeMarksTheStepStartedBeforeItsApplyAndEndedAfter(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	running, _ := runOn(t, server, Record{})
	planned, during := markedDuring(t, server, "packages", nil)

	err := running.take(t.Context(), planned)

	if err != nil || *during != "started" {
		t.Errorf("take() = %v, the record marking the step %q during its apply, want nil and started", err, *during)
	}
	if got := marks(t, server)["packages"]; got != "ended" {
		t.Errorf("the record marks the step %q after take(), want ended", got)
	}
}

func TestTheFirstSaveNamesTheRunningInstallerAndTheDomainInRootsOwnFolder(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	running, _ := runOn(t, server, Record{Installer: "0.0.1", Domain: "www.example.com"})
	planned, _ := markedDuring(t, server, "server-env", nil)

	err := running.take(t.Context(), planned)

	record, readErr := readRecord(server.machine())
	if err := errors.Join(err, readErr); err != nil {
		t.Fatalf("take() then readRecord() = %v, want the record saved", err)
	}
	if record.Installer != version.Version() || record.Domain != "example.com" {
		t.Errorf("take() saved %+v, want the installer %s and example.com", record, version.Version())
	}
	wantEntry(t, server.at(installerFolder), fs.ModeDir|0o700, nil)
	wantEntry(t, server.at(recordPath), 0o600, nil)
}

func TestTakeLeavesTheMarkOfAStepAnEarlierRunEndedWhenItsApplyFails(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	running, _ := runOn(t, server, Record{Steps: map[string]string{"server-env": "ended", "caddy": "ended"}})
	if err := running.save(); err != nil {
		t.Fatalf("save() = %v, want the record the run found saved", err)
	}
	planned, during := markedDuring(t, server, "caddy", errors.New("caddy validate failed"))

	err := running.take(t.Context(), planned)

	wantRefused(t, err, "caddy validate failed")
	if got := marks(t, server)["caddy"]; *during != "ended" || got != "ended" {
		t.Errorf("the record marks the step %q during its apply and %q after, want ended both times", *during, got)
	}
}

func TestTakeStopsBeforeTheApplyWhenItCannotMarkTheStepStarted(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	server.folder(recordPath + "/kept")
	running, _ := runOn(t, server, Record{})
	planned, during := markedDuring(t, server, "packages", nil)

	err := running.take(t.Context(), planned)

	if got := fmt.Sprint(err); !strings.HasPrefix(got, "installer: write "+recordPath+": ") || *during != notApplied {
		t.Errorf("take() = %v, want it stopped before the apply", err)
	}
}

func TestTakeKeepsAStepStartedWhenItsApplyFails(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	running, _ := runOn(t, server, Record{})
	planned, _ := markedDuring(t, server, "packages", errors.New("apt-get failed"))

	err := running.take(t.Context(), planned)

	wantRefused(t, err, "apt-get failed")
	if got := marks(t, server)["packages"]; got != "started" {
		t.Errorf("the record marks the step %q after a failed apply, want started", got)
	}
}

func TestTakeGoesOnPastAStepItsApplyLeavesOpen(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	running, _ := runOn(t, server, Record{})
	planned, _ := markedDuring(t, server, "https", errLeftOpen)

	err := running.take(t.Context(), planned)

	if got := marks(t, server)["https"]; err != nil || got != "started" {
		t.Errorf("take() = %v, the record marking the step %q, want nil and the step left started", err, got)
	}
}

func TestRunPrintsAndTakesEveryStepInOrder(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	running, answer := runOn(t, server, Record{})
	user, userDuring := markedDuring(t, server, "user", nil)
	database, databaseDuring := markedDuring(t, server, "database", nil)

	err := running.run(t.Context(), []step{user, database})

	if err != nil || answer.String() != "Step 1: take the step user\nStep 2: take the step database\n" {
		t.Errorf("run() = %v, printing %q, want both steps printed", err, answer.String())
	}
	if during := []string{*userDuring, *databaseDuring}; !slices.Equal(during, []string{"started", "started"}) {
		t.Errorf("the record marked the steps %q during their applies, want both started", during)
	}
	if got, want := marks(t, server), map[string]string{"user": "ended", "database": "ended"}; !maps.Equal(got, want) {
		t.Errorf("the record marks %v, want %v", got, want)
	}
}

func TestRunStopsAtTheFirstStepThatFails(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	running, answer := runOn(t, server, Record{})
	user, _ := markedDuring(t, server, "user", errors.New("useradd failed"))
	database, databaseDuring := markedDuring(t, server, "database", nil)

	err := running.run(t.Context(), []step{user, database})

	wantRefused(t, err, "useradd failed")
	if *databaseDuring != notApplied || answer.String() != "Step 1: take the step user\n" {
		t.Errorf("run() printed %q and took the next step, want it stopped at the first", answer.String())
	}
}

func TestRunReturnsWhatStopsItsAnswer(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	running, _ := runOn(t, server, Record{})
	running.call.Stdout = refusingWriter{}
	user, during := markedDuring(t, server, "user", nil)

	err := running.run(t.Context(), []step{user})

	wantRefused(t, err, "the terminal is gone")
	if *during != notApplied {
		t.Error("run() took a step it could not print, want none taken")
	}
}

func TestSaveRefusesAnInstallerFolderThatIsALink(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	server.folder("/var/lib/other")
	server.link(installerFolder, "/var/lib/other")
	running, _ := runOn(t, server, Record{})

	err := running.save()

	wantRefused(t, err, "installer: make the folder var/lib/gophenberg-installer: it is not a real folder")
}

func TestSaveRefusesARecordItCannotWrite(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	server.folder(recordPath + "/kept")
	running, _ := runOn(t, server, Record{})

	err := running.save()

	if got := fmt.Sprint(err); !strings.HasPrefix(got, "installer: write "+recordPath+": renameat ") {
		t.Errorf("save() = %v, want the record refused", err)
	}
}

func TestInstallingOnRefusesAServerItCannotOpen(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	server.root = filepath.Join(server.root, "missing")

	_, err := installingOn(server.check(), site{}, gonsole.Call{})

	wantRefused(t, err, "installer: open "+server.root+": no such file or directory")
}

func TestInstallingOnKeepsTheRecordItFoundApartFromTheOneItWrites(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	found := Record{Steps: map[string]string{"server-env": "ended"}, Installed: []string{piecePostgreSQL}}
	running, _ := runOn(t, server, found)

	running.record.Steps["packages"] = "started"
	running.record.Installed[0] = pieceCaddy

	if _, marked := found.Steps["packages"]; marked || found.Installed[0] != piecePostgreSQL {
		t.Errorf("the record found became %+v, want it as it was found", found)
	}
}

func TestCloseClosesTheInstallTreeOnceTheRunOpenedIt(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	running, _ := runOn(t, server, Record{})
	tree, err := server.machine().tree("/opt")
	if err != nil {
		t.Fatalf("opening the install tree: %v", err)
	}
	running.tree = tree

	running.close()

	if _, err := tree.Stat("."); err == nil {
		t.Error("close() left the install tree open")
	}
}

func TestMakeFolderRefusesIdsItMayNotGive(t *testing.T) {
	t.Parallel()

	skipAsRoot(t)
	tree, _ := openTree(t)

	err := makeFolder(tree, "media", 0o750, &owner{uid: 0, gid: 0})

	if got := fmt.Sprint(err); !strings.HasPrefix(got, "make the folder media: chown ") {
		t.Errorf("makeFolder() = %v, want the chown refused", err)
	}
}
