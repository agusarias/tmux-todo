package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"

	"github.com/agusarias/tmux-todo/internal/task"
)

// This file is the published output contract, kept on its own so it reads as one
// thing. Once a user's script parses `tdo list --json`, renaming a field or
// changing a timestamp format breaks them silently — there is no version
// negotiation and no deprecation channel. testdata/list.json pins the byte shape
// so a cosmetic edit here fails loudly instead of shipping.
//
// Shape decisions, all deliberate:
//   - An **object wrapper**, not a bare array. An array can never gain a sibling
//     field, so any later metadata (counts, schema version) would be breaking.
//   - **RFC3339 UTC** timestamps, not unix seconds. Unix seconds are unambiguous
//     but unreadable in jq output, and the store's second resolution survives
//     the round trip either way.
//   - A **nested scope object**, so kind and key stay one unit rather than two
//     loosely-related sibling keys.
//   - `done_at` is **null** while pending, never omitted: a consumer can test the
//     field rather than test for the field.

// wireDoc is the top-level object. Tasks is never nil on the wire — see
// marshalTasks.
type wireDoc struct {
	Tasks []wireTask `json:"tasks"`
}

// wireTask is one task. Field order here is the field order on the wire, which
// the golden file records.
type wireTask struct {
	ID        int64     `json:"id"`
	Text      string    `json:"text"`
	Done      bool      `json:"done"`
	DoneAt    *string   `json:"done_at"`
	Scope     wireScope `json:"scope"`
	CreatedAt string    `json:"created_at"`
}

type wireScope struct {
	Kind string `json:"kind"`
	Key  string `json:"key"`
}

// marshalTasks renders tasks as the documented document, with a trailing newline
// so the output composes with shell pipelines.
func marshalTasks(tasks []task.Task) ([]byte, error) {
	// make, not var: store.query returns a nil slice when nothing matched, and a
	// nil slice marshals to `null` — an empty result must be {"tasks":[]}.
	wire := make([]wireTask, 0, len(tasks))
	for _, t := range tasks {
		wire = append(wire, wireTask{
			ID:        t.ID,
			Text:      t.Text,
			Done:      t.Done,
			DoneAt:    wireTime(t.DoneAt),
			Scope:     wireScope{Kind: string(t.Scope.Kind), Key: t.Scope.Key},
			CreatedAt: rfc3339(t.CreatedAt),
		})
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	// Task text is prose the user typed: "fix <head> & retry" must survive as
	// itself rather than as \u003chead\u003e \u0026. Go's default escaping
	// exists for embedding JSON in HTML, which this output never is.
	enc.SetEscapeHTML(false)
	if err := enc.Encode(wireDoc{Tasks: wire}); err != nil {
		return nil, fmt.Errorf("encode tasks: %w", err)
	}
	return buf.Bytes(), nil
}

func wireTime(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := rfc3339(*t)
	return &s
}

// rfc3339 formats in UTC. The .UTC() is load-bearing: without it the output — and
// the golden file — would depend on the machine's timezone.
func rfc3339(t time.Time) string { return t.UTC().Format(time.RFC3339) }
