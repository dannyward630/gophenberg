// SPDX-License-Identifier: Apache-2.0

package installer

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/gopherium/framework/gonsole"

	"github.com/gopherium/gophenberg/internal/version"
)

// errLeftOpen is what an apply returns once it warned that its step stays open, so the run goes on and ends well.
var errLeftOpen = errors.New("installer: the step stays open for the next run")

// owner is the OS user and group a file or folder the installer writes belongs to, by their ids.
type owner struct {
	uid, gid int
}

// installing is one run of server:install that changes the server, with the record it found and the one it writes.
type installing struct {
	machine Machine
	call    gonsole.Call
	site    site
	found   Record
	record  Record
	archive io.ReadSeeker
	admin   admin
	created bool
	ids     *owner
	system  *os.Root
	tree    *os.Root
}

// installingOn returns the run that takes the steps of planned on the machine check checked, its system tree open.
func installingOn(checked *machineCheck, planned site, call gonsole.Call) (*installing, error) {
	system, err := checked.machine.tree("/")
	if err != nil {
		return nil, err
	}
	record := checked.record
	record.Steps = make(map[string]string, len(checked.record.Steps))
	maps.Copy(record.Steps, checked.record.Steps)
	record.Installed = slices.Clone(checked.record.Installed)
	record.Installer, record.Domain = version.Version(), checked.settings.Domain
	return &installing{
		machine: checked.machine, call: call, site: planned, found: checked.record, record: record, system: system,
	}, nil
}

// close closes the system tree and, once the run opened it, the install tree.
func (r *installing) close() {
	_ = r.system.Close()
	if r.tree != nil {
		_ = r.tree.Close()
	}
}

// run takes each of steps in turn, printing what it does, and stops at the first that fails.
func (r *installing) run(ctx context.Context, steps []step) error {
	for n, planned := range steps {
		if _, err := fmt.Fprintf(r.call.Stdout, "Step %d: %s\n", n+1, planned.does); err != nil {
			return err
		}
		if err := r.take(ctx, planned); err != nil {
			return err
		}
	}
	return nil
}

// take marks planned started unless the record found ended it, applies it, then marks it ended, an ended step kept.
func (r *installing) take(ctx context.Context, planned step) error {
	if !r.found.ended(planned.id) {
		if err := r.mark(planned.id, stepStarted); err != nil {
			return err
		}
	}
	err := planned.apply(r, ctx, planned)
	if errors.Is(err, errLeftOpen) {
		return nil
	}
	if err != nil {
		return err
	}
	return r.mark(planned.id, stepEnded)
}

// mark marks the step called id as mark in the record and saves it.
func (r *installing) mark(id, mark string) error {
	r.record.Steps[id] = mark
	return r.save()
}

// save writes the record through the system tree as root's alone, in root's own folder.
func (r *installing) save() error {
	if err := makeFolder(r.system, inSystemTree(installerFolder), 0o700, nil); err != nil {
		return fmt.Errorf("installer: %w", err)
	}
	body, _ := json.MarshalIndent(r.record, "", "\t")
	saved := file{path: recordPath, owner: "root", mode: 0o600, text: string(body) + "\n"}
	return writeFile(r.system, inSystemTree(recordPath), saved, nil)
}

// inSystemTree returns path, named from the top of the server, as the system tree names it.
func inSystemTree(path string) string {
	return strings.TrimPrefix(path, "/")
}

// tree opens the folder at path, named from the top of the server, as a root that no link inside it leads out of.
func (m Machine) tree(path string) (*os.Root, error) {
	tree, err := os.OpenRoot(filepath.Join(m.Root, path))
	if err != nil {
		return nil, fmt.Errorf("installer: %w", err)
	}
	return tree, nil
}

// writeFile writes written at name in tree under a random name with its mode and the owner ids, then renames it in.
func writeFile(tree *os.Root, name string, written file, ids *owner) error {
	temp := filepath.Join(filepath.Dir(name), "."+filepath.Base(name)+"."+rand.Text()+".partial")
	err := makeParents(tree, name, ids)
	if err == nil {
		err = writeTemp(tree, temp, written, ids)
	}
	if err == nil {
		err = tree.Rename(temp, name)
	}
	if err == nil {
		err = saveFolder(tree, filepath.Dir(name))
	}
	if err != nil {
		_ = tree.Remove(temp)
		return fmt.Errorf("installer: write %s: %w", written.path, err)
	}
	return nil
}

// writeTemp writes written into the new file temp of tree, its mode past the umask and its owner ids, then saves it.
func writeTemp(tree *os.Root, temp string, written file, ids *owner) error {
	out, err := tree.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, written.mode)
	if err != nil {
		return err
	}
	err = settle(out, written.mode, ids)
	if err == nil {
		_, err = io.WriteString(out, written.text)
	}
	if err == nil {
		err = out.Sync()
	}
	return errors.Join(err, out.Close())
}

// settle gives the open file or folder mode, whatever the umask took, then the owner ids unless it stays root's.
func settle(opened *os.File, mode fs.FileMode, ids *owner) error {
	if err := opened.Chmod(mode); err != nil || ids == nil {
		return err
	}
	return opened.Chown(ids.uid, ids.gid)
}

// makeParents makes each missing folder above name in tree at 0755 with the owner ids, leaving every other one alone.
func makeParents(tree *os.Root, name string, ids *owner) error {
	parent := filepath.Dir(name)
	if parent == "." {
		return nil
	}
	if _, err := tree.Lstat(parent); !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := makeParents(tree, parent, ids); err != nil {
		return err
	}
	return makeFolder(tree, parent, 0o755, ids)
}

// makeFolder makes the folder name in tree, or takes the real folder there, then gives it mode and the owner ids.
func makeFolder(tree *os.Root, name string, mode fs.FileMode, ids *owner) error {
	err := tree.Mkdir(name, mode)
	if errors.Is(err, fs.ErrExist) {
		err = realFolder(tree.Lstat(name))
	}
	var folder *os.File
	if err == nil {
		folder, err = tree.Open(name)
	}
	if err == nil {
		err = errors.Join(settle(folder, mode, ids), folder.Close())
	}
	if err != nil {
		return fmt.Errorf("make the folder %s: %w", name, err)
	}
	return nil
}

// realFolder refuses what info, or the err of describing it, shows to be a link, a file or anything but a folder.
func realFolder(info fs.FileInfo, err error) error {
	if err == nil && !info.IsDir() {
		return errors.New("it is not a real folder")
	}
	return err
}

// saveFolder flushes the entries of the folder name of tree to disk, so a file renamed into it survives a power cut.
func saveFolder(tree *os.Root, name string) error {
	folder, err := tree.Open(name)
	if err == nil {
		err = errors.Join(folder.Sync(), folder.Close())
	}
	return err
}
