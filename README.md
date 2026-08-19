# tmux-todo (`tdo`)

A tmux-native TODO manager with sesh-style ergonomics: a keybind opens a popup
where you create, complete, delete and re-scope tasks. Tasks carry one of three
independent scopes — `session`, `dir` or `global` — so returning to a session or
a project brings back its pending action items.

**Status: scaffold.** The project layout, toolchain and dependency choices are
settled and every layer is wired end to end, but the real features are not built
yet. See `docs/design.md` for the agreed design and `docs/tasks/` for the queue.

## Build

```sh
make build     # CGO_ENABLED=0 static binary at ./bin/tdo
make test      # go test ./...
make lint      # go vet + gofmt check
./bin/tdo doctor
```

Requires Go 1.23 or newer.
