// SPDX-License-Identifier: Apache-2.0

package app

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/gopherium/framework/dbkit"
	"github.com/gopherium/framework/gonsole"
	"github.com/gopherium/framework/gonsole/testkit"
	authkitpg "github.com/gopherium/gouncer/authkit/postgres"

	"github.com/gopherium/gophenberg/internal/testdb"
	"github.com/gopherium/gophenberg/internal/updater"
	"github.com/gopherium/gophenberg/internal/version"
)

// coreBuilt reports whether the core schema stands in db.
func coreBuilt(t *testing.T, db *sql.DB) bool {
	t.Helper()
	var found *string
	if err := db.QueryRowContext(t.Context(), "SELECT to_regnamespace('core')::text").Scan(&found); err != nil {
		t.Fatalf("looking for the core schema: %v", err)
	}
	return found != nil
}

// keyTwice returns address with its connect timeout set twice in the query, which pgx reads and dbkit refuses.
func keyTwice(t *testing.T, address string) string {
	t.Helper()
	parsed, err := url.Parse(address)
	if err != nil {
		t.Fatalf("parsing the test database address: %v", err)
	}
	query := parsed.Query()
	query.Add("connect_timeout", "5")
	query.Add("connect_timeout", "5")
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

// schemasBuilt returns the names of the account, record and core schemas that stand in db, joined by spaces.
func schemasBuilt(t *testing.T, db *sql.DB) string {
	t.Helper()
	var built string
	err := db.QueryRowContext(t.Context(), "SELECT coalesce(string_agg(nspname, ' ' ORDER BY nspname), '') "+
		"FROM pg_namespace WHERE nspname IN ('auth', 'gonsole', 'core')").Scan(&built)
	if err != nil {
		t.Fatalf("looking for the schemas: %v", err)
	}
	return built
}

// migratingCommands are the arguments of each command that applies the schema steps before it runs.
var migratingCommands = map[string][]string{
	"migrate":              {"migrate"},
	"seed -yes":            {"seed", "-yes"},
	"account:create-admin": {"account:create-admin", "-email", "admin@example.com", "-name", "Holder", "-role", "admin"},
}

// stopsBeforeAnySchema runs each migrating command under settings and wants it to stop with want, building nothing.
func stopsBeforeAnySchema(t *testing.T, settings func(t *testing.T, address string) map[string]string, want string) {
	t.Helper()
	for name, args := range migratingCommands {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			address := emptyDatabaseURL(t)

			got := testkit.Run(t, Program(testkit.Getenv(settings(t, address)), noPlugins), "", args...)

			built := schemasBuilt(t, testdb.Open(t, address))
			if got.Code != gonsole.ExitFailed || got.Stdout != "" || got.Stderr != want || built != "" {
				t.Errorf("%s = %d with stdout %q, stderr %q and the schemas %q built, want 1, nothing on stdout, "+
					"%q and no schema", name, got.Code, got.Stdout, got.Stderr, built, want)
			}
		})
	}
}

func TestMigrateNamesTheConnectionCapItRefusesOnPostgreSQL(t *testing.T) {
	t.Parallel()

	stopsBeforeAnySchema(t, func(_ *testing.T, address string) map[string]string {
		return map[string]string{"GOPHENBERG_DATABASE_URL": address, "GOPHENBERG_DATABASE_MAX_CONNS": "1"}
	}, "gophenberg: migrate accounts: GOPHENBERG_DATABASE_MAX_CONNS: must be 2 or more, got \"1\"\n")
}

func TestMigrateNamesADatabaseAddressDbkitRefusesOnPostgreSQL(t *testing.T) {
	t.Parallel()

	stopsBeforeAnySchema(t, func(t *testing.T, address string) map[string]string {
		return map[string]string{"GOPHENBERG_DATABASE_URL": keyTwice(t, address)}
	}, "gophenberg: migrate accounts: GOPHENBERG_DATABASE_URL: "+dbkit.ErrAddress.Error()+
		": the query sets a key twice, set each key once, with dbname and database as one key\n")
}

func TestMigrateNamesAnInstallRootItRefusesOnPostgreSQL(t *testing.T) {
	t.Parallel()

	stopsBeforeAnySchema(t, func(_ *testing.T, address string) map[string]string {
		return map[string]string{"GOPHENBERG_DATABASE_URL": address, installRootSetting: "opt/gophenberg"}
	}, "gophenberg: migrate accounts: GOPHENBERG_INSTALL_ROOT: must be an absolute path, got \"opt/gophenberg\"\n")
}

func TestMigrateStopsBeforeAnySchemaOnAnUpdateStateItCannotRead(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	state := updater.Layout{Root: root}.StateFile()
	if err := os.MkdirAll(filepath.Dir(state), 0o755); err != nil {
		t.Fatalf("making the state folder: %v", err)
	}
	if err := os.WriteFile(state, []byte("step: switched"), 0o644); err != nil {
		t.Fatalf("writing the state file: %v", err)
	}

	stopsBeforeAnySchema(t, func(_ *testing.T, address string) map[string]string {
		return map[string]string{"GOPHENBERG_DATABASE_URL": address, installRootSetting: root}
	}, "gophenberg: migrate accounts: updater: read the update state "+state+
		": invalid character 's' looking for beginning of value\n")
}

func TestEachCommandThatMigratesMarksTheDatabaseReachedForTheUpdateThatSwitchedToThisBuild(t *testing.T) {
	t.Parallel()

	for name, args := range migratingCommands {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			if err := switchedInstall(root, "0.18.0", version.Version()); err != nil {
				t.Fatalf("making the install root: %v", err)
			}
			settings := map[string]string{"GOPHENBERG_DATABASE_URL": emptyDatabaseURL(t), installRootSetting: root}

			got := testkit.Run(t, Program(testkit.Getenv(settings), noPlugins), typedPassword+"\n", args...)

			state, err := updater.Layout{Root: root}.ReadState()
			if got.Code != 0 || err != nil {
				t.Fatalf("%s = %d with stderr %q, and reading the update state gave %v, want 0 and a readable state",
					name, got.Code, got.Stderr, err)
			}
			if !state.DatabaseReached {
				t.Errorf("%s left the update state %+v, want the database marked reached", name, state)
			}
		})
	}
}

func TestTheCoreStepOpensUnderTheConnectionCapSetting(t *testing.T) {
	t.Parallel()

	env := settingsEnv(testkit.Getenv(map[string]string{"GOPHENBERG_DATABASE_MAX_CONNS": "1"}))

	err := postgresCore(env).Run(t.Context(), unreachableDatabaseURL)

	if want := `GOPHENBERG_DATABASE_MAX_CONNS: must be 2 or more, got "1"`; fmt.Sprint(err) != want {
		t.Errorf("the core step under a cap of 1 = %v, want %q", err, want)
	}
}

func TestServeMigratesTheCoreSchemaOnTheSitesOwnHandle(t *testing.T) {
	t.Parallel()

	address := emptyDatabaseURL(t)
	handed := emptyDatabaseURL(t)
	if err := authkitpg.Migrate(t.Context(), handed); err != nil {
		t.Fatalf("building the account tables the core schema needs: %v", err)
	}
	db := testdb.Open(t, handed)

	if err := migrate(t.Context(), address, db); err != nil {
		t.Fatalf("migrate() error = %v, want nil", err)
	}

	onHandle, atAddress := coreBuilt(t, db), coreBuilt(t, testdb.Open(t, address))
	if !onHandle || atAddress {
		t.Errorf("the core schema stands on the handle %v and at the address %v, want it on the handle only",
			onHandle, atAddress)
	}
}
