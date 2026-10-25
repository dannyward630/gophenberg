// SPDX-License-Identifier: Apache-2.0

package updater_test

import (
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/gopherium/gophenberg/internal/updater"
)

func TestReachedDatabaseMarksAStartOfTheBuildAnUpdateSwitchedTo(t *testing.T) {
	t.Parallel()

	f := newGuardFixture(t, switchedTo)

	if err := f.started().ReachedDatabase(); err != nil {
		t.Fatalf("ReachedDatabase() = %v, want the start marked", err)
	}

	want := switchedTo
	want.Attempts, want.DatabaseReached = 1, true
	f.wantState(want)
}

func TestReachedDatabaseLeavesAStateThatNamesNoStartOfThisBuildAlone(t *testing.T) {
	t.Parallel()

	for name, state := range map[string]updater.State{
		"a kept update":             {Step: updater.StepCommitted, From: running, To: offered},
		"a switch to another build": {Step: updater.StepSwitched, From: running, To: "0.22.0"},
		"a job that stopped":        {Step: updater.StepBackup, From: running, To: offered},
		"an install never updated":  {},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := newGuardFixture(t, state)

			if err := f.started().ReachedDatabase(); err != nil {
				t.Fatalf("ReachedDatabase() = %v, want nil", err)
			}

			f.wantState(state)
		})
	}
}

func TestReachedDatabaseMarksNothingOnAnInstallWithNoRoot(t *testing.T) {
	t.Parallel()

	guard, err := updater.Start(settingsOf(t, map[string]string{}), offered, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("Start() with no install root = %v, want nil", err)
	}

	if err := guard.ReachedDatabase(); err != nil {
		t.Errorf("ReachedDatabase() with no install root = %v, want nil", err)
	}
}

func TestReachedDatabaseRefusesAStateItCannotReadOrWrite(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		spoil func(t *testing.T, layout updater.Layout)
		says  string
	}{
		"a state file it cannot read": {
			spoil: func(t *testing.T, layout updater.Layout) {
				t.Helper()
				if err := os.WriteFile(layout.StateFile(), []byte("step: switched"), 0o644); err != nil {
					t.Fatalf("writing the state file: %v", err)
				}
			},
			says: "updater: read the update state",
		},
		"a state folder it cannot write": {
			spoil: func(t *testing.T, layout updater.Layout) {
				t.Helper()
				denied(t, filepath.Dir(layout.StateFile()), 0o500)
			},
			says: "updater: write the update state",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := newGuardFixture(t, switchedTo)
			guard := f.started()
			tc.spoil(t, f.layout)

			err := guard.ReachedDatabase()

			wantRefused(t, err, tc.says)
		})
	}
}

func TestStartGoesBackWhenALongJumpFailsBeforeItsBuildReachedTheDatabase(t *testing.T) {
	t.Parallel()

	tried := switchedTo
	tried.Attempts, tried.LongJump, tried.Backup = 3, true, longJumpBackup
	f := newGuardFixture(t, tried)

	_, err := f.start()

	wantCheckFailed(t, err,
		"updater: Gophenberg 0.21.0 failed its start check after the update from 0.20.0: it tried to start 4 times",
		"updater: current names Gophenberg 0.20.0 again, which the next start runs")
	f.wantCurrent(running)
	want := tried
	want.Attempts, want.Refused = 4, []string{"0.19.5", offered}
	f.wantState(want)
}

func TestServeKeepsTheSiteDownWhenTheBuildOfALongJumpReachedTheDatabaseAndFailsItsCheck(t *testing.T) {
	t.Parallel()

	long := switchedTo
	long.LongJump, long.Backup = true, longJumpBackup
	f := newGuardFixture(t, long)
	guard := f.started()
	if err := guard.ReachedDatabase(); err != nil {
		t.Fatalf("ReachedDatabase() = %v, want the start marked", err)
	}

	err := served(t, guard, newTestSite(map[string]int{"/admin/": http.StatusInternalServerError}), passes)

	wantCheckFailed(t, err,
		"updater: Gophenberg 0.21.0 failed its start check after the update from 0.20.0: /admin/ answered 500, want 200",
		"so 0.20.0 cannot open the database and the site stays down")
	f.wantCurrent(offered)
	want := long
	want.Step, want.Attempts, want.Refused, want.DatabaseReached = updater.StepFailed, 1, []string{"0.19.5", offered}, true
	f.wantState(want)
}
