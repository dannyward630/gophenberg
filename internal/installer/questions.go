// SPDX-License-Identifier: Apache-2.0

package installer

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/gopherium/gouncer"
)

// maxAnswerBytes is the longest answer a question reads, the longest password the account rules allow in UTF-8.
const maxAnswerBytes = 4096

// errEndOfInput is how a question ends when the terminal gives no more input, as after Ctrl-D.
var errEndOfInput = errors.New("the terminal gave no more input")

// pastDeadline is a read deadline long gone, which makes a read that waits on the terminal return at once.
var pastDeadline = time.Unix(1, 0)

// adminAnswers are the answers about the first administrator in the order they are asked, each with its flag.
var adminAnswers = []struct{ what, flag string }{
	{"email", "-admin-email"}, {"name", "-admin-name"}, {"password", "-admin-password-file"},
}

// Prompt is the terminal server:install asks its questions on and tells why it asks again, a password typed unseen.
type Prompt interface {
	Ask(ctx context.Context, question string) (string, error)
	AskUnseen(ctx context.Context, question string) (string, error)
	Tell(ctx context.Context, line string) error
	Close() error
}

// ttyPrompt is the prompt on the terminal file of the session that started the installer.
type ttyPrompt struct {
	file *os.File
}

// promptOn returns the prompt on the terminal file, or err when the terminal did not open.
func promptOn(file *os.File, err error) (Prompt, error) {
	if err != nil {
		return nil, err
	}
	return ttyPrompt{file: file}, nil
}

// Ask shows question on the terminal and returns the line typed after it, ending at once when ctx ends.
func (p ttyPrompt) Ask(ctx context.Context, question string) (string, error) {
	return ask(ctx, p.file, question)
}

// AskUnseen asks question as Ask does with the echo of the terminal off, so the answer typed is never shown.
func (p ttyPrompt) AskUnseen(ctx context.Context, question string) (string, error) {
	return unseen(ctx, p.file, question)
}

// Tell shows line on the terminal, ending at once when ctx ends while the terminal takes nothing.
func (p ttyPrompt) Tell(ctx context.Context, line string) error {
	stop := context.AfterFunc(ctx, func() { _ = p.file.SetWriteDeadline(pastDeadline) })
	defer stop()
	if _, err := io.WriteString(p.file, line+"\n"); err != nil {
		return cmp.Or(ctx.Err(), fmt.Errorf("installer: %w", err))
	}
	return nil
}

// Close closes the terminal.
func (p ttyPrompt) Close() error {
	return p.file.Close()
}

// ask shows question on tty and reads the line typed after it, a deadline gone by ending the read once ctx ends.
func ask(ctx context.Context, tty *os.File, question string) (string, error) {
	stop := context.AfterFunc(ctx, func() { _ = tty.SetReadDeadline(pastDeadline) })
	defer stop()
	if _, err := io.WriteString(tty, question); err != nil {
		return "", fmt.Errorf("installer: %w", err)
	}
	return readLine(ctx, tty)
}

// readLine reads one line from typed a byte at a time, so nothing typed after it is taken, and returns it bare.
func readLine(ctx context.Context, typed io.Reader) (string, error) {
	line := make([]byte, 0, 64)
	one := make([]byte, 1)
	for len(line) <= maxAnswerBytes {
		n, err := typed.Read(one)
		switch {
		case n == 1 && one[0] == '\n':
			return string(line), nil
		case n == 1:
			line = append(line, one[0])
		case err != nil:
			return "", readFailed(ctx, err)
		}
	}
	return "", fmt.Errorf("the answer is longer than %d bytes", maxAnswerBytes)
}

// readFailed returns why a read on the terminal stopped: the error of ctx once it ended, the end of input, or err.
func readFailed(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, io.EOF) {
		return errEndOfInput
	}
	return fmt.Errorf("installer: %w", err)
}

// admin is the first administrator a first run makes, its email and name as the account code keeps them.
type admin struct {
	email, name, password string
}

// askAdmin returns the first administrator the flags of request give, asking for the rest, an email under emailRule.
func askAdmin(
	ctx context.Context, machine Machine, request Request, emailRule func(string) (string, error),
) (admin, error) {
	given, err := flaggedAdmin(machine, request)
	if err != nil {
		return admin{}, err
	}
	whats, flags := given.missing()
	if len(whats) == 0 {
		return given, nil
	}
	prompt, err := machine.Terminal()
	if err != nil {
		return admin{}, stop("this run has no terminal to ask the first administrator's %s on, so give %s: %v",
			joined(whats), joined(flags), err)
	}
	defer func() { _ = prompt.Close() }()
	return asking{prompt: prompt, emailRule: emailRule}.complete(ctx, given)
}

// flaggedAdmin returns the answers the flags of request give, each checked once what it is checked beside is known.
func flaggedAdmin(machine Machine, request Request) (admin, error) {
	email, err := flagged("-admin-email", request.AdminEmail, checkEmail)
	if err != nil {
		return admin{}, err
	}
	password, err := machine.passwordFile(request.AdminPasswordFile)
	if err != nil {
		return admin{}, err
	}
	given := admin{email: email, name: request.AdminName, password: password}
	if email != "" {
		given.name, err = given.flaggedName()
	}
	return given, err
}

// flagged returns the answer value its flag gives as check reads it, refusing one the account rules refuse.
func flagged(flag, value string, check func(string) (string, error)) (string, error) {
	if value == "" {
		return "", nil
	}
	checked, err := check(value)
	if err != nil {
		return "", stop("%s: %v, got %q", flag, err, value)
	}
	return checked, nil
}

// flaggedName returns the name the answers hold from -admin-name as the account rules read it beside their email.
func (a admin) flaggedName() (string, error) {
	return flagged("-admin-name", a.name, func(name string) (string, error) {
		return admin{email: a.email, name: name}.checkedName()
	})
}

// passwordFile returns the password on the first line of the file at path, none when no path is given.
func (m Machine) passwordFile(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	head, err := m.head(path, maxAnswerBytes+1)
	if err != nil {
		return "", fmt.Errorf("-admin-password-file: %w", err)
	}
	line, _, ended := strings.Cut(head, "\n")
	switch {
	case !ended && len(head) > maxAnswerBytes:
		return "", stop("-admin-password-file: the first line of %s is longer than %d bytes", path, maxAnswerBytes)
	case strings.HasSuffix(line, "\r"):
		return "", stop("-admin-password-file: the first line of %s ends in a carriage return, as a file saved with "+
			"Windows line endings does, and a password never holds one", path)
	}
	if err := checkPassword(line); err != nil {
		return "", stop("-admin-password-file: %v", err)
	}
	return line, nil
}

// missing returns what the answers leave out, each as people name it and by the flag that gives it.
func (a admin) missing() (whats, flags []string) {
	for n, value := range []string{a.email, a.name, a.password} {
		if value == "" {
			whats, flags = append(whats, adminAnswers[n].what), append(flags, adminAnswers[n].flag)
		}
	}
	return whats, flags
}

// asking is one round of questions about the first administrator on a terminal, with the rule a typed email keeps.
type asking struct {
	prompt    Prompt
	emailRule func(string) (string, error)
}

// complete asks for each answer given leaves out, in order, and returns the first administrator the answers make.
func (a asking) complete(ctx context.Context, given admin) (admin, error) {
	email, err := orAsked(given.email, func() (string, error) { return a.email(ctx) })
	if err != nil {
		return admin{}, err
	}
	name, err := a.name(ctx, admin{email: email, name: given.name})
	if err != nil {
		return admin{}, err
	}
	password, err := orAsked(given.password, func() (string, error) { return a.password(ctx) })
	if err != nil {
		return admin{}, err
	}
	return admin{email: email, name: name, password: password}, nil
}

// orAsked returns the answer given, else the one ask returns.
func orAsked(given string, ask func() (string, error)) (string, error) {
	if given != "" {
		return given, nil
	}
	return ask()
}

// email asks for the first administrator's email until the rule of the round takes one.
func (a asking) email(ctx context.Context) (string, error) {
	return a.until(ctx, "email", func() (string, error) {
		return a.prompt.Ask(ctx, "Email of the first administrator: ")
	}, a.emailRule)
}

// name returns the flagged name of named, else asks until the account rules take one, an empty answer the default.
func (a asking) name(ctx context.Context, named admin) (string, error) {
	if named.name != "" {
		return named.flaggedName()
	}
	fallback, _, _ := strings.Cut(named.email, "@")
	typed := func() (string, error) { return a.prompt.Ask(ctx, "Name of the first administrator ["+fallback+"]: ") }
	return a.until(ctx, "name", typed, func(answer string) (string, error) {
		return admin{email: named.email, name: cmp.Or(strings.TrimSpace(answer), fallback)}.checkedName()
	})
}

// password asks for the first administrator's password unseen until the account rules take it and it comes twice.
func (a asking) password(ctx context.Context) (string, error) {
	for {
		password, err := a.until(ctx, "password", func() (string, error) { return a.prompt.AskUnseen(ctx, "Password: ") },
			func(typed string) (string, error) { return typed, checkPassword(typed) })
		if err != nil {
			return "", err
		}
		again, err := a.prompt.AskUnseen(ctx, "Password again: ")
		if err != nil {
			return "", askEnded("password", err)
		}
		if again == password {
			return password, nil
		}
		if err := a.prompt.Tell(ctx, "the two passwords differ, so type them again"); err != nil {
			return "", err
		}
	}
}

// until asks for what with ask until check takes the answer, telling within ctx why it refused each one.
func (a asking) until(ctx context.Context, what string, ask func() (string, error),
	check func(string) (string, error)) (string, error) {
	for {
		typed, err := ask()
		if err != nil {
			return "", askEnded(what, err)
		}
		checked, err := check(typed)
		if err == nil {
			return checked, nil
		}
		if err := a.prompt.Tell(ctx, err.Error()+", so type it again"); err != nil {
			return "", err
		}
	}
}

// askEnded returns the stop of a run whose question for what ended with err.
func askEnded(what string, err error) error {
	return stop("asking for the first administrator's %s ended: %w", what, err)
}

// checkEmail returns email as the account code keeps it, the email standing in for the name it always passes as.
func checkEmail(email string) (string, error) {
	user, err := gouncer.NewInvitedUser(email, email)
	return user.Email, brokenRule(err)
}

// checkedName returns the name of the answers as the account code keeps it beside their email, which it already took.
func (a admin) checkedName() (string, error) {
	user, err := gouncer.NewInvitedUser(a.email, a.name)
	return user.Name, brokenRule(err)
}

// checkPassword refuses a password the account rules do not take.
func checkPassword(password string) error {
	var user gouncer.User
	return brokenRule(user.SetPassword(password))
}

// brokenRule returns the rule of the account code err names, read without the name of the package behind it.
func brokenRule(err error) error {
	if err == nil {
		return nil
	}
	return errors.New(strings.TrimPrefix(err.Error(), "gouncer: "))
}

// joined returns items as a person lists them: one alone, else with commas and an and before the last.
func joined(items []string) string {
	last := len(items) - 1
	if last < 1 {
		return strings.Join(items, "")
	}
	return strings.Join(items[:last], ", ") + " and " + items[last]
}
