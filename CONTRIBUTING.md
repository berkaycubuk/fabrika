# Contributing to Fabrika

Thanks for your interest in contributing!

## License and the Developer Certificate of Origin

Fabrika is licensed under the [Functional Source License (FSL-1.1-MIT)](LICENSE.md),
under which each release becomes MIT-licensed two years after publication. So that
this future relicensing remains possible, all contributions must be made under the
terms of the [Developer Certificate of Origin (DCO)](https://developercertificate.org/),
and you agree that your contribution is licensed to the project under the same
FSL-1.1-MIT terms (including the future MIT grant).

To certify this, sign off each commit:

```sh
git commit -s
```

This adds a `Signed-off-by: Your Name <you@example.com>` line, which states that you
wrote the change (or have the right to submit it) under the DCO.

## How to contribute

- **Bugs / ideas** — open an issue with reproduction steps or a short rationale.
- **Code** — fork, branch, and open a PR. Before submitting, run:

```sh
make check   # gofmt + go vet + go test ./...
cd web && npx tsc --noEmit   # typecheck the UI
```

- Keep PRs focused — one change per PR merges much faster.
- For larger changes, open an issue first so we can agree on the approach before
  you invest the time.

## Development

See the **Build from source** and **Layout** sections of the [README](README.md),
and [SPECS.md](SPECS.md) for the full design.
