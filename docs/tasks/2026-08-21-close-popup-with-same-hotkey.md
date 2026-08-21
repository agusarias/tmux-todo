# Close The Popup With The Same Hotkey That Opens It

**Status:** draft
**Worktree:** none

## Goal
Close the tdo popup window with the same hotkey that opens it.

## Context (as given)

> Related with tmux and tdo. If I want to close the tdo popup window with the same hotkey that opens it, is it a change to do on the tdo code or in the tmux config?

tdo's code, not the tmux config. While a display-popup has focus, keystrokes go straight to the program inside it — the outer client's root-table binding for C-l never fires, and tmux has no popup key table you could bind a "close" key in. So the second press arrives inside the TUI as ctrl+l, and only tdo can act on it.

The minimal change is one line in `internal/tui/tui.go:437`:

```go
case "q", "esc", "ctrl+c", "ctrl+l":
    return m.quit()
```

Two things worth deciding while you're there:

1. **Hardcode vs. follow `@todo-key`.** Hardcoding ctrl+l drifts the moment someone sets a different `@todo-key`. The cleaner seam: have `install_keybind` in `tmux-todo.tmux` pass the key down — `display-popup -E -e TDO_POPUP_KEY='$key' ...` (tmux 3.7b here, `-e` is fine) — and have the TUI translate the tmux key name to a Bubble Tea key string (`C-l` → `ctrl+l`, `M-t` → `alt+t`) and add it to the quit set. Unknown/unmappable names just yield no extra quit key, so the popup still closes with q/esc.
2. **Which modes it quits from.** `updateNormal` only covers normal mode. If you press the hotkey while an add/edit input is open, ctrl+l currently does nothing (`updateInput` handles esc/ctrl+c). "Same key toggles" reads as "always closes" to a user, so I'd wire it in `updateInput` and the help/confirm modes too — matching whatever ctrl+c already does there, which for input is discard-and-exit-the-field rather than quit the app. That's the one real design call; the rest is mechanical.
