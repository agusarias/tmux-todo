package tui

import (
	"sort"

	"github.com/agusarias/tmux-todo/internal/task"
)

// Where completed rows sit in the list.
//
// The store hands back tier-ordered, newest-first rows and knows nothing about
// completion as a *layout* concern — `tdo list` shares that ORDER BY, so
// sorting there would silently reorder a published command's output as a side
// effect of a popup decision. The re-order therefore lives here, as a pure
// function over a slice, and `internal/cli/testdata/list.json` is the tripwire
// that says so.
//
// The shape is the user's call (2026-08-21): a day's completions grouped at the
// end of their tier rather than left inline. With 13 body rows at the design's
// 60x15 floor, inline done rows would push "what is left" off the top of the
// list, which is the one thing the popup exists to show. There is deliberately
// no separator row between the two blocks — strikethrough and position are the
// signal, and a separator would cost one of those 13 rows per tier.

// partitionDone moves each scope tier's done rows to the end of that tier, most
// recently completed first. Everything else keeps the order it arrived in.
//
// Tiers are read off the rows themselves, as runs of equal Scope.Kind, rather
// than by walking task.ScopeKinds() and filtering: the store already groups
// them, and re-deriving the tier order here would merge a tier that happens to
// have no pending rows into its neighbour.
func partitionDone(tasks []task.Task) []task.Task {
	if len(tasks) == 0 {
		return tasks
	}
	out := make([]task.Task, 0, len(tasks))
	for start := 0; start < len(tasks); {
		end := start + 1
		for end < len(tasks) && tasks[end].Scope.Kind == tasks[start].Scope.Kind {
			end++
		}
		out = appendTierPartitioned(out, tasks[start:end])
		start = end
	}
	return out
}

// appendTierPartitioned appends one tier's rows: pending first in their existing
// order, then done rows by done_at descending.
func appendTierPartitioned(out []task.Task, tier []task.Task) []task.Task {
	pending := 0
	for _, t := range tier {
		if !t.Done {
			pending++
		}
	}
	// Nothing to move, and nothing to sort: a tier with no done rows keeps the
	// store's slice order untouched. This is the overwhelmingly common case.
	//
	// An *all-done* tier deliberately does NOT short-circuit here, even though
	// nothing moves: its rows still have to be ordered by done_at, and the first
	// version of this function returned early on `pending == len(tier)` as well
	// — which left an all-done tier in the store's id order and was caught by
	// TestPartitionDoneWithAnAllDoneTier.
	if pending == len(tier) {
		return append(out, tier...)
	}

	done := make([]task.Task, 0, len(tier)-pending)
	for _, t := range tier {
		if t.Done {
			done = append(done, t)
			continue
		}
		out = append(out, t)
	}
	// Stable, so rows this cannot order — see completedAfter on a nil done_at —
	// keep the store's newest-first sequence rather than an arbitrary one.
	sort.SliceStable(done, func(i, j int) bool { return completedAfter(done[i], done[j]) })
	return append(out, done...)
}

// completedAfter reports whether a was completed more recently than b.
//
// A done row with a nil done_at sorts *last* and never swaps with another nil.
// Nothing in the current code path produces one — store.Complete always stamps
// it, and the query's DoneSince bound would exclude a NULL anyway — but the
// field is a pointer, and a comparison that dereferenced it would turn a stale
// row written by some older path into a panic inside the render loop. Sorting it
// to the end of the block is the cheapest honest answer.
func completedAfter(a, b task.Task) bool {
	switch {
	case a.DoneAt == nil:
		return false
	case b.DoneAt == nil:
		return true
	default:
		return a.DoneAt.After(*b.DoneAt)
	}
}
