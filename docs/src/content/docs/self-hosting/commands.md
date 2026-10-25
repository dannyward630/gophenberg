---
title: Commands
description: Every command the binary runs, how to read its help, and who may change an account.
---

The `gophenberg` binary serves the site, and it also runs the jobs an
operator does beside it, such as creating the first account or
applying the schema. Every command reads the same environment
variables and `.env` file as the server, listed in
[configuration](/self-hosting/configuration/).

With Docker Compose, put the command after the service name:

```sh
docker compose run --rm -T gophenberg list
```

## The listing

Run `gophenberg` with no command, or `gophenberg list`, and it prints
every command it knows, then exits:

```text
Gophenberg Version %VERSION%

Usage:
  gophenberg <command> [flags] [arguments]

Every command answers -h. A command that offers -json answers one JSON document. A command that offers -yes is a dry run until -yes.

Available commands:
  about                 print the version, the build stamp, the commit and the theme kits of this binary
  check                 check every setting, every plugin and every command name
  help                  print the help of one command
  list                  list every command
  migrate               apply every schema step
  seed                  store the demo data
  serve                 run the server
  version               print the version
 account
  account:create-admin  create an account under a role
  account:disable       disable one account
  account:enable        enable one disabled account
  account:grant-role    give a role to every account holding none
  account:list          list every account with its role
  account:records       list who applied which change, the newest first
  account:role          set one account's role
 update
  update:apply          update this server install to a newer Gophenberg, then ask for a restart
  update:check          check the update site for a newer Gophenberg and keep what it found
  update:rollback       take back the last update, pointing current at the build before it
  update:status         show the builds of this server install, its last update and its backups

Every command is described at https://docs.gophenberg.org/self-hosting/commands/
```

## Reading the listing

- A name without a colon is one of the site's own commands.
- A name with a colon sits under a heading, the part before the
  colon, such as `account`. You always type the full name, such as
  `account:list`.
- A heading with the word `plugin` beside it holds the commands one
  plugin offers. They are named after the plugin's id.
- A `Not loaded:` part at the end names each plugin that failed to
  register, and why, such as a bad `GOPHENBERG_FEED_ITEMS`. The
  listing still exits with code 0, but `serve`, `check`, `migrate`,
  `seed -yes` and every account command except `account:list` and
  `account:records` fail until the setting is fixed.

Every command answers `-h`. `gophenberg account:role -h` and
`gophenberg help account:role` print the same page, and neither needs
a database:

```text
set one account's role

Usage:
  gophenberg account:role [flags] <email> <role>

Flags:
  -as email
        email address of the account acting
  -yes
        apply the change, a dry run without it
```

A word in angle brackets is an argument the command needs, in that
order. `[flags]` means the command takes the flags listed below it.
The word after a flag, such as `email` in `-as email`, says what value
that flag wants. Flags can go before or after the arguments.

## Exit codes

| Code | Meaning |
| --- | --- |
| `0` | The command finished, printed a help page, or ended a dry run |
| `1` | The command ran and failed |
| `2` | The command line was misused, such as an unknown command, a missing argument or a missing flag |
| `75` | `update:apply` or `update:rollback` pointed `current` at another build, or `serve` stopped once an update started from the admin did, so restart Gophenberg to start it |
| `76` | `serve` started a new build that failed its start check after an update, see [the boot guard](/self-hosting/updates-and-backups/#the-boot-guard) |

Every error goes to standard error, on a line that starts with
`gophenberg:`. When the command itself was found, a misuse is followed
by its help page.

## On a SQLite database

When `GOPHENBERG_DATABASE_URL` names a SQLite file, such as
`sqlite:/srv/gophenberg/site.db`, only some commands run so far:

| Command | On a SQLite database |
| --- | --- |
| `list`, `help` and `version` | Work as on PostgreSQL |
| `check` | Works, and also checks the SQLite settings and the file |
| `migrate` | Works |
| `seed` | Without `-yes` it still prints `would store the demo data` and exits with code 0. With `-yes` it migrates the file, then exits with code 1 |
| `serve` | Exits with code 1 before it opens anything |
| `account:create-admin` | Works as on PostgreSQL. It migrates the file first, then asks for the password |
| Every other `account:` command | Works as on PostgreSQL, on the accounts and the records kept in the file |
| A plugin command that names a capability | Checks the account in `-as`, then runs, as on PostgreSQL |
| `update:apply` | Exits with code 1 before it opens anything |
| `update:rollback` and `update:status` | Work as on PostgreSQL, since they never open the database |

`serve`, `seed -yes` and `update:apply` stop and name what they need:

```text
gophenberg: serve: content and types do not run on SQLite yet, so this command needs GOPHENBERG_DATABASE_URL to name a PostgreSQL database
```

`seed -yes` says `seed:` in place of `serve:`, and `update:apply`
says `update:apply:`.

`seed -yes` migrates the file before it stops, `account:create-admin`
migrates it first, and every other account command and every plugin
command opens it. So a missing file, a bad address or a bad SQLite
setting stops each of them with its own error, as it stops `migrate`.
On a file that was never migrated, run `migrate` first. Until then
`account:records` and every command that takes `-as` stop with
`the command records are missing, run migrate first`, and
`account:list` stops with SQLite's own error.
[A SQLite database](/self-hosting/configuration/#a-sqlite-database)
covers the address, the file and its settings.

## Dry runs and -yes

Five commands only show what they would change until you add `-yes`:
`seed`, `account:grant-role`, `account:role`, `account:disable` and
`account:enable`. This line, for example:

```sh
gophenberg account:role author@example.com editor -as admin@example.com
```

changes nothing and prints:

```text
would set author@example.com to editor
gophenberg: dry run, nothing changed, pass -yes to apply
```

A dry run exits with code 0. Add `-yes` to the same line to apply the
change, and it prints `set author@example.com to editor` instead.

`migrate` and `account:create-admin` have no dry run. They act at
once.

## Who may change an account

`account:grant-role`, `account:role`, `account:disable` and
`account:enable` need `-as` with the email address of the person
running the command. That account must exist, be enabled and
activated, and hold a role carrying `manage_users`. `admin` carries
it, and so does any role a plugin gives it. The check runs before
anything changes, on a dry run too. When it fails, the command exits
with code 1 and says why:

```text
gophenberg: the account editor@example.com holds the role editor, which lacks manage_users
```

Leaving out `-as`, or giving it only spaces, exits with code 2. The
message names the command, so for `account:role` it reads:

```text
gophenberg: account:role wants -as <email>
```

`account:role` and `account:disable` refuse to act on the account
named in `-as`, on a dry run too, even when another admin is left.
The command exits with code 1 and says why:

```text
gophenberg: the account admin@example.com cannot disable itself
```

`account:role` says
`the account admin@example.com cannot change its own role` instead.
Name another admin in `-as` to make that change.

An account may hand out only a role whose capabilities its own role
carries, and may change only an account whose role it covers that
way. So a role a plugin gives only `manage_users` can make members,
but never an author or an admin. Going past that exits with code 1:

```text
gophenberg: the role admin carries access_admin, which the account lead@example.com lacks
```

A role is one of the core's, `admin`, `editor`, `author` and
`member`, or one a plugin declares. A `member` signs in but never
enters the admin. Every account command except `account:list` and
`account:records` registers the plugins before it runs, so
`account:role` and `-role` take a plugin's roles too.
Naming a role nobody declares exits with code 2 and lists the roles
there are.

The last enabled account whose role carries `manage_users` always
stays. If two changes run at the same time and would together leave
no such account, one of them fails with
`<address> is the last enabled privileged account`.

`-as` asks for no password. It names who answers for the change.
Anyone who can run commands against the database can name any
account, so keep access to the server as tight as before.

The check needs the table the records are kept in. On a database
updated from an earlier release, start the new server or run
`migrate` first, or the command stops with
`the command records are missing, run migrate first`.

## The records

Every change those four commands apply with `-yes` is kept as one
record, in the `gonsole.records` table on PostgreSQL or in the
`gonsole_records` table of a SQLite file, so a backup of the database
carries it. A record names the time, the account in
`-as`, the command, its arguments and its other flags. A dry run
records nothing, and neither do `account:create-admin` and `seed`.

`account:records` lists them, the newest first, with times in UTC:

```text
2026-10-01T09:30:12Z  admin@example.com  account:role        author@example.com editor
2026-09-30T16:02:45Z  admin@example.com  account:grant-role  -role admin
```

It lists the latest 50, unless `-limit` or
`GOPHENBERG_COMMAND_RECORDS_LIMIT` names another number. Storing one
record may take up to `GOPHENBERG_COMMAND_RECORD_TIMEOUT`, 5 seconds
unless set. When a record cannot be stored, the change stays applied,
and the command exits with code 1 and says so.

## Renamed commands

Two commands took new names. The old names are gone, with no alias:

| Old name | New name |
| --- | --- |
| `createadmin` | `account:create-admin` |
| `grantrole` | `account:grant-role` |

An old name exits with code 2:

```text
gophenberg: unknown command "createadmin", run "gophenberg list" to see every command
```

Change any script that runs them. `account:grant-role` also wants
`-as`, and `-yes` to apply.

## Every command

### about

```text
gophenberg about [flags]
```

Prints what this binary is in one line, such as
`Gophenberg %VERSION%, official build, commit 4c3ba60, theme kits 0.9.0 to %KIT_VERSION%`.
With `-json` it answers one JSON document holding `schema`, `name`,
`version`, `commit`, `stamp`, `minimum_version`, `postgresql`, `node`
and `kits`. It needs no database.

- A binary from a release archive or the release image says
  `official build`, and names the commit it was built from.
- A binary you build yourself says `custom build` and names no commit.
- `kits` lists the [theme kit](/themes/how-theming-works/) versions
  this binary serves.
- `minimum_version` is the oldest version allowed to run on a
  database this binary migrated, see
  [going back to an older version](/self-hosting/updates-and-backups/#going-back-to-an-older-version).
- `postgresql` is the oldest PostgreSQL major this binary runs on.
- `node` is the oldest Node.js major this binary runs a theme on. The
  release image ships this major. Outside the image, point
  [`GOPHENBERG_NODE_BIN`](/self-hosting/configuration/) at a Node.js of
  this major or newer.

### check

```text
gophenberg check
```

Reads every setting, registers the plugins and checks the command
names, without touching the database. It prints
`settings, plugins and command names are valid`, or names what it
refuses, one line each, and exits with code 1. Run it before a
rollout. On a SQLite database it also checks the SQLite settings and
that the file exists on a local disk, still without connecting to it.
It does not open the file, so a folder, a file that is not a SQLite
database or a read-only file still passes `check` and stops at
`migrate`. A command that stops on a read-only file leaves the
`-wal` and `-shm` files beside it read-only too. When you make the
database file writable, make those two files writable as well.

### help

```text
gophenberg help
```

`gophenberg help <command>` prints the page of one command, the same
page `-h` prints. Alone, it prints the listing. It needs no database.

### list

```text
gophenberg list
```

Prints [the listing](#the-listing), the same as a run with no
command.

### migrate

```text
gophenberg migrate
```

Applies every schema step in order, `accounts`, `records` and `core`,
then every plugin's schema, and prints `migrated accounts`,
`migrated records`, `migrated core` and `migrated plugins`.
`serve` applies the same steps when it starts. Every step waits for a
lock in the database, so two servers starting together, or a server
and `migrate`, never apply the same step at once. The second waits,
then finds nothing left to do.

On a SQLite database the lock is the file `<database file>.migrate.lock`,
such as `site.db.migrate.lock`. It sits beside the file the path
finally points at, so when the path is a symbolic link, the lock sits
beside the file the link points at. A step waits for it up to
`GOPHENBERG_SQLITE_MIGRATE_LOCK_WAIT`, 30 seconds unless set, then
stops with an error. Never delete that file.

### seed

```text
gophenberg seed [flags]
```

Stores demo data to try Gophenberg on. Without `-yes` it only prints
`would store the demo data`. With `-yes` it applies every schema
step, then stores the demo content, the accounts
`admin@example.com`, `editor@example.com`, `author@example.com` and
`member@example.com`, and each plugin's demo data. It prints
`created` or `kept` beside each account, and the accounts it created
sign in with `password1234`.
A later run stops with an error naming the group when the demo's
Post fields or Category fields group is turned off or no longer
reaches its type. Never seed a production database.

On a SQLite database the dry run still prints
`would store the demo data` and exits with code 0, although `-yes`
stops with code 1 after it migrates the file, see
[on a SQLite database](#on-a-sqlite-database).

### serve

```text
gophenberg serve
```

Applies every schema step, then serves the site until it is stopped,
see [stopping the server](/self-hosting/configuration/#stopping-the-server).
The container image runs `serve` when it is given no command. A
compose file or deployment that sets its own `command` must start it
with `serve`, since a run with no command only prints the listing.

On a SQLite database `serve` exits with code 1 before it opens
anything, see [on a SQLite database](#on-a-sqlite-database).

When the PostgreSQL server is not up yet, `serve` first waits for it,
up to `GOPHENBERG_DATABASE_WAIT`, see
[a database that starts late](/self-hosting/configuration/#a-database-that-starts-late).

Once an update an administrator started from the admin switched
builds, `serve` stops as it does on `SIGTERM` and exits with code 75,
so the service manager starts the new build, see
[updating from the admin](/self-hosting/updates-and-backups/#updating-from-the-admin).

### version

```text
gophenberg version [flags]
```

Prints `gophenberg %VERSION%`. With `-json` it answers one JSON
document holding `name` and `version`.

### account:create-admin

```text
gophenberg account:create-admin [flags]
```

Creates one account with the address in `-email`, the display name in
`-name`, and the role in `-role`, which is `admin`, `editor`,
`author`, `member` or a role a plugin declares. It reads the
password from standard input, keeping it out of your shell history,
and applies the `accounts`, `records` and `core` schema steps first.
It needs no `-as`, since it is how a site gets its first admin:

```sh
docker compose run --rm -T gophenberg \
  account:create-admin -email admin@example.com -name "Maria Perez" -role admin
```

On a SQLite database it works the same way and creates the account in
the file, but `serve` still cannot serve a site from that file, see
[on a SQLite database](#on-a-sqlite-database).

### account:disable

```text
gophenberg account:disable [flags] <email>
```

Disables one account, so it can no longer sign in. It refuses to
disable the account named in `-as`, and the last enabled account
holding `manage_users`.

### account:enable

```text
gophenberg account:enable [flags] <email>
```

Enables one disabled account, so it can sign in again.

### account:grant-role

```text
gophenberg account:grant-role [flags]
```

Gives the role in `-role` to every account that holds none, such as
the accounts made before roles existed. On a site where no account
holds a role yet, create an admin with `account:create-admin` first
and name it in `-as`:

```sh
docker compose run --rm -T gophenberg \
  account:grant-role -role admin -as admin@example.com -yes
```

[Users and signing in](/guides/users/#upgrading-a-site-that-ran-an-earlier-version)
covers the whole upgrade.

### account:list

```text
gophenberg account:list [flags]
```

Lists every account, ordered by display name, with its role, or a
dash when it holds none, and whether it is enabled. With `-json` it
answers one document holding each account's `id`, `email`, `name`,
`role` and `disabled`. On a SQLite database a letter with an accent
sorts after z, so Álvaro lists after Zoe. PostgreSQL follows the
database's own collation, so a database the official PostgreSQL image
made, under `en_US.utf8`, lists Álvaro beside alba.

### account:records

```text
gophenberg account:records [flags]
```

Lists the account changes applied with `-yes`, the newest first, see
[the records](#the-records). `-limit` sets how many, and `-json`
answers one document instead.

### account:role

```text
gophenberg account:role [flags] <email> <role>
```

Sets the role of one account to `admin`, `editor`, `author`,
`member` or a role a plugin declares. It refuses to change the role
of the account named in `-as`, to hand out a role beyond the reach
of that account, and to take `manage_users` from the last enabled
account holding it.

### update:check

```text
gophenberg update:check [flags]
```

Checks the update site for a newer Gophenberg now, and keeps what it
found for the admin, as the site's own checks do. It prints one line
naming the newer version and this one, or saying this is the newest
version the update site offers. With `-json` it answers one document
holding `checked_at`, `offer`, `failed_at` and `error`. It runs the
migrations first. It installs nothing: on a server install,
[`update:apply`](#updateapply) installs the update when you run it.

It fetches three files: the key list, the timestamp file and the
index. It refuses an update site whose files the Gophenberg keys did
not sign, a file that expired, and a file older than the last one the
site took. It also refuses a missing timestamp file, and a key list or
an index older than the one the timestamp file names. Each time it
exits with code 1 naming why. When a file expired, the error names
the time the site's clock reads, since a wrong clock refuses good
files. See
[how a site hears of a new version](/self-hosting/updates-and-backups/#how-a-site-hears-of-a-new-version).

### update:apply

```text
gophenberg update:apply [flags]
```

Updates a server install to a newer Gophenberg, then asks you to
restart it. It works only where `GOPHENBERG_INSTALL_ROOT` names an
install root, see
[updating a server install](/self-hosting/updates-and-backups/#updating-a-server-install).
Run it with the binary that `current` names, as the user Gophenberg
runs as. The server keeps running the old build while it works.

It checks the signed key list, timestamp file and index again,
refuses what a check would refuse, an expired timestamp file
included, then downloads the archive into `downloads/`, checks its
checksum and its signature, unpacks it into `versions/<version>/`, backs
up the database with `pg_dump` into `backups/`, and points `previous`
at the build that runs and `current` at the new one. It ends with this
line on standard error and exit code 75:

```text
gophenberg: restart Gophenberg to start 1.4.1
```

- `-to <version>` takes that version of the update site, newer than
  the one that runs, in place of the newest one offered to this site.
  It may take a version the rollout has not reached this site yet,
  but never one the update site holds back from every site.
- `-retry` takes the version in `-to` again, although it failed its
  start check after an earlier update. Without it, `update:apply`
  never takes that version again on its own.
- `-skip-backup` switches with no backup. For a release that raised
  the minimum version, `update:apply` takes the backup anyway, since
  only the backup can take the site back then.
- `-json` answers the state the update ends at, holding `step`,
  `from`, `to`, `attempts`, `database_reached`, `long_jump`, `backup`
  and `refused`. `database_reached` turns `true` once a start of the
  new build reached the database, just before it migrates.

Before it changes anything it refuses, with exit code 1 and what to
do: a custom build, an install with no install root such as the
Docker image, a release with no archive for this platform, a release
that needs a newer PostgreSQL, an active theme that does not load or
that the release does not serve, a `current` link that does not name
the build that runs, an update that still waits for its first start,
an install root it cannot write to, a `pg_dump` older than the
server, and too little free disk. A second `update:apply` while one
runs stops with `another update runs`. If a step fails, it deletes
what it wrote, leaves `current` as it was and writes which update
failed, when, at which step and why, which `update:status` shows.

It needs a PostgreSQL database. It does not run the migrations, the
new build runs them when it starts.

### update:rollback

```text
gophenberg update:rollback [flags]
```

Takes back the last update. It points `current` at the build before
the update again, adds the version it left to the refused versions,
so `update:apply` never takes it again on its own, and ends asking for
a restart with exit code 75. It refuses when no update switched builds
since the last rollback, and when the update raised the minimum
version and its new build already reached the database, since the
older build cannot open the database the newer one may have migrated.
Then it prints the steps that restore the backup taken before the
update instead. It never opens the database, so it works when the database is
what broke.

`-after-failure` is the form the rollback unit of the service manager
runs after a failed start. It reads `MONITOR_SERVICE_RESULT` and
`MONITOR_EXIT_STATUS`, which systemd hands that unit, and takes the
update back only when the state says the update switched builds and
systemd says the starts hit the start limit, `start-limit-hit`, or the
new build exited with code 76. It then exits with code 0, so the unit
starts the service again. Otherwise it changes nothing and exits with
code 1. It takes back each update once, so two failing builds never
take turns. An update that raised the minimum version and whose new
build already reached the database stays down: it writes that the
update failed, so `update:status` shows the steps that restore the
backup, prints them and exits with code 1. One whose new build never
reached the database, such as one that started while PostgreSQL was
still down, goes back like any other update.

`-json` answers the state the rollback ends at, as `update:apply`
does.

### update:status

```text
gophenberg update:status [flags]
```

Shows the server install: the install root, the builds `current` and
`previous` name, where the last update stands, the refused versions
and the backups kept, the newest first. After an update that raised
the minimum version and failed its start check, it also prints the
steps that restore the backup. For example:

```text
install root: /opt/gophenberg
current build: 1.4.1
previous build: 1.4.0
last update: from 1.4.0 to 1.4.1, kept, since the new build passed its start check
refused versions: none
backups kept: /opt/gophenberg/backups/1.4.0-20261020T120000Z.dump
```

After an update that failed at a step, it also prints which update
failed, when, at which step and why, until the next update starts:

```text
last failure: from 1.4.1 to 1.4.2, at the step download on 2026-10-22T09:12:00Z: updater: refused the archive, no release or standby key the key list names signed it
```

With `-json` it answers one document holding `root`, `current`,
`previous`, `update`, `restore` and `backups`. `update` holds the same
fields as the `-json` answer of `update:apply`, and `failure` after an
update that failed at a step, holding `at`, `from`, `to`, `step` and
`error`. On an install with no install root it says that the install
updates by hand. It never opens the database.
