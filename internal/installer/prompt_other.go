// SPDX-License-Identifier: Apache-2.0

//go:build !linux

package installer

import (
	"context"
	"errors"
	"os"
)

// unseen refuses to ask, since typing an answer unseen needs the terminal requests of Linux.
func unseen(context.Context, *os.File, string) (string, error) {
	return "", errors.New("installer: typing a password unseen needs Linux")
}
