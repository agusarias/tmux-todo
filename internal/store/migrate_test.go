package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/agusarias/tmux-todo/internal/task"
)

func TestOpenMigratesToLatest(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()

	latest, err := SchemaVersion()
	if err != nil {
		t.Fatalf("SchemaVersion: %v", err)
	}
	if latest < 1 {
		t.Fatalf("SchemaVersion = %d, want at least 1", latest)
	}

	got, err := db.Version(ctx)
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if got != latest {
		t.Errorf("version after Open = %d, want %d", got, latest)
	}

	// The v1 table exists with exactly the design's column set.
	rows, err := db.QueryContext(ctx, `SELECT name FROM pragma_table_info('tasks')`)
	if err != nil {
		t.Fatalf("inspect tasks: %v", err)
	}
	defer rows.Close()
	var cols []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			t.Fatalf("scan column: %v", err)
		}
		cols = append(cols, c)
	}
	want := "id,text,done,done_at,scope_kind,scope_key,created_at"
	if got := strings.Join(cols, ","); got != want {
		t.Errorf("tasks columns = %q, want %q", got, want)
	}
}

// TestReopenIsANoOp is DoD 3: migrating an already-migrated database changes
// neither the version nor the data.
func TestReopenIsANoOp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.db")
	ctx := context.Background()

	first, err := Open(path)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	for _, text := range []string{"one", "two", "three"} {
		if _, err := first.Add(ctx, text, task.Scope{Kind: task.ScopeGlobal}); err != nil {
			t.Fatalf("Add: %v", err)
		}
	}
	versionBefore, err := first.Version(ctx)
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	countBefore, err := first.Count(ctx, Filter{IncludeDone: true})
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	first.Close()

	second := openAt(t, path)
	versionAfter, err := second.Version(ctx)
	if err != nil {
		t.Fatalf("Version after reopen: %v", err)
	}
	countAfter, err := second.Count(ctx, Filter{IncludeDone: true})
	if err != nil {
		t.Fatalf("Count after reopen: %v", err)
	}

	if versionAfter != versionBefore {
		t.Errorf("version changed on reopen: %d -> %d", versionBefore, versionAfter)
	}
	if countAfter != countBefore || countAfter != 3 {
		t.Errorf("row count changed on reopen: %d -> %d (want 3)", countBefore, countAfter)
	}
}

// TestFailedMigrationRollsBack is DoD 2: a migration that fails partway leaves
// no DDL behind and does not move the recorded version.
func TestFailedMigrationRollsBack(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()

	before, err := db.Version(ctx)
	if err != nil {
		t.Fatalf("Version: %v", err)
	}

	bad := migration{
		version: before + 1,
		name:    "broken",
		sql: `CREATE TABLE partial_a (x);
		      CREATE TABLE definitely_not_sql ((( ;`,
	}
	err = db.applyMigrations(ctx, []migration{bad})
	if err == nil {
		t.Fatal("applyMigrations returned no error for broken SQL")
	}
	if !strings.Contains(err.Error(), bad.String()) {
		t.Errorf("error does not name the migration: %v", err)
	}

	after, err := db.Version(ctx)
	if err != nil {
		t.Fatalf("Version after failure: %v", err)
	}
	if after != before {
		t.Errorf("version moved despite failure: %d -> %d", before, after)
	}

	// The first statement of the batch succeeds before the second one fails, so
	// this asserts the transaction actually rolled it back.
	var n int
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM sqlite_master WHERE name = 'partial_a'`).Scan(&n); err != nil {
		t.Fatalf("inspect sqlite_master: %v", err)
	}
	if n != 0 {
		t.Error("partial DDL survived the failed migration")
	}
}

// TestSchemaVersionIsStructurallySingleRow is DoD 4: the check-then-insert race
// the scaffold shipped with is impossible now, because a second row cannot exist.
func TestSchemaVersionIsStructurallySingleRow(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()

	if _, err := db.ExecContext(ctx, `INSERT INTO schema_version (id, version) VALUES (2, 9)`); err == nil {
		t.Error("inserting a second schema_version row succeeded")
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO schema_version (id, version) VALUES (1, 9)`); err == nil {
		t.Error("inserting a duplicate id = 1 row succeeded")
	}
	// Re-running the seed is what a concurrent second process would do.
	if _, err := db.ExecContext(ctx,
		`INSERT OR IGNORE INTO schema_version (id, version) VALUES (1, 0)`); err != nil {
		t.Fatalf("re-seed: %v", err)
	}

	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM schema_version`).Scan(&n); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if n != 1 {
		t.Errorf("schema_version holds %d rows, want 1", n)
	}
}

// TestLegacyVersionTableIsReplaced covers databases the scaffold created: an
// id-less schema_version at version 0, which is disposable by decision.
func TestLegacyVersionTableIsReplaced(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	writeLegacyDB(t, path, 0)

	db := openAt(t, path)
	ctx := context.Background()

	latest, err := SchemaVersion()
	if err != nil {
		t.Fatalf("SchemaVersion: %v", err)
	}
	got, err := db.Version(ctx)
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if got != latest {
		t.Errorf("version = %d, want %d", got, latest)
	}
	if _, err := db.Add(ctx, "works", task.Scope{Kind: task.ScopeGlobal}); err != nil {
		t.Errorf("Add against upgraded database: %v", err)
	}
}

// TestLegacyVersionTableWithDataIsRefused: an id-less table above version 0 is a
// database this code does not understand, so Open must refuse rather than drop it.
func TestLegacyVersionTableWithDataIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	writeLegacyDB(t, path, 4)

	db, err := Open(path)
	if err == nil {
		db.Close()
		t.Fatal("Open accepted a legacy schema_version at version 4")
	}
	if !strings.Contains(err.Error(), "refusing") {
		t.Errorf("error should explain the refusal, got: %v", err)
	}
}

// writeLegacyDB creates the scaffold's original schema_version shape.
func writeLegacyDB(t *testing.T, path string, version int) {
	t.Helper()
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}
	defer raw.Close()
	if _, err := raw.Exec(`CREATE TABLE schema_version (version INTEGER NOT NULL)`); err != nil {
		t.Fatalf("create legacy table: %v", err)
	}
	if _, err := raw.Exec(`INSERT INTO schema_version (version) VALUES (?)`, version); err != nil {
		t.Fatalf("seed legacy table: %v", err)
	}
}

func TestParseMigrationName(t *testing.T) {
	for _, tc := range []struct {
		file    string
		version int
		name    string
		wantErr bool
	}{
		{file: "001_init.sql", version: 1, name: "init"},
		{file: "012_add_tags.sql", version: 12, name: "add_tags"},
		{file: "init.sql", wantErr: true},
		{file: "abc_init.sql", wantErr: true},
		{file: "000_zero.sql", wantErr: true},
	} {
		version, name, err := parseMigrationName(tc.file)
		if tc.wantErr {
			if err == nil {
				t.Errorf("%s: want error, got %d/%q", tc.file, version, name)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", tc.file, err)
			continue
		}
		if version != tc.version || name != tc.name {
			t.Errorf("%s: got %d/%q, want %d/%q", tc.file, version, name, tc.version, tc.name)
		}
	}
}

func TestLoadMigrationsIsOrdered(t *testing.T) {
	migs, err := loadMigrations()
	if err != nil {
		t.Fatalf("loadMigrations: %v", err)
	}
	if len(migs) == 0 {
		t.Fatal("no migrations embedded")
	}
	for i := range migs {
		if migs[i].sql == "" {
			t.Errorf("migration %s has empty SQL", migs[i])
		}
		if i > 0 && migs[i-1].version >= migs[i].version {
			t.Errorf("migrations out of order: %s before %s", migs[i-1], migs[i])
		}
	}
}

// TestConcurrentFirstOpen is DoD 13: several processes opening the same brand
// new database must all succeed. This is the case DoD 9 misses — that test races
// two handles against a database the first Open already migrated, so the
// migration itself never runs concurrently.
func TestConcurrentFirstOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.db")
	const openers = 8

	start := make(chan struct{})
	errs := make(chan error, openers)
	dbs := make(chan *DB, openers)
	var wg sync.WaitGroup

	for i := 0; i < openers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start // release them together, to actually contend
			db, err := Open(path)
			if err != nil {
				errs <- err
				return
			}
			dbs <- db
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	close(dbs)

	for err := range errs {
		t.Errorf("concurrent first Open failed: %v", err)
	}

	ctx := context.Background()
	latest, err := SchemaVersion()
	if err != nil {
		t.Fatalf("SchemaVersion: %v", err)
	}
	var opened int
	for db := range dbs {
		opened++
		v, err := db.Version(ctx)
		if err != nil {
			t.Errorf("Version: %v", err)
		}
		if v != latest {
			t.Errorf("version = %d, want %d", v, latest)
		}
		db.Close()
	}
	if opened != openers {
		t.Errorf("%d of %d openers succeeded", opened, openers)
	}
}

// TestConcurrentUpgradeAppliesOnce covers the other window DoD 13 names: the
// first opens after a release that adds 002_*.sql. Several handles race to apply
// the same new migration; exactly one may win, and the losers must skip it
// rather than collide with its DDL.
func TestConcurrentUpgradeAppliesOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.db")
	ctx := context.Background()

	const handles = 4
	dbs := make([]*DB, handles)
	for i := range dbs {
		dbs[i] = openAt(t, path)
	}

	base, err := SchemaVersion()
	if err != nil {
		t.Fatalf("SchemaVersion: %v", err)
	}
	next := []migration{{
		version: base + 1,
		name:    "add_widgets",
		sql:     `CREATE TABLE widgets (id INTEGER PRIMARY KEY, label TEXT NOT NULL);`,
	}}

	start := make(chan struct{})
	errs := make(chan error, handles)
	var wg sync.WaitGroup
	for _, db := range dbs {
		wg.Add(1)
		go func(db *DB) {
			defer wg.Done()
			<-start
			if err := db.applyMigrations(ctx, next); err != nil {
				errs <- err
			}
		}(db)
	}
	close(start)
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent upgrade failed: %v", err)
	}

	v, err := dbs[0].Version(ctx)
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if v != base+1 {
		t.Errorf("version = %d, want %d", v, base+1)
	}
	var tables int
	if err := dbs[0].QueryRowContext(ctx,
		`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'widgets'`).Scan(&tables); err != nil {
		t.Fatalf("inspect sqlite_master: %v", err)
	}
	if tables != 1 {
		t.Errorf("widgets table count = %d, want 1", tables)
	}
}

// TestDSNCarriesConnectionPragmasOnly pins the split that makes concurrent first
// open safe: per-connection settings belong on the DSN, but journal_mode is a
// property of the file and must not be re-requested by every pooled connection —
// SQLite answers that with SQLITE_BUSY without consulting busy_timeout.
func TestDSNCarriesConnectionPragmasOnly(t *testing.T) {
	got := dsn("/tmp/example/tasks.db")
	busy := strings.Index(got, "busy_timeout")
	if busy < 0 {
		t.Fatalf("dsn is missing busy_timeout: %s", got)
	}
	if fk := strings.Index(got, "foreign_keys"); fk < 0 {
		t.Fatalf("dsn is missing foreign_keys: %s", got)
	} else if busy > fk {
		t.Errorf("busy_timeout should come first so the wait is in force: %s", got)
	}
	if strings.Contains(got, "journal_mode") {
		t.Errorf("journal_mode must not be a DSN pragma, it is set once in Open: %s", got)
	}
}
