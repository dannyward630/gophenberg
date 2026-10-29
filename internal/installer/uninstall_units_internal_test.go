// SPDX-License-Identifier: Apache-2.0

package installer

import (
	"slices"
	"strings"
	"testing"
)

func TestUninstallTakesAResetFailedOfUnitsSystemdAlreadyLetGoOfAsDone(t *testing.T) {
	t.Parallel()

	server := firstInstalled(t)
	server.unloads = true

	stdout, _, err := uninstallOn(server, nil, true, false)

	if err != nil || stdout != wantedRemoves+wantedKeeps+removedLine {
		t.Fatalf("Uninstall() = %v, printing\n%s\nwant the run through to its last line", err, stdout)
	}
	if !slices.Equal(server.ran, removedPrograms) {
		t.Errorf("the run ran %q, want %q", server.ran, removedPrograms)
	}
	wantNothingAt(t, server, uninstalledPaths...)
}

// deniedReset is what systemctl reset-failed prints for a unit it may not clear.
const deniedReset = "Failed to reset failed state of unit gophenberg.service: Access denied\n"

func TestUninstallStopsWithWhatAResetFailedPrintedUnlessEachLineSaysTheUnitIsNotLoaded(t *testing.T) {
	t.Parallel()

	for _, row := range []struct{ printed, want string }{
		{deniedReset, "installer: run systemctl: exit status 1, after it printed:\n" +
			strings.TrimSuffix(deniedReset, "\n")},
		{notLoadedLines([]string{pieceService}) + deniedReset, "installer: run systemctl: exit status 1, after it " +
			"printed:\nFailed to reset failed state of unit gophenberg.service: Unit gophenberg.service not loaded.\n" +
			strings.TrimSuffix(deniedReset, "\n")},
		{"", "installer: run systemctl: exit status 1"},
	} {
		server := firstInstalled(t)
		answering := server.effects["systemctl"]
		server.effects["systemctl"] = func(program Program) answer {
			if program.Args[0] == "reset-failed" {
				return failed("systemctl", row.printed)
			}
			return answering(program)
		}

		stdout, _, err := uninstallOn(server, nil, true, false)

		wantRefused(t, err, row.want)
		if !slices.Equal(server.ran, removedPrograms[:2]) || strings.HasSuffix(stdout, removedLine) {
			t.Errorf("the run ran %q, printing\n%s\nwant it stopped at the reset-failed", server.ran, stdout)
		}
		wantThere(t, server, unitPaths...)
	}
}
