// SPDX-License-Identifier: Apache-2.0

package installer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"
	"unsafe"
)

// unseen asks question on tty as ask does with its echo off, so the answer is never shown, then puts the echo back.
func unseen(ctx context.Context, tty *os.File, question string) (string, error) {
	conn, err := tty.SyscallConn()
	if err != nil {
		return "", fmt.Errorf("installer: %w", err)
	}
	shown, err := hideTyping(conn)
	if err != nil {
		return "", fmt.Errorf("installer: turn the echo of %s off: %w", tty.Name(), err)
	}
	defer func() { _ = termios(conn, syscall.TCSETS, &shown) }()
	answer, err := ask(ctx, tty, question)
	_, newline := io.WriteString(tty, "\n")
	if err := errors.Join(err, newline); err != nil {
		return "", err
	}
	return answer, nil
}

// hideTyping turns the echo off at once on the terminal conn reaches, keeping what was typed, and returns how it was.
func hideTyping(conn syscall.RawConn) (syscall.Termios, error) {
	var shown syscall.Termios
	if err := termios(conn, syscall.TCGETS, &shown); err != nil {
		return shown, err
	}
	hidden := shown
	hidden.Lflag &^= syscall.ECHO
	return shown, termios(conn, syscall.TCSETS, &hidden)
}

// termios runs request on the terminal conn reaches, reading its settings into settings or setting them from it.
func termios(conn syscall.RawConn, request uintptr, settings *syscall.Termios) error {
	var failed error
	err := conn.Control(func(fd uintptr) {
		_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, request, uintptr(unsafe.Pointer(settings)))
		if errno != 0 {
			failed = errno
		}
	})
	return errors.Join(err, failed)
}
