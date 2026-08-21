# `closekey-6-quote-in-key-ctrl-quote` Fails On tmux 3.4 (CI Red)

**Status:** in-progress
**Worktree:** ../todo-closekey-quote-probe

## Goal
`make test-plugin` is green on ubuntu's tmux 3.4 as well as the dev machine's 3.7b, without
the quote-in-key cases losing their teeth where the keys *are* bindable. CI run 32510088055
(`main` @ b1d6222) is red at `plugin harness` 186 passed, **2 failed**; everything else in that
run is green.

## Why
This is the only thing keeping `v0.1.0` uncut — `prove-ci-and-cut-v0.1.0` DoD 9 wants the suites
clean on the tagged commit. It is also the fifth instance of this repo's signature failure mode
in reverse: a guard whose *premise* is version-dependent, so it reports a product defect where
there is none. Left alone, the honest fix (a green CI) is indistinguishable from deleting the
case.

## Constraints
- **`test/plugin_install_test.sh` only.** No change to `tmux-todo.tmux`, and no change to the
  four quote-containing key names as *subjects* — `'`, `"`, `M-'` and `C-"` are the keys DoD 3
  of `close-popup-with-same-hotkey` is about.
- **The skip must be earned per key, by asking this tmux**, not by version-sniffing. A
  `tmux -V` comparison encodes today's guess about which release changed the parser; a direct
  `bind-key` probe answers the actual question on whatever tmux is present.
- **The probe is the discriminator, and must keep the real bug catchable.** If tmux binds the
  key but the plugin's install produces no binding, that is the quoting bug the case exists for
  and it must still FAIL. Only "this tmux cannot bind this key name at all" may skip.
- Skips are loud, in the established style: a printed `SKIP` line inside the case and a line in
  the harness header beside `wget`, `python3` and `show-messages`.
- **The whole loop may not silently skip.** If no key in the list is bindable, that is a
  vacuous suite reporting success — it must fail, not skip.
- Out of scope: pushing, and the CI run itself. Those belong to `prove-ci-and-cut-v0.1.0`,
  which is curator-run for exactly this reason.

## Critical surface
None in the product. The surface at risk is **the CI gate itself**: a change here can make the
suite pass by asking less. That is what the mutation proofs in the DoD are for.

## Definition of done
1. `make test-plugin` is green on this machine with **all four** quote-key cases *running*
   (not skipped) — 3.7b binds all four, so a skip here would be the fix hiding the feature.
2. On a tmux that rejects one of those key names, that case prints a `SKIP` naming the key and
   the suite stays green. Exercised by forcing the probe's answer, since 3.4 is not installed
   here.
3. **The real bug stays catchable**: with the probe forced to report "bindable" for a key tmux
   actually rejects, the case FAILS rather than skips. This is the assertion that the skip is
   narrow.
4. **A second mutation**: with `tmux-todo.tmux`'s quote guard removed (so the plugin embeds a
   quote in the bind body and the keybind really does vanish), the case FAILS on a key this
   tmux *can* bind. The skip must not absorb a genuine regression.
5. **No-vacuity guard**: if zero keys in the list are bindable on the running tmux, the suite
   FAILS with a message saying the quote-key coverage was empty.
6. The harness header reports which of the four keys this tmux can bind, so a reader can tell
   from the log what actually ran.
7. `closekey-7` (the server-started-from-config leg, which uses `'`) gets the same treatment, or
   is documented as not needing it with the reason.
8. `make test`, `make lint` clean; `gofmt -l .` empty. `make test-plugin` green with the count
   recorded (188 today).

## Verification
- The full harness locally, showing all four cases running and the total.
- The DoD 2 run with the probe forced negative for one key: `SKIP` printed, suite green, header
  line reflecting it.
- The DoD 3 and DoD 4 mutation runs, with real output.
- The DoD 5 run with the probe forced negative for all four: suite FAILS.
- CI green is **not** part of this task's DoD — it is `prove-ci-and-cut-v0.1.0`'s, because the
  executor does not push.

## Decisions
- **2026-08-21 (curator)** — Probe bindability by asking tmux, never by comparing `tmux -V`. A
  version comparison bakes in today's guess about which release changed the key parser and goes
  stale silently; the probe is correct on every tmux, including ones that do not exist yet.
- **2026-08-21 (curator)** — Measured on 3.7b before writing this: `'`, `"`, `M-'` and `C-"` all
  bind, and `C-lx` is rejected with `unknown key`. So the probe has a working positive control
  *and* a known-negative on this machine, which is what makes DoD 3 testable without tmux 3.4.
- **2026-08-21 (curator)** — The reading that ubuntu's tmux 3.4 rejects `C-"` as a key name is
  **probable but unconfirmed** (3.4 is not installed here). The fix deliberately does not depend
  on it: the probe distinguishes "tmux cannot bind this" from "the plugin failed to bind it" on
  whatever tmux is running, so if the real cause turns out to be the plugin's quoting, DoD 3's
  mutation is the thing that catches it and the case fails as it should.

- **2026-08-21 (executor)** — the probe gets its **own server** (`tdo-keyprobe-$$`) rather than
  binding on each case's server via `tm`, as the plan sketched. One definition then answers both
  the header line and the loop, so the header cannot claim a key ran that did not. Same tmux
  binary either way, so nothing is given up; the probe table is still private
  (`tdo-keyprobe`) and still unbinds after itself.
- **2026-08-21 (executor)** — `tmux_can_bind` **fails open**: if the probe server cannot be
  started at all, the answer is "bindable" and every case runs exactly as it did before this
  guard existed. Failing closed would let a broken probe turn the whole group into silent
  skips — the very failure this task exists to prevent, arriving by a different door. The
  header says so when it happens.
- **2026-08-21 (executor)** — the forced-probe mutations were run by **temporarily editing the
  harness**, not through an env-var override. A `TDO_FORCE_UNBINDABLE`-style backdoor left in
  the shipped file would be a supported way to make CI assert less; the vacuity guard catches
  an all-skip but not a single forced skip, so the backdoor would be a real hole. The edits and
  their output are in Evidence, and the file was diffed back to byte-identical afterwards.
- **2026-08-21 (executor)** — DoD 8's "188 today" is stale: `main` gained the clipboard task's
  two harness assertions between this brief being written and being executed, so the pre-change
  baseline was 196 and the post-change total is 197 (+1 for the new coverage assertion). No
  assertion was removed.

## Plan
Approved at Checkpoint 1, 2026-08-21. One file, `test/plugin_install_test.sh`.

**1. A probe helper.** `tmux_can_bind <key>` binds the key into a scratch key table on the
case's own server and unbinds it, so no real table is polluted and validity is asked of the
running tmux:

```sh
tmux_can_bind() {  # key -> 0 if this tmux accepts the key name
    tm bind-key -T tdo-keyprobe "$1" display-message ok >/dev/null 2>&1 || return 1
    tm unbind-key -T tdo-keyprobe "$1" >/dev/null 2>&1
    return 0
}
```

**2. The loop consults it, and counts what ran.** Inside the `for spec in …` loop, when
`tmux_can_bind "$badkey"` fails, print
`    SKIP this tmux cannot bind the key name "<key>"` and `continue`; otherwise run the case
exactly as today. A `ran=$((ran+1))` counter feeds DoD 5: after the loop,
`[ "$ran" -gt 0 ] || bad "quote-key coverage was empty: no key in the list is bindable here"`.

**3. The header line.** Beside the existing `show-messages :` line, print
`quote keys    : ' " M-' C-"` listing only the bindable ones, so the log says what ran.

**4. `closekey-7`.** Same probe around its `'`, since a tmux that cannot bind `'` would fail it
for the same reason; it is one key rather than a loop, so a plain `if` with a `SKIP` line.

**Sequencing.** Probe helper and header first, then the loop guard, then the three mutation
runs (forced-negative-one, forced-positive-on-an-invalid-key, quote-guard-removed), then the
forced-negative-all vacuity run, then the sweep. The mutations come before the sweep because
they are the only evidence that distinguishes this fix from asking less.

**What could go wrong.**
- *The probe polluting a key table the assertions then count.* It binds into `tdo-keyprobe`,
  a table nothing else touches, and unbinds after. `count_plugin_keybinds_for` filters on the
  binding's command containing `display-popup`, so a stray `display-message` bind could not be
  counted anyway — but do not rely on that alone.
- *A skip that hides the regression.* This is the whole risk of the change, which is why DoD 3
  and DoD 4 are both mutations and both required. A run where the case skips is only acceptable
  when tmux itself refused the key.
- *`unbind-key -T` on a nonexistent table* printing to stderr and polluting `plugin_run` output
  comparisons. Redirect both streams, as the surrounding helpers already do.
- *Assuming the cause.* Do not add a `tmux -V` guard "for clarity" alongside the probe: two
  guards for one question is how the next reader learns the wrong reason.

## Evidence

### Scope (Constraints)

```
$ git status --short
 M test/plugin_install_test.sh

$ git diff --stat tmux-todo.tmux
(empty — the plugin script is untouched)
```

The four key names are unchanged as subjects: `QUOTE_KEY_SPECS` holds the same
`' " M-' C-"` the loop had inline, just hoisted so the header and the loop read one list.

### DoD 1 + 6 + 7 — the local run: all four cases *run*, nothing skipped

```
$ make test-plugin
quote keys    : this tmux binds [' " M-' C-"]; unbindable names skip     <- DoD 6
...
== closekey-6-quote-in-key-apostrophe
== closekey-6-quote-in-key-quote
== closekey-6-quote-in-key-alt-apostrophe
== closekey-6-quote-in-key-ctrl-quote
== closekey-6-quote-key-coverage
    ok   4 of 4 quote key names were bindable and actually ran
== closekey-7-server-starts-with-a-quoted-key
    ok   a server whose config sets a quoted @todo-key starts
    ok   and the install warns about the key
    ok   and still binds the popup to it
    ok   with no TDO_POPUP_KEY
----------------------------------------
plugin harness: 197 passed, 0 failed
```

The only `SKIP` in the whole run is the pre-existing `wget=none` one. DoD 7 is satisfied by
giving `closekey-7` the same probe, not by documenting it away — its key is `'`, so a tmux that
cannot bind that name would fail it for the same non-reason.

Probe premise, measured on this tmux before any of it was written:

```
tmux 3.7b
BINDABLE   : [']      BINDABLE   : ["]      BINDABLE   : [M-']    BINDABLE   : [C-"]
REJECTED   : [C-lx] -> unknown key: C-lx
REJECTED   : [F13]  -> unknown key: F13
```

So the probe has a working positive control *and* a known-negative here, which is what makes
DoD 3 testable without tmux 3.4 installed.

### Mutation proofs — the evidence that this is not "asking less"

All four were run. The harness was diffed back to byte-identical afterwards
(`diff <(cat /tmp/harness.bak) test/plugin_install_test.sh` → no output).

**DoD 2 — probe forced negative for one key (`C-"`): skips, stays green.**

```
quote keys    : this tmux binds [' " M-']; unbindable names skip
== closekey-6-quote-in-key-ctrl-quote
    SKIP this tmux cannot bind the key name "C-"" at all, so the plugin
    ok   3 of 4 quote key names were bindable and actually ran
plugin harness: 192 passed, 0 failed
```

The header dropped `C-"` and the coverage line says 3 of 4 — a reader of the log can tell
exactly what ran.

**DoD 3 — a key tmux really rejects, probe forced to say "bindable": FAILS, does not skip.**
`bogus:C-lx` added to the list, `tmux_can_bind` forced to `return 0`:

```
== closekey-6-quote-in-key-bogus
    FAIL the script says why the close key is off (no 'cannot be passed to the popup' in: unknown key: C-lx)
    FAIL the keybind is still installed (want '1', got '0')
    FAIL all four popup branches survive (want '4', got '0')
plugin harness: 199 passed, 3 failed
```

This is the assertion that the skip is *narrow*: only the probe's honest "no" causes one.

**DoD 4 — the plugin's quote guard removed, harness unmutated: FAILS on keys this tmux binds.**

```
== closekey-6-quote-in-key-apostrophe
    FAIL the script says why the close key is off (no 'cannot be passed to the popup' in: )
    FAIL and carry no TDO_POPUP_KEY (want '0', got '4')
== closekey-7-server-starts-with-a-quoted-key
    FAIL and the install warns about the key (no 'cannot be passed to the popup' in: )
    FAIL with no TDO_POPUP_KEY (want '0', got '4')
plugin harness: 187 passed, 10 failed
```

Ten failures, zero skips — the probe answered "bindable" (correctly) and every case ran and
caught the regression. `closekey-7` fails too, so its guard does not absorb one either.

Worth noting from this run: `the keybind is still installed` *passed* for `'` even with the
guard gone — tmux parsed the bind body anyway — so the assertion that actually catches this
regression is the `TDO_POPUP_KEY` count, not the keybind count. The case is only sharp because
it asserts both.

**DoD 5 — probe forced negative for all four: the suite FAILS.**

```
quote keys    : this tmux binds [none]; unbindable names skip
    SKIP this tmux cannot bind the key name "'" at all, so the plugin
    SKIP this tmux cannot bind the key name """ at all, so the plugin
    SKIP this tmux cannot bind the key name "M-'" at all, so the plugin
    SKIP this tmux cannot bind the key name "C-"" at all, so the plugin
== closekey-6-quote-key-coverage
    FAIL quote-key coverage was empty: this tmux bound none of the quote key names, so the whole group was vacuous
plugin harness: 172 passed, 1 failed
```

### DoD 8 — sweep

```
$ make lint
go vet ./...

$ gofmt -l .
(empty)

$ make test
?   	github.com/agusarias/tmux-todo/cmd/tdo	[no test files]
ok  	github.com/agusarias/tmux-todo/internal/cli	0.575s
ok  	github.com/agusarias/tmux-todo/internal/scope	1.226s
ok  	github.com/agusarias/tmux-todo/internal/store	1.180s
ok  	github.com/agusarias/tmux-todo/internal/task	0.379s
ok  	github.com/agusarias/tmux-todo/internal/tui	6.234s

$ make test-plugin
plugin harness: 197 passed, 0 failed
```

Count: **196 before → 197 after**, the +1 being the new `closekey-6-quote-key-coverage`
assertion. No assertion was removed or weakened on this machine. (The brief's "188 today" was
written before the clipboard task merged two harness assertions into `main`; see Decisions.)

### Not verified

- **CI on tmux 3.4 is not proven green** — it cannot be from here, and it is deliberately not
  in this task's DoD. 3.4 is not installed on this machine and the executor does not push, so
  the real run belongs to `prove-ci-and-cut-v0.1.0`. What *is* proven is that whichever key 3.4
  rejects will skip loudly instead of failing, and that a genuine plugin regression still fails
  there (DoD 3 and 4 are both about that distinction, on this tmux).
- **Which of the four keys 3.4 rejects is still unconfirmed**, exactly as the brief's third
  Decision anticipated. The fix does not depend on knowing: if 3.4 turns out to bind all four
  and the CI failure was the plugin's quoting after all, nothing skips and the case fails —
  which is the correct outcome and is what DoD 3's mutation demonstrates.
