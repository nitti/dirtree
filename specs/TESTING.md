# dirtree — acceptance criteria

These are the behaviors the pure-logic layer's unit suite must lock down — no terminal/rendering tests, since that layer isn't practically unit-testable. Treat each bullet as a required test case in the implementation, regardless of test framework. Group names below mirror the spec sections in `SPEC.md` they correspond to; they are not meant to prescribe file/module layout.

Wherever these tests reference "the tree," they mean the pure navigation/model layer described in `SPEC.md` §2 and §9 — keep that layer free of any terminal-rendering dependency in the implementation, the same way the prototype kept its model code free of direct terminal-library calls, so all of the below is testable without a real terminal.

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
- The background full-tree index (§5) respects `.dirtreeignore` exclusions the same way it respects `.gitignore` ones — this is app-wide filtering, not limited to the jump/fuzzy-picker mode.

## Background full-tree index (`list_all_paths`-equivalent)

- The index reaches into directories that are currently collapsed in the tree explorer (i.e. it does not depend on or mutate the tree explorer's expand/collapse state at all).
- Building the index does not mutate the tree explorer's node objects (no shared state — assert the tree's structure is bit-for-bit unchanged after an index build).
- The index is sorted by root-relative slash-delimited path, case-insensitively.
- The index respects the same `.gitignore` exclusion rules as interactive listing.
- A symlink cycle does not cause infinite recursion or non-termination (construct a symlink pointing back to an ancestor and confirm the walk still terminates and doesn't duplicate/loop that subtree).

## Live refresh on filesystem changes

- Refreshing after a new file/directory appears on disk adds it to the corresponding loaded node's children.
- Refreshing after a file/directory is deleted removes it from the corresponding loaded node's children.
- Refreshing an unrelated part of the tree preserves an already-expanded directory's node identity, its expanded state, and its own already-loaded children (verified by object identity, not just equal content) — a refresh must not disturb state for anything that didn't change.
- Refreshing does not touch (and does not mark loaded) a directory that has never been expanded/loaded.
- Refreshing respects the same `.gitignore`/`.dirtreeignore` exclusion rules as initial listing for newly-appeared entries.
- If the currently-selected tree explorer node still exists after a refresh, the selection-fallback helper returns that same node.
- If the currently-selected tree explorer node was deleted by the change, the selection-fallback helper returns its nearest surviving ancestor.
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
- Reading a file above the byte cap returns truncated content plus a "truncated" marker line, and does not read past the cap.
- Reading a nonexistent/unreadable file returns a single explanatory line rather than raising.
- An empty file's read result is a single empty line, not an empty list.
- Highlighting returns `None`/equivalent-"unavailable" when no rule-set matches the file (must degrade to plain text upstream, not raise).
- Highlighting produces exactly one segment-list per source line (padding or truncating defensively if a lexing pass produces a mismatched row count).
- Wrapping a line whose content exceeds the target width splits it into multiple rows, each at most `width` columns.
- Wrapping preserves segment/category boundaries where possible (a wrapped chunk keeps the category of the segment it came from).
- `build_display_rows` assigns the source line number only to each source line's first wrapped row; continuation rows carry no line number.
- `build_display_rows`'s returned line→display-row index correctly points at the first display row for every source line, including source lines that wrapped into multiple rows.

## Binary-file detection at open time

- Opening a path not already in the open-files list, whose read bytes contain a NUL byte, returns a "binary" result: no entry is created, and the previously-displayed entry (if any) is unaffected.
- Opening a path not already in the open-files list, whose read bytes contain no NUL byte, returns an "opened" result and creates/displays an entry as normal — binary detection does not false-positive on ordinary text content.
- Opening a path that already has an entry in the open-files list returns an "opened" result and reuses that entry without re-reading the file, regardless of current on-disk content (an existing entry is, by construction, never binary, since a binary open never creates one).
- The binary check is derived from the same byte-cap-bounded read used for normal preview loading (§7) — it does not require a second, separate read of the file.
- A read error (permission denied, nonexistent file) is reported as its own explanatory single-line entry (§7), distinct from a "binary" result — it still produces an "opened" result with that explanatory content, not a binary short-circuit.
- Tree explorer's Space action on a binary file leaves the explorer open and selection unchanged (rather than its normal close-and-display), and does not add an entry to the open-files list.
- Tree explorer's `a` action on a binary file behaves the same as Space's binary case (no entry added; explorer already stays open either way).
- The jump/fuzzy-picker overlay's open-into-list action on a binary file leaves the overlay open and match selection unchanged (rather than exiting to the preview), and does not add an entry to the open-files list.
- A "binary" result does not block subsequent input: the next open attempt (same or different path) from the same context proceeds normally, evaluated independently.

## Open files list

- Opening a file whose resolved absolute path is not already in the list appends a new entry at the end, reads/highlights its content, resets its scroll to top, and marks it as the displayed entry.
- Opening a file whose resolved absolute path already matches an existing entry does not create a duplicate, does not re-read the file, does not move the entry's position, and preserves its existing scroll/goto state — it only changes which entry is displayed.
- Displaying a different existing entry (without opening/removing anything) never changes list order.
- Each entry's scroll/goto-line state is independent: advancing scroll on one open entry does not affect any other entry's stored scroll position.
- Removing a non-displayed entry via the open-files-list overlay's `x` action leaves the displayed entry and its state unaffected; only the list shrinks.
- Removing the displayed entry via `x` promotes the adjacent surviving entry (next in list order, or previous if the removed entry was last) to displayed, restoring that entry's own stored scroll state (not resetting it).
- Removing the last remaining entry via `x` results in no displayed entry (list is empty) and the overlay auto-closes to the primary preview view's empty state.
- The open-files-list overlay's own selection index is clamped to a valid remaining index after an `x` removal (never left pointing past the end of the shrunken list).
- Escape from the open-files-list overlay does not change which entry is displayed, but does not undo any `x` removals already performed during that overlay session.

## Right-arrow / left-arrow semantics (tree explorer)

- Right on a collapsed directory expands it and returns the same node (selection doesn't move).
- Right on an expanded directory with children returns its first child.
- Right on an expanded directory with zero children returns the same node.
- Right on a file returns the same node (no-op).
- Left on an expanded directory collapses it and returns the same node.
- Left on a collapsed directory with a parent returns the parent.
- Left on a collapsed directory with no parent (root) returns the same node.
- Left on a file returns its parent **and** that parent ends up collapsed, in one call — this is the "jump to parent and close it" combined behavior; verify both the returned node and the parent's `expanded` state.
- Left on a file whose parent is the root behaves the same way (parent has no further parent, but still gets collapsed).

## Jump/fuzzy-picker mode: entry-point action wiring

- Opening the picker from the tree explorer and pressing Enter on a match performs reveal-in-tree (expands ancestors, leaves tree explorer open with the match selected).
- Opening the picker from the tree explorer and pressing Space on a match performs open-into-list (opens/reuses the open-files entry, closes tree explorer, lands on preview showing it).
- Opening the picker from the primary preview view and pressing Enter on a match performs open-into-list.
- Opening the picker from the primary preview view and pressing Space on a match performs reveal-in-tree (opens the tree explorer overlay if it wasn't already open, with the match selected).
- Reveal-in-tree resolution failure (path no longer exists) exits the overlay without changing tree selection, regardless of which key/entry-point triggered it.
- Escape from either entry point returns to the exact screen the overlay was opened from (tree explorer stays open and unchanged; preview view's displayed entry, or empty state, is unchanged) without performing either action.

## Layout computation

- `compute_tree_pane_width` returns at least the configured minimum even for a very short/empty node list.
- `compute_tree_pane_width` grows to fit the longest currently-visible label, up to the configured maximum, and clamps at that maximum for a pathologically long name.
- `should_split_view` returns true only when total width is at least tree-explorer-pane width + minimum preview width + the separator column; false just under that threshold (boundary-test both sides of the inequality).

## Indexing-delay / spinner-suppression logic

- The loading indicator is hidden whenever indexing is already marked done, regardless of elapsed time.
- The loading indicator is hidden while indexing is still running but elapsed time is under the configured delay threshold.
- The loading indicator is shown once indexing is still running and elapsed time has reached/exceeded the configured delay threshold (boundary-test at exactly the threshold).
- Spinner frame selection is deterministic given the same elapsed time (same input → same glyph, not randomized).
- Spinner frame advances (cycles to a different glyph) as elapsed time increases across at least one full frame-interval step.
- The completion message is shown in full throughout the display-duration window (including at its start and just under its end).
- The completion message starts fading (a nonzero, growing hidden-prefix count) once elapsed time since completion reaches the display duration, and is fully hidden once elapsed time reaches display duration + fade duration (boundary-test both transitions).
- The bottom-right badge shows no completion message at all when indexing finished before the perceptibility threshold — i.e. time-to-completion, not time-since-completion, is what's checked against the threshold — since the spinner never showed for that instant a run in the first place.
- Once indexing has crossed the perceptibility threshold but finishes before the minimum display duration has elapsed, the badge keeps showing the spinner (not the completion message) until that minimum duration is reached (boundary-test at exactly the minimum).
- Once indexing has run at least as long as the minimum display duration before finishing, the badge shows the completion message immediately upon completion (no extension needed).
- The bottom-right badge stops showing anything once the completion message's display+fade window has fully elapsed.
- Debug-only always-show mode is a build-time switch (`-tags spinnerdebug`), not something exercised by the normal automated test suite; manual verification is to build once without the tag and confirm the badge behaves normally (threshold-suppressed, then spinner, then completion message + fade), then build with `-tags spinnerdebug` on a small directory and confirm: the spinner appears immediately (threshold bypassed) even though real indexing finishes almost instantly, stays visible for the same minimum display duration as the non-debug path, then hands off to the same completion message and fade; the jump/fuzzy-picker overlay's indexing-blocked behavior is unaffected either way.
- The bottom-right badge's background is visually distinct from the default row background (a contrasting/accent color), confirmed by manual inspection in a real terminal alongside the other rendering-layer checks below.
- When the minimum-display-duration skip is set, the badge shows the completion message in full immediately, treating the moment the skip happened — not the index's actual completion time — as when indexing finished.
- The minimum-display-duration skip still shows the completion message in full even when the index's real completion time is already well past the completion message's entire display+fade window (e.g. it was masked for a long time by an artificially-held spinner) — this is the regression case for a bug where the badge briefly vanished the instant the picker overlay was opened instead of visibly transitioning.
- The minimum-display-duration skip does not bypass the completion message's own display+fade timing — it still fades out on schedule, counted forward from the moment of the skip.
- Opening the jump/fuzzy-picker overlay while indexing is already done sets the badge's minimum-display-duration skip and records the elapsed-since-indexing-started value at that moment.
- A filesystem-change-triggered index rebuild resets the minimum-display-duration skip (and its recorded moment), so a fresh indexing cycle gets the flash-prevention floor back.
- The bottom-right badge renders identically whether the tree explorer or the jump/fuzzy-picker overlay is the currently active screen — this is a manual/rendering-layer check (see the notes below), since the corner badge itself is drawn by the terminal-rendering layer, not the underlying pure decision logic already covered above.

## Manual / rendering-layer verification (not unit-testable)

The following require a real terminal (ideally inside a multiplexer like Zellij or tmux) and should be explicitly confirmed, and their status stated, whenever a change touching them is reported complete:

- Startup shows the empty preview state with the tree explorer auto-opened on top of it.
- Tree explorer's Space opens a file and closes the explorer in one keystroke; `a` opens a file and leaves the explorer open, allowing several files to be queued before Escape.
- Selecting a binary file from the tree explorer (Space or `a`) or the picker's open-into-list action renders the "binary file, preview not available" message inline in that overlay without navigating away or adding an open-files entry.
- The open-files-list overlay (`Tab` from preview) correctly lists entries in insertion order, supports Enter-to-display and `x`-to-remove, and its empty-list message renders when reachable.
- The jump/fuzzy-picker overlay's header legend correctly reflects which of Enter/Space maps to which action depending on entry point (tree explorer vs. preview).
- Tree-explorer split-view vs. popup layout flips correctly on live resize, with the preview pane visible-but-inert in split view.
- Escape responsiveness (§14), resize handling via periodic polling (§13), and the spinner badge's visual distinctness (§12) per the checks above.
