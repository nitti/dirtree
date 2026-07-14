# dirtree

`dirtree` is a terminal directory-tree browser: progressive-disclosure navigation, a background-indexed fuzzy-finder jump mode, a syntax-highlighted file preview pane, and live refresh as files change on disk, all in a single dependency-free binary.

The spec was derived from a working Python/curses prototype (iterated on directly in a terminal, feature by feature, against real usage) and generalized so it can be reimplemented in any language, with no assumption of a particular runtime. The implementation here is Go, using `tcell` for terminal rendering — see "Language and Dependencies" in `CLAUDE.md` for the rationale.

## Goals

- **Single static binary, no required runtime dependencies.** No interpreter, no package manager, no dynamically-loaded libraries needed at runtime. Optional-but-desired features (syntax highlighting, `.gitignore` parsing) must be implemented directly rather than delegated to an external library the binary depends on.
- **Cross-platform terminal UI** that behaves correctly inside terminal multiplexers (Zellij, tmux) that don't always deliver resize signals reliably.
- **Usable as a released artifact** other projects can pull in by version (e.g. a container image build fetching a pinned release binary), not something that only lives copy-pasted inside one repo.

## Installation

**macOS or Linux, via Homebrew:**

```
brew install nitti/dirtree/dirtree
```

This installs from the [`nitti/homebrew-dirtree`](https://github.com/nitti/homebrew-dirtree) tap, which is kept up to date automatically on every tagged release.

**Direct download:** grab a prebuilt binary (`darwin`/`linux`, `amd64`/`arm64`) from the [releases page](https://github.com/nitti/dirtree/releases), extract it, and put `dirtree` on your `PATH`.

**From source:** see "Build and run" below.

## Contents

- [`specs/SPEC.md`](specs/SPEC.md) — full behavioral specification: CLI, data model, traversal/ignore rules, navigation, background indexing, jump mode, preview pane, layout, rendering conventions, keybindings, resize handling.
- [`specs/TESTING.md`](specs/TESTING.md) — acceptance criteria, expressed as test cases the implementation must satisfy.
- [`docs/GO_STYLE.md`](docs/GO_STYLE.md) — Go-specific style and architecture rules (layering, error conventions, concurrency, tooling), enforced via `.golangci.yml` and `make lint`.
- `cmd/dirtree/` — CLI entry point (argument parsing, path resolution, startup error handling, `--version`).
- `internal/tree/` — the lazily-loaded node model, flattening, navigation semantics, and the refresh/merge logic behind live updates (spec §3.1, §3.4, §6.1).
- `internal/ignore/` — the dependency-free `.gitignore`/`.dirtreeignore` pattern subset (spec §3.2).
- `internal/index/` — the background full-tree index used by jump mode, including live rebuilds (spec §4.1, §6.1).
- `internal/match/` — the shared substring/glob query-matching rule (spec §4.2).
- `internal/preview/` — file reading (`Load`, distinguishing a failed open from a successful one per spec §2.2), best-effort syntax highlighting, and line wrapping for the preview pane (spec §2.1).
- `internal/openfiles/` — the open-files list: per-entry scroll/goto state, open/reuse/fail semantics, and the list-overlay's remove/reorder operations (spec §2.2, §2.3).
- `internal/layout/` — the pure split-view/popup layout math (spec §5.1).
- `internal/spinner/` — the delayed-loading-indicator timing/frame logic, including the minimum-display-duration skip and the full badge decision sequence (spec §5.2).
- `internal/watch/` — the `fsnotify`-backed, debounced filesystem-change watcher driving live refresh (spec §6.1); OS-facility-adjacent like `internal/ui`, verified manually.
- `internal/ui/` — the tcell-backed terminal-rendering layer (draw loop, input handling, resize polling, wiring the watcher to tree/index refresh). Being rebuilt in stages against the new spec (see Status below); not unit-tested, verified manually in a real terminal.
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

This cross-compiles `darwin`/`linux` × `amd64`/`arm64` binaries, attaches them (plus checksums) to a GitHub Release, and pushes an updated cask formula to `nitti/homebrew-dirtree`. Requires the `HOMEBREW_TAP_GITHUB_TOKEN` repository secret (a PAT scoped to `contents: write` on the tap repo only).

## Status

`specs/SPEC.md` was redrafted around a different primary-view model than the original implementation: a running list of open file previews as the primary view, with the tree browser (§3), an open-files switcher (§2.3), and a unified jump/fuzzy-picker (§4) all becoming overlays reachable from it (§5.1 covers the resulting view model). The reimplementation against that spec is underway in stages (each stage sized to land as its own reviewable change):

1. **Done.** Core open-files data layer: `internal/openfiles` (open/reuse/fail semantics, remove/reorder), `preview.Load`'s failed-vs-opened result type, and `spinner`'s minimum-display-duration-skip/badge-decision logic — all pure, all unit-tested. The prior `internal/ui` (built around the old "tree explorer is primary" model) was removed and replaced with a placeholder stub so the binary still builds; it does not yet implement the interactive UI.
2. **Done.** Tree explorer overlay wiring: `internal/ui` rebuilt with a real (if simplified — full-width, no split/popup yet) draw loop; the tree explorer auto-opens on startup, supports the full §3.4 navigation set, and Space/`a` open files through `internal/openfiles` with inline failure messaging in the explorer's footer, per §2.2's open-failure signaling.
3. **Done.** Jump/fuzzy-picker overlay wiring: `/` opens it from both the tree explorer and the primary preview view, with the header legend and Enter/Space action mapping flipping per entry point (§4.2); reveal-in-tree expands ancestors and lands back on the tree explorer with the match selected, open-into-list opens through the same `internal/openfiles` path as the tree explorer with the same inline-failure-message behavior. The primary preview view's real content is still stage 5 — it currently renders a placeholder ("`<path>` (`<n>` lines) — preview rendering not yet implemented").
4. **Done.** Open-files list overlay (`Tab` from the primary preview view): lists every open entry in list order as its root-relative path with the displayed entry marked, Enter displays the selection and closes the overlay, `x` removes the selection (auto-closing to the tree-explorer-over-empty-state per §1 if that empties the list), Shift-Up/Shift-Down reorder without wraparound, Escape closes without undoing removals, and an empty list renders a "no open files" message.
5. Primary preview view: per-entry scroll/goto restore, empty state, `Tab`/`e`/`/`/`q` wiring.
6. Layout & rendering: header content per overlay, tree explorer's dual split/popup, badge rendering, gutter/wrap, selection highlight.
7. System behaviors & polish: resize polling, escape timeout, live-refresh integration, manual terminal/multiplexer verification pass.

The pure-logic layers (`internal/tree`, `internal/ignore`, `internal/index`, `internal/match`, `internal/preview`, `internal/openfiles`, `internal/layout`, `internal/spinner`) have automated test coverage. `internal/ui` is hand-verified in a real terminal (tmux) as of stage 4 for what's implemented so far (tree navigation, expand/collapse, Space/`a` open actions, `e` to reopen the explorer, jump mode from both entry points including reveal-in-tree and open-into-list, the open-files overlay including Shift-Up/Shift-Down reordering — which tmux delivered reliably, so no fallback keys were needed — and quit); real preview content is not yet reachable.

## Non-negotiable constraints

- **No required runtime dependencies.**
- **No assumed language or runtime** — `specs/SPEC.md` is phrased in terms of observable behavior, not any particular language's types or library calls.
- **Terminal resize must be handled by polling**, not solely by relying on a resize signal/event — see "Resize handling" in `specs/SPEC.md`.

## License

TBD.
