SHELL := /bin/bash

.PHONY: check test land

# Everything that must pass before a commit.
check:
	@test -z "$$(gofmt -l .)" || { gofmt -l .; echo "gofmt: the files above need formatting" >&2; exit 1; }
	go vet ./...
	go test ./...

test:
	go test ./...

land:
	release-damnit land
