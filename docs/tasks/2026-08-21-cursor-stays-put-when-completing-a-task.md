# Configurable Cursor Behaviour and Done-Row Placement

**Status:** review
**Worktree:** none

## Goal
When completing a task it goes to the bottom, that's ok. The issue is the cursor,
it should stay where it was, not follow the task to the bottom

Grilled into a configuration surface (2026-08-21, user's instruction). Three
user-authored settings, read from an XDG config file:

| Setting | Values | Default |
|---|---|---|
| `follow-on-complete` | `true` / `false` | `false` |
| `follow-on-uncomplete` | `true` / `false` | `true` |
| `complete-to-bottom` | `always` / `never` / `on-start` | `on-start` |

`follow-*` decide whether the cursor chases the row it just toggled or stays at
its screen position. `complete-to-bottom` decides which done rows are grouped at
the end of their scope tier: `always` (today's behaviour), `never` (done rows
always stay inline), or `on-start` — rows already done when the popup opened are
grouped below, rows completed during this popup session stay where they are.

## Why
Completing several tasks in a row is the popup's most common action, and today
each `space` drags the cursor to the bottom of the tier. That is `partitionDone`
(2026-08-21, done-items-visible-for-24h) meeting id-anchoring (2026-08-19,
completed-task-lifecycle DoD 6) — neither task considered the pair.

The settings exist because both halves are taste, not correctness: some people
want a completed row to sink immediately, some want the list to hold still while
they work through it. `on-start` is the default because it makes the list stable
*while you are looking at it* and still tidy every time you arrive.

Note the interaction, which is why the defaults are what they are: under
`on-start` a row you complete does not move at all, so `follow-on-complete` is
unobservable in the default config — and `space` twice still undoes a mis-press.
`follow-on-complete` only bites under `complete-to-bottom: always`.

## Constraints
- **`internal/tui` stays environment-blind.** It never reads the config file;
  `internal/cli` loads it and passes the values in through `tui.Config`, the same
  seam that already carries the store, scopes, home dir and version.
- **`internal/store` and `tdo list` do not change.** Placement is a layout
  decision. `internal/cli/testdata/list.json` must stay byte-identical — it is
  the tripwire that says the store's `ORDER BY` was not touched.
- **A config problem must never stop the popup opening.** An unreadable file, an
  unknown key or a bad value falls back to that setting's default. Same rule as
  the sticky preferences, and for the same reason.
- **...but it must be diagnosable.** Silently ignoring a typo is the failure this
  repo keeps shipping, so `tdo doctor` reports the config path, the effective
  values, and any line it did not understand.
- The user-authored config lives in the XDG **config** dir; the machine-written
  sticky preferences stay in the XDG **state** dir. Different authors, different
  lifecycles, different dirs — `internal/scope/sticky.go` is not the home for this.
- Both views (merged and all-tasks) share `toggleDone`, so they behave the same
  way; the cursor must never come to rest on a group header.
- The other anchored reloads — `a` add, `e` edit, `s`/`r` re-scope, `u`
  un-delete — keep chasing the row they acted on. This task does not touch them.
- **Out of scope, explicitly:** `u` does not learn to undo completion. It was
  discussed and dropped (2026-08-21, user: "forget this, no undo for complete").

## Critical surface
None. No migrations, no auth, no prod data, no external side effects. The one
published contract in the blast radius is `tdo list --json`, and it is only here
as a tripwire: it must not change.

## Definition of done
1. `$XDG_CONFIG_HOME/tmux-todo/config` (falling back to
   `~/.config/tmux-todo/config`) is read at popup start; the three settings take
   the values above, and a missing file yields exactly the defaults.
2. An unreadable file, an unknown key, a malformed line, or a bad value leaves
   that setting at its default and the popup opens normally.
3. `tdo doctor` prints the config path, the three effective values, and any line
   it could not parse.
4. `complete-to-bottom: always` reproduces today's list exactly.
5. `complete-to-bottom: never` leaves every done row inline, including rows
   completed before the popup opened.
6. `complete-to-bottom: on-start`: a row done before the popup opened is grouped
   at the end of its tier; a row completed during this popup session stays at its
   position, and moves below only on the next popup open.
7. With `complete-to-bottom: always` and `follow-on-complete: false`, pressing
   `space` on a mid-list pending row leaves the cursor at that screen position —
   the row that slid up is now selected. With `follow-on-complete: true` the
   cursor lands on the completed row at the tier's end.
8. `follow-on-uncomplete: true` (default) puts the cursor on the row that just
   returned to pending; `false` leaves it at its screen position.
9. Rules 7–8 hold in the all-tasks view too, and the cursor never rests on a
   group header. Completing the last selectable row does not dangle the cursor.
10. `TestCursorReAnchorsWhenRowsShift` still passes: the general reload path is
    still id-anchored, and this task did not weaken it.
11. `internal/cli/testdata/list.json` is byte-identical. `make test` and
    `make lint` green.
12. `docs/design.md` is amended for the new placement rule and the settings, with
    the previous wording quoted, per this repo's amendment convention.

## Verification
- Go tests: a parse table for the new config package; cursor and placement tests
  in `internal/tui`; a wiring test proving `internal/cli` really loads the file
  and passes it through (this repo has shipped two "the seam was never wired"
  bugs — `internal/cli/wiring_test.go` exists for exactly that).
- **Mutation proof for every new guard.** Two specific traps are known in advance:
  - a cursor test written under `complete-to-bottom: on-start` is **vacuous** —
    the row does not move, so the cursor lands on it either way. Every cursor
    assertion must be set under `always`.
  - `TestCursorReAnchorsOnTaskID` currently asserts the *old* behaviour. Under
    the new defaults it would still pass, for the wrong reason. It has to be
    re-pointed at `always` + `follow-on-complete: true` or it becomes a third
    vacuous test in this file's history.
- A headless pane capture (`tmux new-session -d … 'bin/tdo tui'` + `capture-pane`)
  against a seeded temp `$XDG_DATA_HOME` and `$XDG_CONFIG_HOME`, showing the
  cursor's row and the completed row's position before and after `space`, for the
  default config and for `always` + `follow-on-complete: false`.
- `tdo doctor` output against a config file containing a deliberate typo.

## Decisions
- 2026-08-21 — Triage: full stack. Ambiguity was high (the description fixed the
  cursor; the shape of the fix was not implied by it), and it turned out to be a
  new user-facing configuration surface rather than a one-line anchor change.
- 2026-08-21 — Grill Q1, **config surface**: an XDG config file read by
  `tdo tui`, not tmux `@todo-*` options. It works outside tmux, takes effect on
  the next popup open rather than a tmux re-source, and needs no plugin change.
  The tmux-option route would also have had to answer an unverified question —
  whether `display-popup -e` expands `#{...}`, which it does *not* do for
  `-w`/`-h` — since `-e` values are baked into the keybind body at install time.
- 2026-08-21 — Grill Q2, **placement default**: `on-start`, with the user's
  explicit sign-off that this contradicts the `docs/design.md` bullet amended
  one day earlier. design.md is to be amended again, quoting the previous
  wording, exactly as the 24h task did.
- 2026-08-21 — Grill Q3, **`u` undoes completion**: dropped. It was raised only
  because a cursor that leaves the completed row breaks `space`-`space` undo;
  under the `on-start` default the row does not move and that undo still works.
  User: "forget this, no undo for complete." Not split into a follow-up task
  either — deliberately not in the queue.
- 2026-08-21 — `openedAt` returns to the model. The 24h task deleted it because
  it was dead code in the *visibility* rule (`doneSince` took the later of it and
  `now−24h`, so it always won and `store.DoneRetention` never bit). It comes back
  as a **layout** input for `on-start` only. The visibility rule stays "24h since
  completion, full stop" and must not re-acquire an `openedAt` clause.
- 2026-08-21 — The cursor "stays put" by reloading with **anchor 0**, which is
  the existing `reloadCmd` semantics: skip `anchorCursor`, keep the index, clamp.
  Cost, accepted and to be documented: index preservation is vulnerable to
  another pane inserting a row in the window between the write and the re-read,
  where id-anchoring would not be. The window is one store round-trip and the
  failure is a cursor one row off.

## Plan

### Approach

A new `internal/config` package owns the file and nothing else: parse bytes to
three values, no tmux, no store, no filesystem policy beyond finding the path.
`internal/cli` loads it and passes the values into `tui.Config` — the seam that
already carries `DefaultScope`, `AllTasks` and `LiveSessions` — so `internal/tui`
stays environment-blind and every new behaviour is testable by setting a struct
field.

The two behaviours are independent code paths and stay that way:

- **Placement** is `partitionDone`, which grows a predicate: "does this done row
  belong below?" The three modes are three predicates, built in one place in
  `internal/tui` from the setting plus `m.openedAt`.
- **The cursor** is one expression in `toggleDone` choosing the reload's anchor:
  `target.ID` to follow the row, `0` to keep the screen position. Anchor `0` is
  not new machinery — it is exactly what `reloadCmd` already does (skip
  `anchorCursor`, clamp the existing index).

### Files

| File | Change |
|---|---|
| `internal/config/config.go` *(new)* | `Prefs` struct, `Defaults()`, `Parse([]byte) (Prefs, []Problem)`, `Load(path)`, `DefaultPath()`. Named `Prefs`, not `Config`, so `tui.Config` holding it reads sanely. |
| `internal/config/config_test.go` *(new)* | Parse table; `DefaultPath` honours `XDG_CONFIG_HOME`. |
| `internal/tui/tui.go` | Three `Config` fields; `openedAt` back on the Model, stamped in `New()`; `toggleDone` anchor choice; `belowRule()`; the `rowsMsg` handler passes the rule to `partitionDone`. |
| `internal/tui/donerows.go` | `partitionDone(tasks, below func(task.Task) bool)`; fix the tier short-circuit (see risks). |
| `internal/tui/alltasks.go` | `visibleGroups`'s `partitionDone` call site. |
| `internal/cli` | Load the file before `tui.Run`, pass the three values; `doctor` reports path, effective values, unparsed lines. |
| `docs/design.md` | Amend the placement bullet and the `space`-undoes bullet; add a Configuration section. Previous wording quoted, per this repo's convention. |
| tests | Placement (3 modes x before/during), cursor (4 combinations, all under `always`), all-tasks view, wiring, `doctor`. |

### File format

`$XDG_CONFIG_HOME/tmux-todo/config`, falling back to
`~/.config/tmux-todo/config` — the same shape as `Resolver.stateDir()`, one dir
over. One setting per line, `key value` (an optional `=` accepted), `#` comments,
blank lines ignored. Booleans are `true` / `false` only. Anything unrecognised —
unknown key, malformed line, bad value — leaves that setting at its default and
is recorded as a `Problem` for `doctor` to print. `Load` on a missing or
unreadable file returns `Defaults()` and no problems: not having a config file is
the normal case, not a fault.

### Sequencing

1. `internal/config` with its parse table. Pure, no dependencies, red-to-green in
   isolation.
2. `partitionDone`'s predicate + the placement tests. `always` must reproduce
   today's output exactly — the existing `donerows_test.go` cases are the proof,
   re-pointed at the `always` predicate and otherwise untouched.
3. `toggleDone`'s anchor + the cursor tests, **all under `complete-to-bottom:
   always`**, plus re-pointing `TestCursorReAnchorsOnTaskID`.
4. Wire through `internal/cli`; wiring test; `doctor` output.
5. Amend `docs/design.md`.
6. Mutation proofs for every new guard, then the headless pane capture.

### What could go wrong

- **The vacuous-test trap, third time in this file.** Under the `on-start`
  default a completed row does not move, so *every* cursor assertion written
  against the defaults passes with the implementation deleted. All cursor tests
  set `always` explicitly, and each is checked red by mutation before it counts.
  `TestCursorReAnchorsOnTaskID` is the specific landmine: it currently asserts the
  old behaviour and would keep passing under the new defaults for the wrong
  reason.
- **`appendTierPartitioned`'s short-circuit is now asking the wrong question.** It
  returns early when `pending == len(tier)`, counting `!t.Done`. With `on-start` a
  tier can hold done rows that stay *inline*, so the count has to become "rows
  that are not moving" — pending plus done-but-not-below. Getting this wrong
  silently reorders a tier, and the existing all-done-tier case
  (`TestPartitionDoneWithAnAllDoneTier`) does not cover the mixed one.
- **Second-granularity timestamps.** `done_at` and `openedAt` are unix seconds, so
  a row completed in the same second the popup opened compares equal. `on-start`
  uses strictly-before, so such a row stays inline — right for your own keypress,
  arguably wrong for another pane's, harmless either way. Documented, with a test.
- **A done row with a nil `done_at`** cannot be shown to belong to this session,
  so `on-start` groups it below. `completedAfter` already refuses to dereference
  it; the predicate must too.
- **`openedAt` coming back is a re-litigation risk.** It was deleted one day ago
  for being dead code in the visibility rule. Its comment must say plainly that it
  is a layout input and that `doneSince()` is not to consult it again.
- **The index-preservation cost**, accepted in Decisions: another pane inserting a
  row between the write and the re-read leaves the cursor one row off, where
  id-anchoring would not. One store round-trip wide.
- **`list.json`** is checked byte-identical at the end, as the proof no layout
  change leaked into the store.

## Evidence

Branch `cursor-and-placement-config` off `main`. No worktree: clean tree, nothing
else in flight.

### The reported bug, on screen

Headless pane capture (`tmux new-session -d -x 80 -y 20 … 'bin/tdo tui'` +
`capture-pane`) against a seeded temp `$XDG_DATA_HOME`/`$XDG_CONFIG_HOME`. The
XDG vars are set when the **server** starts, since the pane child inherits the
server environment, not a pane's.

`complete-to-bottom always`, `follow-on-complete false` — cursor on the middle
row, then one `space`:

```
   ◉ charlie pending          (global)        ◉ charlie pending          (global)
 ▸ ◉ bravo pending              --space-->  ▸ ◉ alpha pending
   ◉ alpha pending                            ◉ bravo pending
```

The row sank; the cursor held row 2. That is the reported bug fixed.

The same fixture and the same keypress with only `follow-on-complete true`
changed in the file — which is what proves the setting is read end to end by the
shipped binary, not just by a test that injects it:

```
   ◉ charlie pending          (global)        ◉ charlie pending          (global)
 ▸ ◉ bravo pending              --space-->    ◉ alpha pending
   ◉ alpha pending                          ▸ ◉ bravo pending
```

Under the **default** config (no file at all), the same keypress on `bravo`:

```
   ◉ charlie pending          (global)        ◉ charlie pending          (global)
 ▸ ◉ bravo pending              --space-->  ▸ ◉ bravo pending      (struck through)
   ◉ alpha pending                            ◉ alpha pending
   ◉ delta done-before-open                   ◉ delta done-before-open
```

Nothing moved, which is `on-start`: the row was completed during this session.
`delta`, done two hours before the popup opened, is grouped below. The database
reads `bravo pending|1` and `capture-pane -pe` shows `;9m` on bravo's line (the
strikethrough+faint pair — `[9m` alone matches nothing, per CLAUDE.md).

### doctor

```
config   /…/cfg/tmux-todo/config
         complete-to-bottom   always
         follow-on-complete   false
         follow-on-uncomplete true
ok
```

and against a file with two deliberate typos
(`follow-on-complet true` / `follow-on-uncomplete maybe`):

```
         complete-to-bottom   always
         follow-on-complete   false
         follow-on-uncomplete true
         ! line 2: unknown setting: follow-on-complet true
         ! line 3: want true or false: follow-on-uncomplete maybe
```

The good line took effect, the bad ones fell back, and both are named with line
numbers. Pinned by `TestDoctorReportsTheConfig`.

### Suites

```
$ make lint        # go vet ./... + gofmt check
lint rc=0          (gofmt -l reports nothing)

$ make test
ok  github.com/agusarias/tmux-todo/internal/cli     1.296s
ok  github.com/agusarias/tmux-todo/internal/config  0.528s
ok  github.com/agusarias/tmux-todo/internal/scope   1.172s
ok  github.com/agusarias/tmux-todo/internal/store   (cached)
ok  github.com/agusarias/tmux-todo/internal/task    (cached)
ok  github.com/agusarias/tmux-todo/internal/tui     7.812s

$ make test-plugin
plugin harness: 213 passed, 0 failed
```

`internal/cli/testdata/list.json` is byte-identical (`git status` clean for
`internal/cli/testdata/`) — the store's ORDER BY was not touched.

Cold start unchanged: 20 runs of `tdo list` in 0.197s total (~10ms each). The
popup path gains one `os.ReadFile` of a small file and no new subprocess.

### Mutation proofs

Every new guard was run against a broken implementation before it was trusted.

| Mutation | Result |
|---|---|
| `toggleDone` always anchors to `target.ID` (the old behaviour) | **red** — `TestCursorHoldsItsPositionOnComplete`, `…OnUncompleteWhenAsked`, `…InTheAllTasksView` |
| uncomplete reads `FollowOnComplete` instead of its own setting | **red** — `TestCursorHoldsItsPositionOnUncompleteWhenAsked` |
| `partitionDone` partitions by `t.Done` instead of the caller's rule | **red** — `TestPartitionDoneOnStartMixedTier` |
| `runTUI` hard-codes `config.Defaults()` instead of loading the file | **red** — `TestTUIConfigWiring/Prefs` |
| `runTUI` passes a zero `config.Prefs` | **red** — the above plus `TestTUIConfigWiringWithNoConfigFile`, `…WithAnUnparseableConfig` |

### Two findings the plan got wrong, corrected in the tree

1. **The `appendTierPartitioned` short-circuit is not a correctness risk.** The
   plan called it the most likely place to introduce a silent reorder. It is not:
   the loop below it already keys off the rule, so a tier with nothing sinking
   comes back untouched whether it short-circuits or falls through. Restoring the
   old `pending == len(tier)` condition changes **no** test's outcome — verified.
   Counting sinking rows is still the right shape (the two cannot drift into
   disagreeing, and `never` now takes the cheap path), but the comment claiming it
   prevents a bug was false and has been rewritten to say what is true.
2. **`TestPartitionDoneOnStartTierWithNothingSinking` is masked by that
   short-circuit** and is *not* a discriminating guard — break the partition and
   it still passes, because `sinking == 0` returns before the broken loop runs.
   `TestPartitionDoneOnStartMixedTier` is the one that fails. Both stay, and the
   test's own comment now says which is which, in the same shape CLAUDE.md already
   uses for `TestCursorReAnchorsOnTaskID` vs `TestCursorReAnchorsWhenRowsShift`.

### The vacuous-test trap, handled

Under the shipped default a completed row does not move, so **every** cursor
assertion written against the defaults passes with the feature deleted. All of
`cursorfollow_test.go` therefore forces `complete-to-bottom always`, and
`TestFollowSettingsAreVacuousUnderTheDefault` asserts that vacuity so the caution
is a red test rather than a comment nobody re-reads.

`TestCursorReAnchorsOnTaskID` had to be re-pointed at `always` +
`follow-on-complete true` for the same reason — it would otherwise have kept
passing for the wrong reason, which would have been its *third* change of teeth.
