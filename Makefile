BINARY  := tdo
PKG     := github.com/agusarias/tmux-todo
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X $(PKG)/internal/cli.Version=$(VERSION)

export CGO_ENABLED := 0

.PHONY: all build test test-plugin vet fmt fmt-check lint install clean

all: build

build:
	go build -trimpath -ldflags '$(LDFLAGS)' -o bin/$(BINARY) ./cmd/tdo

test:
	go test ./...

# The TPM plugin's shell harness. Separate from `test` on purpose: `test` is
# `go test ./...` and must stay runnable without a tmux server (CLAUDE.md).
# Skips with a message, rather than failing, when tmux is absent.
test-plugin:
	bash test/plugin_install_test.sh

vet:
	go vet ./...

fmt:
	gofmt -w .

fmt-check:
	@out="$$(gofmt -l .)"; \
	if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

lint: vet fmt-check

install:
	go install -trimpath -ldflags '$(LDFLAGS)' ./cmd/tdo

clean:
	rm -rf bin
