.PHONY: all web build run test clean dev install uninstall fmt fmt-check vet lint check hooks dist tag

# Installation prefix for `make install` (override with `make install PREFIX=...`).
PREFIX ?= /usr/local

# Version stamped into the binary (override for releases: `make build VERSION=v0.1.0`).
# Defaults to `git describe` (nearest tag, or short hash before any tag exists).
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT ?= $(shell git rev-parse HEAD 2>/dev/null || echo "")
LDFLAGS := -X main.version=$(VERSION) -X main.commit=$(COMMIT)

all: build

# Build the TypeScript UI into web/dist (embedded by go:embed).
web:
	cd web && npm install --silent && npm run build

# Auto-format Go sources in place.
fmt:
	gofmt -w internal/ cmd/

# Build the self-contained binary (depends on the embedded web assets).
build: hooks web
	go build -ldflags "$(LDFLAGS)" -o fabrika ./cmd/fabrika

# Build + run from the current repo.
run: build
	./fabrika

# Install the binary system-wide (to $(PREFIX)/bin).
install: build
	install -d $(DESTDIR)$(PREFIX)/bin
	install -m 755 fabrika $(DESTDIR)$(PREFIX)/bin/fabrika

# Remove the installed binary.
uninstall:
	rm -f $(DESTDIR)$(PREFIX)/bin/fabrika

test:
	go test ./...

clean:
	rm -f fabrika
	rm -rf web/dist/*
	touch web/dist/.gitkeep

fmt-check:
	@test -z "$$(gofmt -l internal/ cmd/)" || (echo "Go files not formatted:"; gofmt -l internal/ cmd/; exit 1)

vet:
	go vet ./...

lint: fmt-check vet

check: lint
	go test ./...

# Install the committed git hooks (runs gofmt -l on push).
hooks:
	git config core.hooksPath .githooks

# Local dry-run of the full release (archives, .deb, checksums) into dist/ via
# GoReleaser — no tag, no publish. Mirrors exactly what CI does on a tag push.
# Requires goreleaser (brew install goreleaser).
dist:
	goreleaser release --snapshot --clean

# Cut a release: tag the current commit and push it (triggers the release
# workflow). Usage: make tag VERSION=v0.1.0
tag:
	@echo "$(VERSION)" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$$' || (echo "set a semver tag: make tag VERSION=v0.1.0"; exit 1)
	git tag -a $(VERSION) -m "$(VERSION)"
	git push origin $(VERSION)
