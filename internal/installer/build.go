// SPDX-License-Identifier: Apache-2.0

package installer

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/gopherium/gophenberg/internal/updater"
	"github.com/gopherium/gophenberg/internal/version"
)

// build unpacks the archive into a fresh build folder and points current at it, unless current names a build already.
func (r *installing) build(_ context.Context, _ step) error {
	if r.site.current {
		return nil
	}
	name := version.Version()
	dir := updater.Layout{}.Build(name)
	_, err := r.archive.Seek(0, io.SeekStart)
	if err == nil {
		err = r.tree.RemoveAll(dir)
	}
	if err == nil {
		err = updater.UnpackIn(r.tree, r.archive, dir)
	}
	if err == nil {
		err = r.giveAll(dir)
	}
	if err == nil {
		err = updater.PointIn(r.tree, updater.Current, name)
	}
	if err == nil {
		err = r.tree.Lchown(updater.Current, r.ids.uid, r.ids.gid)
	}
	if err != nil {
		return fmt.Errorf("installer: unpack the build %s: %w", name, err)
	}
	return nil
}

// giveAll gives the folder dir of the install tree and everything in it to gophenberg, following no link.
func (r *installing) giveAll(dir string) error {
	return fs.WalkDir(r.tree.FS(), dir, func(path string, _ fs.DirEntry, err error) error {
		if err == nil {
			err = r.tree.Lchown(path, r.ids.uid, r.ids.gid)
		}
		return err
	})
}

// keepCopy copies the running installer to installerCopy as root's, unless the run runs that copy already.
func (r *installing) keepCopy(_ context.Context, _ step) error {
	running, err := r.machine.Executable()
	if err != nil {
		return fmt.Errorf("installer: find the running installer: %w", err)
	}
	if r.runsCopy(running) {
		return nil
	}
	binary, err := os.ReadFile(running)
	if err != nil {
		return fmt.Errorf("installer: read the running installer: %w", err)
	}
	copied := file{path: installerCopy, owner: "root", mode: 0o755, text: string(binary)}
	return writeFile(r.system, inSystemTree(installerCopy), copied, nil)
}

// runsCopy reports whether the installer at running is the installer's own copy, under its name or another.
func (r *installing) runsCopy(running string) bool {
	copied, err := os.Stat(filepath.Join(r.machine.Root, installerCopy))
	if err != nil {
		return false
	}
	ran, err := os.Stat(running)
	return err == nil && os.SameFile(ran, copied)
}
