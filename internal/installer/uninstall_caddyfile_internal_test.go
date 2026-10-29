// SPDX-License-Identifier: Apache-2.0

package installer

import (
	"os"
	"slices"
	"strings"
	"testing"
)

// siteAddedMidRun is a site a person adds to the Caddyfile while server:uninstall runs.
const siteAddedMidRun = "\nexample.net {\n}\n"

func TestUninstallKeepsWhatThePersonAddsToTheCaddyfileWhileTheRunGoesOn(t *testing.T) {
	t.Parallel()

	server := firstInstalled(t)
	server.write(caddyfilePath, wantedCaddyfile+personSite)
	answering := server.effects["systemctl"]
	server.effects["systemctl"] = func(program Program) answer {
		if lineOf(program) == "systemctl disable --now gophenberg.service" {
			held, err := os.ReadFile(server.at(caddyfilePath))
			if err != nil || !strings.Contains(string(held), caddyImport+"\n") {
				t.Errorf("the Caddyfile reads %q, %v while the site stops, want the import line still in it", held, err)
			}
			server.write(caddyfilePath, string(held)+siteAddedMidRun)
		}
		return answering(program)
	}

	_, _, err := uninstallOn(server, nil, true, false)

	if err != nil {
		t.Fatalf("Uninstall() = %v, want the import line taken out", err)
	}
	wantText(t, server.at(caddyfilePath),
		strings.Replace(wantedCaddyfile, caddyImport+"\n", "", 1)+personSite+siteAddedMidRun)
}

// wantedLostImport is the stop of a later server:install whose Caddyfile lacks the import line.
const wantedLostImport = `/etc/caddy/Caddyfile lacks the line "import /etc/caddy/gophenberg.caddy" that an earlier ` +
	"run wrote, which has Caddy serve the site, and it must go back in before server:install runs again, so nothing " +
	"was changed"

// caddyTurns are the lines that have Caddy take its files, by a reload or a restart.
var caddyTurns = []string{"systemctl reload caddy.service", "systemctl restart caddy.service"}

// failCaddyTurns has every reload and restart of Caddy on server fail, as on a Caddyfile Caddy cannot take.
func failCaddyTurns(server *fakeServer) {
	answering := server.effects["systemctl"]
	server.effects["systemctl"] = func(program Program) answer {
		if slices.Contains(caddyTurns, lineOf(program)) {
			return failed("systemctl", "Job for caddy.service failed.\n")
		}
		return answering(program)
	}
}

// wantReinstallStopsAtTheImportLine fails t unless a later server:install on server stops at the lost import line.
func wantReinstallStopsAtTheImportLine(t *testing.T, server *fakeServer) {
	t.Helper()
	server.answerRemovals()
	server.forgetRun()

	_, _, err := runInstall(server, Request{Archive: archivePath})

	wantRefused(t, err, wantedLostImport)
}

func TestUninstallSavesTheSumOfACaddyfileThePersonChangedAfterACutOffRunBeforeCaddyTakesIt(t *testing.T) {
	t.Parallel()

	server := firstInstalled(t)
	cutOffInCaddyOn(t, server)
	server.write(caddyfilePath, wantedCaddyfile+personSite)
	want := recordOf(t, server)
	want.Caddyfile = caddyfileSum(wantedCaddyfile)
	failCaddyTurns(server)

	_, _, err := uninstallOn(server, nil, true, false)

	wantRefused(t, err, "installer: run systemctl: exit status 1, after it printed:\nJob for caddy.service failed.")
	if got := recordOf(t, server); !recordsEqual(got, want) {
		t.Errorf("the failed run left the record %+v, want the one found with the sum %+v", got, want)
	}
	kept := strings.Replace(wantedCaddyfile, caddyImport+"\n", "", 1) + personSite
	wantText(t, server.at(caddyfilePath), kept)
	wantReinstallStopsAtTheImportLine(t, server)
	wantText(t, server.at(caddyfilePath), kept)
}

func TestUninstallSavesTheSumOfACaddyfileThePersonChangedAfterACutOffRunBeforeItsImportLineComesOut(t *testing.T) {
	t.Parallel()

	server := firstInstalled(t)
	cutOffInCaddyOn(t, server)
	server.write(caddyfilePath, wantedCaddyfile+personSite)
	answering := server.effects["systemctl"]
	server.effects["systemctl"] = func(program Program) answer {
		if lineOf(program) == "systemctl daemon-reload" {
			server.remove(caddyfilePath)
		}
		return answering(program)
	}

	_, _, err := uninstallOn(server, nil, true, false)

	wantRefused(t, err, "installer: openat etc/caddy/Caddyfile: no such file or directory")
	if got := recordOf(t, server).Caddyfile; got != caddyfileSum(wantedCaddyfile) {
		t.Errorf("the run that stopped at the Caddyfile left the sum %q, want %q", got, caddyfileSum(wantedCaddyfile))
	}
}

func TestUninstallKeepsTheSumWithACaddyfileThePersonWroteAfterACutOffRunSoAReinstallLeavesItAlone(t *testing.T) {
	t.Parallel()

	server := firstInstalled(t)
	cutOffInCaddyOn(t, server)
	server.write(caddyfilePath, ownCaddyfile)

	stdout, _, err := uninstallOn(server, nil, true, false)

	want := slices.Concat(removedPrograms[:3], []string{"systemctl restart caddy.service"})
	if err != nil || !slices.Equal(server.ran, want) {
		t.Errorf("Uninstall() = %v, running %q, want %q", err, server.ran, want)
	}
	if !strings.Contains(stdout, wantedKeptOwnCaddyfile) {
		t.Errorf("Uninstall() printed\n%s\nwant it to hold%s", stdout, wantedKeptOwnCaddyfile)
	}
	wantKeptCaddy(t, server, caddyfileSum(wantedCaddyfile))
	wantReinstallStopsAtTheImportLine(t, server)
	wantText(t, server.at(caddyfilePath), ownCaddyfile)
}

func TestUninstallRestartsACaddyAFailedRestartLeftStoppedOnACaddyfileThePersonChanged(t *testing.T) {
	t.Parallel()

	server := firstInstalled(t)
	cutOffInCaddyOn(t, server)
	record := recordOf(t, server)
	record.Caddyfile = caddyfileSum(wantedCaddyfile)
	server.saveRecord(record)
	server.running[caddyUnit] = false
	server.write(caddyfilePath, wantedCaddyfile+personSite)

	_, _, err := uninstallOn(server, nil, true, false)

	want := slices.Concat(removedPrograms[:3], []string{"systemctl restart caddy.service"})
	if err != nil || !slices.Equal(server.ran, want) {
		t.Errorf("Uninstall() = %v, running %q, want %q", err, server.ran, want)
	}
	if !server.running[caddyUnit] {
		t.Errorf("Caddy is stopped, want it started on the Caddyfile the person changed")
	}
	wantKeptCaddy(t, server, caddyfileSum(wantedCaddyfile))
}

// wantedLinkedCaddyfile is the stop of a run whose Caddyfile is a link while its import line has to come out.
const wantedLinkedCaddyfile = "/etc/caddy/Caddyfile is a link, and the line \"import /etc/caddy/gophenberg.caddy\" " +
	"must come out of the file it leads to before server:uninstall runs again, so nothing was changed"

// linkCaddyfileOn writes text to main.caddy beside the Caddyfile of server and puts a link to it in its place.
func linkCaddyfileOn(t *testing.T, server *fakeServer, text string) {
	t.Helper()
	server.remove(caddyfilePath)
	server.write("/etc/caddy/main.caddy", text)
	if err := os.Symlink("main.caddy", server.at(caddyfilePath)); err != nil {
		t.Fatalf("linking the Caddyfile: %v", err)
	}
}

func TestUninstallStopsBeforeAnyChangeWhereACaddyfileThePersonChangedIsALink(t *testing.T) {
	t.Parallel()

	server := firstInstalled(t)
	linkCaddyfileOn(t, server, wantedCaddyfile+personSite)
	before := server.snapshot()

	stdout, _, err := uninstallOn(server, nil, true, false)

	wantRefused(t, err, wantedLinkedCaddyfile)
	if stdout != "" {
		t.Errorf("Uninstall() printed %q, want nothing", stdout)
	}
	wantUnchanged(t, server, before)
}

func TestUninstallLeavesALinkedCaddyfileWithoutTheImportLineALink(t *testing.T) {
	t.Parallel()

	server := firstInstalled(t)
	linkCaddyfileOn(t, server, ownCaddyfile)

	_, _, err := uninstallOn(server, nil, true, false)

	if err != nil {
		t.Fatalf("Uninstall() = %v, want the run through", err)
	}
	if target, err := os.Readlink(server.at(caddyfilePath)); err != nil || target != "main.caddy" {
		t.Errorf("the Caddyfile reads as a link to %q, %v, want the link to main.caddy kept", target, err)
	}
	wantText(t, server.at("/etc/caddy/main.caddy"), ownCaddyfile)
}
