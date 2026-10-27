// SPDX-License-Identifier: Apache-2.0

package installer

import (
	"cmp"
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/gopherium/framework/gonsole"

	"github.com/gopherium/gophenberg/internal/version"
)

// goodPassword is a password the account rules take.
const goodPassword = "correct horse battery staple"

// passwordFile is where the tests keep the first administrator's password file, from the top of the server.
const passwordFile = "/root/admin-password"

// emailQuestion is the question that asks for the first administrator's email.
const emailQuestion = "Email of the first administrator: "

// allAsked is every question a run asks while the flags give no answer, the name's default that of admin@example.com.
var allAsked = []asked{
	{question: emailQuestion}, {question: "Name of the first administrator [admin]: "},
	{question: "Password: ", unseen: true}, {question: "Password again: ", unseen: true},
}

// askedOn asks for the first administrator on server as request asks, returning the answers and the error.
func askedOn(t *testing.T, server *fakeServer, request Request) (admin, error) {
	t.Helper()
	return askAdmin(t.Context(), server.machine(), request, checkEmail)
}

// toldOn is the line a fake terminal shows when told why it asks again.
func toldOn(line string) asked {
	return asked{told: line}
}

// answering gives server a terminal that answers each of answers in turn, and returns it.
func answering(server *fakeServer, answers ...string) *fakePrompt {
	server.prompt = &fakePrompt{answers: answers}
	return server.prompt
}

// wantAsked fails t unless prompt was asked want, in order, and closed afterwards.
func wantAsked(t *testing.T, prompt *fakePrompt, want []asked) {
	t.Helper()
	if !slices.Equal(prompt.asked, want) || !prompt.closed {
		t.Errorf("the terminal was asked %+v and closed %t, want %+v and closed", prompt.asked, prompt.closed, want)
	}
}

// wantNoTerminal fails t when the run opened the terminal of server.
func wantNoTerminal(t *testing.T, server *fakeServer) {
	t.Helper()
	if server.terminals != 0 {
		t.Errorf("the run opened the terminal %d times, want it never opened", server.terminals)
	}
}

func TestReadLineReadsNothingAfterTheEndOfTheLine(t *testing.T) {
	t.Parallel()

	typed := strings.NewReader("admin@example.com\nMaria Perez\n")

	line, err := readLine(t.Context(), typed)

	if err != nil || [2]any{line, typed.Len()} != [2]any{"admin@example.com", len("Maria Perez\n")} {
		t.Errorf("readLine() = %q, %v, leaving %d bytes, want the first line and the second left", line, err,
			typed.Len())
	}
}

func TestReadLineTakesALineRightAtTheLongestAnswer(t *testing.T) {
	t.Parallel()

	longest := strings.Repeat("a", maxAnswerBytes)

	line, err := readLine(t.Context(), strings.NewReader(longest+"\n"))

	if err != nil || line != longest {
		t.Errorf("readLine() = %d bytes, %v, want the %d bytes typed", len(line), err, maxAnswerBytes)
	}
}

func TestReadLineRefusesALineLongerThanTheLongestAnswer(t *testing.T) {
	t.Parallel()

	_, err := readLine(t.Context(), strings.NewReader(strings.Repeat("a", maxAnswerBytes+1)+"\n"))

	wantRefused(t, err, "the answer is longer than 4096 bytes")
}

func TestReadLineEndsWhenTheInputEnds(t *testing.T) {
	t.Parallel()

	_, err := readLine(t.Context(), strings.NewReader("admin@exam"))

	if !errors.Is(err, errEndOfInput) {
		t.Errorf("readLine() = %v, want the end of input", err)
	}
}

func TestReadLineReturnsWhatStopsTheRead(t *testing.T) {
	t.Parallel()

	_, err := readLine(t.Context(), iotest.ErrReader(errors.New("the terminal is gone")))

	wantRefused(t, err, "installer: the terminal is gone")
}

func TestReadLineReturnsTheErrorOfAnEndedContextOverTheReadsOwn(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := readLine(ctx, iotest.ErrReader(io.ErrUnexpectedEOF))

	if !errors.Is(err, context.Canceled) {
		t.Errorf("readLine() = %v, want the context's own error", err)
	}
}

func TestPromptOnReturnsWhyTheTerminalDidNotOpen(t *testing.T) {
	t.Parallel()

	prompt, err := promptOn(nil, errors.New("open /dev/tty: no such device or address"))

	if prompt != nil {
		t.Errorf("promptOn() = %v, want no prompt", prompt)
	}
	wantRefused(t, err, "open /dev/tty: no such device or address")
}

func TestAskAdminTakesEveryAnswerFromTheFlagsAndOpensNoTerminal(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	server.write(passwordFile, goodPassword+"\nthe second line\n")

	answered, err := askedOn(t, server, Request{
		AdminEmail: " Admin@Example.com ", AdminName: " Maria Perez ", AdminPasswordFile: passwordFile,
	})

	want := admin{email: "admin@example.com", name: "Maria Perez", password: goodPassword}
	if err != nil || answered != want {
		t.Errorf("askAdmin() = %+v, %v, want %+v", answered, err, want)
	}
	wantNoTerminal(t, server)
}

func TestAskAdminRefusesAFlaggedEmailOrNameTheAccountRulesRefuseBeforeItOpensTheTerminal(t *testing.T) {
	t.Parallel()

	tooLong := strings.Repeat("a", 257)
	for _, refused := range []struct {
		request Request
		want    string
	}{
		{Request{AdminEmail: "maria"}, `-admin-email: invalid email, got "maria"`},
		{Request{AdminEmail: "admin@example.com", AdminName: "   "}, `-admin-name: empty name, got "   "`},
		{Request{AdminEmail: "admin@example.com", AdminName: tooLong},
			`-admin-name: name longer than 256 characters, got "` + tooLong + `"`},
	} {
		server := newFakeServer(t)
		answering(server, goodPassword, goodPassword)

		_, err := askedOn(t, server, refused.request)

		wantRefused(t, err, refused.want+", so nothing was changed")
		wantNoTerminal(t, server)
	}
}

func TestAskAdminChecksAFlaggedNameOnceTheEmailIsTyped(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	prompt := answering(server, "admin@example.com", goodPassword, goodPassword)

	_, err := askedOn(t, server, Request{AdminName: "   "})

	wantRefused(t, err, `-admin-name: empty name, got "   ", so nothing was changed`)
	wantAsked(t, prompt, allAsked[:1])
}

func TestAskAdminRefusesAPasswordFileTheAccountRulesRefuse(t *testing.T) {
	t.Parallel()

	for body, want := range map[string]string{
		goodPassword + "\r\n": "-admin-password-file: the first line of /root/admin-password ends in a carriage " +
			"return, as a file saved with Windows line endings does, and a password never holds one",
		"too short\n":             "-admin-password-file: password shorter than 12 characters",
		"":                        "-admin-password-file: password shorter than 12 characters",
		strings.Repeat("a", 1025): "-admin-password-file: password longer than 1024 characters",
		strings.Repeat("a", maxAnswerBytes+1): "-admin-password-file: the first line of /root/admin-password is " +
			"longer than 4096 bytes",
	} {
		server := newFakeServer(t)
		server.write(passwordFile, body)

		_, err := askedOn(t, server, Request{
			AdminEmail: "admin@example.com", AdminName: "Maria Perez", AdminPasswordFile: passwordFile,
		})

		wantRefused(t, err, want+", so nothing was changed")
		wantNoTerminal(t, server)
	}
}

func TestAskAdminTakesAPasswordFileWhoseFirstLineIsTheLongestAnswer(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	longest := strings.Repeat("\U0001D11E", maxAnswerBytes/4)
	server.write(passwordFile, longest)

	answered, err := askedOn(t, server, Request{
		AdminEmail: "admin@example.com", AdminName: "Maria Perez", AdminPasswordFile: passwordFile,
	})

	if err != nil || answered.password != longest {
		t.Errorf("askAdmin() = %d bytes, %v, want the %d bytes of the file", len(answered.password), err,
			maxAnswerBytes)
	}
}

func TestAskAdminRefusesAPasswordFileItCannotRead(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	server.folder("/root/folder")

	for path, want := range map[string]string{
		"/root/missing": "open " + server.at("/root/missing") + ": no such file or directory",
		"/root/folder":  "read " + server.at("/root/folder") + ": is a directory",
	} {
		_, err := askedOn(t, server, Request{AdminEmail: "admin@example.com", AdminPasswordFile: path})

		wantRefused(t, err, "-admin-password-file: "+want)
	}
}

func TestAskAdminRefusesARunWithNoTerminalNamingTheFlagsForWhatTheyLeaveOut(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	server.write(passwordFile, goodPassword+"\n")
	for _, missing := range []struct {
		request     Request
		what, flags string
	}{
		{Request{}, "email, name and password", "-admin-email, -admin-name and -admin-password-file"},
		{Request{AdminEmail: "admin@example.com"}, "name and password", "-admin-name and -admin-password-file"},
		{Request{AdminEmail: "admin@example.com", AdminName: "Maria Perez"}, "password", "-admin-password-file"},
		{Request{AdminName: "Maria Perez", AdminPasswordFile: passwordFile}, "email", "-admin-email"},
	} {
		_, err := askedOn(t, server, missing.request)

		wantRefused(t, err, "this run has no terminal to ask the first administrator's "+missing.what+" on, so give "+
			missing.flags+": open /dev/tty: no such device or address, so nothing was changed")
	}
}

func TestAskAdminAsksForWhatTheFlagsLeaveOutInOrder(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	prompt := answering(server, "Admin@Example.com", " Maria Perez ", goodPassword, goodPassword)

	answered, err := askedOn(t, server, Request{})

	want := admin{email: "admin@example.com", name: "Maria Perez", password: goodPassword}
	if err != nil || answered != want {
		t.Errorf("askAdmin() = %+v, %v, want %+v", answered, err, want)
	}
	wantAsked(t, prompt, allAsked)
}

func TestAskAdminTakesThePartOfTheEmailBeforeTheAtForAnEmptyName(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	prompt := answering(server, "maria@example.com", "  ", goodPassword, goodPassword)

	answered, err := askedOn(t, server, Request{})

	if err != nil || answered.name != "maria" {
		t.Errorf("askAdmin() = %+v, %v, want the name maria", answered, err)
	}
	if want := "Name of the first administrator [maria]: "; prompt.asked[1].question != want {
		t.Errorf("the terminal asked %q for the name, want %q", prompt.asked[1].question, want)
	}
}

func TestAskAdminAsksOnlyForThePasswordTheFlagsLeaveOut(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	prompt := answering(server, goodPassword, goodPassword)

	answered, err := askedOn(t, server, Request{AdminEmail: "admin@example.com", AdminName: "Maria Perez"})

	if err != nil || answered.password != goodPassword {
		t.Errorf("askAdmin() = %+v, %v, want the password typed", answered, err)
	}
	wantAsked(t, prompt, allAsked[2:])
}

func TestAskAdminAsksAgainForEachTypedAnswerTheAccountRulesRefuse(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	prompt := answering(server, "maria", "admin@example.com", strings.Repeat("a", 257), "Maria Perez", "too short",
		goodPassword, goodPassword)

	answered, err := askedOn(t, server, Request{})

	want := admin{email: "admin@example.com", name: "Maria Perez", password: goodPassword}
	if err != nil || answered != want {
		t.Errorf("askAdmin() = %+v, %v, want %+v", answered, err, want)
	}
	wantAsked(t, prompt, []asked{
		allAsked[0], toldOn("invalid email, so type it again"), allAsked[0], allAsked[1],
		toldOn("name longer than 256 characters, so type it again"), allAsked[1], allAsked[2],
		toldOn("password shorter than 12 characters, so type it again"), allAsked[2], allAsked[3],
	})
}

func TestAskAdminAsksForBothPasswordsAgainWhenTheyDiffer(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	prompt := answering(server, goodPassword, "correct horse battery stable", goodPassword, goodPassword)

	answered, err := askedOn(t, server, Request{AdminEmail: "admin@example.com", AdminName: "Maria Perez"})

	if err != nil || answered.password != goodPassword {
		t.Errorf("askAdmin() = %+v, %v, want the password typed twice alike", answered, err)
	}
	differ := toldOn("the two passwords differ, so type them again")
	wantAsked(t, prompt, slices.Concat(allAsked[2:], []asked{differ}, allAsked[2:]))
}

func TestAskAdminStopsWhenTheTerminalEndsAtAnyQuestion(t *testing.T) {
	t.Parallel()

	typed := []string{"admin@example.com", "Maria Perez", goodPassword}
	for given, what := range []string{"email", "name", "password", "password"} {
		server := newFakeServer(t)
		prompt := answering(server, typed[:given]...)

		_, err := askedOn(t, server, Request{})

		wantRefused(t, err, "asking for the first administrator's "+what+" ended: the terminal gave no more input, "+
			"so nothing was changed")
		wantAsked(t, prompt, allAsked[:given+1])
	}
}

func TestAskAdminStopsWithTheErrorOfItsEndedContext(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	answering(server, "admin@example.com")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := askAdmin(ctx, server.machine(), Request{}, checkEmail)

	wantRefused(t, err, "asking for the first administrator's email ended: context canceled, so nothing was changed")
	if !errors.Is(err, context.Canceled) {
		t.Errorf("askAdmin() = %v, want it to hold the context's error", err)
	}
}

// onlyChecking is how a run without -dry-run stops once every check passed and every answer came.
const onlyChecking = "this build of server:install only checks the server, and -dry-run prints the steps it would " +
	"take, so nothing was changed"

// certificateEmailTold is what the terminal is told of a typed email that cannot become the certificate email.
const certificateEmailTold = "the email also becomes GOPHENBERG_INSTALL_CERTIFICATE_EMAIL, which must be one plain " +
	`email address such as admin@example.com, got "maria{perez}@example.com", so type it again`

// installedOn runs server:install for example.com from the archive on server under env, returning the check it took.
func installedOn(t *testing.T, server *fakeServer, env map[string]string) (*machineCheck, error) {
	t.Helper()
	server.write(archivePath, releaseArchiveOf(t, version.Version(), serverPlatform))
	request := Request{Domain: "example.com", Archive: archivePath}
	call := gonsole.Call{Env: envOf(env), Stdout: io.Discard, Stderr: io.Discard}
	check, err := checkFor(t.Context(), server.machine(), call, request)
	if err != nil {
		t.Fatalf("checkFor() = %v, want every check passed", err)
	}
	return check, install(t.Context(), call, request, check)
}

func TestInstallReadsTheSettingsAgainWithTheEmailTyped(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	answering(server, "Admin@Example.com", "Maria Perez", goodPassword, goodPassword)

	check, err := installedOn(t, server, nil)

	wantRefused(t, err, onlyChecking)
	if check.settings.CertificateEmail != "admin@example.com" {
		t.Errorf("the certificate email after the questions = %q, want the email typed", check.settings.CertificateEmail)
	}
}

func TestInstallAsksAgainForATypedEmailThatCannotBecomeTheCertificateEmail(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	prompt := answering(server, "maria{perez}@example.com", "admin@example.com", "Maria Perez", goodPassword,
		goodPassword)

	check, err := installedOn(t, server, nil)

	wantRefused(t, err, onlyChecking)
	wantAsked(t, prompt, slices.Concat(allAsked[:1], []asked{toldOn(certificateEmailTold)}, allAsked))
	if check.settings.CertificateEmail != "admin@example.com" {
		t.Errorf("the certificate email after the questions = %q, want the email typed again",
			check.settings.CertificateEmail)
	}
}

func TestInstallTakesATypedEmailTheAccountRulesTakeWhileASettingGivesTheCertificateEmail(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	prompt := answering(server, "maria{perez}@example.com", "Maria Perez", goodPassword, goodPassword)

	check, err := installedOn(t, server, map[string]string{"GOPHENBERG_INSTALL_CERTIFICATE_EMAIL": "admin@example.com"})

	wantRefused(t, err, onlyChecking)
	if told := slices.IndexFunc(prompt.asked, func(shown asked) bool { return shown.told != "" }); told >= 0 {
		t.Errorf("the terminal was told %q, want the email taken as typed", prompt.asked[told].told)
	}
	if check.settings.CertificateEmail != "admin@example.com" {
		t.Errorf("the certificate email after the questions = %q, want the one the setting gives",
			check.settings.CertificateEmail)
	}
}

func TestInstallRefusesAServerEnvChangedWhileTheQuestionsWereOpen(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	answering(server, "admin@example.com", "Maria Perez", goodPassword, goodPassword)
	server.write(archivePath, releaseArchiveOf(t, version.Version(), serverPlatform))
	request := Request{Domain: "example.com", Archive: archivePath}
	call := gonsole.Call{Env: envOf(nil), Stdout: io.Discard, Stderr: io.Discard}
	check, err := checkFor(t.Context(), server.machine(), call, request)
	if err != nil {
		t.Fatalf("checkFor() = %v, want every check passed", err)
	}
	server.write(serverEnvPath, "GOPHENBERG_ADDR=0.0.0.0:8081\n")

	err = install(t.Context(), call, request, check)

	wantRefused(t, err, `GOPHENBERG_ADDR: must be a loopback address with a port, such as 127.0.0.1:8081, got `+
		`"0.0.0.0:8081"`)
}

func TestAskAdminReturnsWhatStopsItTellingWhyItAsksAgain(t *testing.T) {
	t.Parallel()

	for _, answers := range [][]string{
		{"maria"},
		{"admin@example.com", "Maria Perez", "too short"},
		{"admin@example.com", "Maria Perez", goodPassword, "correct horse battery stable"},
	} {
		server := newFakeServer(t)
		answering(server, answers...).untold = errors.New("the terminal is gone")

		_, err := askedOn(t, server, Request{})

		wantRefused(t, err, "the terminal is gone")
	}
}

// asked is one question a fake terminal was asked and whether its answer was typed unseen, or a line it was told.
type asked struct {
	question string
	unseen   bool
	told     string
}

// fakePrompt is a terminal that answers from a list, in order, and records each question and line it shows.
type fakePrompt struct {
	answers []string
	asked   []asked
	untold  error
	closed  bool
}

// Tell records line among the questions, then returns the error a test gave the terminal, if any.
func (p *fakePrompt) Tell(_ context.Context, line string) error {
	p.asked = append(p.asked, asked{told: line})
	return p.untold
}

// Ask records question and returns the next answer, or the end of input once every answer is given.
func (p *fakePrompt) Ask(ctx context.Context, question string) (string, error) {
	return p.answer(ctx, question, false)
}

// AskUnseen records question as asked unseen and returns the next answer, as Ask does.
func (p *fakePrompt) AskUnseen(ctx context.Context, question string) (string, error) {
	return p.answer(ctx, question, true)
}

// Close records that the run is done with the terminal.
func (p *fakePrompt) Close() error {
	p.closed = true
	return nil
}

// answer records question and returns the next answer, the error of ctx once it ended, or the end of input.
func (p *fakePrompt) answer(ctx context.Context, question string, unseen bool) (string, error) {
	p.asked = append(p.asked, asked{question: question, unseen: unseen})
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if len(p.answers) == 0 {
		return "", errEndOfInput
	}
	answer := p.answers[0]
	p.answers = p.answers[1:]
	return answer, nil
}

// shown returns every question and line the terminal showed, one a line, each question asked unseen marked so.
func (p *fakePrompt) shown() string {
	lines := make([]string, 0, len(p.asked))
	for _, question := range p.asked {
		line := cmp.Or(question.told, strings.TrimSpace(question.question))
		if question.unseen {
			line += " (unseen)"
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}
