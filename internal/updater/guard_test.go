// SPDX-License-Identifier: Apache-2.0

package updater_test

import (
	"cmp"
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gopherium/gophenberg/internal/updater"
)

// longJumpBackup is the backup an update that raised the minimum version took before its switch.
const longJumpBackup = "backups/0.20.0-20261020T120000Z.dump"

// switchedTo is the state an update leaves once it switched from 0.20.0 to 0.21.0, refusing 0.19.5 since before.
var switchedTo = updater.State{Step: updater.StepSwitched, From: running, To: offered, Refused: []string{"0.19.5"}}

// guardFixture is one boot guard test: an install root where an update switched from 0.20.0 to 0.21.0.
type guardFixture struct {
	t      *testing.T
	layout updater.Layout
	env    map[string]string
	logs   *keptLog
}

// newGuardFixture returns a boot guard test whose install root holds both builds and the state given.
func newGuardFixture(t *testing.T, state updater.State) *guardFixture {
	t.Helper()
	f := &guardFixture{t: t, layout: updater.Layout{Root: t.TempDir()}, logs: &keptLog{kept: make(chan struct{})}}
	f.env = map[string]string{"GOPHENBERG_INSTALL_ROOT": f.layout.Root}
	placeBuildIn(t, f.layout, running)
	placeBuildIn(t, f.layout, offered)
	pointLink(t, f.layout, updater.Previous, running)
	pointLink(t, f.layout, updater.Current, offered)
	writeState(t, f.layout, state)
	return f
}

// placeBuildIn writes a stand-in gophenberg of the build called name into the versions folder of layout.
func placeBuildIn(t *testing.T, layout updater.Layout, name string) {
	t.Helper()
	if err := os.MkdirAll(layout.Build(name), 0o755); err != nil {
		t.Fatalf("making the build folder of %s: %v", name, err)
	}
	if err := os.WriteFile(layout.Binary(name), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("writing the build of %s: %v", name, err)
	}
}

// pointLink points the link called link of layout at the build called name, failing t when it cannot.
func pointLink(t *testing.T, layout updater.Layout, link, name string) {
	t.Helper()
	if err := layout.Point(link, name); err != nil {
		t.Fatalf("pointing %s at %s: %v", link, name, err)
	}
}

// start starts the boot guard of the fixture for a start of 0.21.0 and returns what it answered.
func (f *guardFixture) start() (*updater.Guard, error) {
	return f.startAs(offered)
}

// startAs starts the boot guard of the fixture for a start of the build called name and returns what it answered.
func (f *guardFixture) startAs(name string) (*updater.Guard, error) {
	return updater.Start(settingsOf(f.t, f.env), name, slog.New(slog.NewTextHandler(f.logs, nil)))
}

// started starts the boot guard of the fixture for a start of 0.21.0, failing t unless it starts.
func (f *guardFixture) started() *updater.Guard {
	f.t.Helper()
	return f.startedAs(offered)
}

// startedAs starts the boot guard of the fixture for a start of the build called name, failing t unless it starts.
func (f *guardFixture) startedAs(name string) *updater.Guard {
	f.t.Helper()
	guard, err := f.startAs(name)
	if err != nil {
		f.t.Fatalf("Start() = %v, want the start counted", err)
	}
	return guard
}

// wantState fails t unless the state file of the fixture holds want.
func (f *guardFixture) wantState(want updater.State) {
	f.t.Helper()
	if state := readState(f.t, f.layout); !reflect.DeepEqual(state, want) {
		f.t.Errorf("the state file holds %+v, want %+v", state, want)
	}
}

// wantCurrent fails t unless the current link of the fixture names the build called name.
func (f *guardFixture) wantCurrent(name string) {
	f.t.Helper()
	wantLink(f.t, filepath.Join(f.layout.Root, updater.Current), filepath.Join("versions", name))
}

// settledMarks are the words of each line a guard logs once its start check settled what becomes of the update.
var settledMarks = []string{"kept the update", "took back the update", "the update stays failed"}

// keptLog records what a guard logs and closes kept once the guard logs what became of the update.
type keptLog struct {
	mu   sync.Mutex
	text strings.Builder
	kept chan struct{}
	once sync.Once
}

// Write records p and closes kept when p says what the guard did with the update.
func (l *keptLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if slices.ContainsFunc(settledMarks, func(mark string) bool { return strings.Contains(string(p), mark) }) {
		l.once.Do(func() { close(l.kept) })
	}
	return l.text.Write(p)
}

// String returns everything the guard logged.
func (l *keptLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.text.String()
}

// testSite is a site answering the status statuses names for its paths and 200 for any other, noting each path asked.
type testSite struct {
	srv      *http.Server
	statuses map[string]int
	mu       sync.Mutex
	asked    []string
}

// newTestSite returns a site answering statuses for their paths and 200 for any other.
func newTestSite(statuses map[string]int) *testSite {
	s := &testSite{statuses: statuses}
	s.srv = &http.Server{Handler: http.HandlerFunc(s.answer), ReadHeaderTimeout: time.Second}
	return s
}

// answer notes the path asked and answers its status, naming a page that answers 200 for a client that follows it.
func (s *testSite) answer(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.asked = append(s.asked, r.URL.Path)
	s.mu.Unlock()
	w.Header().Set("Location", "/signed-out")
	w.WriteHeader(cmp.Or(s.statuses[r.URL.Path], http.StatusOK))
}

// paths returns each path the site was asked for, in order.
func (s *testSite) paths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.asked)
}

// serveAt returns a serve that serves the site on a listener at address until its context ends, as gonsole.Serve does.
func (s *testSite) serveAt(address string) func(context.Context) error {
	return func(ctx context.Context) error {
		listener, err := net.Listen("tcp", address)
		if err != nil {
			return err
		}
		stop := context.AfterFunc(ctx, func() { _ = s.srv.Close() })
		defer stop()
		if err := s.srv.Serve(listener); !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}

// passes is the check inside the process of a build that holds what its site planned.
func passes(context.Context) error {
	return nil
}

// served runs guard over the site until the check stops it, failing t when it serves past half a minute.
func served(t *testing.T, guard *updater.Guard, site *testSite, inside func(context.Context) error) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	err := guard.Serve(ctx, site.srv, inside, site.serveAt("127.0.0.1:0"))
	if ctx.Err() != nil {
		t.Fatal("Serve() served until the test stopped it, want the failed check to stop it")
	}
	return err
}

// keptServing serves guard over the site until it logs what became of the update, then stops it and returns its error.
func keptServing(t *testing.T, f *guardFixture, guard *updater.Guard, site *testSite) error {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	ended := make(chan error, 1)
	go func() { ended <- guard.Serve(ctx, site.srv, passes, site.serveAt("127.0.0.1:0")) }()
	select {
	case <-f.logs.kept:
	case err := <-ended:
		t.Fatalf("Serve() = %v before it settled the update, want it serving", err)
	case <-time.After(30 * time.Second):
		t.Fatal("Serve() never settled the update")
	}
	cancel()
	return <-ended
}

// wantCheckFailed fails t unless err is the start check failing, saying each of texts.
func wantCheckFailed(t *testing.T, err error, texts ...string) {
	t.Helper()
	if !errors.As(err, &updater.CheckError{}) {
		t.Errorf("error = %v, want the start check failed", err)
	}
	for _, text := range texts {
		wantRefused(t, err, text)
	}
}

// wentBack fails t unless the install root of f names 0.20.0 again and refuses 0.21.0 after the attempts given.
func (f *guardFixture) wentBack(attempts int) {
	f.t.Helper()
	f.wantCurrent(running)
	want := switchedTo
	want.Attempts, want.Refused = attempts, []string{"0.19.5", offered}
	f.wantState(want)
}

func TestStartChecksNothingOnAnInstallWithNoRoot(t *testing.T) {
	t.Parallel()

	guard, err := updater.Start(settingsOf(t, map[string]string{}), offered, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("Start() with no install root = %v, want nil", err)
	}
	errServe := errors.New("the serve ended")
	var handed context.Context
	site := newTestSite(nil)

	err = guard.Serve(t.Context(), site.srv, passes, func(ctx context.Context) error {
		handed = ctx
		return errServe
	})

	if !errors.Is(err, errServe) || handed != t.Context() {
		t.Errorf("Serve() = %v under %v, want the serve run as it is under the context given", err, handed)
	}
	if site.srv.BaseContext != nil {
		t.Error("Serve() set the server up for a check, want no check on an install with no root")
	}
}

func TestStartLeavesAStateThatNamesNoStartOfThisBuildAlone(t *testing.T) {
	t.Parallel()

	for name, state := range map[string]updater.State{
		"a kept update":             {Step: updater.StepCommitted, From: running, To: offered},
		"a switch to another build": {Step: updater.StepSwitched, From: running, To: "0.22.0"},
		"a job that stopped":        {Step: updater.StepBackup, From: running, To: offered},
		"an update between other builds that stays down": {
			Step: updater.StepFailed, From: "0.19.0", To: "0.22.0", LongJump: true,
		},
		"an install never updated": {},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := newGuardFixture(t, state)
			site := newTestSite(nil)

			err := f.started().Serve(t.Context(), site.srv, passes, func(context.Context) error { return nil })

			if err != nil || site.srv.BaseContext != nil {
				t.Errorf("Serve() = %v, want the serve run with no check", err)
			}
			f.wantState(state)
		})
	}
}

func TestStartCountsEachStartOfTheBuildAnUpdateSwitchedToUpToTheAttemptsTheSettingAllows(t *testing.T) {
	t.Parallel()

	f := newGuardFixture(t, switchedTo)

	f.started()
	f.started()
	f.started()

	want := switchedTo
	want.Attempts = 3
	f.wantState(want)
	f.wantCurrent(offered)
}

func TestStartGoesBackOnceTheBuildTriedToStartMoreTimesThanTheSettingAllows(t *testing.T) {
	t.Parallel()

	tried := switchedTo
	tried.Attempts = 3
	f := newGuardFixture(t, tried)

	_, err := f.start()

	wantCheckFailed(t, err,
		"updater: Gophenberg 0.21.0 failed its start check after the update from 0.20.0: it tried to start 4 times "+
			"and GOPHENBERG_UPDATE_ATTEMPTS allows 3",
		"updater: current names Gophenberg 0.20.0 again, which the next start runs, and update:apply never takes "+
			"0.21.0 again on its own")
	f.wentBack(4)
}

func TestStartTakesTheAttemptsTheSettingNames(t *testing.T) {
	t.Parallel()

	tried := switchedTo
	tried.Attempts = 3
	f := newGuardFixture(t, tried)
	f.env["GOPHENBERG_UPDATE_ATTEMPTS"] = "5"

	f.started()

	tried.Attempts = 4
	f.wantState(tried)
	f.wantCurrent(offered)
}

func TestStartKeepsTheSiteDownWhenALongJumpFailsAfterItsBuildReachedTheDatabase(t *testing.T) {
	t.Parallel()

	tried := switchedTo
	tried.Attempts, tried.LongJump, tried.Backup, tried.DatabaseReached = 3, true, longJumpBackup, true
	f := newGuardFixture(t, tried)

	_, err := f.start()

	wantCheckFailed(t, err,
		"updater: Gophenberg 0.21.0 failed its start check after the update from 0.20.0: it tried to start 4 times",
		"updater: Gophenberg 0.21.0 raised the minimum version past 0.20.0, so 0.20.0 cannot open the database "+
			"and the site stays down",
		"updater: restore the backup taken before the update, "+filepath.Join(f.layout.Root, longJumpBackup)+
			", into an empty database, point current at versions/0.20.0 and start Gophenberg again, as "+
			"https://docs.gophenberg.org/self-hosting/updates-and-backups/#restoring-on-a-server-install shows, or "+
			"fix what failed and start Gophenberg 0.21.0 again")
	f.wantCurrent(offered)
	want := tried
	want.Step, want.Attempts, want.Refused = updater.StepFailed, 4, []string{"0.19.5", offered}
	f.wantState(want)
}

func TestStartFinishesGoingBackWhenAnEarlierStartStoppedBeforeCurrentMoved(t *testing.T) {
	t.Parallel()

	halfway := switchedTo
	halfway.Attempts, halfway.Refused = 1, []string{"0.19.5", offered}
	f := newGuardFixture(t, halfway)

	_, err := f.start()

	wantCheckFailed(t, err,
		"updater: Gophenberg 0.21.0 failed its start check after the update from 0.20.0: it failed the check at an "+
			"earlier start, which stopped before it went back",
		"updater: current names Gophenberg 0.20.0 again")
	f.wentBack(1)
}

func TestStartRefusesAStateFileItCannotRead(t *testing.T) {
	t.Parallel()

	f := newGuardFixture(t, switchedTo)
	if err := os.WriteFile(f.layout.StateFile(), []byte("step: switched"), 0o644); err != nil {
		t.Fatalf("writing the state file: %v", err)
	}

	_, err := f.start()

	wantRefused(t, err, "updater: read the update state "+f.layout.StateFile())
	if errors.As(err, &updater.CheckError{}) {
		t.Errorf("Start() = %v, want a state it cannot read told from a failed start check", err)
	}
}

func TestStartRefusesAStartItCannotCount(t *testing.T) {
	t.Parallel()

	f := newGuardFixture(t, switchedTo)
	denied(t, filepath.Dir(f.layout.StateFile()), 0o500)

	_, err := f.start()

	wantRefused(t, err, "updater: write the update state")
	if errors.As(err, &updater.CheckError{}) {
		t.Errorf("Start() = %v, want a start it cannot count told from a failed start check", err)
	}
}

func TestStartStillFailsTheCheckWhenItCannotGoBack(t *testing.T) {
	t.Parallel()

	for name, deny := range map[string]struct {
		folder   string
		longJump bool
		says     string
	}{
		"a root where current cannot move":           {folder: ".", says: "updater: point current at 0.20.0"},
		"a state folder it cannot write":             {folder: "state", says: "updater: write the update state"},
		"a state folder it cannot write, a long one": {folder: "state", longJump: true, says: "updater: write the update"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tried := switchedTo
			tried.Attempts, tried.LongJump, tried.Backup = 3, deny.longJump, longJumpBackup
			tried.DatabaseReached = deny.longJump
			f := newGuardFixture(t, tried)
			denied(t, filepath.Join(f.layout.Root, deny.folder), 0o500)

			_, err := f.start()

			wantCheckFailed(t, err, "updater: Gophenberg 0.21.0 failed its start check", deny.says)
			f.wantCurrent(offered)
		})
	}
}

func TestServeKeepsAnUpdateWhoseServerAnswersItsCheck(t *testing.T) {
	t.Parallel()

	f := newGuardFixture(t, switchedTo)
	site := newTestSite(map[string]int{"/": http.StatusNotFound})

	err := keptServing(t, f, f.started(), site)

	if err != nil {
		t.Errorf("Serve() = %v, want a clean stop once the update was kept", err)
	}
	want := switchedTo
	want.Step, want.Attempts = updater.StepCommitted, 1
	f.wantState(want)
	f.wantCurrent(offered)
	if asked := site.paths(); !slices.Equal(asked, []string{"/admin/", "/api/locale", "/"}) {
		t.Errorf("the check asked for %v, want the admin, the locale and the home page", asked)
	}
	if logs := f.logs.String(); !strings.Contains(logs, "version=0.21.0 from=0.20.0") {
		t.Errorf("the guard logged %q, want the update it kept named", logs)
	}
}

func TestServeKeepsTheNewestBuildsAndTheLinkedOnes(t *testing.T) {
	t.Parallel()

	f := newGuardFixture(t, switchedTo)
	for _, name := range []string{"0.17.0", "0.18.0", "0.19.0", "0.19.5"} {
		placeBuildIn(t, f.layout, name)
	}
	pointLink(t, f.layout, updater.Previous, "0.17.0")
	notes := filepath.Join(f.layout.Root, "versions", "notes", "kept.txt")
	stray := filepath.Join(f.layout.Root, "versions", "0.16.0")
	for _, path := range []string{notes, stray} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("making the folder of %s: %v", path, err)
		}
		if err := os.WriteFile(path, []byte("a person's file"), 0o644); err != nil {
			t.Fatalf("writing %s: %v", path, err)
		}
	}
	f.env["GOPHENBERG_UPDATE_VERSIONS_KEEP"] = "3"

	if err := keptServing(t, f, f.started(), newTestSite(nil)); err != nil {
		t.Fatalf("Serve() = %v, want the update kept", err)
	}

	entries, err := os.ReadDir(filepath.Join(f.layout.Root, "versions"))
	if err != nil {
		t.Fatalf("listing the versions folder: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	if want := []string{"0.16.0", "0.17.0", "0.19.5", running, offered, "notes"}; !slices.Equal(names, want) {
		t.Errorf("the versions folder holds %v, want %v", names, want)
	}
}

func TestServeKeepsTheBuildTheCurrentLinkNamesWhenANewerOneIsThere(t *testing.T) {
	t.Parallel()

	f := newGuardFixture(t, switchedTo)
	for _, name := range []string{"0.19.0", "0.22.0"} {
		placeBuildIn(t, f.layout, name)
	}
	f.env["GOPHENBERG_UPDATE_VERSIONS_KEEP"] = "1"

	if err := keptServing(t, f, f.started(), newTestSite(nil)); err != nil {
		t.Fatalf("Serve() = %v, want the update kept", err)
	}

	wantMissing(t, f.layout.Build("0.19.0"))
	for _, name := range []string{running, offered, "0.22.0"} {
		wantFile(t, f.layout.Binary(name), "#!/bin/sh\n")
	}
}

func TestServeGoesBackWhenAPageAnswersBadly(t *testing.T) {
	t.Parallel()

	for name, failing := range map[string]struct {
		statuses map[string]int
		says     string
	}{
		"the admin": {
			statuses: map[string]int{"/admin/": http.StatusFound}, says: "/admin/ answered 302, want 200",
		},
		"the locale": {
			statuses: map[string]int{"/api/locale": http.StatusNotFound}, says: "/api/locale answered 404, want 200",
		},
		"the home": {
			statuses: map[string]int{"/": http.StatusInternalServerError}, says: "/ answered 500, want below 500",
		},
		"the gateway": {
			statuses: map[string]int{"/": http.StatusBadGateway}, says: "/ answered 502, want below 500",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := newGuardFixture(t, switchedTo)

			err := served(t, f.started(), newTestSite(failing.statuses), passes)

			wantCheckFailed(t, err,
				"updater: Gophenberg 0.21.0 failed its start check after the update from 0.20.0: "+failing.says,
				"updater: current names Gophenberg 0.20.0 again")
			f.wentBack(1)
		})
	}
}

func TestServeGoesBackWhenTheBuildFailsItsCheckInside(t *testing.T) {
	t.Parallel()

	f := newGuardFixture(t, switchedTo)
	site := newTestSite(nil)
	errTheme := errors.New("themehost: the site chose the theme aurora, which does not serve the public site")

	err := served(t, f.started(), site, func(context.Context) error { return errTheme })

	wantCheckFailed(t, err, "failed its start check after the update from 0.20.0: "+errTheme.Error())
	if !errors.Is(err, errTheme) {
		t.Errorf("Serve() = %v, want the failure of the check inside in its chain", err)
	}
	if asked := site.paths(); len(asked) != 0 {
		t.Errorf("the check asked for %v, want no page asked once the check inside failed", asked)
	}
	f.wentBack(1)
}

func TestServeGoesBackWhenTheCheckInsideOutlastsTheVerifyTimeout(t *testing.T) {
	t.Parallel()

	f := newGuardFixture(t, switchedTo)
	f.env["GOPHENBERG_UPDATE_VERIFY_TIMEOUT"] = "50ms"
	site := newTestSite(nil)

	err := served(t, f.started(), site, func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})

	wantCheckFailed(t, err, "failed its start check after the update from 0.20.0: context deadline exceeded")
	if asked := site.paths(); len(asked) != 0 {
		t.Errorf("the check asked for %v, want no page asked once the check inside ran out of time", asked)
	}
	f.wentBack(1)
}

func TestServeGoesBackWhenTheServerNeverListens(t *testing.T) {
	t.Parallel()

	f := newGuardFixture(t, switchedTo)
	f.env["GOPHENBERG_UPDATE_VERIFY_TIMEOUT"] = "50ms"

	err := f.started().Serve(t.Context(), newTestSite(nil).srv, passes, func(ctx context.Context) error {
		<-ctx.Done()
		return nil
	})

	wantCheckFailed(t, err,
		"failed its start check after the update from 0.20.0: it did not listen within 50ms, the time "+
			"GOPHENBERG_UPDATE_VERIFY_TIMEOUT allows")
	f.wentBack(1)
}

func TestServeGoesBackWhenTheServerStopsAnswering(t *testing.T) {
	t.Parallel()

	f := newGuardFixture(t, switchedTo)
	site := newTestSite(nil)

	err := f.started().Serve(t.Context(), site.srv, passes, func(ctx context.Context) error {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return err
		}
		_ = listener.Close()
		site.srv.BaseContext(listener)
		<-ctx.Done()
		return nil
	})

	wantCheckFailed(t, err, "failed its start check after the update from 0.20.0: it could not ask for /admin/: ")
	f.wentBack(1)
}

func TestServeAsksAServerThatListensOnEveryInterfaceOverLoopback(t *testing.T) {
	t.Parallel()

	f := newGuardFixture(t, switchedTo)
	guard, site := f.started(), newTestSite(nil)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	ended := make(chan error, 1)

	go func() { ended <- guard.Serve(ctx, site.srv, passes, site.serveAt("0.0.0.0:0")) }()

	select {
	case <-f.logs.kept:
	case err := <-ended:
		t.Fatalf("Serve() = %v, want the update kept over loopback", err)
	case <-time.After(30 * time.Second):
		t.Fatal("Serve() never kept the update")
	}
	cancel()
	if err := <-ended; err != nil {
		t.Errorf("Serve() = %v, want a clean stop", err)
	}
}

func TestServeLeavesTheUpdateAloneWhenTheServeEndsBeforeTheCheck(t *testing.T) {
	t.Parallel()

	errListen := errors.New("http server: listen tcp: address already in use")
	for name, serve := range map[string]func(ctx context.Context, cancel context.CancelFunc) error{
		"a serve that cannot listen": func(context.Context, context.CancelFunc) error { return errListen },
		"a signal during the check": func(ctx context.Context, cancel context.CancelFunc) error {
			cancel()
			<-ctx.Done()
			return errListen
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := newGuardFixture(t, switchedTo)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			blocked := func(ctx context.Context) error {
				<-ctx.Done()
				return ctx.Err()
			}

			err := f.started().Serve(ctx, newTestSite(nil).srv, blocked, func(ctx context.Context) error {
				return serve(ctx, cancel)
			})

			if !errors.Is(err, errListen) || errors.As(err, &updater.CheckError{}) {
				t.Errorf("Serve() = %v, want only the serve's own error", err)
			}
			want := switchedTo
			want.Attempts = 1
			f.wantState(want)
			f.wantCurrent(offered)
		})
	}
}

func TestServeKeepsAnUpdateThatStayedDownOnceItsBuildPassesTheCheckWithNoAttemptCounted(t *testing.T) {
	t.Parallel()

	tried := stayedDown()
	tried.Attempts = 4
	f := newGuardFixture(t, tried)

	if err := keptServing(t, f, f.started(), newTestSite(nil)); err != nil {
		t.Fatalf("Serve() = %v, want the update kept", err)
	}

	want := tried
	want.Step, want.Refused = updater.StepCommitted, []string{"0.19.5"}
	f.wantState(want)
	f.wantCurrent(offered)
}

func TestServeTakesBackAnUpdateThatStayedDownOnceTheBuildBeforeItPassesTheCheck(t *testing.T) {
	t.Parallel()

	f := newGuardFixture(t, stayedDown())
	pointLink(t, f.layout, updater.Current, running)

	if err := keptServing(t, f, f.startedAs(running), newTestSite(nil)); err != nil {
		t.Fatalf("Serve() = %v, want the update taken back", err)
	}

	want := stayedDown()
	want.Step = updater.StepRolledBack
	f.wantState(want)
	f.wantCurrent(running)
	if logs := f.logs.String(); !strings.Contains(logs, "took back the update after the build before it passed its "+
		"start check") {
		t.Errorf("the guard logged %q, want the update it took back named", logs)
	}
}

func TestServeLeavesAnUpdateThatStayedDownAsItWasWhenTheCheckFailsAgain(t *testing.T) {
	t.Parallel()

	f := newGuardFixture(t, stayedDown())
	site := newTestSite(map[string]int{"/admin/": http.StatusInternalServerError})

	err := keptServing(t, f, f.started(), site)

	if err != nil {
		t.Errorf("Serve() = %v, want it serving on until it stops", err)
	}
	f.wantState(stayedDown())
	f.wantCurrent(offered)
	if logs := f.logs.String(); !strings.Contains(logs, "/admin/ answered 500, want 200") {
		t.Errorf("the guard logged %q, want the failed check named", logs)
	}
}

func TestServeStopsWhenItCannotKeepTheUpdate(t *testing.T) {
	t.Parallel()

	f := newGuardFixture(t, switchedTo)
	guard := f.started()
	denied(t, filepath.Dir(f.layout.StateFile()), 0o500)

	err := served(t, guard, newTestSite(nil), passes)

	wantRefused(t, err, "updater: write the update state")
	if errors.As(err, &updater.CheckError{}) {
		t.Errorf("Serve() = %v, want a state it cannot write told from a failed start check", err)
	}
	f.wantCurrent(offered)
}
