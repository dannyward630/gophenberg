// SPDX-License-Identifier: Apache-2.0

package installer

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// lockInstallerFolder locks the installer folder for this run alone, none while it is missing, and returns the unlock.
func lockInstallerFolder(machine Machine) (func(), error) {
	folder, err := absentAsNil(os.Open(filepath.Join(machine.Root, installerFolder)))
	if err != nil {
		return nil, err
	}
	if folder == nil {
		return func() {}, nil
	}
	return heldAlone(folder, syscall.Flock)
}

// heldAlone locks the open folder unless another run holds it, and returns what lets go of the lock.
func heldAlone(folder *os.File, flock func(fd, how int) error) (func(), error) {
	err := flock(int(folder.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == nil {
		return func() { _ = folder.Close() }, nil
	}
	_ = folder.Close()
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return nil, stop("another server:install or server:uninstall is running on this server")
	}
	return nil, fmt.Errorf("installer: lock %s: %w", installerFolder, err)
}
