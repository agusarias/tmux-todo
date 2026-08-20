# tmux-todo (`tdo`)

A tmux-native TODO manager with sesh-style ergonomics: a keybind opens a popup
where you create, complete, delete and re-scope tasks. Tasks carry one of three
independent scopes — `session`, `dir` or `global` — so returning to a session or
a project brings back its pending action items.

```
  tdo

  ▸ ⌘ check CI                 (session: work)
    ⌘ rebase onto main
    · fix auth redirect        (dir: ~/workspace/todo)
    ◉ call the dentist         (global)

  1/2/3 filter · j/k move · space done · q quit
```

The popup stays open across actions — toggle a few tasks, add two more, delete
one, all without reopening it.

## Install

### With TPM

```tmux
set -g @plugin 'agusarias/tmux-todo'
set -g @todo-key 't'   # optional; 't' is the default
```

Then `prefix + I` to install, and `prefix + t` opens the popup.

The plugin needs a `tdo` binary and finds one in this order, stopping at the
first success:

1. `tdo` on your `$PATH` — so your own install always wins
2. `bin/tdo` inside the plugin directory — a build we made earlier
3. otherwise it builds one with `go build`, **once**, if `go` 1.25 or newer is
   available
4. if none of that works, **no keybind is installed** and tmux shows a message
   saying why

Step 4 is deliberate: a keybind pointing at a missing binary looks like the
plugin's fault when you press the key, which is a worse failure than being told
at install time. If you see that message, either put `tdo` on your `$PATH` or run
`make build` in the plugin directory.

Steps 1 and 2 are the steady state, and they do no build and touch no network —
tmux re-runs the plugin script on every server start, so that path stays cheap.

### Without TPM

Build and install the binary, then add the two commands to your config yourself:

```sh
git clone https://github.com/agusarias/tmux-todo
cd tmux-todo
make build && cp bin/tdo ~/.local/bin/   # anywhere on your $PATH
```

```tmux
run-shell '/path/to/tmux-todo/tmux-todo.tmux'
```

`run-shell`-ing the plugin script is all TPM does, so this gets you the same
keybind and the same rename hook. If you would rather not run the script at all,
it is short and readable — copy the `bind-key` and `set-hook` lines out of it and
substitute your own path.

## Configuration

| Option | Default | Meaning |
|---|---|---|
| `@todo-key` | `t` | The key, in the **prefix** table, that opens the popup |

That is the only option. The popup's size is fixed on purpose (≈60% × 60%, never
smaller than 60 × 15): below that floor the popup's own footer starts truncating,
so a user-settable size would silently break the frame.

## Keys in the popup

| Key | Does |
|---|---|
| `j` / `k` | move the cursor |
| `space` | toggle done — the row stays visible, struck through, until you close |
| `a` | add a task on an inline row; `Tab` cycles its scope, `Enter` saves |
| `e` | edit the row's text in place |
| `s` | cycle the row's scope |
| `d` / `u` | delete a row / undo that (until the popup closes) |
| `1` / `2` / `3` | filter to one scope tier |
| `g` | the all-tasks view: every task, grouped by scope |
| `Enter` | in the all-tasks view, jump to that session (and close) |
| `r` / `D` | in the all-tasks view, re-home a group / delete a group |
| `?` | the full keymap, and the version |
| `q` / `Esc` | close |

Deletes are queued and only written when the popup closes, which is what lets
`u` bring a row back with its original id, timestamp and position.

## From the command line

Everything in the popup is reachable non-interactively:

```sh
tdo add "fix flaky test" [--session|--dir|--global]
tdo list [--scope=session|dir|global|all] [--json]
tdo done <id>
tdo rm <id>
tdo count [--pending]
tdo doctor          # schema version, journal mode, task counts
```

`tdo list --json` is a stable contract — an object wrapper, RFC3339 UTC
timestamps and a nested `scope` — so it is safe to script against. Outside tmux
everything works except `session` scope, which does not exist there.

## sesh integration (optional)

In the all-tasks view, `Enter` on a task belonging to a session that is not
running uses [sesh](https://github.com/joshmedeski/sesh) (`sesh connect -s`) to
bring that session back with its directory and startup command. If `sesh` is not
installed, or does not know the name, tdo falls back to `tmux new-session -d` +
`switch-client`. sesh is an enhancement, never a dependency.

## Where your data lives

One SQLite database:

```
$XDG_DATA_HOME/tmux-todo/tasks.db      # if XDG_DATA_HOME is set
~/.local/share/tmux-todo/tasks.db      # otherwise
```

It runs in WAL mode, so `tasks.db-wal` and `tasks.db-shm` sit beside it while a
process has it open; include them in any backup. `tdo doctor` reports the schema
version and journal mode.

Session-scoped tasks are filed under the session **name**, so tdo installs a
`session-renamed` hook that moves them when you rename a session. The hook is
appended (`set-hook -ga`), so your own `session-renamed` hooks keep working.

## Building from source

**Requires Go 1.25 or newer** — that is the floor `modernc.org/sqlite` needs.

```sh
make build        # CGO_ENABLED=0 static binary at ./bin/tdo
make test         # go test ./...
make test-plugin  # the tmux plugin's shell harness (needs a tmux binary)
make lint         # go vet + gofmt check
```

The binary is pure Go and statically linked — no libsqlite3, nothing to install
alongside it.

See `docs/design.md` for the agreed design and `docs/tasks/` for the work queue.
