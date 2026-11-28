SHELL := /bin/bash

.PHONY: check test land

# Everything that must pass before a commit.
check:
	@test -z "$$(gofmt -l .)" || { gofmt -l .; echo "gofmt: the files above need formatting" >&2; exit 1; }
	go vet ./...
	go test ./...

test:
	go test ./...

# Lands main: release-damnit land runs the config's check (make check) on
# exactly what lands and signs its proof, which the release verifies. From a
# worktree: make land BRANCH=<branch>.
land:
	release-damnit land $(BRANCH)
