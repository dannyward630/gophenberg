// SPDX-License-Identifier: Apache-2.0

package installer

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/gopherium/gophenberg/internal/updater"
)

// aptLines are the programs the packages step of a first run with Caddy runs, in order.
var aptLines = []string{
	`dpkg-query -W -f ${db:Status-Abbrev}\n postgresql caddy`,
	"apt-get -o DPkg::Lock::Timeout=300 update",
	"apt-get -o DPkg::Lock::Timeout=300 install -y --no-install-recommends postgresql caddy",
}

// useraddLine is the useradd line the user step runs for the default install root.
const useraddLine = "useradd --system --user-group --home-dir /opt/gophenberg --no-create-home --shell " +
	"/usr/sbin/nologin gophenberg"

// databaseLines are the programs the database step runs as postgres, in order.
var databaseLines = []string{
	"runuser -u postgres -- createuser --no-superuser --no-createdb --no-createrole gophenberg",
	"runuser -u postgres -- createdb --owner=gophenberg --encoding=UTF8 --template=template0 --locale-provider=icu " +
		"--icu-locale=und gophenberg",
}

// plannedStep returns the step called id of the steps the run running takes.
func plannedStep(t *testing.T, running *installing, id string) step {
	t.Helper()
	for _, planned := range running.site.steps() {
		if planned.id == id {
			return planned
		}
	}
	t.Fatalf("the run takes no step %s", id)
	return step{}
}

// otherGroupIDs returns the test's own user with another group it belongs to, so a chown shows, or skips t.
func otherGroupIDs(t *testing.T) *owner {
	t.Helper()
	group := siteGroup(t)
	if group == os.Getgid() {
		t.Skip("the test's user belongs to one group alone, so no chown would show")
	}
	return &owner{uid: os.Getuid(), gid: group}
}

// wantRan fails t unless server ran the program lines want, in order, and nothing else.
func wantRan(t *testing.T, server *fakeServer, want ...string) {
	t.Helper()
	if !slices.Equal(server.ran, want) {
		t.Errorf("the run ran\n%s\nwant\n%s", strings.Join(server.ran, "\n"), strings.Join(want, "\n"))
	}
}

func TestTheServerEnvStepWritesTheServerSettingsAsRootsAlone(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	running, _ := runOn(t, server, Record{})

	err := running.take(t.Context(), plannedStep(t, running, "server-env"))

	if err != nil {
		t.Fatalf("take(server-env) = %v, want server.env written", err)
	}
	wantText(t, server.at(serverEnvPath), firstServerEnv)
	wantEntry(t, server.at(serverEnvPath), 0o600, nil)
	wantEntry(t, server.at("/etc/gophenberg"), fs.ModeDir|0o755, nil)
	if got := marks(t, server)["server-env"]; got != "ended" {
		t.Errorf("the record marks the step %q, want ended", got)
	}
}

func TestTheServerEnvStepStopsWhenItCannotWriteTheServerSettings(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	server.write("/etc/gophenberg", "a file where the folder goes\n")
	running, _ := runOn(t, server, Record{})

	err := running.take(t.Context(), plannedStep(t, running, "server-env"))

	if got := fmt.Sprint(err); !strings.HasPrefix(got, "installer: write "+serverEnvPath+": ") {
		t.Errorf("take(server-env) = %v, want the write refused", err)
	}
}

func TestThePackagesStepWritesTheAptFilesAndOwnsThePackagesBeforeAptGetStarts(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	running, _ := runOn(t, server, Record{})
	var owned []string
	var sourced bool
	server.effects["apt-get"] = func(program Program) answer {
		if program.Args[len(program.Args)-1] == "update" {
			record, _ := readRecord(server.machine())
			owned, sourced = record.Installed, server.machine().there("/etc/apt/sources.list.d/caddy.sources")
		}
		return server.aptGet(program)
	}

	err := running.take(t.Context(), plannedStep(t, running, "packages"))

	if err != nil {
		t.Fatalf("take(packages) = %v, want PostgreSQL and Caddy installed", err)
	}
	wantRan(t, server, aptLines...)
	if want := []string{piecePostgreSQL, pieceCaddy}; !slices.Equal(owned, want) || !sourced {
		t.Errorf("apt-get update found the record owning %q, Caddy's source written %t, want %q and true", owned,
			sourced, want)
	}
	for _, written := range aptFilesOf()[:3] {
		wantText(t, server.at(written.path), written.text)
		wantEntry(t, server.at(written.path), 0o644, nil)
	}
}

func TestThePackagesStepRunsEveryProgramWithoutQuestionsAndRestartsServicesOnItsOwn(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	running, _ := runOn(t, server, Record{})

	err := running.take(t.Context(), plannedStep(t, running, "packages"))

	if err != nil {
		t.Fatalf("take(packages) = %v, want the packages installed", err)
	}
	for _, program := range server.runs {
		if want := []string{"DEBIAN_FRONTEND=noninteractive", "NEEDRESTART_MODE=a"}; !slices.Equal(program.Env, want) {
			t.Errorf("%s ran with the settings %q, want %q", lineOf(program), program.Env, want)
		}
	}
}

func TestThePackagesStepWaitsOnTheAptLockAsLongAsTheSettingsSay(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	running, _ := runOn(t, server, Record{})
	running.site.settings.Checks.AptLockWait = 90 * time.Second

	err := running.take(t.Context(), plannedStep(t, running, "packages"))

	if err != nil || !slices.Contains(server.ran, "apt-get -o DPkg::Lock::Timeout=90 update") {
		t.Errorf("take(packages) = %v, running\n%s\nwant apt-get to wait 90 seconds", err, strings.Join(server.ran, "\n"))
	}
}

func TestThePackagesStepWithTheirOwnWebServerInstallsPostgreSQLAlone(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	running, _ := runOn(t, server, Record{})
	running.site = ownSite(t, "example.com")

	err := running.take(t.Context(), plannedStep(t, running, "packages"))

	if err != nil {
		t.Fatalf("take(packages) = %v, want PostgreSQL installed", err)
	}
	wantRan(t, server, `dpkg-query -W -f ${db:Status-Abbrev}\n postgresql`, "apt-get -o DPkg::Lock::Timeout=300 update",
		"apt-get -o DPkg::Lock::Timeout=300 install -y --no-install-recommends postgresql")
	if server.machine().there("/etc/apt/sources.list.d/caddy.sources") || server.machine().there("/usr/bin/caddy") {
		t.Error("take(packages) brought Caddy to a server that keeps its own web server")
	}
}

func TestThePackagesStepLeavesAptGetAloneOnceDpkgHasEveryPackageInstalledHeldOrNot(t *testing.T) {
	t.Parallel()

	for _, status := range []string{"ii", "hi"} {
		server := newFakeServer(t)
		server.dpkg[piecePostgreSQL], server.dpkg[pieceCaddy] = status, "ii"
		running, _ := runOn(t, server, Record{Installed: []string{piecePostgreSQL, pieceCaddy}})

		err := running.take(t.Context(), plannedStep(t, running, "packages"))

		if err != nil || !slices.Equal(server.ran, aptLines[:1]) {
			t.Errorf("take(packages) with PostgreSQL %q = %v, running %q, want dpkg-query alone", status, err,
				server.ran)
		}
		wantText(t, server.at(caddyKeyPath), caddyKey)
	}
}

func TestThePackagesStepInstallsAPackageDpkgListsWithoutHavingItCleanlyInstalled(t *testing.T) {
	t.Parallel()

	for _, status := range []string{"rc", "un", "iU", "iF", "iiR", "hiR"} {
		server := newFakeServer(t)
		server.dpkg[piecePostgreSQL], server.dpkg[pieceCaddy] = status, "ii"
		running, _ := runOn(t, server, Record{})

		err := running.take(t.Context(), plannedStep(t, running, "packages"))

		if err != nil || !slices.Equal(server.ran, aptLines) {
			t.Errorf("take(packages) with PostgreSQL %q = %v, running %q, want %q", status, err, server.ran, aptLines)
		}
	}
}

func TestThePackagesStepOfARunCutOffInItSetsUpWhatDpkgLeftFirst(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	server.dpkg[piecePostgreSQL] = "iF"
	found := Record{Steps: map[string]string{"server-env": "ended", "packages": "started"},
		Installed: []string{piecePostgreSQL, pieceCaddy}}
	running, _ := runOn(t, server, found)

	err := running.take(t.Context(), plannedStep(t, running, "packages"))

	if err != nil {
		t.Fatalf("take(packages) = %v, want the packages installed", err)
	}
	wantRan(t, server, slices.Concat([]string{"dpkg --configure -a"}, aptLines)...)
	if want := []string{piecePostgreSQL, pieceCaddy}; !slices.Equal(running.record.Installed, want) {
		t.Errorf("the record owns %q, want %q once each", running.record.Installed, want)
	}
}

func TestThePackagesStepStopsWithWhatAFailedProgramPrinted(t *testing.T) {
	t.Parallel()

	for _, failed := range []struct {
		name string
		cut  bool
		want string
	}{
		{"dpkg", true, "installer: run dpkg: exit status 2, after it printed:\ndpkg: error processing package"},
		{"apt-get", false, "installer: run apt-get: exit status 100, after it printed:\ndpkg: error processing package"},
	} {
		server := newFakeServer(t)
		server.effects[failed.name] = func(program Program) answer {
			return answer{printed: "dpkg: error processing package\n", err: fmt.Errorf("installer: run %s: exit "+
				"status %d", program.Name, map[string]int{"dpkg": 2, "apt-get": 100}[program.Name])}
		}
		found := Record{}
		if failed.cut {
			found.Steps = map[string]string{"packages": "started"}
		}
		running, _ := runOn(t, server, found)

		err := running.take(t.Context(), plannedStep(t, running, "packages"))

		wantRefused(t, err, failed.want)
	}
}

func TestThePackagesStepNamesAFailedProgramThatPrintedNothing(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	server.effects["apt-get"] = func(Program) answer {
		return answer{printed: " \n", err: errors.New("installer: run apt-get: signal: killed")}
	}
	running, _ := runOn(t, server, Record{})

	err := running.take(t.Context(), plannedStep(t, running, "packages"))

	wantRefused(t, err, "installer: run apt-get: signal: killed")
}

func TestThePackagesStepStopsBeforeAptGetWhenItCannotSaveTheRecord(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	running, _ := runOn(t, server, Record{})
	planned := plannedStep(t, running, "packages")
	server.folder(recordPath + "/kept")

	err := planned.apply(running, t.Context(), planned)

	if err == nil || slices.Contains(server.ran, aptLines[1]) {
		t.Errorf("the packages step = %v, running\n%s\nwant it stopped before apt-get", err,
			strings.Join(server.ran, "\n"))
	}
}

func TestThePackagesStepStopsWhenItCannotWriteTheAptFiles(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	server.write("/etc/apt/keyrings", "a file where the folder goes\n")
	running, _ := runOn(t, server, Record{})

	err := running.take(t.Context(), plannedStep(t, running, "packages"))

	if got := fmt.Sprint(err); !strings.HasPrefix(got, "installer: write "+caddyKeyPath+": ") || len(server.ran) != 0 {
		t.Errorf("take(packages) = %v, running %q, want the write refused before any program", err, server.ran)
	}
}

func TestTheUserStepMakesTheSystemUserGophenbergAndKeepsItsIDs(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	running, _ := runOn(t, server, Record{})

	err := running.take(t.Context(), plannedStep(t, running, "user"))

	if err != nil {
		t.Fatalf("take(user) = %v, want the user made", err)
	}
	if want := (owner{uid: os.Getuid(), gid: siteGroup(t)}); running.ids == nil || *running.ids != want {
		t.Errorf("take(user) kept the ids %v, want the ids %v of the user useradd made", running.ids, want)
	}
	wantRan(t, server, useraddLine)
}

func TestTheUserStepLeavesTheUserGophenbergAloneOnceItIsThere(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	server.users["gophenberg"] = &user.User{Username: "gophenberg", Uid: "998", Gid: "997", HomeDir: "/opt/gophenberg"}
	running, _ := runOn(t, server, Record{})

	err := running.take(t.Context(), plannedStep(t, running, "user"))

	if err != nil {
		t.Fatalf("take(user) = %v, want the user taken", err)
	}
	if running.ids == nil || *running.ids != (owner{uid: 998, gid: 997}) {
		t.Errorf("take(user) kept the ids %v, want 998:997", running.ids)
	}
	wantRan(t, server)
}

func TestTheUserStepStopsWhenUseraddFails(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	server.effects["useradd"] = func(Program) answer {
		return answer{printed: "useradd: cannot lock /etc/passwd, try again later.\n",
			err: errors.New("installer: run useradd: exit status 1")}
	}
	running, _ := runOn(t, server, Record{})

	err := running.take(t.Context(), plannedStep(t, running, "user"))

	wantRefused(t, err, "installer: run useradd: exit status 1, after it printed:\nuseradd: cannot lock /etc/passwd, "+
		"try again later.")
}

func TestTheUserStepStopsWhenItCannotLookUpTheUser(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	running, _ := runOn(t, server, Record{})
	running.machine.LookupUser = func(name string) (*user.User, error) {
		return nil, fmt.Errorf("user: lookup username %s: %w", name, syscall.EIO)
	}

	err := running.take(t.Context(), plannedStep(t, running, "user"))

	wantRefused(t, err, "installer: user: lookup username gophenberg: input/output error")
	wantRan(t, server)
}

func TestTheUserStepRefusesIDsThatAreNotNumbers(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	server.users["gophenberg"] = &user.User{Username: "gophenberg", Uid: "x", Gid: "997"}
	running, _ := runOn(t, server, Record{})

	err := running.take(t.Context(), plannedStep(t, running, "user"))

	wantRefused(t, err, `installer: the user gophenberg has ids that are not numbers: strconv.Atoi: parsing "x": `+
		"invalid syntax")
}

// rootFolderModes are the install root and its folders, each with its mode, named from the top of the server.
var rootFolderModes = map[string]fs.FileMode{
	"/opt/gophenberg": 0o750, "/opt/gophenberg/versions": 0o755, "/opt/gophenberg/downloads": 0o755,
	"/opt/gophenberg/state": 0o700, "/opt/gophenberg/backups": 0o700, "/opt/gophenberg/media": 0o750,
	"/opt/gophenberg/themes": 0o750,
}

func TestTheInstallRootStepMakesTheRootAndItsFoldersAsGophenbergs(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	running, _ := runOn(t, server, Record{})
	running.ids = otherGroupIDs(t)

	err := running.take(t.Context(), plannedStep(t, running, "install-root"))

	if err != nil {
		t.Fatalf("take(install-root) = %v, want the install root made", err)
	}
	for path, mode := range rootFolderModes {
		wantEntry(t, server.at(path), fs.ModeDir|mode, running.ids)
	}
	wantOnly(t, server.at("/opt/gophenberg"), "backups", "downloads", "media", "state", "themes", "versions")
	if running.tree == nil || running.tree.Name() != server.at("/opt/gophenberg") {
		t.Errorf("take(install-root) left the install tree %v, want it open on the install root", running.tree)
	}
}

func TestTheInstallRootStepOwnsTheRootInTheRecordBeforeItMakesIt(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	server.write("/opt/gophenberg", "a file where the install root goes\n")
	running, _ := runOn(t, server, Record{})
	running.ids = testIDs()

	err := running.take(t.Context(), plannedStep(t, running, "install-root"))

	wantRefused(t, err, "installer: make the install root /opt/gophenberg: make the folder opt/gophenberg: it is "+
		"not a real folder")
	if record, _ := readRecord(server.machine()); !record.owns("/opt/gophenberg") {
		t.Errorf("the record owns %q, want the install root among them", record.Installed)
	}
}

func TestTheInstallRootStepMakesTheMissingFoldersAboveTheRootAsRootsAt0755(t *testing.T) {
	ids := otherGroupIDs(t)
	kept := syscall.Umask(0o077)
	defer syscall.Umask(kept)
	server := newFakeServer(t)
	running, _ := runOn(t, server, Record{})
	running.site.settings.Root, running.ids = "/srv/sites/gophenberg", ids

	err := running.take(t.Context(), plannedStep(t, running, "install-root"))

	if err != nil {
		t.Fatalf("take(install-root) = %v, want the install root made", err)
	}
	wantEntry(t, server.at("/srv"), fs.ModeDir|0o755, nil)
	wantEntry(t, server.at("/srv/sites"), fs.ModeDir|0o755, nil)
	wantEntry(t, server.at("/srv/sites/gophenberg"), fs.ModeDir|0o750, ids)
	wantEntry(t, server.at("/srv/sites/gophenberg/state"), fs.ModeDir|0o700, ids)
}

func TestTheInstallRootStepGivesTheFoldersThereTheirModesAgain(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	server.folder("/opt/gophenberg/media")
	server.chmod("/opt/gophenberg/media", 0o777)
	server.write("/opt/gophenberg/media/big.mp4", "kept\n")
	running, _ := runOn(t, server, Record{Installed: []string{"/opt/gophenberg"}})
	running.ids = testIDs()

	err := running.take(t.Context(), plannedStep(t, running, "install-root"))

	if err != nil {
		t.Fatalf("take(install-root) = %v, want the folders taken", err)
	}
	wantEntry(t, server.at("/opt/gophenberg/media"), fs.ModeDir|0o750, nil)
	wantText(t, server.at("/opt/gophenberg/media/big.mp4"), "kept\n")
}

func TestTheInstallRootStepRefusesALinkAtAFolderOfTheRoot(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	server.folder("/opt/gophenberg", "/etc/ssh")
	server.chmod("/etc/ssh", 0o700)
	if err := os.Symlink(server.at("/etc/ssh"), server.at("/opt/gophenberg/media")); err != nil {
		t.Fatalf("linking media to /etc/ssh: %v", err)
	}
	running, _ := runOn(t, server, Record{})
	running.ids = testIDs()

	err := running.take(t.Context(), plannedStep(t, running, "install-root"))

	wantRefused(t, err, "installer: make the install root /opt/gophenberg: make the folder media: it is not a real "+
		"folder")
	wantEntry(t, server.at("/etc/ssh"), fs.ModeDir|0o700, nil)
}

func TestTheInstallRootStepStopsWhenItCannotSaveTheRecord(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	running, _ := runOn(t, server, Record{})
	planned := plannedStep(t, running, "install-root")
	server.folder(recordPath + "/kept")

	err := planned.apply(running, t.Context(), planned)

	if err == nil || server.machine().there("/opt/gophenberg") {
		t.Errorf("the install-root step = %v, want it stopped before the install root is made", err)
	}
}

func TestTheInstallRootFoldersTheUpdaterWritesAreTheOnesItNames(t *testing.T) {
	t.Parallel()

	layout := updater.Layout{Root: "/opt/gophenberg"}
	for _, path := range []string{
		filepath.Dir(layout.Build("0.20.0")), filepath.Dir(layout.Download("0.20.0")), layout.Backups(),
		filepath.Dir(layout.StateFile()),
	} {
		if !slices.ContainsFunc(rootFolders, func(folder rootFolder) bool { return folder.name == filepath.Base(path) }) {
			t.Errorf("the install root step makes no folder %s, which the updater writes", path)
		}
	}
}

func TestADryRunSaysTheInstallRootStepMakesTheMissingFoldersAboveTheRoot(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)

	stdout, _, err := runInstallUnder(server, map[string]string{"GOPHENBERG_INSTALL_ROOT": "/srv/sites/gophenberg"},
		firstRun)

	line := "\nStep 4: make the install root /srv/sites/gophenberg and its folders, owned by gophenberg, after the " +
		"missing /srv and /srv/sites above it, owned by root with mode 0755\n"
	if err != nil || !strings.Contains(stdout, line) {
		t.Errorf("Install() = %v, printing\n%s\nwant the line %q", err, stdout, line)
	}
}

func TestTheInstallRootStepNamesOneMissingFolderAboveTheRoot(t *testing.T) {
	t.Parallel()

	running := firstSite(t, "example.com", 255)
	running.missing = []string{"/srv"}
	running.settings.Root = "/srv/gophenberg"

	if got, want := running.installRootDoes(), "make the install root /srv/gophenberg and its folders, owned by "+
		"gophenberg, after the missing /srv above it, owned by root with mode 0755"; got != want {
		t.Errorf("the install-root step does %q, want %q", got, want)
	}
}

func TestTheDatabaseStepMakesTheRoleAndTheDatabaseAsPostgres(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	running, _ := runOn(t, server, Record{})

	err := running.take(t.Context(), plannedStep(t, running, "database"))

	if err != nil {
		t.Fatalf("take(database) = %v, want the role and the database made", err)
	}
	wantRan(t, server, databaseLines...)
}

func TestTheDatabaseStepTakesARoleOrADatabaseThatAlreadyExistsAsMade(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	for name, what := range map[string]string{"createuser": `role "gophenberg"`, "createdb": `database "gophenberg"`} {
		server.answers[name] = answer{printed: name + ": error: creation failed: ERROR:  " + what + " already exists\n",
			err: errors.New("installer: run " + name + ": exit status 1")}
	}
	running, _ := runOn(t, server, Record{})

	err := running.take(t.Context(), plannedStep(t, running, "database"))

	if err != nil {
		t.Fatalf("take(database) = %v, want what already exists taken as made", err)
	}
	wantRan(t, server, databaseLines...)
}

func TestTheDatabaseStepStopsWithWhatAFailedCommandPrinted(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	server.answers["createuser"] = answer{printed: "createuser: error: connection to server on socket failed\n",
		err: errors.New("installer: run createuser: exit status 1")}
	running, _ := runOn(t, server, Record{})

	err := running.take(t.Context(), plannedStep(t, running, "database"))

	wantRefused(t, err, "installer: run createuser: exit status 1, after it printed:\ncreateuser: error: connection to "+
		"server on socket failed")
	wantRan(t, server, databaseLines[0])
}

func TestTheDatabaseStepRunsNothingOnceTheRecordSaysAnEarlierRunEndedIt(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	running, _ := runOn(t, server, Record{Steps: map[string]string{"database": "ended"}})

	err := running.take(t.Context(), plannedStep(t, running, "database"))

	if err != nil {
		t.Fatalf("take(database) = %v, want nothing to do", err)
	}
	wantRan(t, server)
}

func TestOwnAddsAPieceToTheRecordOnce(t *testing.T) {
	t.Parallel()

	var record Record

	record.own(piecePostgreSQL)
	record.own(pieceCaddy)
	record.own(piecePostgreSQL)

	if want := []string{piecePostgreSQL, pieceCaddy}; !slices.Equal(record.Installed, want) {
		t.Errorf("the record owns %q, want %q", record.Installed, want)
	}
}
