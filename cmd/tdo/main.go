// Command tdo is the tmux-native TODO manager. It is a thin shell around
// internal/cli so every command is reachable from tests.
package main

import (
	"os"

	"github.com/agusarias/tmux-todo/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
