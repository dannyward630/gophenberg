// SPDX-License-Identifier: Apache-2.0

package updater

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
)

const (
	// binaryName is the file of a release archive that holds the Gophenberg binary.
	binaryName = "gophenberg"
	// licensesFolder is the one folder a release archive holds, with one license text in each of its files.
	licensesFolder = "LICENSES"
	// licenseMode is the mode each license text of the licenses folder is written with.
	licenseMode fs.FileMode = 0o644
)

// knownFiles maps each file at the top of a release archive to the mode it is written with.
var knownFiles = map[string]fs.FileMode{
	binaryName:     0o755,
	"release.json": 0o644,
	"LICENSE":      0o644,
	"NOTICE":       0o644,
}

// unpacker writes the entries of one release archive into its build folder dir of tree.
type unpacker struct {
	reader *tar.Reader
	tree   *os.Root
	dir    string
}

// UnpackIn writes the known files of the release archive into dir, a folder it makes in tree, deleting it on a failure.
func UnpackIn(tree *os.Root, archive io.Reader, dir string) error {
	if err := tree.Mkdir(dir, 0o755); err != nil {
		return folderNotMade(err)
	}
	if err := unpackInto(tree, archive, dir); err != nil {
		_ = tree.RemoveAll(dir)
		return err
	}
	return nil
}

// Unpack writes the known files of the release archive into dir, a folder it makes, deleting dir when any entry fails.
func Unpack(archive io.Reader, dir string) error {
	tree, err := os.OpenRoot(filepath.Dir(dir))
	if err != nil {
		return folderNotMade(err)
	}
	defer func() { _ = tree.Close() }()
	return UnpackIn(tree, archive, filepath.Base(dir))
}

// folderNotMade says the build folder could not be made, and why.
func folderNotMade(err error) error {
	return fmt.Errorf("updater: make the build folder: %w", err)
}

// unpackInto saves the new build folder dir of tree to disk, then writes the entries of the archive and saves them.
func unpackInto(tree *os.Root, archive io.Reader, dir string) error {
	if err := saveFolders(tree, filepath.Dir(dir)); err != nil {
		return err
	}
	unzipped, err := gzip.NewReader(archive)
	if err != nil {
		return fmt.Errorf("updater: refused the archive, which is not gzip: %w", err)
	}
	if err := (unpacker{reader: tar.NewReader(unzipped), tree: tree, dir: dir}).entries(); err != nil {
		return err
	}
	return saveFolders(tree, filepath.Join(dir, licensesFolder), dir)
}

// saveFolders flushes the entries of each folder of tree to disk, so a power cut leaves no build half written.
func saveFolders(tree *os.Root, folders ...string) error {
	for _, folder := range folders {
		if err := syncIn(tree, folder); err != nil {
			return fmt.Errorf("updater: save the build to disk: %w", err)
		}
	}
	return nil
}

// syncFolder flushes the entries of the folder at name to disk, doing nothing for a folder that is not there.
func syncFolder(name string) error {
	return syncOpened(os.Open(name))
}

// syncIn flushes the entries of the folder at name in tree to disk, doing nothing for a folder that is not there.
func syncIn(tree *os.Root, name string) error {
	return syncOpened(tree.Open(name))
}

// syncOpened flushes the entries of the folder an open gave, or returns its error, taking a folder not there as done.
func syncOpened(folder *os.File, err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = folder.Close() }()
	return folder.Sync()
}

// entries writes every entry of the archive, refusing an archive that holds no binary.
func (u unpacker) entries() error {
	holdsBinary := false
	for {
		header, err := u.reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("updater: refused the archive, which cannot be read: %w", err)
		}
		if err := u.entry(header); err != nil {
			return err
		}
		holdsBinary = holdsBinary || path.Clean(header.Name) == binaryName
	}
	if !holdsBinary {
		return errors.New("updater: refused the archive, which holds no gophenberg binary")
	}
	return nil
}

// entry writes the entry header names, refusing what a release archive does not hold.
func (u unpacker) entry(header *tar.Header) error {
	switch {
	case !filepath.IsLocal(filepath.FromSlash(header.Name)):
		return fmt.Errorf("updater: refused the archive, its entry %q is outside the build folder", header.Name)
	case isLink(header):
		return fmt.Errorf("updater: refused the archive, its entry %q is a link", header.Name)
	case isLicensesFolder(header):
		return nil
	}
	mode, known := modeOf(header)
	if !known || header.Typeflag != tar.TypeReg {
		return fmt.Errorf("updater: refused the archive, its entry %q is not a file a release holds", header.Name)
	}
	return u.write(header, mode)
}

// modeOf returns the mode the file the entry header names is written with, and whether a release holds that file.
func modeOf(header *tar.Header) (fs.FileMode, bool) {
	name := path.Clean(header.Name)
	if path.Dir(name) == licensesFolder {
		return licenseMode, true
	}
	mode, known := knownFiles[name]
	return mode, known
}

// isLink reports whether the entry is a symbolic or a hard link.
func isLink(header *tar.Header) bool {
	return header.Typeflag == tar.TypeSymlink || header.Typeflag == tar.TypeLink
}

// isLicensesFolder reports whether the entry is the folder that holds the license texts.
func isLicensesFolder(header *tar.Header) bool {
	return header.Typeflag == tar.TypeDir && path.Clean(header.Name) == licensesFolder
}

// write copies the entry header names into a new file of the build folder with mode and flushes it to disk.
func (u unpacker) write(header *tar.Header, mode fs.FileMode) error {
	name := path.Clean(header.Name)
	target := filepath.Join(u.dir, filepath.FromSlash(name))
	_ = u.tree.MkdirAll(filepath.Dir(target), 0o755)
	out, err := u.tree.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return fmt.Errorf("updater: unpack: %w", err)
	}
	_, err = io.Copy(out, u.reader)
	if err == nil {
		err = out.Sync()
	}
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("updater: unpack %s: %w", name, err)
	}
	return nil
}
