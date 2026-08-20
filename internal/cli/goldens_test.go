package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/agusarias/tmux-todo/internal/store"
	"github.com/agusarias/tmux-todo/internal/task"
)

// The golden file is the published --json contract, and it is pinned twice on
// purpose, because neither test alone is enough:
//
//   - TestJSONGoldenBytes marshals hand-built task.Task values, so it is
//     byte-exact and needs no database. It is the one that would catch a renamed
//     field or a reordered key.
//   - TestListJSONMatchesGolden drives Run against a real database whose
//     timestamps were pinned with a raw UPDATE. store.DB.now is unexported, so a
//     cli test cannot freeze the clock any other way — and without this leg the
//     golden would only prove that marshalTasks is stable, not that `tdo list
//     --json` actually calls it.
//
// The golden's scope keys are literals rather than resolved ones so the file is
// not machine-specific; the e2e leg reads with --scope=all for the same reason.

const goldenPath = "testdata/list.json"

// golden fixture timestamps, as unix seconds and their expected RFC3339 form.
const (
	tsSession = 1787227200 // 2026-08-20T12:00:00Z
	tsDir     = 1787223600 // 2026-08-20T11:00:00Z
	tsGlobal  = 1787220000 // 2026-08-20T10:00:00Z
	tsDoneAt  = 1787230800 // 2026-08-20T13:00:00Z
)

// goldenTasks is the fixture in merged-list order: session tier, then dir, then
// global. The session task's text carries quotes, angle brackets and an
// ampersand — the characters Go's default JSON encoder would escape into
// \u003c-style sequences, which would be a silent contract change for anyone
// reading the field.
func goldenTasks() []task.Task {
	doneAt := time.Unix(tsDoneAt, 0)
	return []task.Task{
		{
			ID:        3,
			Text:      `fix "flaky" <test> & retry`,
			Scope:     task.Scope{Kind: task.ScopeSession, Key: "pulsar"},
			CreatedAt: time.Unix(tsSession, 0),
		},
		{
			ID:        2,
			Text:      "update README",
			Scope:     task.Scope{Kind: task.ScopeDir, Key: "/p"},
			CreatedAt: time.Unix(tsDir, 0),
		},
		{
			ID:        1,
			Text:      "call the dentist",
			Done:      true,
			DoneAt:    &doneAt,
			Scope:     task.Scope{Kind: task.ScopeGlobal},
			CreatedAt: time.Unix(tsGlobal, 0),
		},
	}
}

func readGolden(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	return string(body)
}

// failGolden explains what a mismatch means, because the natural reaction to a
// failing golden test is to update the golden file — which is exactly the wrong
// move here unless the change is intended to be breaking.
func failGolden(t *testing.T, got, want string) {
	t.Helper()
	t.Errorf(`--json output does not match %s.

got:  %s
want: %s

This file is a published contract: a user's script parses these field names and
timestamp formats. If the change is deliberate, it is a breaking change and
belongs in a Decisions entry, not a golden-file update.`, goldenPath, got, want)
}

func TestJSONGoldenBytes(t *testing.T) {
	// A local zone nine hours off UTC. If rfc3339 ever loses its .UTC(), the
	// output shifts and this test fails — under plain UTC it would not.
	// time.Local is set directly rather than via TZ: the time package resolves
	// the zone once, so an env var set inside a test may come too late.
	restore := time.Local
	time.Local = time.FixedZone("Asia/Tokyo", 9*60*60)
	t.Cleanup(func() { time.Local = restore })

	got, err := marshalTasks(goldenTasks())
	if err != nil {
		t.Fatalf("marshalTasks: %v", err)
	}
	if want := readGolden(t); string(got) != want {
		failGolden(t, string(got), want)
	}
}

func TestJSONEmptyResultIsAnEmptyArray(t *testing.T) {
	// store.query returns a nil slice when nothing matched, and a nil slice
	// marshals to `null`. Both nil and an empty slice must reach the wire as [].
	for name, tasks := range map[string][]task.Task{
		"nil":   nil,
		"empty": {},
	} {
		got, err := marshalTasks(tasks)
		if err != nil {
			t.Fatalf("%s: marshalTasks: %v", name, err)
		}
		if want := "{\"tasks\":[]}\n"; string(got) != want {
			t.Errorf("%s: got %q, want %q", name, got, want)
		}
	}
}

// TestListJSONMatchesGolden is the end-to-end leg: a real database, the real
// dispatch, the same golden file.
func TestListJSONMatchesGolden(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	ctx := context.Background()
	for _, want := range []task.Task{goldenTasks()[2], goldenTasks()[1], goldenTasks()[0]} {
		got, err := db.Add(ctx, want.Text, want.Scope)
		if err != nil {
			t.Fatalf("Add: %v", err)
		}
		if got.ID != want.ID {
			t.Fatalf("Add assigned id %d, golden expects %d", got.ID, want.ID)
		}
	}
	if err := db.Complete(ctx, 1); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	// db.now is unexported, so the clock cannot be frozen from here. store.DB
	// embeds *sql.DB, which makes pinning the timestamps directly the only way
	// to get a byte-stable comparison.
	for _, pin := range []struct {
		id      int64
		created int64
	}{{1, tsGlobal}, {2, tsDir}, {3, tsSession}} {
		if _, err := db.ExecContext(ctx,
			`UPDATE tasks SET created_at = ? WHERE id = ?`, pin.created, pin.id); err != nil {
			t.Fatalf("pin created_at: %v", err)
		}
	}
	if _, err := db.ExecContext(ctx,
		`UPDATE tasks SET done_at = ? WHERE id = 1`, tsDoneAt); err != nil {
		t.Fatalf("pin done_at: %v", err)
	}
	db.Close()

	// --scope=all so the fixture's literal scope keys are read back regardless of
	// where this test process happens to be running.
	code, stdout, stderr := run(t, "list", "--db", path, "--scope=all", "--all", "--json")
	if code != 0 {
		t.Fatalf("exit code %d, want 0 (stderr: %s)", code, stderr)
	}
	if want := readGolden(t); stdout != want {
		failGolden(t, stdout, want)
	}
}
