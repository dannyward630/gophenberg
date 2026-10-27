// SPDX-License-Identifier: Apache-2.0

package installer

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/gopherium/gophenberg/internal/version"
)

// builtOn returns the run of a first run for example.com on server whose install root is made, with its own archive.
func builtOn(t *testing.T, server *fakeServer, ids *owner) *installing {
	t.Helper()
	running, _ := runOn(t, server, Record{})
	running.ids = ids
	if err := running.take(t.Context(), plannedStep(t, running, "install-root")); err != nil {
		t.Fatalf("take(install-root) = %v, want the install root made", err)
	}
	running.archive = strings.NewReader(releaseArchiveOf(t, version.Version(), serverPlatform))
	return running
}

// wantOwnBuild fails t unless server holds this installer's build, as its archive holds it, owned by ids.
func wantOwnBuild(t *testing.T, server *fakeServer, ids *owner) {
	t.Helper()
	wantOnly(t, server.at(ownBuild), "LICENSE", "NOTICE", "gophenberg", "release.json")
	for name, text := range map[string]string{"LICENSE": "Apache License\n", "gophenberg": "#!/bin/sh\n"} {
		wantText(t, server.at(ownBuild+"/"+name), text)
	}
	wantEntry(t, server.at(ownBuild), fs.ModeDir|0o755, ids)
	wantEntry(t, server.at(ownBuild+"/gophenberg"), 0o755, ids)
	wantEntry(t, server.at(ownBuild+"/release.json"), 0o644, ids)
	target, err := os.Readlink(server.at("/opt/gophenberg/current"))
	if err != nil || target != "versions/"+version.Version() {
		t.Errorf("the current link names %q, %v, want versions/%s", target, err, version.Version())
	}
	wantEntry(t, server.at("/opt/gophenberg/current"), fs.ModeSymlink|0o777, ids)
}

func TestTheBuildStepUnpacksTheArchiveAndPointsCurrentAtItAsGophenbergs(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	running := builtOn(t, server, otherGroupIDs(t))

	err := running.take(t.Context(), plannedStep(t, running, "build"))

	if err != nil {
		t.Fatalf("take(build) = %v, want the build unpacked", err)
	}
	wantOwnBuild(t, server, running.ids)
}

func TestTheBuildStepRemovesTheBuildACutOffRunLeftFirst(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	running := builtOn(t, server, testIDs())
	server.write(ownBuild+"/gophenberg", "#!/bin/sh\n# cut off while it was written\n")
	server.folder(ownBuild + "/LICENSES")

	err := running.take(t.Context(), plannedStep(t, running, "build"))

	if err != nil {
		t.Fatalf("take(build) = %v, want the build unpacked again", err)
	}
	wantOwnBuild(t, server, nil)
}

func TestTheBuildStepReadsTheArchiveFromItsStartAfterTheCheckReadItsReleaseJSON(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	running := builtOn(t, server, testIDs())
	if _, err := io.CopyN(io.Discard, running.archive, 64); err != nil {
		t.Fatalf("reading into the archive: %v", err)
	}

	err := running.take(t.Context(), plannedStep(t, running, "build"))

	if err != nil {
		t.Fatalf("take(build) = %v, want the build unpacked from the start of the archive", err)
	}
	wantOwnBuild(t, server, nil)
}

func TestTheBuildStepKeepsTheBuildCurrentNames(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	server.pointCurrent()
	running := builtOn(t, server, testIDs())
	running.site.current = true
	running.archive = unseekable{Reader: running.archive}

	err := running.take(t.Context(), plannedStep(t, running, "build"))

	target, readErr := os.Readlink(server.at("/opt/gophenberg/current"))
	if err := errors.Join(err, readErr); err != nil || target != "versions/"+earlierBuild {
		t.Errorf("take(build) = %v, current naming %q, want the build %s kept", err, target, earlierBuild)
	}
	wantOnly(t, server.at("/opt/gophenberg/versions/"+earlierBuild), "gophenberg")
}

func TestTheBuildStepStopsOnAnArchiveItCannotUnpackAndLeavesNoBuildFolder(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	running := builtOn(t, server, testIDs())
	running.archive = strings.NewReader("a release note, which is no gzip stream")

	err := running.take(t.Context(), plannedStep(t, running, "build"))

	wantRefused(t, err, "installer: unpack the build "+version.Version()+": updater: refused the archive, which is "+
		"not gzip: gzip: invalid header")
	wantOnly(t, server.at("/opt/gophenberg/versions"))
}

// unseekable is an archive that cannot go back to its start.
type unseekable struct {
	io.Reader
}

// Seek refuses to move.
func (unseekable) Seek(int64, int) (int64, error) {
	return 0, errors.New("the archive cannot go back")
}

func TestTheBuildStepStopsWhenItCannotGoBackToTheStartOfTheArchive(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	running := builtOn(t, server, testIDs())
	running.archive = unseekable{Reader: running.archive}

	err := running.take(t.Context(), plannedStep(t, running, "build"))

	wantRefused(t, err, "installer: unpack the build "+version.Version()+": the archive cannot go back")
}

func TestTheBuildStepStopsWhenItCannotGiveTheBuildToGophenberg(t *testing.T) {
	t.Parallel()

	skipAsRoot(t)
	server := newFakeServer(t)
	running := builtOn(t, server, testIDs())
	running.ids = &owner{uid: 0, gid: 0}

	err := running.take(t.Context(), plannedStep(t, running, "build"))

	if got := fmt.Sprint(err); !strings.HasPrefix(got, "installer: unpack the build "+version.Version()+": lchownat ") {
		t.Errorf("take(build) = %v, want the chown refused", err)
	}
	if server.machine().there("/opt/gophenberg/current") {
		t.Error("take(build) pointed current at a build it could not give to gophenberg")
	}
}

func TestTheInstallerCopyStepCopiesTheRunningInstallerAsRootsAt0755(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	server.write(fakeInstaller, "#!/bin/sh\n# the installer this run runs\n")
	running, _ := runOn(t, server, Record{})

	err := running.take(t.Context(), plannedStep(t, running, "installer-copy"))

	if err != nil {
		t.Fatalf("take(installer-copy) = %v, want the copy written", err)
	}
	wantText(t, server.at(installerCopy), "#!/bin/sh\n# the installer this run runs\n")
	wantEntry(t, server.at(installerCopy), 0o755, nil)
	wantEntry(t, server.at(filepath.Dir(installerCopy)), fs.ModeDir|0o755, nil)
}

func TestTheInstallerCopyStepLeavesTheCopyAloneWhenTheRunRunsIt(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	server.program(installerCopy)
	server.folder("/usr/local/bin")
	if err := os.Link(server.at(installerCopy), server.at("/usr/local/bin/other-name")); err != nil {
		t.Fatalf("linking the copy under another name: %v", err)
	}
	before, err := os.Stat(server.at(installerCopy))
	if err != nil {
		t.Fatalf("describing the copy: %v", err)
	}
	for _, running := range []string{installerCopy, "/usr/local/bin/other-name"} {
		run, _ := runOn(t, server, Record{})
		run.machine.Executable = func() (string, error) { return server.at(running), nil }

		err := run.take(t.Context(), plannedStep(t, run, "installer-copy"))

		after, statErr := os.Stat(server.at(installerCopy))
		if err := errors.Join(err, statErr); err != nil || !os.SameFile(before, after) {
			t.Errorf("take(installer-copy) from %s = %v, want the copy left as it is", running, err)
		}
	}
}

func TestTheInstallerCopyStepStopsWhenItCannotFindTheRunningInstaller(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	running, _ := runOn(t, server, Record{})
	running.machine.Executable = func() (string, error) { return "", syscall.ENOENT }

	err := running.take(t.Context(), plannedStep(t, running, "installer-copy"))

	wantRefused(t, err, "installer: find the running installer: no such file or directory")
}

func TestTheInstallerCopyStepStopsWhenItCannotReadTheRunningInstaller(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	server.program(installerCopy)
	server.remove(fakeInstaller)
	running, _ := runOn(t, server, Record{})

	err := running.take(t.Context(), plannedStep(t, running, "installer-copy"))

	wantRefused(t, err, "installer: read the running installer: open "+server.at(fakeInstaller)+
		": no such file or directory")
}
