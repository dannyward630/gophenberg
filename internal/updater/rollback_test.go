// SPDX-License-Identifier: Apache-2.0

package updater_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gopherium/gophenberg/internal/updater"
)

// failedCheck is what the service manager hands its rollback unit after a start that failed its start check.
var failedCheck = updater.Failure{ServiceResult: "exit-code", ExitStatus: "76"}

// startLimitHit is what the service manager hands its rollback unit once the starts of a build hit the start limit.
var startLimitHit = updater.Failure{ServiceResult: "start-limit-hit", ExitStatus: "1"}

// takenBack is the state an update from 0.20.0 to 0.21.0 ends at once update:rollback takes it back.
var takenBack = updater.State{
	Step: updater.StepRolledBack, From: running, To: offered, Attempts: 1, Refused: []string{"0.19.5", offered},
}

// longJumped returns the state at step of a long jump from 0.20.0 to 0.21.0 whose build reached the database.
func longJumped(step updater.Step) updater.State {
	state := switchedTo
	state.Step, state.LongJump, state.Backup, state.Attempts = step, true, longJumpBackup, 1
	state.DatabaseReached = true
	return state
}

// stayedDown is the state an update from 0.20.0 to 0.21.0 that raised the minimum version ends at once it failed.
func stayedDown() updater.State {
	state := longJumped(updater.StepFailed)
	state.Refused = []string{"0.19.5", offered}
	return state
}

// wantTakenBack fails t unless the rollback answered the update taken back and the install root names 0.20.0 again.
func (f *guardFixture) wantTakenBack(state updater.State, err error) {
	f.t.Helper()
	if err != nil || !reflect.DeepEqual(state, takenBack) {
		f.t.Errorf("the rollback = %+v, %v, want %+v", state, err, takenBack)
	}
	f.wantCurrent(running)
	f.wantState(takenBack)
}

// wantLeftAlone fails t unless the rollback refused saying text, current names 0.21.0 and the state file holds kept.
func (f *guardFixture) wantLeftAlone(state updater.State, err error, kept updater.State, text string) {
	f.t.Helper()
	wantRefused(f.t, err, text)
	if !reflect.DeepEqual(state, updater.State{}) {
		f.t.Errorf("the rollback = %+v, want no state when it refuses", state)
	}
	f.wantCurrent(offered)
	f.wantState(kept)
}

func TestRollbackTakesBackAnUpdateThatSwitchedBuilds(t *testing.T) {
	t.Parallel()

	for _, step := range []updater.Step{updater.StepSwitched, updater.StepCommitted} {
		t.Run(string(step), func(t *testing.T) {
			t.Parallel()

			switched := switchedTo
			switched.Step, switched.Attempts = step, 1
			f := newGuardFixture(t, switched)

			state, err := updater.Rollback(settingsOf(t, f.env))

			f.wantTakenBack(state, err)
		})
	}
}

func TestRollbackRefusesAnInstallWithNoRoot(t *testing.T) {
	t.Parallel()

	for name, rollback := range map[string]func(updater.Settings) (updater.State, error){
		"update:rollback": updater.Rollback,
		"update:rollback -after-failure": func(settings updater.Settings) (updater.State, error) {
			return updater.RollbackAfterFailure(settings, failedCheck)
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := rollback(settingsOf(t, map[string]string{}))

			wantRefused(t, err, "updater: GOPHENBERG_INSTALL_ROOT is empty, so this install keeps no build to go "+
				"back to, go back by hand as https://docs.gophenberg.org/self-hosting/updates-and-backups/ shows")
		})
	}
}

func TestRollbackHasNothingToTakeBackWithoutAnUpdateThatSwitchedBuilds(t *testing.T) {
	t.Parallel()

	stopped := switchedTo
	stopped.Step = updater.StepBackup
	for name, tc := range map[string]struct {
		state updater.State
		says  string
	}{
		"an install never updated": {
			says: "updater: no update ran on this install root, so update:rollback has nothing to take back",
		},
		"an update taken back": {state: takenBack, says: "updater: the last update, from 0.20.0 to 0.21.0, stands " +
			"at the step rolled-back, so update:rollback has nothing to take back"},
		"an update that stopped before its switch": {state: stopped, says: "stands at the step backup"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := newGuardFixture(t, tc.state)

			state, err := updater.Rollback(settingsOf(t, f.env))

			f.wantLeftAlone(state, err, tc.state, tc.says)
		})
	}
}

func TestRollbackRefusesAnUpdateThatRaisedTheMinimumVersionAndNamesItsBackup(t *testing.T) {
	t.Parallel()

	for _, step := range []updater.Step{updater.StepSwitched, updater.StepCommitted, updater.StepFailed} {
		t.Run(string(step), func(t *testing.T) {
			t.Parallel()

			f := newGuardFixture(t, longJumped(step))

			state, err := updater.Rollback(settingsOf(t, f.env))

			f.wantLeftAlone(state, err, longJumped(step), "updater: Gophenberg 0.21.0 raised the minimum version "+
				"past 0.20.0, so 0.20.0 cannot open the database and update:rollback cannot go back to it")
			wantRefused(t, err, "updater: restore the backup taken before the update, "+
				filepath.Join(f.layout.Root, longJumpBackup)+", into an empty database, point current at "+
				"versions/0.20.0 and start Gophenberg again")
		})
	}
}

func TestRollbackTakesBackAnUpdateThatRaisedTheMinimumVersionBeforeItsBuildReachedTheDatabase(t *testing.T) {
	t.Parallel()

	for name, rollback := range map[string]func(updater.Settings) (updater.State, error){
		"update:rollback": updater.Rollback,
		"update:rollback -after-failure": func(settings updater.Settings) (updater.State, error) {
			return updater.RollbackAfterFailure(settings, startLimitHit)
		},
	} {
		for attempts, started := range map[int]string{0: "never started", 3: "started three times"} {
			t.Run(name+", a build that "+started, func(t *testing.T) {
				t.Parallel()

				unreached := longJumped(updater.StepSwitched)
				unreached.Attempts, unreached.DatabaseReached = attempts, false
				f := newGuardFixture(t, unreached)

				state, err := rollback(settingsOf(t, f.env))

				want := unreached
				want.Step, want.Refused = updater.StepRolledBack, []string{"0.19.5", offered}
				if err != nil || !reflect.DeepEqual(state, want) {
					t.Errorf("the rollback = %+v, %v, want %+v", state, err, want)
				}
				f.wantCurrent(running)
				f.wantState(want)
			})
		}
	}
}

func TestRollbackRefusesAStateFileItCannotRead(t *testing.T) {
	t.Parallel()

	f := newGuardFixture(t, switchedTo)
	if err := os.WriteFile(f.layout.StateFile(), []byte("step: switched"), 0o644); err != nil {
		t.Fatalf("writing the state file: %v", err)
	}

	_, err := updater.Rollback(settingsOf(t, f.env))

	wantRefused(t, err, "updater: read the update state "+f.layout.StateFile())
	f.wantCurrent(offered)
}

func TestRollbackKeepsTheUpdateWhenItCannotPointCurrent(t *testing.T) {
	t.Parallel()

	f := newGuardFixture(t, switchedTo)
	if err := os.Mkdir(filepath.Join(f.layout.Root, ".current.partial"), 0o755); err != nil {
		t.Fatalf("blocking the new current link: %v", err)
	}
	if err := os.WriteFile(filepath.Join(f.layout.Root, ".current.partial", "kept"), nil, 0o644); err != nil {
		t.Fatalf("filling the folder that blocks the new current link: %v", err)
	}

	state, err := updater.Rollback(settingsOf(t, f.env))

	f.wantLeftAlone(state, err, switchedTo, "updater: point current at 0.20.0")
}

func TestRollbackReportsAnUpdateItTookBackButCouldNotWriteDown(t *testing.T) {
	t.Parallel()

	f := newGuardFixture(t, switchedTo)
	denied(t, filepath.Dir(f.layout.StateFile()), 0o500)

	_, err := updater.Rollback(settingsOf(t, f.env))

	wantRefused(t, err, "updater: write the update state")
	f.wantCurrent(running)
}

func TestRollbackAfterFailureTakesBackAStartThatFailed(t *testing.T) {
	t.Parallel()

	for name, failure := range map[string]updater.Failure{
		"a start that failed its check": failedCheck,
		"starts that hit the limit":     startLimitHit,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			switched := switchedTo
			switched.Attempts = 1
			f := newGuardFixture(t, switched)

			state, err := updater.RollbackAfterFailure(settingsOf(t, f.env), failure)

			f.wantTakenBack(state, err)
		})
	}
}

func TestRollbackAfterFailureFinishesWhatTheBootGuardStarted(t *testing.T) {
	t.Parallel()

	wentBack := switchedTo
	wentBack.Attempts, wentBack.Refused = 1, []string{"0.19.5", offered}
	f := newGuardFixture(t, wentBack)
	pointLink(t, f.layout, updater.Current, running)

	state, err := updater.RollbackAfterFailure(settingsOf(t, f.env), failedCheck)

	f.wantTakenBack(state, err)
}

func TestRollbackAfterFailureTakesBackEachUpdateOnce(t *testing.T) {
	t.Parallel()

	switched := switchedTo
	switched.Attempts = 1
	f := newGuardFixture(t, switched)
	if _, err := updater.RollbackAfterFailure(settingsOf(t, f.env), failedCheck); err != nil {
		t.Fatalf("the first rollback = %v, want the update taken back", err)
	}
	pointLink(t, f.layout, updater.Current, offered)

	state, err := updater.RollbackAfterFailure(settingsOf(t, f.env), failedCheck)

	f.wantLeftAlone(state, err, takenBack, "updater: the last update, from 0.20.0 to 0.21.0, stands at the step "+
		"rolled-back, so update:rollback -after-failure has nothing to take back")
}

func TestRollbackAfterFailureLeavesAloneAnUpdateThatPassedItsCheck(t *testing.T) {
	t.Parallel()

	kept := switchedTo
	kept.Step = updater.StepCommitted
	f := newGuardFixture(t, kept)

	state, err := updater.RollbackAfterFailure(settingsOf(t, f.env), startLimitHit)

	f.wantLeftAlone(state, err, kept, "stands at the step committed, so update:rollback -after-failure has nothing "+
		"to take back")
}

func TestRollbackAfterFailureLeavesAloneAStartTheServiceManagerSaysDidNotFail(t *testing.T) {
	t.Parallel()

	f := newGuardFixture(t, switchedTo)

	state, err := updater.RollbackAfterFailure(settingsOf(t, f.env), updater.Failure{
		ServiceResult: "exit-code", ExitStatus: "1",
	})

	f.wantLeftAlone(state, err, switchedTo, `updater: the service manager says the service result is "exit-code" `+
		`and the exit status is "1", not a start that failed its check or hit the start limit, so `+
		`update:rollback -after-failure leaves the update alone`)
}

func TestRollbackAfterFailureKeepsTheSiteDownAfterAnUpdateThatRaisedTheMinimumVersion(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		state   updater.State
		failure updater.Failure
	}{
		"starts that hit the limit before the guard gave up": {state: longJumped(updater.StepSwitched),
			failure: startLimitHit},
		"a start the guard kept down": {state: stayedDown(), failure: failedCheck},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := newGuardFixture(t, tc.state)

			state, err := updater.RollbackAfterFailure(settingsOf(t, f.env), tc.failure)

			f.wantLeftAlone(state, err, stayedDown(), "updater: Gophenberg 0.21.0 raised the minimum version past "+
				"0.20.0, so 0.20.0 cannot open the database and the site stays down")
			wantRefused(t, err, "updater: restore the backup taken before the update, "+
				filepath.Join(f.layout.Root, longJumpBackup))
			status, err := updater.ReadStatus(settingsOf(t, f.env))
			if err != nil || !strings.HasPrefix(status.Restore, "restore the backup taken before the update") {
				t.Errorf("ReadStatus() = %+v, %v, want the restore steps shown", status, err)
			}
		})
	}
}

func TestRollbackAfterFailureSaysWhenItCannotWriteThatTheSiteStaysDown(t *testing.T) {
	t.Parallel()

	f := newGuardFixture(t, longJumped(updater.StepSwitched))
	denied(t, filepath.Dir(f.layout.StateFile()), 0o500)

	_, err := updater.RollbackAfterFailure(settingsOf(t, f.env), startLimitHit)

	wantRefused(t, err, "updater: write the update state")
	wantRefused(t, err, "updater: restore the backup taken before the update")
	f.wantCurrent(offered)
}
