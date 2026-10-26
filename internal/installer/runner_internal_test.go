// SPDX-License-Identifier: Apache-2.0

package installer

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// executables writes an empty program called each of names into folder, one anybody may run.
func executables(t *testing.T, folder string, names ...string) {
	t.Helper()
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(folder, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatalf("writing the program %s: %v", name, err)
		}
	}
}

func TestArgsOfPutsRunuserInFrontOfAProgramRunAsAnotherUser(t *testing.T) {
	t.Parallel()

	folder := t.TempDir()
	executables(t, folder, "runuser", "createuser")

	args, err := argsOf(Program{
		Name: "createuser", Args: []string{"--no-superuser", "gophenberg"}, User: "postgres",
	}, []string{folder})

	want := []string{
		filepath.Join(folder, "runuser"), "-u", "postgres", "--", filepath.Join(folder, "createuser"),
		"--no-superuser", "gophenberg",
	}
	if err != nil || !slices.Equal(args, want) {
		t.Errorf("argsOf() = %q, %v, want %q", args, err, want)
	}
}

func TestArgsOfRunsAProgramAsRootWithoutRunuser(t *testing.T) {
	t.Parallel()

	folder := t.TempDir()
	executables(t, folder, "systemctl")

	args, err := argsOf(Program{Name: "systemctl", Args: []string{"--version"}}, []string{folder})

	if want := []string{filepath.Join(folder, "systemctl"), "--version"}; err != nil || !slices.Equal(args, want) {
		t.Errorf("argsOf() = %q, %v, want %q", args, err, want)
	}
}

func TestArgsOfRefusesAProgramRunAsAnotherUserWhenNoFolderHoldsRunuser(t *testing.T) {
	t.Parallel()

	first, second := t.TempDir(), t.TempDir()
	executables(t, second, "createuser")

	args, err := argsOf(Program{Name: "createuser", User: "postgres"}, []string{first, second})

	want := "installer: no program runuser in " + first + ":" + second
	if err == nil || err.Error() != want || args != nil {
		t.Errorf("argsOf() = %q, %v, want %q", args, err, want)
	}
}

func TestFindSkipsAFileNobodyMayRunAndAFolderOfTheSameName(t *testing.T) {
	t.Parallel()

	first, second, third := t.TempDir(), t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(first, "caddy"), []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatalf("writing a file nobody may run: %v", err)
	}
	if err := os.Mkdir(filepath.Join(second, "caddy"), 0o755); err != nil {
		t.Fatalf("making a folder named caddy: %v", err)
	}
	executables(t, third, "caddy")

	found, err := find("caddy", []string{first, second, third})

	if want := filepath.Join(third, "caddy"); err != nil || found != want {
		t.Errorf("find() = %q, %v, want %q", found, err, want)
	}
}
