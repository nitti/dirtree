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

## Documentation

- [`specs/SPEC.md`](specs/SPEC.md) — full behavioral specification: CLI, data model, traversal/ignore rules, navigation, background indexing, quick open and jump to file, preview pane, layout, rendering conventions, keybindings, resize handling.
- [`specs/TESTING.md`](specs/TESTING.md) — acceptance criteria, expressed as test cases the implementation must satisfy.
- [`docs/GO_STYLE.md`](docs/GO_STYLE.md) — Go-specific style and architecture rules (layering, error conventions, concurrency, tooling), enforced via `.golangci.yml` and `make lint`.
- [`docs/ANIMATION_IDEAS.md`](docs/ANIMATION_IDEAS.md) — brainstormed, not-yet-decided animation ideas, checked against the animation principles in `specs/SPEC.md` §5.3.
- [`docs/STREAMING_PREVIEW_DESIGN.md`](docs/STREAMING_PREVIEW_DESIGN.md) — a design proposal (not yet implemented) for removing the preview pane's fixed read cap while keeping goto-line fast via an async background line-offset index.
- [`docs/CHANGELOG.md`](docs/CHANGELOG.md) — chronological development history: each stage, fix, and rework, and what was manually verified at the time.
- [`examples/`](examples/README.md) — `make` targets that fetch real-world source trees (Linux, LLVM, CPython) and generate synthetic large files as ZIP-downloaded, fully `.gitignore`'d test fixtures, for manually exercising dirtree and content search at scale.

The `cmd/` and `internal/` package layout maps directly onto `specs/SPEC.md`'s sections — browse the source (with `dirtree` itself, if you like) rather than relying on a manually-kept-in-sync file list here.

## Build and run

```
make build          # builds ./dirtree
make run             # builds and runs against .
make run ARGS=/path  # builds and runs against a specific path
make test            # runs the unit test suite
make bench           # benchmarks content search's concurrency knob (needs DIRTREE_BENCH_DIR; see examples/README.md)
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

The implementation matches `specs/SPEC.md`'s open-files-primary-view design: a running list of open file previews as the primary view, with the browser (§3), an open-files switcher (§2.3), quick open and jump to file (§4), and content search (§9) as overlays reachable from it (§5.1 covers the resulting view model). A `?`-toggled help overlay (§5.4) lists every keybinding currently live, grouped and ordered to match the title bar(s) actually on screen. All of the design's core interactions are implemented and manually verified: navigation and open/close actions across the browser, quick open, jump to file, content search, and the open-files overlay; real preview rendering with syntax highlighting, word-wrapping, scrolling (with a reverse-video "bump" flash on the edge row when scrolling past the top or bottom), goto-line, in-file find, and copy mode; live filesystem refresh and live reload of open files; the delayed-loading indexing badge and toast notifications; the too-small-terminal fallback; narrow-terminal legend behavior; and a hold-to-quit gesture (`q` held for ~1s, with an attention-grabbing, left-to-right-fading header/title bar and the content area beneath it dimmed for the duration) so a stray keypress can't discard the session's open-files state.

Every opened file also gets a background pass (`internal/preview.StreamIndex`, the streaming-preview design proposed in `docs/STREAMING_PREVIEW_DESIGN.md`, stages 1-3) that gates goto-line and drives a file-legend "building…" spinner while running, and splits files into two tiers by size (`preview.HighlightCeiling`, default 25MB): a file at or under the ceiling is fully read/highlighted in the background, same as before, just non-blocking; a file over the ceiling is never fully resident — it renders as plain text and is read on demand in small windows, seeking through the background pass's line-offset index, so opening an arbitrarily large file no longer costs memory proportional to the file's size. In-file find over an over-the-ceiling file runs as its own background, cancelable scan (`internal/find.Scan`) with a "searching…" spinner in the file title bar, rather than needing the whole file resident. Only the `internal/openfiles.ResidentCap` (4) most-recently-displayed highlighted-tier entries keep their full content resident at once — the open-files list itself has no cap, but displaying an entry beyond that count evicts the least-recently-displayed one's content, transparently rebuilt (re-read/re-highlighted in the background) the next time it's displayed, bounding worst-case memory to a small multiple of `HighlightCeiling` regardless of how many files a session accumulates.

A file whose leading bytes contain a NUL byte opens as a read-only **hex view** (`preview.TierBinary`, SPEC.md §2.1a) instead of failing to open: an offset gutter and left-aligned hex-byte grid (always a whole number of 8-byte groups) alongside a right-aligned ASCII column, the gap between the two absorbing whatever width the hex grid's whole-groups sizing doesn't use, reading only the bytes needed for the current viewport (`preview.ReadRange`) rather than holding the file resident regardless of size. The file title bar shows a size tag sized to the gutter's own width — a plain integer under 1024 bytes, otherwise an integer plus a single unit letter widened with as many decimal places as fit (e.g. `256.0K`, never `256.0 KB`) — padded so the path lines up with the hex-byte grid's own start column regardless of the file's size. Goto-offset (`g`, always hex — the prompt shows a fixed `0x` ahead of the input, which only accepts hex digits) and a byte/ASCII-substring find (`/`, always a background scan via `internal/hexfind`, since a hex view never holds a file's full content resident) mirror goto-line and in-file find's own UX; copy mode does not apply to a hex view. Scrolling past the top or bottom of the file reverse-video flashes the edge row, the same cue the text tiers give, and Home/End fill the whole viewport with the file's head/tail (last row flush to the bottom) rather than leaving most of the screen blank. Manually verified in tmux against a 256KB synthetic fixture (`examples/data/hexfile`, `make -C examples hexfile`): rendering, goto-offset (including EOF clamping), find-and-jump, edge-bump flashing, and Home/End/Page Up/Page Down navigation.

The pure-logic layers (`internal/tree`, `internal/ignore`, `internal/index`, `internal/match`, `internal/preview`, `internal/openfiles`, `internal/spinner`, `internal/search`, `internal/find`, `internal/hexfind`, `internal/toast`) have automated test coverage against every group in `specs/TESTING.md`. `internal/ui` and `internal/watch` are terminal- and OS-facility-adjacent and are instead verified manually in a real terminal (tmux/Zellij), per the testing discipline in `CLAUDE.md`.

For the detailed development history — each stage, fix, and rework, with what was manually verified at the time — see [`docs/CHANGELOG.md`](docs/CHANGELOG.md).

## Non-negotiable constraints

- **No required runtime dependencies.**
- **No assumed language or runtime** — `specs/SPEC.md` is phrased in terms of observable behavior, not any particular language's types or library calls.
- **Terminal resize must be handled by polling**, not solely by relying on a resize signal/event — see "Resize handling" in `specs/SPEC.md`.

## License

MIT — see [`LICENSE`](LICENSE).
