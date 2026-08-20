package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/agusarias/tmux-todo/internal/task"
)

// openAtV1 creates a database holding only the v1 schema — what a release before
// 002 left on disk. It drives applyMigrations with a filtered list rather than
// hand-writing the v1 DDL, so the fixture cannot drift from 001_init.sql.
func openAtV1(t *testing.T, path string) *DB {
	t.Helper()
	migs, err := loadMigrations()
	if err != nil {
		t.Fatalf("loadMigrations: %v", err)
	}
	v1 := make([]migration, 0, 1)
	for _, m := range migs {
		if m.version <= 1 {
			v1 = append(v1, m)
		}
	}
	if len(v1) != 1 {
		t.Fatalf("expected exactly one migration at or below v1, got %d", len(v1))
	}

	sqlDB, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	db := &DB{DB: sqlDB, path: path, now: time.Now}
	ctx := context.Background()
	if err := db.ensureJournalMode(ctx); err != nil {
		t.Fatalf("ensureJournalMode: %v", err)
	}
	if err := db.applyMigrations(ctx, v1); err != nil {
		t.Fatalf("apply v1: %v", err)
	}
	if v, err := db.Version(ctx); err != nil || v != 1 {
		t.Fatalf("fixture is at version %d (err %v), want 1", v, err)
	}
	return db
}

func session(key string) task.Scope { return task.Scope{Kind: task.ScopeSession, Key: key} }

// TestUpgradeFromV1KeepsTasks is DoD 1: a database written by the previous
// release migrates to 2 and arrives with every task still in it.
func TestUpgradeFromV1KeepsTasks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.db")
	ctx := context.Background()

	old := openAtV1(t, path)
	want := map[string]task.Scope{
		"rebase onto main":  session("pulsar"),
		"fix auth redirect": {Kind: task.ScopeDir, Key: "/ws/pulsar"},
		"call the dentist":  {Kind: task.ScopeGlobal},
	}
	for text, sc := range want {
		if _, err := old.Add(ctx, text, sc); err != nil {
			t.Fatalf("Add(%q): %v", text, err)
		}
	}
	if err := old.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	db := openAt(t, path)
	latest, err := SchemaVersion()
	if err != nil {
		t.Fatalf("SchemaVersion: %v", err)
	}
	if latest != 2 {
		t.Errorf("SchemaVersion() = %d, want 2 now that 002_sessions.sql exists", latest)
	}
	got, err := db.Version(ctx)
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if got != latest {
		t.Fatalf("version after upgrade = %d, want %d", got, latest)
	}

	tasks, err := db.List(ctx, Filter{IncludeDone: true})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(tasks) != len(want) {
		t.Fatalf("%d tasks survived the upgrade, want %d", len(tasks), len(want))
	}
	for _, got := range tasks {
		sc, ok := want[got.Text]
		if !ok {
			t.Errorf("unexpected task %q after upgrade", got.Text)
			continue
		}
		if got.Scope != sc {
			t.Errorf("task %q scope = %+v, want %+v", got.Text, got.Scope, sc)
		}
	}

	// And the new table is usable, not merely present.
	if err := db.RecordSession(ctx, "$0", "pulsar"); err != nil {
		t.Errorf("RecordSession after upgrade: %v", err)
	}
}

// TestConcurrentFirstOpenOfAV1Database races the real 002 upgrade, which is the
// window CLAUDE.md warns about: racing an *already-migrated* database proves
// nothing, because every opener skips the apply loop. Here the migration has
// genuinely not run yet, so one opener applies it while the others wait on the
// outer BEGIN IMMEDIATE and must then find the version already current.
func TestConcurrentFirstOpenOfAV1Database(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.db")
	ctx := context.Background()

	old := openAtV1(t, path)
	if _, err := old.Add(ctx, "survive the race", session("pulsar")); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := old.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	const openers = 8
	start := make(chan struct{})
	errs := make(chan error, openers)
	dbs := make(chan *DB, openers)
	var wg sync.WaitGroup
	for i := 0; i < openers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start // released together, to actually contend
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
		t.Errorf("concurrent upgrade of a v1 database failed: %v", err)
	}
	latest, err := SchemaVersion()
	if err != nil {
		t.Fatalf("SchemaVersion: %v", err)
	}
	opened := 0
	for db := range dbs {
		opened++
		if v, err := db.Version(ctx); err != nil || v != latest {
			t.Errorf("version = %d (err %v), want %d", v, err, latest)
		}
		db.Close()
	}
	if opened != openers {
		t.Errorf("%d of %d openers succeeded", opened, openers)
	}

	db := openAt(t, path)
	tasks, err := db.List(ctx, Filter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("%d tasks after the race, want 1", len(tasks))
	}
}

// TestRecordSessionRoundTrip is DoD 2: the upsert, the read back, and the frozen
// clock stamping updated_at.
func TestRecordSessionRoundTrip(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	at := time.Unix(1_700_000_000, 0)
	freezeClock(db, at)

	if err := db.RecordSession(ctx, "$3", "pulsar"); err != nil {
		t.Fatalf("RecordSession: %v", err)
	}
	name, err := db.SessionName(ctx, "$3")
	if err != nil {
		t.Fatalf("SessionName: %v", err)
	}
	if name != "pulsar" {
		t.Errorf("SessionName = %q, want %q", name, "pulsar")
	}
	if got := sessionStamp(t, db, "$3"); got != at.Unix() {
		t.Errorf("updated_at = %d, want the frozen clock %d", got, at.Unix())
	}

	// The same id again is an update, not a second row and not a conflict.
	later := at.Add(time.Hour)
	freezeClock(db, later)
	if err := db.RecordSession(ctx, "$3", "pulsar-renamed"); err != nil {
		t.Fatalf("RecordSession (upsert): %v", err)
	}
	name, err = db.SessionName(ctx, "$3")
	if err != nil {
		t.Fatalf("SessionName after upsert: %v", err)
	}
	if name != "pulsar-renamed" {
		t.Errorf("SessionName after upsert = %q, want %q", name, "pulsar-renamed")
	}
	if got := sessionStamp(t, db, "$3"); got != later.Unix() {
		t.Errorf("updated_at after upsert = %d, want %d", got, later.Unix())
	}
	var rows int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM sessions`).Scan(&rows); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if rows != 1 {
		t.Errorf("sessions has %d rows after two records of one id, want 1", rows)
	}
}

func sessionStamp(t *testing.T, db *DB, id string) int64 {
	t.Helper()
	var at int64
	if err := db.QueryRowContext(context.Background(),
		`SELECT updated_at FROM sessions WHERE session_id = ?`, id).Scan(&at); err != nil {
		t.Fatalf("read updated_at: %v", err)
	}
	return at
}

func TestSessionNameUnknownIDIsErrNoSession(t *testing.T) {
	db := openTemp(t)
	_, err := db.SessionName(context.Background(), "$99")
	if !errors.Is(err, ErrNoSession) {
		t.Errorf("SessionName for an unknown id: %v, want ErrNoSession", err)
	}
}

func TestRecordSessionRejectsEmptyValues(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	if err := db.RecordSession(ctx, "", "pulsar"); err == nil {
		t.Error("RecordSession accepted an empty id")
	}
	if err := db.RecordSession(ctx, "$1", "  "); err == nil {
		t.Error("RecordSession accepted a blank name")
	}
}

// TestRenameSessionMovesTasks is DoD 3: pending and done rows both move, and no
// other scope is touched.
func TestRenameSessionMovesTasks(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()

	pending := mustAdd(t, db, "rebase onto main", session("old"))
	finished := mustAdd(t, db, "check CI", session("old"))
	if err := db.Complete(ctx, finished.ID); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	other := mustAdd(t, db, "someone else's task", session("untouched"))
	dir := mustAdd(t, db, "fix auth redirect", task.Scope{Kind: task.ScopeDir, Key: "old"})
	global := mustAdd(t, db, "call the dentist", task.Scope{Kind: task.ScopeGlobal})

	moved, err := db.RenameSession(ctx, "old", "new")
	if err != nil {
		t.Fatalf("RenameSession: %v", err)
	}
	if moved != 2 {
		t.Errorf("RenameSession moved %d rows, want 2 (one pending, one done)", moved)
	}
	for _, id := range []int64{pending.ID, finished.ID} {
		if got := scopeOf(t, db, id); got != session("new") {
			t.Errorf("task %d scope = %+v, want %+v", id, got, session("new"))
		}
	}
	// A dir scope whose key happens to equal the old session key is the case a
	// missing scope_kind predicate would break, hence "old" above.
	if got := scopeOf(t, db, dir.ID); got.Kind != task.ScopeDir || got.Key != "old" {
		t.Errorf("dir task scope = %+v, want dir/old untouched", got)
	}
	if got := scopeOf(t, db, other.ID); got != session("untouched") {
		t.Errorf("other session's task scope = %+v, want it untouched", got)
	}
	if got := scopeOf(t, db, global.ID); got.Kind != task.ScopeGlobal || got.Key != "" {
		t.Errorf("global task scope = %+v, want global untouched", got)
	}
}

// TestRenameSessionOntoExistingKeyMerges is DoD 4. docs/design.md:42-43 accepts
// this: "a new session that reuses an old name inherits that name's tasks". The
// test pins the merge rather than guarding against it — including the duplicate
// text case, which reads like a defect and is not one.
func TestRenameSessionOntoExistingKeyMerges(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()

	mustAdd(t, db, "rebase onto main", session("old"))
	mustAdd(t, db, "shared text", session("old"))
	mustAdd(t, db, "already here", session("new"))
	mustAdd(t, db, "shared text", session("new"))

	moved, err := db.RenameSession(ctx, "old", "new")
	if err != nil {
		t.Fatalf("RenameSession: %v", err)
	}
	if moved != 2 {
		t.Errorf("moved %d rows, want 2", moved)
	}

	tasks, err := db.List(ctx, Filter{Scopes: []task.Scope{session("new")}})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(tasks) != 4 {
		t.Fatalf("%d tasks under the new key, want 4 (2 merged in)", len(tasks))
	}
	duplicates := 0
	for _, tk := range tasks {
		if tk.Text == "shared text" {
			duplicates++
		}
	}
	if duplicates != 2 {
		t.Errorf("%d rows read %q, want 2 — the merge keeps both, by design", duplicates, "shared text")
	}
	left, err := db.List(ctx, Filter{Scopes: []task.Scope{session("old")}})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(left) != 0 {
		t.Errorf("%d tasks left under the old key, want 0", len(left))
	}
}

func TestRenameSessionNoOps(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	mustAdd(t, db, "rebase onto main", session("pulsar"))

	// Same key: nothing to do, and not an error.
	moved, err := db.RenameSession(ctx, "pulsar", "pulsar")
	if err != nil || moved != 0 {
		t.Errorf("RenameSession(same key) = %d, %v; want 0, nil", moved, err)
	}
	// No tasks under the old key: still not an error.
	moved, err = db.RenameSession(ctx, "never-used", "whatever")
	if err != nil || moved != 0 {
		t.Errorf("RenameSession(no rows) = %d, %v; want 0, nil", moved, err)
	}
	// Empty keys are a caller bug, not a no-op.
	if _, err := db.RenameSession(ctx, "", "new"); err == nil {
		t.Error("RenameSession accepted an empty old key")
	}
	if _, err := db.RenameSession(ctx, "old", ""); err == nil {
		t.Error("RenameSession accepted an empty new key")
	}
}

func mustAdd(t *testing.T, db *DB, text string, sc task.Scope) task.Task {
	t.Helper()
	tk, err := db.Add(context.Background(), text, sc)
	if err != nil {
		t.Fatalf("Add(%q, %+v): %v", text, sc, err)
	}
	return tk
}

func scopeOf(t *testing.T, db *DB, id int64) task.Scope {
	t.Helper()
	tk, err := db.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("Get(%d): %v", id, err)
	}
	return tk.Scope
}
