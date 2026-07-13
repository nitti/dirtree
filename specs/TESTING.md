# dirtree — acceptance criteria

These are the behaviors the prototype's unit suite (64 cases, pure-logic layer only — no terminal/rendering tests, since that layer isn't practically unit-testable) locked down. Treat each bullet as a required test case in the new implementation, regardless of test framework. Group names below mirror the spec sections in `SPEC.md` they correspond to; they are not meant to prescribe file/module layout.

Wherever these tests reference "the tree," they mean the pure navigation/model layer described in `SPEC.md` §2 and §5 — keep that layer free of any terminal-rendering dependency in the implementation, the same way the prototype kept its model code free of direct terminal-library calls, so all of the below is testable without a real terminal.

## Node / tree construction and flattening

- A freshly-built root node is expanded and has its children loaded.
- `flatten()` on a root with no expansion beyond itself returns just the root.
- `flatten()` includes a child's own subtree only when every ancestor down to the root is expanded; collapsing an intermediate ancestor removes its entire subtree from the flattened list even if descendants are individually marked expanded.
- Loading a directory's children twice is a no-op the second time (doesn't re-list the filesystem or replace the children list).
- A directory whose listing raises an OS-level error (e.g. permission denied) ends up with zero children and a non-empty error string, not an exception.
- Directory entries are sorted directories-first, then case-insensitively by name.

## Expand / collapse / toggle

- Expanding a file is a no-op.
- Expanding a collapsed directory loads its children (if not already loaded) and marks it expanded.
- Collapsing marks a directory not-expanded regardless of prior state (idempotent).
- Toggle expands a collapsed directory and collapses an expanded one.

## Right-arrow / left-arrow semantics

- Right on a collapsed directory expands it and returns the same node (selection doesn't move).
- Right on an expanded directory with children returns its first child.
- Right on an expanded directory with zero children returns the same node.
- Right on a file returns the same node (no-op).
- Left on an expanded directory collapses it and returns the same node.
- Left on a collapsed directory with a parent returns the parent.
- Left on a collapsed directory with no parent (root) returns the same node.
- Left on a file returns its parent **and** that parent ends up collapsed, in one call — this is the "jump to parent and close it" combined behavior; verify both the returned node and the parent's `expanded` state.
- Left on a file whose parent is the root behaves the same way (parent has no further parent, but still gets collapsed).

## Selection movement

- `move_selection` wraps forward past the last index back to index 0.
- `move_selection` wraps backward past index 0 to the last index.
- `move_selection` on a single-item list (count=1) always returns 0 regardless of delta.
- `move_selection` on an empty list (count=0) returns 0 without dividing by zero or raising.

## `.git` and `.gitignore` exclusion

- A `.git` directory is never listed as a child, with or without a `.gitignore` present.
- With no `.gitignore` present, no additional filtering happens beyond the `.git` skip.
- A simple `.gitignore` pattern (e.g. `*.log`) excludes matching files from listing at any depth.
- A negation pattern (`!keep.log` after a broader `*.log` exclude) re-includes the negated path.
- A directory-only pattern (trailing `/`) does not exclude a same-named file.
- An anchored pattern (containing `/`, not just a trailing one) only matches from the root, not at arbitrary depth.
- Ignored directories are excluded from listing entirely — their contents must not be enumerable through the tree even after attempting to expand them (verifies "never even enumerated," not just "hidden after listing").

## `.dirtreeignore` exclusion

- With no `.dirtreeignore` present, no additional filtering happens beyond `.git` and `.gitignore`.
- A `.dirtreeignore` pattern uses the same syntax as `.gitignore` (glob, anchoring, directory-only, negation all behave identically).
- A path matching either the `.gitignore` set or the `.dirtreeignore` set is excluded (union, not intersection).
- A negation pattern in `.dirtreeignore` does not re-include a path excluded by a `.gitignore` pattern, and vice versa — negation precedence is scoped to a single file's own rule list, not across the two files.
- The background full-tree index (§6) respects `.dirtreeignore` exclusions the same way it respects `.gitignore` ones — this is app-wide filtering, not jump-mode-only.

## Background full-tree index (`list_all_paths`-equivalent)

- The index reaches into directories that are currently collapsed in the interactive tree (i.e. it does not depend on or mutate the interactive tree's expand/collapse state at all).
- Building the index does not mutate the interactive tree's node objects (no shared state — assert the interactive tree's structure is bit-for-bit unchanged after an index build).
- The index is sorted by root-relative slash-delimited path, case-insensitively.
- The index respects the same `.gitignore` exclusion rules as interactive listing.
- A symlink cycle does not cause infinite recursion or non-termination (construct a symlink pointing back to an ancestor and confirm the walk still terminates and doesn't duplicate/loop that subtree).

## Live refresh on filesystem changes

- Refreshing after a new file/directory appears on disk adds it to the corresponding loaded node's children.
- Refreshing after a file/directory is deleted removes it from the corresponding loaded node's children.
- Refreshing an unrelated part of the tree preserves an already-expanded directory's node identity, its expanded state, and its own already-loaded children (verified by object identity, not just equal content) — a refresh must not disturb state for anything that didn't change.
- Refreshing does not touch (and does not mark loaded) a directory that has never been expanded/loaded.
- Refreshing respects the same `.gitignore`/`.dirtreeignore` exclusion rules as initial listing for newly-appeared entries.
- If the currently-selected node still exists after a refresh, the selection-fallback helper returns that same node.
- If the currently-selected node was deleted by the change, the selection-fallback helper returns its nearest surviving ancestor.
- If an entire subtree containing the selection was deleted, the selection-fallback helper walks all the way up to the root (which always survives).
- The background index rebuilds and reflects a newly-created path once the rebuild completes.

## Path display and reveal

- `relative_display_path` renders a path relative to the root as a POSIX (forward-slash) string regardless of host path-separator conventions.
- `reveal_path` on a deeply nested target expands every intermediate ancestor directory and returns the target node.
- `reveal_path` on a root-level (depth-1) target works without needing any intermediate expansion beyond the root itself.
- `reveal_path` on a path that doesn't exist under the root returns "not found" (null/None/equivalent) rather than raising.
- `reveal_path` on a path outside the root entirely (not a descendant) returns "not found" rather than raising or misbehaving.

## Filtering / matching (tree-name and full-path variants)

- An empty query matches every candidate.
- A plain (non-wildcard) query is a case-insensitive substring match.
- A query containing `*`, `?`, or `[` is matched as a case-insensitive shell-glob against the *entire* candidate string, not as a substring.
- Filtering the flat full-path index matches on any path segment, including a directory-name component, not just the leaf/file name — e.g. a query matching a middle directory's name returns files nested under it.
- A query that legitimately matches the same relative path only once returns exactly one result even when the same basename recurs at multiple nesting depths elsewhere in the tree (regression case: verify the matcher isn't accidentally treating differently-nested-but-distinctly-pathed matches as duplicates of each other, and isn't failing to de-duplicate a genuinely repeated symlinked/aliased path either way — assert against a known-good expected set built directly from the fixture layout).

## Preview: reading, highlighting, wrapping

- Reading a normal small text file returns its lines split correctly, preserving empty lines.
- Reading a file containing a NUL byte returns the single "binary file" placeholder line instead of file content.
- Reading a file above the byte cap returns truncated content plus a "truncated" marker line, and does not read past the cap.
- Reading a nonexistent/unreadable file returns a single explanatory line rather than raising.
- An empty file's read result is a single empty line, not an empty list.
- Highlighting returns `None`/equivalent-"unavailable" when no rule-set matches the file (must degrade to plain text upstream, not raise).
- Highlighting produces exactly one segment-list per source line (padding or truncating defensively if a lexing pass produces a mismatched row count).
- Wrapping a line whose content exceeds the target width splits it into multiple rows, each at most `width` columns.
- Wrapping preserves segment/category boundaries where possible (a wrapped chunk keeps the category of the segment it came from).
- `build_display_rows` assigns the source line number only to each source line's first wrapped row; continuation rows carry no line number.
- `build_display_rows`'s returned line→display-row index correctly points at the first display row for every source line, including source lines that wrapped into multiple rows.

## Layout computation

- `compute_tree_pane_width` returns at least the configured minimum even for a very short/empty node list.
- `compute_tree_pane_width` grows to fit the longest currently-visible label, up to the configured maximum, and clamps at that maximum for a pathologically long name.
- `should_split_view` returns true only when total width is at least tree-pane width + minimum preview width + the separator column; false just under that threshold (boundary-test both sides of the inequality).

## Indexing-delay / spinner-suppression logic

- The loading indicator is hidden whenever indexing is already marked done, regardless of elapsed time.
- The loading indicator is hidden while indexing is still running but elapsed time is under the configured delay threshold.
- The loading indicator is shown once indexing is still running and elapsed time has reached/exceeded the configured delay threshold (boundary-test at exactly the threshold).
- Spinner frame selection is deterministic given the same elapsed time (same input → same glyph, not randomized).
- Spinner frame advances (cycles to a different glyph) as elapsed time increases across at least one full frame-interval step.
