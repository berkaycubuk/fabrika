.PHONY: all web build run test clean dev install uninstall fmt-check vet lint check hooks dist tag

# Installation prefix for `make install` (override with `make install PREFIX=...`).
PREFIX ?= /usr/local

# Version stamped into the binary (override for releases: `make build VERSION=v0.1.0`).
# Defaults to `git describe` (nearest tag, or short hash before any tag exists).
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X main.version=$(VERSION)

all: build

# Build the TypeScript UI into web/dist (embedded by go:embed).
web:
	cd web && npm install --silent && npm run build

# Build the self-contained binary (depends on the embedded web assets).
build: web
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

# Platforms to cross-compile for `make dist` (os/arch).
PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64

# Cross-compile release archives into dist/. The UI is built once (web/dist is
# embedded into every platform binary), then pure-Go sqlite lets CGO_ENABLED=0
# cross-compile each target without a C toolchain.
dist: web
	rm -rf dist && mkdir -p dist
	@for platform in $(PLATFORMS); do \
		os=$${platform%/*}; arch=$${platform#*/}; \
		echo "building $$os/$$arch ($(VERSION))..."; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch \
			go build -ldflags "$(LDFLAGS)" -o dist/fabrika ./cmd/fabrika || exit 1; \
		tar -czf dist/fabrika_$(VERSION)_$${os}_$${arch}.tar.gz -C dist fabrika; \
		rm dist/fabrika; \
	done
	cd dist && shasum -a 256 *.tar.gz > checksums.txt
	@echo "release archives in dist/:" && ls dist/

# Cut a release: tag the current commit and push it (triggers the release
# workflow). Usage: make tag VERSION=v0.1.0
tag:
	@echo "$(VERSION)" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+' || (echo "set a semver tag: make tag VERSION=v0.1.0"; exit 1)
	git tag -a $(VERSION) -m "$(VERSION)"
	git push origin $(VERSION)
