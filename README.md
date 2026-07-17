# dirtree

`dirtree` is a terminal directory-tree browser: progressive-disclosure navigation in the browser overlay, a background-indexed quick open and jump-to-file finder, a background content-search mode, a syntax-highlighted file preview pane, and live refresh as files change on disk, all in a single dependency-free binary.

The spec was derived from a working Python/curses prototype (iterated on directly in a terminal, feature by feature, against real usage) and generalized so it can be reimplemented in any language, with no assumption of a particular runtime. The implementation here is Go, using `tcell` for terminal rendering — see "Language and Dependencies" in `CLAUDE.md` for the rationale.

## Goals

- **Single static binary, no required runtime dependencies.** No interpreter, no package manager, no dynamically-loaded libraries needed at runtime. Optional-but-desired features (syntax highlighting, `.gitignore` parsing) may use a statically-linked library compiled into the binary (e.g. syntax highlighting uses `chroma`), but must never depend on something present only on the target system at runtime.
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

- [`specs/SPEC.md`](specs/SPEC.md) — full behavioral specification: CLI, data model, traversal/ignore rules, navigation, background indexing, quick open and jump to file, preview pane, layout, rendering conventions, keybindings, resize handling.
- [`specs/TESTING.md`](specs/TESTING.md) — acceptance criteria, expressed as test cases the implementation must satisfy.
- [`docs/GO_STYLE.md`](docs/GO_STYLE.md) — Go-specific style and architecture rules (layering, error conventions, concurrency, tooling), enforced via `.golangci.yml` and `make lint`.
- `cmd/dirtree/` — CLI entry point (argument parsing, path resolution, startup error handling, `--version`).
- `internal/tree/` — the lazily-loaded node model, flattening, navigation semantics, and the refresh/merge logic behind live updates (spec §3.1, §3.4, §6.1).
- `internal/ignore/` — the dependency-free `.gitignore`/`.dirtreeignore` pattern subset (spec §3.2).
- `internal/index/` — the background full-tree index used by quick open and jump to file, including live rebuilds (spec §4.1, §6.1).
- `internal/match/` — the shared substring/glob query-matching rule (spec §4.1).
- `internal/search/` — the background content-search scan: case-insensitive substring matching against each indexed file's (byte-capped, binary-excluded) content, cancelable mid-scan when a newer query supersedes it (spec §9).
- `internal/preview/` — file reading (`Load`, distinguishing a failed open from a successful one per spec §2.2), best-effort syntax highlighting, and line wrapping for the preview pane (spec §2.1).
- `internal/openfiles/` — the open-files list: per-entry scroll/goto state, open/reuse/fail semantics, and the list-overlay's remove/reorder operations (spec §2.2, §2.3).
- `internal/layout/` — the pure split-view/popup layout math (spec §5.1).
- `internal/spinner/` — the delayed-loading-indicator timing/frame logic, including the minimum-display-duration skip and the full badge decision sequence (spec §5.2).
- `internal/watch/` — the `fsnotify`-backed, debounced filesystem-change watcher driving live refresh (spec §6.1); OS-facility-adjacent like `internal/ui`, verified manually.
- `internal/ui/` — the tcell-backed terminal-rendering layer (draw loop, input handling, resize polling, wiring the watcher to tree/index refresh, the browser's split/popup layout). Not unit-tested; verified manually in a real terminal (see Status below).
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

The implementation matches `specs/SPEC.md`'s open-files-primary-view design: a running list of open file previews as the primary view, with the browser (§3), an open-files switcher (§2.3), and quick open plus jump to file (§4) as overlays reachable from it (§5.1 covers the resulting view model). This was rebuilt in seven stages (each landed as its own PR) from an earlier design where the browser was the primary screen, followed by a keybinding/terminology pass that split the original combined jump/fuzzy-picker overlay into the two single-purpose overlays described below and renamed "tree explorer" to "browser" throughout the spec, tests, and code:

1. Core open-files data layer: `internal/openfiles` (open/reuse/fail semantics, remove/reorder), `preview.Load`'s failed-vs-opened result type, and `spinner`'s minimum-display-duration-skip/badge-decision logic.
2. Browser overlay wiring: auto-opens on startup, full §3.4 navigation, Space/`a` open files through `internal/openfiles` with inline failure messaging, per §2.2.
3. Jump/fuzzy-picker overlay wiring from both entry points (superseded — see stage 8): reveal-in-tree and open-into-list, with the header legend and Enter/Space mapping flipping per entry point.
4. Open-files list overlay (`Tab`): list rendering, Enter/`x`/Shift-Up/Shift-Down/Escape (§2.3).
5. Primary preview view: real content rendering (gutter, syntax highlighting, wrapping), scrolling, goto-line, and per-entry state restoration (§2.1).
6. Browser's dual split/popup layout (§5.1), recomputed every frame: split view puts the browser pane on the left with a read-only preview pane on the right; popup view floats a bordered window over the unmodified, full-width preview.
7. System behaviors and a full manual verification pass (resize polling, escape timeout, live filesystem refresh, the spinner badge's full sequence including debug mode, open-failure messaging for binary/permission-denied files).
8. Split stage 3's combined jump/fuzzy-picker overlay into two single-purpose overlays per current `specs/SPEC.md` §4: **quick open** (`O`, toggle, reachable from the primary preview view, opens the selected match into the open-files list) and **jump to file** (`/`, reachable only from within the browser, reveals the selected match in the browser and replaces the browser view while active). `B` (browser) and `O` (quick open) are now both toggles — pressing the same key again closes the overlay it opened, the same as Escape.

The pure-logic layers (`internal/tree`, `internal/ignore`, `internal/index`, `internal/match`, `internal/preview`, `internal/openfiles`, `internal/layout`, `internal/spinner`, `internal/search`) have automated test coverage against every group in `specs/TESTING.md`. `internal/ui` is hand-verified in a real terminal (tmux): startup and the auto-opened browser; navigation, expand/collapse; Space/`a` open actions including inline binary/permission-denied failure messages; `B`/`Tab`/`O`/`/`/Escape/`q` view switching, including `B` and `O` closing their own overlay as a toggle; quick open's single open action and jump to file's single reveal-in-browser action (including jump to file replacing and then correctly returning to the browser); the open-files overlay including Shift-Up/Shift-Down reordering; real preview rendering with scrolling, goto-line, and per-entry scroll-state restoration; the split-view/popup layout flipping live on resize; live filesystem refresh (add/delete reflected in both the browser and the background index) while the app is running; and the spinner badge's full threshold/spinner/completion/fade sequence, confirmed both in normal builds (tiny directories correctly show nothing at all) and via `-tags spinnerdebug` (bypasses only the perceptibility threshold; the minimum-display-duration floor, completion message, and fade still run on their normal schedule). The periodic resize-poll ticker's behavior inside Zellij — the primary target multiplexer per `specs/SPEC.md` §6.2, which doesn't always deliver resize signals reliably — has also been confirmed working as expected in earlier stages and was not re-verified in stage 8 (the resize/ticker code itself is unchanged by the rename).

**Content search (spec §9)**, added after the stages above: a separate `s`-triggered overlay, reachable from both the browser and the primary preview view, that scans indexed files' content (not just paths) for a plain-string, case-insensitive substring match, running the scan in a cancelable background goroutine per keystroke so typing never blocks on a large tree. `internal/search`'s matching/binary-detection/cancellation logic is unit-tested; the overlay wiring itself (`s` from both entry points, live query typing including literal spaces, Enter-to-open, Escape-to-return, and the index badge rendering in this overlay too) is hand-verified in a real terminal alongside the rest of `internal/ui`.

## Non-negotiable constraints

- **No required runtime dependencies.**
- **No assumed language or runtime** — `specs/SPEC.md` is phrased in terms of observable behavior, not any particular language's types or library calls.
- **Terminal resize must be handled by polling**, not solely by relying on a resize signal/event — see "Resize handling" in `specs/SPEC.md`.

## License

TBD.
