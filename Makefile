.PHONY: all web build run test clean dev

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

test:
	go test ./...

clean:
	rm -f fabrika
	rm -rf web/dist/*
	touch web/dist/.gitkeep
