package store

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
)

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

// applyMigrations applies every migration above the recorded version. Each one
// runs in a single transaction that also bumps schema_version, so a failure
// rolls back the DDL and the version together and the next Open retries from
// the same point.
func (db *DB) applyMigrations(ctx context.Context, migs []migration) error {
	if err := db.ensureVersionTable(ctx); err != nil {
		return err
	}
	current, err := db.Version(ctx)
	if err != nil {
		return err
	}
	for _, m := range migs {
		if m.version <= current {
			continue
		}
		if err := db.applyMigration(ctx, m); err != nil {
			return fmt.Errorf("migration %s: %w", m, err)
		}
	}
	return nil
}

func (db *DB) applyMigration(ctx context.Context, m migration) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op once committed

	// The driver applies a multi-statement batch up to the first failure, so
	// the surrounding transaction is what makes a partial migration impossible.
	if _, err := tx.ExecContext(ctx, m.sql); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE schema_version SET version = ? WHERE id = 1`, m.version); err != nil {
		return fmt.Errorf("record version: %w", err)
	}
	return tx.Commit()
}

// ensureVersionTable creates the single-row schema_version table.
//
// The row is pinned to id = 1 by a CHECK constraint and seeded with INSERT OR
// IGNORE, so two processes opening the same fresh database cannot each insert a
// row — the check-then-insert race the scaffold shipped with.
func (db *DB) ensureVersionTable(ctx context.Context) error {
	const ddl = `CREATE TABLE IF NOT EXISTS schema_version (
		id      INTEGER PRIMARY KEY CHECK (id = 1),
		version INTEGER NOT NULL
	)`
	if err := db.replaceLegacyVersionTable(ctx); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, ddl); err != nil {
		return fmt.Errorf("create schema_version: %w", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT OR IGNORE INTO schema_version (id, version) VALUES (1, 0)`); err != nil {
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
func (db *DB) replaceLegacyVersionTable(ctx context.Context) error {
	exists, hasID, err := db.versionTableShape(ctx)
	if err != nil || !exists || hasID {
		return err
	}

	var version int
	if err := db.QueryRowContext(ctx, `SELECT version FROM schema_version LIMIT 1`).Scan(&version); err != nil {
		return fmt.Errorf("read legacy schema_version: %w", err)
	}
	if version != 0 {
		return fmt.Errorf("schema_version has the pre-migration shape but reports version %d; refusing to rewrite it", version)
	}
	if _, err := db.ExecContext(ctx, `DROP TABLE schema_version`); err != nil {
		return fmt.Errorf("drop legacy schema_version: %w", err)
	}
	return nil
}

func (db *DB) versionTableShape(ctx context.Context) (exists, hasID bool, err error) {
	rows, err := db.QueryContext(ctx, `SELECT name FROM pragma_table_info('schema_version')`)
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
