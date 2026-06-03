.PHONY: all web build run test clean dev install uninstall

# Installation prefix for `make install` (override with `make install PREFIX=...`).
PREFIX ?= /usr/local

all: build

# Build the TypeScript UI into web/dist (embedded by go:embed).
web:
	cd web && npm install --silent && npm run build

# Build the self-contained binary (depends on the embedded web assets).
build: web
	go build -o fabrika ./cmd/fabrika

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
