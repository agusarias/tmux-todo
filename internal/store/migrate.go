package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
)

// sqlConn is the subset of *sql.Conn and *sql.DB the migration helpers use, so
// they can run either on a pinned connection inside a transaction or standalone.
type sqlConn interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

//go:embed migrations/*.sql
var migrationFS embed.FS

// migration is one embedded .sql file. Files are named NNN_description.sql and
// applied in ascending NNN order.
type migration struct {
	version int
	name    string
	sql     string
}

func (m migration) String() string { return fmt.Sprintf("%03d_%s", m.version, m.name) }

// loadMigrations reads and orders the embedded migrations.
func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("read migrations: %w", err)
	}

	migs := make([]migration, 0, len(entries))
	seen := map[int]string{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		version, name, err := parseMigrationName(e.Name())
		if err != nil {
			return nil, err
		}
		if prev, dup := seen[version]; dup {
			return nil, fmt.Errorf("migrations: version %d claimed by both %s and %s", version, prev, e.Name())
		}
		seen[version] = e.Name()

		body, err := migrationFS.ReadFile("migrations/" + e.Name())
		if err != nil {
			return nil, fmt.Errorf("read migration %s: %w", e.Name(), err)
		}
		migs = append(migs, migration{version: version, name: name, sql: string(body)})
	}

	sort.Slice(migs, func(i, j int) bool { return migs[i].version < migs[j].version })
	return migs, nil
}

// parseMigrationName splits "001_init.sql" into 1 and "init".
func parseMigrationName(filename string) (int, string, error) {
	base := strings.TrimSuffix(filename, ".sql")
	num, name, found := strings.Cut(base, "_")
	if !found {
		return 0, "", fmt.Errorf("migration %q: want NNN_description.sql", filename)
	}
	version, err := strconv.Atoi(num)
	if err != nil {
		return 0, "", fmt.Errorf("migration %q: %q is not a version number", filename, num)
	}
	if version < 1 {
		return 0, "", fmt.Errorf("migration %q: version must be >= 1", filename)
	}
	return version, name, nil
}

// SchemaVersion is the version a freshly migrated database ends up at. It is
// derived from the embedded files so adding 002_*.sql is the only step needed.
func SchemaVersion() (int, error) {
	migs, err := loadMigrations()
	if err != nil {
		return 0, err
	}
	if len(migs) == 0 {
		return 0, nil
	}
	return migs[len(migs)-1].version, nil
}

// migrate brings the database up to the newest embedded migration.
func (db *DB) migrate(ctx context.Context) error {
	migs, err := loadMigrations()
	if err != nil {
		return err
	}
	return db.applyMigrations(ctx, migs)
}

// applyMigrations applies every migration above the recorded version.
//
// The whole run — version table bootstrap, version read and apply loop — happens
// inside one BEGIN IMMEDIATE transaction on a single pinned connection. That is
// what makes concurrent *first* open safe: two processes opening the same brand
// new database both decide what to do while holding the write lock, so the loser
// waits at BEGIN, then reads the version the winner already committed and skips
// the migration instead of colliding with it.
//
// database/sql has no API for the IMMEDIATE variant of BEGIN, which is why this
// drives the transaction with raw statements on a *sql.Conn rather than BeginTx.
// A deferred transaction would take its write lock only at the first write, by
// which point both processes have already read version 0.
func (db *DB) applyMigrations(ctx context.Context, migs []migration) error {
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire connection: %w", err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return fmt.Errorf("begin migration transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			// WithoutCancel: the rollback must still run when ctx is what failed.
			conn.ExecContext(context.WithoutCancel(ctx), `ROLLBACK`) //nolint:errcheck
		}
	}()

	if err := ensureVersionTable(ctx, conn); err != nil {
		return err
	}
	current, err := readVersion(ctx, conn)
	if err != nil {
		return err
	}

	// A migration that fails stops the run, but the ones already applied are
	// kept: each sits in its own savepoint and the recorded version follows it,
	// so the next Open resumes from exactly where this one stopped.
	var applyErr error
	for _, m := range migs {
		if m.version <= current {
			continue
		}
		if err := applyMigration(ctx, conn, m); err != nil {
			applyErr = fmt.Errorf("migration %s: %w", m, err)
			break
		}
	}

	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		return fmt.Errorf("commit migrations: %w", err)
	}
	committed = true
	return applyErr
}

// applyMigration runs one migration inside a savepoint, together with the
// version bump, so a failure leaves neither partial DDL nor a moved version.
func applyMigration(ctx context.Context, conn sqlConn, m migration) error {
	if _, err := conn.ExecContext(ctx, `SAVEPOINT migration`); err != nil {
		return fmt.Errorf("savepoint: %w", err)
	}
	if err := runMigrationBody(ctx, conn, m); err != nil {
		// ROLLBACK TO leaves the savepoint on the stack; RELEASE pops it.
		conn.ExecContext(context.WithoutCancel(ctx), `ROLLBACK TO migration`) //nolint:errcheck
		conn.ExecContext(context.WithoutCancel(ctx), `RELEASE migration`)     //nolint:errcheck
		return err
	}
	if _, err := conn.ExecContext(ctx, `RELEASE migration`); err != nil {
		return fmt.Errorf("release savepoint: %w", err)
	}
	return nil
}

func runMigrationBody(ctx context.Context, conn sqlConn, m migration) error {
	// The driver applies a multi-statement batch up to the first failure, so the
	// surrounding savepoint is what makes a partial migration impossible.
	if _, err := conn.ExecContext(ctx, m.sql); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, `UPDATE schema_version SET version = ? WHERE id = 1`, m.version); err != nil {
		return fmt.Errorf("record version: %w", err)
	}
	return nil
}

// ensureVersionTable creates the single-row schema_version table.
//
// The row is pinned to id = 1 by a CHECK constraint and seeded with INSERT OR
// IGNORE, so two processes opening the same fresh database cannot each insert a
// row — the check-then-insert race the scaffold shipped with.
func ensureVersionTable(ctx context.Context, conn sqlConn) error {
	const ddl = `CREATE TABLE IF NOT EXISTS schema_version (
		id      INTEGER PRIMARY KEY CHECK (id = 1),
		version INTEGER NOT NULL
	)`
	if err := replaceLegacyVersionTable(ctx, conn); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, ddl); err != nil {
		return fmt.Errorf("create schema_version: %w", err)
	}
	if _, err := conn.ExecContext(ctx, `INSERT OR IGNORE INTO schema_version (id, version) VALUES (1, 0)`); err != nil {
		return fmt.Errorf("seed schema_version: %w", err)
	}
	return nil
}

// replaceLegacyVersionTable drops the scaffold's id-less schema_version table.
//
// Those databases are all at version 0 — the scaffold never created a tasks
// table — so there is nothing to preserve. A version above 0 in the old shape
// would mean a database this code does not understand, so refuse rather than
// destroy it.
func replaceLegacyVersionTable(ctx context.Context, conn sqlConn) error {
	exists, hasID, err := versionTableShape(ctx, conn)
	if err != nil || !exists || hasID {
		return err
	}

	var version int
	if err := conn.QueryRowContext(ctx, `SELECT version FROM schema_version LIMIT 1`).Scan(&version); err != nil {
		return fmt.Errorf("read legacy schema_version: %w", err)
	}
	if version != 0 {
		return fmt.Errorf("schema_version has the pre-migration shape but reports version %d; refusing to rewrite it", version)
	}
	if _, err := conn.ExecContext(ctx, `DROP TABLE schema_version`); err != nil {
		return fmt.Errorf("drop legacy schema_version: %w", err)
	}
	return nil
}

func versionTableShape(ctx context.Context, conn sqlConn) (exists, hasID bool, err error) {
	rows, err := conn.QueryContext(ctx, `SELECT name FROM pragma_table_info('schema_version')`)
	if err != nil {
		return false, false, fmt.Errorf("inspect schema_version: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var col string
		if err := rows.Scan(&col); err != nil {
			return false, false, fmt.Errorf("inspect schema_version: %w", err)
		}
		exists = true
		if col == "id" {
			hasID = true
		}
	}
	return exists, hasID, rows.Err()
}

func readVersion(ctx context.Context, conn sqlConn) (int, error) {
	var v int
	if err := conn.QueryRowContext(ctx, `SELECT version FROM schema_version WHERE id = 1`).Scan(&v); err != nil {
		return 0, fmt.Errorf("read schema_version: %w", err)
	}
	return v, nil
}
