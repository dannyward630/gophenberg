// SPDX-License-Identifier: Apache-2.0

package installer

import (
	"io/fs"
	"os"
	"strings"
	"testing"
)

// emptiedFolder is a folder server:uninstall removes once it took the last entry, by that entry.
type emptiedFolder struct{ entry, folder string }

// emptiedFolders are the folders server:uninstall -delete-data empties on a server a first run for example.com set up.
var emptiedFolders = []emptiedFolder{
	{maintenanceFolder, "/usr/local/share/gophenberg"},
	{installerCopy, "/usr/local/lib/gophenberg"},
	{serverEnvPath, "/etc/gophenberg"},
}

// personNotes is what a file a person keeps in a folder of Gophenberg's holds.
const personNotes = "Maria Perez's notes\n"

// listedLine returns path as server:uninstall lists it, on a line of its own.
func listedLine(path string) string {
	return "\n  " + path + "\n"
}

// onDisable has server make change once the run disables the site, after the listing and before any removal.
func onDisable(server *fakeServer, change func()) {
	answering := server.effects["systemctl"]
	server.effects["systemctl"] = func(program Program) answer {
		if program.Args[0] == "disable" {
			change()
		}
		return answering(program)
	}
}

// wantFoldersListed fails t unless stdout lists each emptied folder in place of its entry.
func wantFoldersListed(t *testing.T, stdout string) {
	t.Helper()
	for _, emptied := range emptiedFolders {
		if !strings.Contains(stdout, listedLine(emptied.folder)) || strings.Contains(stdout, listedLine(emptied.entry)) {
			t.Errorf("Uninstall() printed\n%s\nwant %s listed in place of %s", stdout, emptied.folder, emptied.entry)
		}
	}
}

func TestEmptiedNamesTheFolderOfAPathWhenItIsARealFolderHoldingNothingElse(t *testing.T) {
	t.Parallel()

	for what, row := range map[string]struct {
		put  func(*fakeServer)
		want string
	}{
		"the path alone": {func(s *fakeServer) { s.write("/srv/kept/entry", personNotes) }, "/srv/kept"},
		"nothing at all": {func(s *fakeServer) { s.folder("/srv/kept") }, "/srv/kept"},
		"the path and another file": {func(s *fakeServer) {
			s.write("/srv/kept/entry", personNotes)
			s.write("/srv/kept/notes.txt", personNotes)
		}, "/srv/kept/entry"},
		"no folder": {func(*fakeServer) {}, "/srv/kept/entry"},
		"a link to a folder holding the path alone": {func(s *fakeServer) {
			s.write("/srv/real/entry", personNotes)
			s.link("/srv/kept", "/srv/real")
		}, "/srv/kept/entry"},
	} {
		server := newFakeServer(t)
		row.put(server)

		if got := server.machine().emptied("/srv/kept/entry"); got != row.want {
			t.Errorf("emptied() with %s = %q, want %q", what, got, row.want)
		}
	}
}

func TestEmptiedNamesThePathItselfInAFolderItCannotRead(t *testing.T) {
	t.Parallel()
	skipAsRoot(t)

	server := newFakeServer(t)
	server.write("/srv/kept/entry", personNotes)
	server.chmod("/srv/kept", 0o311)
	t.Cleanup(func() { server.chmod("/srv/kept", 0o755) })

	if got := server.machine().emptied("/srv/kept/entry"); got != "/srv/kept/entry" {
		t.Errorf("emptied() in a folder it cannot read = %q, want the path itself", got)
	}
}

func TestUninstallRemovesEachFolderItEmptiesAndListsItInPlaceOfItsLastEntry(t *testing.T) {
	t.Parallel()

	server := firstInstalled(t)

	stdout, _, err := uninstallOn(server, nil, true, true)

	if err != nil || !strings.HasSuffix(stdout, removedLine) {
		t.Fatalf("Uninstall() = %v, printing\n%s\nwant the run through to its last line", err, stdout)
	}
	wantFoldersListed(t, stdout)
	for _, emptied := range emptiedFolders {
		wantNothingAt(t, server, emptied.folder)
	}
	wantThere(t, server, "/usr/local/share", "/usr/local/lib", "/etc")
}

func TestUninstallKeepsAFolderThatHoldsAnotherEntryAndListsTheEntryAlone(t *testing.T) {
	t.Parallel()

	for _, emptied := range emptiedFolders {
		server := firstInstalled(t)
		notes := emptied.folder + "/notes.txt"
		server.write(notes, personNotes)

		stdout, _, err := uninstallOn(server, nil, true, true)

		if err != nil || !strings.Contains(stdout, listedLine(emptied.entry)) {
			t.Errorf("Uninstall() = %v, printing\n%s\nwant %s listed", err, stdout, emptied.entry)
		}
		if strings.Contains(stdout, listedLine(emptied.folder)) {
			t.Errorf("Uninstall() printed\n%s\nwant %s left out, since it holds a file of the person's", stdout,
				emptied.folder)
		}
		wantNothingAt(t, server, emptied.entry)
		wantText(t, server.at(notes), personNotes)
	}
}

func TestUninstallKeepsAFolderAFileWasAddedToBetweenTheListingAndTheRemoval(t *testing.T) {
	t.Parallel()

	server := firstInstalled(t)
	onDisable(server, func() {
		for _, emptied := range emptiedFolders {
			server.write(emptied.folder+"/notes.txt", personNotes)
		}
	})

	stdout, _, err := uninstallOn(server, nil, true, true)

	if err != nil || !strings.HasSuffix(stdout, removedLine) {
		t.Fatalf("Uninstall() = %v, printing\n%s\nwant the run through to its last line", err, stdout)
	}
	wantFoldersListed(t, stdout)
	for _, emptied := range emptiedFolders {
		wantNothingAt(t, server, emptied.entry)
		wantText(t, server.at(emptied.folder+"/notes.txt"), personNotes)
	}
}

func TestUninstallTakesAnEmptiedFolderAlreadyGoneAsRemoved(t *testing.T) {
	t.Parallel()

	server := firstInstalled(t)
	onDisable(server, func() {
		for _, emptied := range emptiedFolders {
			server.remove(emptied.folder)
		}
	})

	stdout, _, err := uninstallOn(server, nil, true, true)

	if err != nil || !strings.HasSuffix(stdout, removedLine) {
		t.Fatalf("Uninstall() = %v, printing\n%s\nwant the run through to its last line", err, stdout)
	}
	wantFoldersListed(t, stdout)
}

func TestUninstallRemovesTheEmptyFolderARunCutOffAfterItsLastEntryLeft(t *testing.T) {
	t.Parallel()

	server := firstInstalled(t)
	for _, emptied := range emptiedFolders {
		server.remove(emptied.entry)
	}

	listing, _, err := uninstallOn(server, nil, false, false)

	if _, kept, _ := strings.Cut(listing, "server:uninstall keeps:"); err != nil ||
		!strings.Contains(kept, listedLine("/etc/gophenberg")) {
		t.Errorf("Uninstall() = %v, printing\n%s\nwant /etc/gophenberg listed as kept", err, listing)
	}

	stdout, _, err := uninstallOn(server, nil, true, true)

	if err != nil || !strings.HasSuffix(stdout, removedLine) {
		t.Fatalf("Uninstall() = %v, printing\n%s\nwant the run through to its last line", err, stdout)
	}
	wantFoldersListed(t, stdout)
	for _, emptied := range emptiedFolders {
		wantNothingAt(t, server, emptied.folder)
	}
}

// linkFolder moves the folder at path to /srv/moved and leaves a link to it in its place, failing t when it cannot.
func linkFolder(t *testing.T, server *fakeServer, path string) {
	t.Helper()
	server.folder("/srv")
	if err := os.Rename(server.at(path), server.at("/srv/moved")); err != nil {
		t.Fatalf("moving %s: %v", path, err)
	}
	if err := os.Symlink("../../../srv/moved", server.at(path)); err != nil {
		t.Fatalf("linking %s: %v", path, err)
	}
}

func TestUninstallNeverRemovesAFolderThatIsALink(t *testing.T) {
	t.Parallel()

	for when, listed := range map[string]string{"before the run": installerCopy, "midway": "/usr/local/lib/gophenberg"} {
		server := firstInstalled(t)
		move := func() { linkFolder(t, server, "/usr/local/lib/gophenberg") }
		if when == "midway" {
			onDisable(server, move)
		} else {
			move()
		}

		stdout, _, err := uninstallOn(server, nil, true, false)

		if err != nil || !strings.Contains(stdout, listedLine(listed)) {
			t.Errorf("Uninstall() with a link made %s = %v, printing\n%s\nwant %s listed", when, err, stdout, listed)
		}
		info, err := os.Lstat(server.at("/usr/local/lib/gophenberg"))
		if err != nil || info.Mode()&fs.ModeSymlink == 0 {
			t.Errorf("the run left /usr/local/lib/gophenberg as %v, %v, want the link kept", info, err)
		}
		wantThere(t, server, "/srv/moved")
		wantNothingAt(t, server, "/srv/moved/gophenberg")
	}
}

func TestUninstallStopsWhereItCannotRemoveAnEmptiedFolder(t *testing.T) {
	t.Parallel()
	skipAsRoot(t)

	for locked, want := range map[string]string{
		"/usr/local/lib":            "installer: remove /usr/local/lib/gophenberg: ",
		"/usr/local/lib/gophenberg": "installer: remove /usr/local/lib/gophenberg/gophenberg: ",
	} {
		server := firstInstalled(t)
		server.chmod(locked, 0o555)
		t.Cleanup(func() { server.chmod(locked, 0o755) })

		_, _, err := uninstallOn(server, nil, true, false)

		if err == nil || !strings.HasPrefix(err.Error(), want) {
			t.Errorf("Uninstall() under a locked %s = %v, want %q", locked, err, want)
		}
		wantThere(t, server, "/usr/local/lib/gophenberg")
	}
}
