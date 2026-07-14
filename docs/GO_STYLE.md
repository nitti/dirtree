# Go Style Rules

These rules are enforced by `.golangci.yml` (`make lint`) where a linter can catch them, and apply by convention otherwise. If a rule here and the linter config disagree, fix the drift in the same change — don't let them diverge silently, per `CLAUDE.md`.

## Baseline

Default to the official Go documentation (https://go.dev/doc/) and [Effective Go](https://go.dev/doc/effective_go) for anything not called out below — naming conventions, formatting, control-flow idioms, interface design, etc. The rules in this file only exist to record project-specific decisions that go beyond, or narrow a choice within, that baseline (e.g. this project's package layering, or a house rule on error wrapping); they are not a replacement for it.

## Layering

- Packages under `internal/tree`, `internal/ignore`, `internal/index`, `internal/match`, `internal/preview`, `internal/layout`, and `internal/spinner` are pure logic: no `tcell`, no direct terminal I/O, no `os.Stdin`/`os.Stdout` reads. They must be unit-testable without a real terminal or pty. This is the load-bearing architectural rule from `CLAUDE.md` — a change that makes one of these packages depend on the terminal layer is a design regression, not a style nit.
- `internal/ui` (tcell draw/input loop) and `internal/watch` (`fsnotify`) are the only packages allowed to touch the terminal or the filesystem-event API directly. They are verified manually, not by unit test, and `errcheck` is relaxed there (see `.golangci.yml`) because deferred `Close()`/`Stop()` calls and best-effort redraws on a closing terminal are expected to fire-and-forget.
- Never pass a `*tree.Node` (or other mutable model type) into the indexer goroutine. `internal/index` communicates with the rest of the program over channels or by copying data, matching the no-shared-mutable-state discipline described in `CLAUDE.md`.

## Errors

- Don't default to `%w`. Wrapping with `fmt.Errorf("...: %w", err)` exposes the wrapped error as part of your function's API — callers can now depend on `errors.Is`/`errors.As` reaching into it, which couples them to an error that may originate in a third-party package you don't control. Follow the decision guidance in https://go.dev/blog/go1.13-errors#whether-to-wrap: use `%w` only when a caller genuinely needs to programmatically inspect the underlying error; use `%v` (adding context without exposing the chain) as the default otherwise.
- Error strings are lowercase, no trailing punctuation, and don't repeat the calling function's name (`revive`'s `error-strings` check enforces this).
- Sentinel/wrapped error values are named `errXxx`; custom error types are named `XxxError` (`error-naming`).
- `cmd/dirtree` is the only package allowed to call `os.Exit`. Everywhere else, return an `error` and let the caller decide.

## Comments and naming

- Every exported package has a package doc comment (`// Package x ...`) on `doc.go` or the primary file, following the existing convention in `cmd/dirtree/main.go`. `main` packages instead lead with `// Command dirtree ...`.
- Exported identifiers get a doc comment starting with the identifier's name, per `revive`'s `exported` check. Unexported identifiers only get a comment when the *why*, not the *what*, is non-obvious (see `CLAUDE.md`'s top-level comment guidance — that applies to Go same as anywhere else).

## Concurrency

- Goroutines started by `internal/index` or `internal/watch` must have an unambiguous shutdown path (a `context.Context`, a `done`/`stop` channel, or equivalent) — no goroutine should be left running past the point its owner considers it stopped.
- Prefer channels over shared state guarded by a mutex when both are workable; when a mutex is genuinely simpler (e.g. protecting a single counter), that's fine — don't force channels where they don't fit.

## Testing

- Table-driven tests are the default shape for pure-logic packages; match the grouping used in `specs/TESTING.md` so coverage against the acceptance checklist is auditable at a glance (`CLAUDE.md`).
- Terminal-rendering code in `internal/ui` is not held to the same unit-test bar; state explicitly in the PR what was manually verified (ideally inside Zellij/tmux) instead.

## Tooling

- `make fmt` / `gofmt -l .` and `make vet` must be clean before commit; CI enforces both.
- `make lint` runs `golangci-lint` using `.golangci.yml` at the repo root; CI enforces this too. Install locally via `go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest` (or your platform's package manager) if it isn't already on `PATH`.
