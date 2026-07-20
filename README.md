# dirtree

`dirtree` is a terminal directory-tree browser: progressive-disclosure navigation in the browser overlay, a background-indexed quick open finder, an in-browser incremental jump-to-file typing mode, a background content-search mode, a syntax-highlighted file preview pane, and live refresh as files change on disk, all in a single dependency-free binary.

The spec was derived from a working Python/curses prototype (iterated on directly in a terminal, feature by feature, against real usage) and generalized so it can be reimplemented in any language, with no assumption of a particular runtime. The implementation here is Go, using `tcell` for terminal rendering — see "Language and Dependencies" in `CLAUDE.md` for the rationale.

## Goals

- **Single static binary, no required runtime dependencies.** No interpreter, no package manager, no dynamically-loaded libraries needed at runtime. Optional-but-desired features (syntax highlighting, `.gitignore` parsing) may use a statically-linked library compiled into the binary (e.g. syntax highlighting uses `chroma`), but must never depend on something present only on the target system at runtime.
- **Cross-platform terminal UI** that behaves correctly inside terminal multiplexers (Zellij, tmux) that don't always deliver resize signals reliably.
- **Usable as a released artifact** other projects can pull in by version (e.g. a container image build fetching a pinned release binary), not something that only lives copy-pasted inside one repo.

## Installation

**macOS or Linux, via Homebrew:**

```
brew install nitti/tap/dirtree
```

This installs from the [`nitti/homebrew-tap`](https://github.com/nitti/homebrew-tap) tap, which is kept up to date automatically on every tagged release.

**Direct download:** grab a prebuilt binary (`darwin`/`linux`, `amd64`/`arm64`) from the [releases page](https://github.com/nitti/dirtree/releases), extract it, and put `dirtree` on your `PATH`.

**From source:** see "Build and run" below.

## Contents

- [`specs/SPEC.md`](specs/SPEC.md) — full behavioral specification: CLI, data model, traversal/ignore rules, navigation, background indexing, quick open and jump to file, preview pane, layout, rendering conventions, keybindings, resize handling.
- [`specs/TESTING.md`](specs/TESTING.md) — acceptance criteria, expressed as test cases the implementation must satisfy.
- [`docs/GO_STYLE.md`](docs/GO_STYLE.md) — Go-specific style and architecture rules (layering, error conventions, concurrency, tooling), enforced via `.golangci.yml` and `make lint`.
- [`docs/ANIMATION_IDEAS.md`](docs/ANIMATION_IDEAS.md) — brainstormed, not-yet-decided animation ideas, checked against the animation principles in `specs/SPEC.md` §5.3.
- [`docs/CHANGELOG.md`](docs/CHANGELOG.md) — chronological development history: each stage, fix, and rework, and what was manually verified at the time.
- `cmd/dirtree/` — CLI entry point (argument parsing, path resolution, startup error handling, `--version`).
- `internal/tree/` — the lazily-loaded node model, flattening, navigation semantics, jump-to-file's visible-row matching (`JumpMatches`), and the refresh/merge logic behind live updates (spec §3.1, §3.4, §4.3, §6.1).
- `internal/ignore/` — the dependency-free `.gitignore`/`.dirtreeignore` pattern subset (spec §3.2).
- `internal/index/` — the background full-tree index used by quick open and content search, including live rebuilds (spec §4.1, §6.1).
- `internal/match/` — quick open's substring/glob query-matching rule and jump to file's simpler leaf-name prefix rule (spec §4.1, §4.3).
- `internal/search/` — the background content-search scan: case-insensitive substring or regex matching, streamed line-by-line against each indexed file's content (no size cap, binary-excluded, bounded per-file timeout and concurrency), cancelable mid-scan when a newer query supersedes it (spec §9).
- `internal/preview/` — file reading (`Load`, distinguishing a failed open from a successful one per spec §2.2), best-effort syntax highlighting, and line wrapping for the preview pane (spec §2.1).
- `internal/find/` — in-file find: case-insensitive substring matching over an already-open file's lines, in rune coordinates that compose with `internal/preview`'s wrapped display rows (spec §2.4).
- `internal/openfiles/` — the open-files list: per-entry scroll/goto/in-file-find state, open/reuse/fail semantics, the list-overlay's remove/reorder operations, and the dropdown overlay's paging/bulk-reorder math (spec §2.2, §2.3, §2.4).
- `internal/spinner/` — the delayed-loading-indicator timing/frame logic, including the minimum-display-duration skip and the full badge decision sequence (spec §5.2).
- `internal/toast/` — the "show in full, then fade left-to-right from an anchor point" timing primitive (spec §5.3) `internal/spinner`'s completion message builds on.
- `internal/watch/` — the `fsnotify`-backed, debounced filesystem-change watcher driving live refresh (spec §6.1); OS-facility-adjacent like `internal/ui`, verified manually.
- `internal/ui/` — the tcell-backed terminal-rendering layer (draw loop, input handling, resize polling, wiring the watcher to tree/index refresh). Not unit-tested; verified manually in a real terminal (see Status below).
- `.goreleaser.yaml` / `.github/workflows/release.yml` — cross-compiles and publishes a GitHub Release plus an updated Homebrew cask on every `vX.Y.Z` tag push.

## Build and run

```
make build          # builds ./dirtree
make run             # builds and runs against .
make run ARGS=/path  # builds and runs against a specific path
make test            # runs the unit test suite
make lint            # runs golangci-lint (see docs/GO_STYLE.md)
```

Or directly with `go`:

```
go build -o dirtree ./cmd/dirtree
./dirtree [path]
```

## Releasing

Push a tag matching `vX.Y.Z` to trigger the release workflow:

```
git tag v0.1.0
git push origin v0.1.0
```

This cross-compiles `darwin`/`linux` × `amd64`/`arm64` binaries, attaches them (plus checksums) to a GitHub Release, and pushes an updated cask formula to `nitti/homebrew-tap`. Requires the `HOMEBREW_TAP_GITHUB_TOKEN` repository secret (a PAT scoped to `contents: write` on the tap repo only).

## Status

The implementation matches `specs/SPEC.md`'s open-files-primary-view design: a running list of open file previews as the primary view, with the browser (§3), an open-files switcher (§2.3), quick open and jump to file (§4), and content search (§9) as overlays reachable from it (§5.1 covers the resulting view model). All of the design's core interactions are implemented and manually verified: navigation and open/close actions across the browser, quick open, jump to file, content search, and the open-files overlay; real preview rendering with syntax highlighting, word-wrapping, scrolling, goto-line, in-file find, and copy mode; live filesystem refresh and live reload of open files; the delayed-loading indexing badge and toast notifications; the too-small-terminal fallback; and narrow-terminal legend behavior.

The pure-logic layers (`internal/tree`, `internal/ignore`, `internal/index`, `internal/match`, `internal/preview`, `internal/openfiles`, `internal/spinner`, `internal/search`, `internal/find`, `internal/toast`) have automated test coverage against every group in `specs/TESTING.md`. `internal/ui` and `internal/watch` are terminal- and OS-facility-adjacent and are instead verified manually in a real terminal (tmux/Zellij), per the testing discipline in `CLAUDE.md`.

For the detailed development history — each stage, fix, and rework, with what was manually verified at the time — see [`docs/CHANGELOG.md`](docs/CHANGELOG.md).

## Non-negotiable constraints

- **No required runtime dependencies.**
- **No assumed language or runtime** — `specs/SPEC.md` is phrased in terms of observable behavior, not any particular language's types or library calls.
- **Terminal resize must be handled by polling**, not solely by relying on a resize signal/event — see "Resize handling" in `specs/SPEC.md`.

## License

MIT — see [`LICENSE`](LICENSE).
