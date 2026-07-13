# dirtree

`dirtree` is a terminal directory-tree browser: progressive-disclosure navigation, a background-indexed fuzzy-finder jump mode, and a syntax-highlighted file preview pane, all in a single dependency-free binary.

The spec was derived from a working Python/curses prototype (iterated on directly in a terminal, feature by feature, against real usage) and generalized so it can be reimplemented in any language, with no assumption of a particular runtime. The implementation here is Go, using `tcell` for terminal rendering — see "Language and Dependencies" in `CLAUDE.md` for the rationale.

## Goals

- **Single static binary, no required runtime dependencies.** No interpreter, no package manager, no dynamically-loaded libraries needed at runtime. Optional-but-desired features (syntax highlighting, `.gitignore` parsing) must be implemented directly rather than delegated to an external library the binary depends on.
- **Cross-platform terminal UI** that behaves correctly inside terminal multiplexers (Zellij, tmux) that don't always deliver resize signals reliably.
- **Usable as a released artifact** other projects can pull in by version (e.g. a container image build fetching a pinned release binary), not something that only lives copy-pasted inside one repo.

## Contents

- [`specs/SPEC.md`](specs/SPEC.md) — full behavioral specification: CLI, data model, traversal/ignore rules, navigation, background indexing, jump mode, preview pane, layout, rendering conventions, keybindings, resize handling.
- [`specs/TESTING.md`](specs/TESTING.md) — acceptance criteria, expressed as test cases the implementation must satisfy.
- `cmd/dirtree/` — CLI entry point (argument parsing, path resolution, startup error handling).
- `internal/tree/` — the lazily-loaded node model, flattening, and navigation semantics (spec §2, §5).
- `internal/ignore/` — the dependency-free `.gitignore`/`.dirtreeignore` pattern subset (spec §3).
- `internal/index/` — the background full-tree index used by jump mode (spec §6).
- `internal/match/` — the shared substring/glob query-matching rule (spec §7).
- `internal/preview/` — file reading, best-effort syntax highlighting, and line wrapping for the preview pane (spec §8).
- `internal/layout/` — the pure split-view/popup layout math (spec §9).
- `internal/spinner/` — the delayed-loading-indicator timing/frame logic (spec §10).
- `internal/ui/` — the tcell-backed terminal-rendering layer (draw loop, input handling, resize polling); not unit-tested, verified manually in a real terminal.

## Build and run

```
make build          # builds ./dirtree
make run             # builds and runs against .
make run ARGS=/path  # builds and runs against a specific path
make test            # runs the unit test suite
```

Or directly with `go`:

```
go build -o dirtree ./cmd/dirtree
./dirtree [path]
```

## Status

Implemented in Go with `tcell`. The pure-logic layers (`internal/tree`, `internal/ignore`, `internal/index`, `internal/match`, `internal/preview`, `internal/layout`, `internal/spinner`) have automated test coverage per `specs/TESTING.md`. The `internal/ui` terminal-rendering layer has been verified by building the binary and exercising the CLI's error path (non-directory argument); full interactive verification (navigation, jump mode, preview split/popup, resize behavior, escape responsiveness) inside a real terminal/multiplexer session is still outstanding.

## Non-negotiable constraints

- **No required runtime dependencies.**
- **No assumed language or runtime** — `specs/SPEC.md` is phrased in terms of observable behavior, not any particular language's types or library calls.
- **Terminal resize must be handled by polling**, not solely by relying on a resize signal/event — see "Resize handling" in `specs/SPEC.md`.

## License

TBD.
