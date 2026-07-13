# CLAUDE.md

## Project Context

- This repository is the home for `dirtree`, a terminal directory-tree browser meant to ship as a single dependency-free binary and be consumed by other projects as a released artifact (e.g. pulled into a container image build by version).
- The project currently has **no implementation** — only a specification (`specs/SPEC.md`, `specs/TESTING.md`). The spec was reverse-derived from a working Python/curses prototype built in a different repo (`homeserver`, `tools/dirtree/`) and generalized to be language-independent. That prototype is not present here and should not be assumed to exist or be referenced as "the old code" — this repo starts from the spec, not from a port.
- `specs/SPEC.md` is the source of truth for required behavior. `specs/TESTING.md` is the source of truth for acceptance criteria. When implementation reveals the spec is ambiguous, underspecified, or wrong, **update the spec in the same change** that resolves the ambiguity — don't let the code and the spec diverge silently.

## Language and Dependencies

- No language has been chosen yet. Go and Rust are the natural fits (single static binary, mature terminal UI libraries), but the choice is open. Once made, record the choice and rationale in this file.
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

## Commits and Pull Requests

- Never push commits directly to `main`. Always create a feature branch, commit there, and open a PR.
- Write clear, descriptive commit messages that explain what changed and why.
- Do not open a PR unless explicitly asked to, even after committing and pushing a completed change.
- Never hard-wrap PR descriptions or PR/issue comments — write each paragraph as a single unbroken line and let the renderer wrap it.

## Documentation Standards

- Do not wrap prose with hard line breaks. Write each paragraph as a single unbroken line.
- Always insert a blank line between body text and a list (ordered or unordered).
- Update `README.md` in the same commit whenever the repository layout, build/run instructions, or status changes.
