# CLAUDE.md

## Project Context

- This repository is the home for `dirtree`, a terminal directory-tree browser meant to ship as a single dependency-free binary and be consumed by other projects as a released artifact (e.g. pulled into a container image build by version).
- The project currently has **no implementation** — only a specification (`specs/SPEC.md`, `specs/TESTING.md`). The spec was reverse-derived from a working Python/curses prototype built in a different repo (`homeserver`, `tools/dirtree/`) and generalized to be language-independent. That prototype is not present here and should not be assumed to exist or be referenced as "the old code" — this repo starts from the spec, not from a port.
- `specs/SPEC.md` is the source of truth for required behavior. `specs/TESTING.md` is the source of truth for acceptance criteria. When implementation reveals the spec is ambiguous, underspecified, or wrong, **update the spec in the same change** that resolves the ambiguity — don't let the code and the spec diverge silently.

## Language and Dependencies

- **Language: Go.** Terminal library: `tcell` (low-level cell-drawing API), chosen over higher-level Elm-architecture frameworks like `bubbletea` because it maps directly onto the spec's "recompute layout every frame from raw terminal dimensions" model (§11) rather than fighting it. Rationale: `CGO_ENABLED=0` gives a trivially reproducible static binary and the easiest cross-compile story for the "pulled into a container image by version" use case in Project Context; goroutines + channels map directly onto §6's background-index requirement (no shared mutable state between the UI and the indexer, enforced by simply never passing node pointers into the indexer goroutine); the `.gitignore` subset (§3) and shell-glob matching (§7) are small enough to hand-roll or cover with `path/filepath.Match`, needing no extra dependency.
- **No required runtime dependencies**, ever. This is the whole point of generalizing the tool out of a Python prototype that needed `pygments`/`pathspec`. A statically-linked library compiled into the binary is fine (e.g. a vendored terminal-UI crate/module); a dependency that must be present on the target system at runtime (an interpreter, a dynamically loaded library, a package fetched at startup) is not acceptable.
- Prefer the standard library or a small number of well-maintained, statically-linkable libraries for terminal handling over hand-rolling raw terminal escape sequences, unless a language's ecosystem genuinely lacks a good option.

## General

- Keep `specs/SPEC.md` and `specs/TESTING.md` aligned with actual implemented behavior in the same commit that changes behavior. A behavior described in the spec but not implemented (or implemented differently) is a bug in either the code or the spec — resolve it, don't leave it silently inconsistent.
- Prefer small, focused changes that are easy to review and reason about.
- Do not perform unrelated refactors unless explicitly requested.
- Flag assumptions and open questions early when requirements are ambiguous — the spec was written by someone who wasn't watching the implementation session, so gaps are expected and should be surfaced, not silently guessed around.

## Testing

- Every acceptance criterion in `specs/TESTING.md` must have a corresponding automated test. Group tests by the spec section they verify (matching `specs/TESTING.md`'s own grouping) so it's easy to audit coverage against the checklist.
- Keep the core navigation/model/matching/layout logic free of any terminal-rendering dependency (no direct calls into the terminal library from that layer), so it can be unit-tested without a real terminal or a pty — this was a deliberate design discipline in the prototype and should carry over regardless of language.
- Terminal-rendering code itself (the actual draw calls) is not expected to be unit-tested the same way; manual verification in a real terminal (ideally including inside a multiplexer like Zellij or tmux, since that's the primary target environment) is the verification path for that layer. State explicitly what was and wasn't verified when reporting a change as complete.

## Working in Isolated Worktrees

- Multiple Claude Code instances may be working in this repo at the same time, each in its own terminal/session, against the same local clone. To avoid two instances clobbering each other's uncommitted work, **isolate code changes to a separate git worktree by default.**
- Before making any code change, check the state of the current branch: if it's `main` (or any shared branch) with a clean working tree, create a new worktree (and matching feature branch) with `git worktree add` and make the change there, not in the primary checkout.
- Exception: if the current branch is already a feature branch and already has uncommitted or committed changes in progress (i.e. you're mid-task on it), keep working in place — don't switch to a worktree mid-task just to satisfy this rule.
- Worktrees must be normal, fully-checked-out working directories (no `--no-checkout`, sparse-checkout, or detached-HEAD-only setups) so the user can `cd` into one and immediately `go build`/`go run` it to smoke test — never leave a worktree in a state that requires extra steps to produce a runnable checkout.
- Place worktrees as sibling directories of the main checkout (e.g. `../dirtree-<branch-name>`), not nested inside it, so tooling that walks the repo tree doesn't trip over them.
- When a task is done and its branch is merged, remove the worktree (`git worktree remove`) rather than leaving it around indefinitely.

## Commits and Pull Requests

- Never push commits directly to `main`. Always create a feature branch, commit there, and open a PR.
- Write clear, descriptive commit messages that explain what changed and why.
- Never hard-wrap PR descriptions or PR/issue comments — write each paragraph as a single unbroken line and let the renderer wrap it.

## Documentation Standards

- Do not wrap prose with hard line breaks. Write each paragraph as a single unbroken line.
- Always insert a blank line between body text and a list (ordered or unordered).
- Update `README.md` in the same commit whenever the repository layout, build/run instructions, or status changes.
