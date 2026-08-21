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
