# `closekey-6-quote-in-key-ctrl-quote` Fails On tmux 3.4 (CI Red)

**Status:** draft
**Worktree:** none

## Goal
`make test-plugin` is green on ubuntu's tmux 3.4, not only on the dev machine's 3.7b. CI run
32510088055 (`main` @ b1d6222) is red: `plugin harness` 186 passed, **2 failed**.

## Context (curator, 2026-08-21)
Both failures are in one case, `closekey-6-quote-in-key-ctrl-quote` (`@todo-key 'C-"'`):

```
== closekey-6-quote-in-key-ctrl-quote
    ok   the script says why the close key is off
    FAIL the keybind is still installed (want '1', got '0')
    FAIL all four popup branches survive (want '4', got '0')
    ok   and carry no TDO_POPUP_KEY
    ok   the tmux server is still answering
```

Runner header: `tmux : /usr/bin/tmux (tmux 3.4)`. The three sibling cases (`'`, `"`, `M-'`)
pass; only `C-"` fails, and it fails by not being bound **at all**.

Reading: this looks like the harness's *premise* being version-dependent rather than a plugin
defect — `C-"` is a bindable key name on 3.7b and apparently not on 3.4, so `bind-key` is
rejected and nothing is installed. The plugin behaved correctly either way (it warns, it
passes no `TDO_POPUP_KEY`, the server survives). Same family as the `show-messages`
capability probe this harness already carries: a question the local tmux cannot answer must
skip loudly, not fail. **Confirm the reading before fixing it** — if 3.4 *can* bind `C-"` and
the plugin's quoting is what breaks, that is a real bug and a different fix.

Blocks `prove-ci-and-cut-v0.1.0` step 6: DoD 9 wants the suites clean on the tagged commit.
