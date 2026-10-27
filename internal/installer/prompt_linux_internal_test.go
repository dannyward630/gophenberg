// SPDX-License-Identifier: Apache-2.0

//go:build linux

package installer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

// ptyWait is how long a test waits on the controlling side of a pseudo terminal before it gives up.
const ptyWait = 5 * time.Second

// ptyPair opens a pseudo terminal and returns the side that types and shows, and the terminal a prompt asks on.
func ptyPair(t *testing.T) (control, terminal *os.File) {
	t.Helper()
	control, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatalf("opening a pseudo terminal: %v", err)
	}
	t.Cleanup(func() { _ = control.Close() })
	var unlocked, number uint32
	if err := ptyRequest(control, syscall.TIOCSPTLCK, &unlocked); err != nil {
		t.Fatalf("unlocking the pseudo terminal: %v", err)
	}
	if err := ptyRequest(control, syscall.TIOCGPTN, &number); err != nil {
		t.Fatalf("numbering the pseudo terminal: %v", err)
	}
	terminal, err = os.OpenFile(fmt.Sprintf("/dev/pts/%d", number), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatalf("opening the terminal of the pseudo terminal: %v", err)
	}
	t.Cleanup(func() { _ = terminal.Close() })
	return control, terminal
}

// ptyRequest runs the terminal request on file with value, through the file's own descriptor.
func ptyRequest(file *os.File, request uintptr, value *uint32) error {
	conn, err := file.SyscallConn()
	if err != nil {
		return err
	}
	var failed error
	err = conn.Control(func(fd uintptr) {
		if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, request, uintptr(unsafe.Pointer(value))); errno != 0 {
			failed = errno
		}
	})
	return errors.Join(err, failed)
}

// screenUntil reads what control shows until it ends with want, failing t once ptyWait passes.
func screenUntil(t *testing.T, control *os.File, want string) string {
	t.Helper()
	if err := control.SetReadDeadline(time.Now().Add(ptyWait)); err != nil {
		t.Fatalf("setting the read deadline: %v", err)
	}
	var shown strings.Builder
	chunk := make([]byte, 256)
	for !strings.HasSuffix(shown.String(), want) {
		n, err := control.Read(chunk)
		shown.Write(chunk[:n])
		if err != nil {
			t.Fatalf("the terminal showed %q, then %v, want it to end with %q", shown.String(), err, want)
		}
	}
	return shown.String()
}

// typeOn types text on the controlling side of a pseudo terminal, as a person at its keyboard does.
func typeOn(t *testing.T, control *os.File, text string) {
	t.Helper()
	if _, err := io.WriteString(control, text); err != nil {
		t.Fatalf("typing %q: %v", text, err)
	}
}

// echoOf reports whether terminal shows what is typed on it.
func echoOf(t *testing.T, terminal *os.File) bool {
	t.Helper()
	conn, err := terminal.SyscallConn()
	if err != nil {
		t.Fatalf("reaching the terminal: %v", err)
	}
	var settings syscall.Termios
	if err := termios(conn, syscall.TCGETS, &settings); err != nil {
		t.Fatalf("reading the terminal settings: %v", err)
	}
	return settings.Lflag&syscall.ECHO != 0
}

// reply is what one question on a terminal returned.
type reply struct {
	answer string
	err    error
}

// askedAside asks question on terminal in a goroutine, unseen when hidden, and returns where the reply arrives.
func askedAside(ctx context.Context, terminal *os.File, question string, hidden bool) <-chan reply {
	replies := make(chan reply, 1)
	prompt, _ := promptOn(terminal, nil)
	go func() {
		ask := prompt.Ask
		if hidden {
			ask = prompt.AskUnseen
		}
		answer, err := ask(ctx, question)
		replies <- reply{answer: answer, err: err}
	}()
	return replies
}

// replyOf returns the reply that arrives on replies, failing t once ptyWait passes.
func replyOf(t *testing.T, replies <-chan reply) reply {
	t.Helper()
	select {
	case got := <-replies:
		return got
	case <-time.After(ptyWait):
		t.Fatalf("the question got no reply within %s", ptyWait)
		return reply{}
	}
}

func TestAskShowsTheQuestionAndReturnsTheLineTypedAfterIt(t *testing.T) {
	t.Parallel()

	control, terminal := ptyPair(t)
	replies := askedAside(t.Context(), terminal, "Email of the first administrator: ", false)
	screenUntil(t, control, "Email of the first administrator: ")

	typeOn(t, control, "admin@example.com\n")

	if got := replyOf(t, replies); got.err != nil || got.answer != "admin@example.com" {
		t.Errorf("Ask() = %q, %v, want the line typed", got.answer, got.err)
	}
	screenUntil(t, control, "admin@example.com\r\n")
}

func TestAskUnseenShowsNothingTypedAndPutsTheEchoBack(t *testing.T) {
	t.Parallel()

	control, terminal := ptyPair(t)
	replies := askedAside(t.Context(), terminal, "Password: ", true)
	screenUntil(t, control, "Password: ")

	typeOn(t, control, "correct horse battery staple\n")

	if got := replyOf(t, replies); got.err != nil || got.answer != "correct horse battery staple" {
		t.Errorf("AskUnseen() = %q, %v, want the line typed", got.answer, got.err)
	}
	if shown := screenUntil(t, control, "\r\n"); shown != "\r\n" {
		t.Errorf("the terminal showed %q after the question, want the newline alone", shown)
	}
	if !echoOf(t, terminal) {
		t.Error("the terminal hides what is typed after AskUnseen, want the echo back")
	}
}

func TestAskEndsWhenTheTerminalGivesNoMoreInput(t *testing.T) {
	t.Parallel()

	for _, hidden := range []bool{false, true} {
		control, terminal := ptyPair(t)
		replies := askedAside(t.Context(), terminal, "Name of the first administrator [admin]: ", hidden)
		screenUntil(t, control, "[admin]: ")

		typeOn(t, control, "\x04")

		if got := replyOf(t, replies); !errors.Is(got.err, errEndOfInput) {
			t.Errorf("a question asked unseen %t after Ctrl-D = %q, %v, want the end of input", hidden, got.answer,
				got.err)
		}
		if !echoOf(t, terminal) {
			t.Errorf("the terminal hides what is typed after a question asked unseen %t, want the echo back", hidden)
		}
	}
}

func TestAskEndsAtOnceWhenItsContextEndsWhileItWaits(t *testing.T) {
	t.Parallel()

	for _, hidden := range []bool{false, true} {
		control, terminal := ptyPair(t)
		ctx, cancel := context.WithCancel(t.Context())
		replies := askedAside(ctx, terminal, "Password: ", hidden)
		screenUntil(t, control, "Password: ")

		cancel()

		if got := replyOf(t, replies); !errors.Is(got.err, context.Canceled) {
			t.Errorf("a question asked unseen %t once its context ended = %q, %v, want it canceled", hidden,
				got.answer, got.err)
		}
		if !echoOf(t, terminal) {
			t.Errorf("the terminal hides what is typed after a question asked unseen %t, want the echo back", hidden)
		}
	}
}

func TestAskUnseenRefusesAFileThatIsNoTerminal(t *testing.T) {
	t.Parallel()

	file, err := os.Create(filepath.Join(t.TempDir(), "answers"))
	if err != nil {
		t.Fatalf("making a file: %v", err)
	}
	defer func() { _ = file.Close() }()

	_, err = ttyPrompt{file: file}.AskUnseen(t.Context(), "Password: ")

	if !errors.Is(err, syscall.ENOTTY) {
		t.Errorf("AskUnseen() on a plain file = %v, want it refused as no terminal", err)
	}
}

func TestAskUnseenRefusesATerminalThatIsClosed(t *testing.T) {
	t.Parallel()

	_, terminal := ptyPair(t)
	if err := terminal.Close(); err != nil {
		t.Fatalf("closing the terminal: %v", err)
	}

	_, err := ttyPrompt{file: terminal}.AskUnseen(t.Context(), "Password: ")

	wantRefused(t, err, "installer: turn the echo of "+terminal.Name()+" off: use of closed file")
}

func TestAskUnseenRefusesAPromptWithNoTerminal(t *testing.T) {
	t.Parallel()

	_, err := ttyPrompt{}.AskUnseen(t.Context(), "Password: ")

	if !errors.Is(err, os.ErrInvalid) {
		t.Errorf("AskUnseen() with no terminal = %v, want it refused as invalid", err)
	}
}

func TestAskRefusesATerminalItCannotShowTheQuestionOn(t *testing.T) {
	t.Parallel()

	_, terminal := ptyPair(t)
	if err := terminal.Close(); err != nil {
		t.Fatalf("closing the terminal: %v", err)
	}

	_, err := ttyPrompt{file: terminal}.Ask(t.Context(), "Email of the first administrator: ")

	if !errors.Is(err, os.ErrClosed) {
		t.Errorf("Ask() on a closed terminal = %v, want it refused as closed", err)
	}
}

func TestTellShowsTheLineAndANewlineOnTheTerminal(t *testing.T) {
	t.Parallel()

	control, terminal := ptyPair(t)

	err := ttyPrompt{file: terminal}.Tell(t.Context(), "invalid email, so type it again")

	if err != nil {
		t.Errorf("Tell() = %v, want the line shown", err)
	}
	if shown := screenUntil(t, control, "\r\n"); shown != "invalid email, so type it again\r\n" {
		t.Errorf("the terminal showed %q, want the line and its newline", shown)
	}
}

func TestTellEndsAtOnceWhenItsContextEndsWhileTheTerminalTakesNothing(t *testing.T) {
	t.Parallel()

	_, terminal := ptyPair(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := ttyPrompt{file: terminal}.Tell(ctx, strings.Repeat("a", 1<<20))

	if !errors.Is(err, context.Canceled) {
		t.Errorf("Tell() on a terminal nobody reads once its context ended = %v, want it canceled", err)
	}
}

func TestTellRefusesATerminalThatIsClosed(t *testing.T) {
	t.Parallel()

	_, terminal := ptyPair(t)
	if err := terminal.Close(); err != nil {
		t.Fatalf("closing the terminal: %v", err)
	}

	err := ttyPrompt{file: terminal}.Tell(t.Context(), "the two passwords differ, so type them again")

	wantRefused(t, err, "installer: write "+terminal.Name()+": file already closed")
}

func TestClosingThePromptClosesItsTerminal(t *testing.T) {
	t.Parallel()

	_, terminal := ptyPair(t)
	prompt, err := promptOn(terminal, nil)
	if err != nil {
		t.Fatalf("promptOn() = %v, want the prompt on the terminal", err)
	}

	if err := prompt.Close(); err != nil {
		t.Errorf("Close() = %v, want the terminal closed", err)
	}
	if err := terminal.Close(); !errors.Is(err, os.ErrClosed) {
		t.Errorf("closing the terminal again = %v, want it already closed", err)
	}
}
