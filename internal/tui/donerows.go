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
//
// *Which* done rows are grouped there became a setting a day later
// (config.Placement, 2026-08-22): always, never, or — the default — only those
// already done when the popup opened. The default is what makes the list hold
// still while you work through it: a row you complete now does not move, so the
// cursor has nothing to chase and the next row is where you left it.

// belowRule reports whether one row belongs in its tier's bottom block. It is
// how config.Placement reaches this file without this file importing the config
// package or knowing when the popup opened — see Model.belowRule.
//
// It is only ever asked about rows the caller has; a rule that answered true for
// a *pending* row would produce a list nothing else in this package expects, so
// every implementation must start by checking Done.
type belowRule func(task.Task) bool

// partitionDone moves each scope tier's below rows to the end of that tier, most
// recently completed first. Everything else keeps the order it arrived in.
//
// Tiers are read off the rows themselves, as runs of equal Scope.Kind, rather
// than by walking task.ScopeKinds() and filtering: the store already groups
// them, and re-deriving the tier order here would merge a tier that happens to
// have no pending rows into its neighbour.
//
// Which done rows sink is the caller's rule rather than "all of them", because
// the answer is a user setting: always, never, or only the ones that were
// already done when this popup opened (config.Placement). A nil rule means
// nothing sinks, which is config.Never and is also the safe reading of "no rule
// was wired" — the list is then exactly the store's order.
func partitionDone(tasks []task.Task, below belowRule) []task.Task {
	if len(tasks) == 0 || below == nil {
		return tasks
	}
	out := make([]task.Task, 0, len(tasks))
	for start := 0; start < len(tasks); {
		end := start + 1
		for end < len(tasks) && tasks[end].Scope.Kind == tasks[start].Scope.Kind {
			end++
		}
		out = appendTierPartitioned(out, tasks[start:end], below)
		start = end
	}
	return out
}

// appendTierPartitioned appends one tier's rows: the rows that stay, first, in
// their existing order, then the below rows by done_at descending.
func appendTierPartitioned(out []task.Task, tier []task.Task, below belowRule) []task.Task {
	sinking := 0
	for _, t := range tier {
		if below(t) {
			sinking++
		}
	}
	// Nothing to move, and nothing to sort: a tier no row sinks out of keeps the
	// store's slice order untouched. This is the overwhelmingly common case, and
	// under config.Never it is every case.
	//
	// The question asked here is "how many rows are sinking", NOT "how many are
	// pending". Those were the same question while every done row sank, and they
	// stopped being the same the moment a done row could stay put.
	//
	// It is worth being exact about what that costs, because it is less than it
	// looks: this short-circuit is an *optimization*, not a correctness guard. The
	// loop below already keys off `below`, so a tier with nothing sinking comes
	// back untouched whether it short-circuits or falls through — proven by
	// mutation, where restoring the old `pending == len(tier)` condition changes
	// no test's outcome. Asking the same question the body asks is still the right
	// shape: the two cannot drift into disagreeing, and under config.Never every
	// tier now takes the cheap path.
	if sinking == 0 {
		return append(out, tier...)
	}
	// An *all-sinking* tier deliberately does NOT short-circuit, even though
	// nothing changes position: its rows still have to be ordered by done_at, and
	// the first version of this function returned early on that case too — which
	// left an all-done tier in the store's id order and was caught by
	// TestPartitionDoneWithAnAllDoneTier.

	done := make([]task.Task, 0, sinking)
	for _, t := range tier {
		if below(t) {
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
