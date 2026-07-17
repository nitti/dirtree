# dirtree — acceptance criteria

These are the behaviors the pure-logic layer's unit suite must lock down — no terminal/rendering tests, since that layer isn't practically unit-testable. Treat each bullet as a required test case in the implementation, regardless of test framework. Group names below mirror the spec sections in `SPEC.md` they correspond to; they are not meant to prescribe file/module layout.

Groups are ordered the same way `SPEC.md` is: primary view (§2) first, then the browser (§3), then quick open and jump to file (§4), then layout/rendering (§5), then the system behaviors that cut across all of them (§6).

Wherever these tests reference "the tree," they mean the pure navigation/model layer described in `SPEC.md` §3.1 and §3.4 — keep that layer free of any terminal-rendering dependency in the implementation, the same way the prototype kept its model code free of direct terminal-library calls, so all of the below is testable without a real terminal.

## Preview: reading, highlighting, wrapping (§2.1)

- Reading a normal small text file returns its lines split correctly, preserving empty lines.
- Reading a file above the byte cap returns truncated content plus a "truncated" marker line, and does not read past the cap.
- An empty file's read result is a single empty line, not an empty list.
- A tab character is expanded to spaces up to the next tab-stop column (8 columns), not left as a single narrow column.
- Tab stops reset at the start of every line: a line with leading content before its first tab expands relative to that line's own start, not some running total carried over from a previous line.
- Highlighting returns `None`/equivalent-"unavailable" when no rule-set matches the file (must degrade to plain text upstream, not raise).
- Highlighting produces exactly one segment-list per source line (padding or truncating defensively if a lexing pass produces a mismatched row count).
- Highlighting's segments for every source line concatenate back to exactly that line's text — this is the invariant a token whose value spans a newline (e.g. a combined "end this line + indent the next one" whitespace token) must not violate by attributing text after the newline to the line that just ended instead of the new one (regression case: a YAML/Go-shaped fixture with indentation after a blank or short line, verified against every line, not just line 0).
- Wrapping a line whose content exceeds the target width splits it into multiple rows, each at most `width` columns.
- Wrapping preserves segment/category boundaries where possible (a wrapped chunk keeps the category of the segment it came from).
- Wrapping prefers breaking at a space over breaking mid-word, when a space-break exists within the target width.
- Wrapping moves a word to the next row whole, rather than splitting it, whenever the word fits entirely within a fresh full-width row (even though it didn't fit in the remaining space on the row it started on).
- Wrapping allows breaking immediately after a dash/hyphen, keeping the dash on the row it ends (not dropped, unlike a triggering space).
- A word longer than the full target width, with no space or dash anywhere in it, still hard-breaks mid-word rather than being left overflowing a row — the only case where a mid-word break is expected.
- A space that triggers a wrap is dropped from the row entirely (not left dangling at the row's end, not carried over as leading whitespace on the next row); real trailing whitespace at a line's actual end (not a wrap point) is preserved.
- `build_display_rows` assigns the source line number only to each source line's first wrapped row; continuation rows carry no line number.
- `build_display_rows`'s returned line→display-row index correctly points at the first display row for every source line, including source lines that wrapped into multiple rows.

## Open-failure detection at open time (§2.2)

- Opening a path not already in the open-files list, whose read bytes contain a NUL byte, returns a "failed" result with the message "binary file, preview not available": no entry is created, and the previously-displayed entry (if any) is unaffected.
- Opening a path not already in the open-files list that raises an OS-level read error (permission denied, path no longer exists) returns a "failed" result with an explanatory message derived from that error: no entry is created, and the previously-displayed entry (if any) is unaffected — this is the same outcome shape as the binary case, just a different message.
- Opening a path not already in the open-files list, whose read succeeds and whose bytes contain no NUL byte, returns an "opened" result and creates/displays an entry as normal — neither failure check false-positives on ordinary readable text content.
- Opening a path that already has an entry in the open-files list returns an "opened" result and reuses that entry without re-reading the file, regardless of current on-disk content (an existing entry is, by construction, never a "failed" open, since a failed open never creates one).
- Both the read-error check and the binary check are derived from the same byte-cap-bounded read used for normal preview loading (§2.1) — determining either does not require a second, separate read of the file.
- The browser's Return action on a path that fails to open (read error or binary) leaves the browser open and selection unchanged, and does not add an entry to the open-files list.
- Quick open's open action on a path that fails to open leaves the overlay open and match selection unchanged (rather than exiting to the preview), and does not add an entry to the open-files list.
- A "failed" result does not block subsequent input: the next open attempt (same or different path) from the same context proceeds normally, evaluated independently.

## Open files list (§2.2, §2.3)

- Opening a file whose resolved absolute path is not already in the list appends a new entry at the end, reads/highlights its content, resets its scroll to top, and marks it as the displayed entry.
- Opening a file whose resolved absolute path already matches an existing entry does not create a duplicate, does not re-read the file, does not move the entry's position, and preserves its existing scroll/goto state — it only changes which entry is displayed.
- Displaying a different existing entry (without opening/removing anything) never changes list order.
- Each entry's scroll/goto-line state is independent: advancing scroll on one open entry does not affect any other entry's stored scroll position.
- Removing a non-displayed entry via the open-files-list overlay's `x` action leaves the displayed entry and its state unaffected; only the list shrinks.
- Removing the displayed entry via `x` promotes the adjacent surviving entry (next in list order, or previous if the removed entry was last) to displayed, restoring that entry's own stored scroll state (not resetting it).
- Removing the last remaining entry via `x` results in no displayed entry (list is empty) and the overlay auto-closes to the primary preview view's empty state.
- The open-files-list overlay's own selection index is clamped to a valid remaining index after an `x` removal (never left pointing past the end of the shrunken list).
- Escape from the open-files-list overlay does not change which entry is displayed, but does not undo any `x` removals already performed during that overlay session.
- Shift-Down on a non-last entry swaps it with its immediate successor and moves overlay selection to follow it to its new position.
- Shift-Up on a non-first entry swaps it with its immediate predecessor and moves overlay selection to follow it to its new position.
- Shift-Down on the last entry is a no-op: list order and overlay selection are both unchanged.
- Shift-Up on the first entry is a no-op: list order and overlay selection are both unchanged.
- Reordering via Shift-Up/Shift-Down does not change which entry is displayed and does not reset the moved entry's stored scroll/goto state.
- Opening a new file after the list has been manually reordered still appends the new entry at the end, regardless of the manual reordering already applied to existing entries.

## In-file find matching (§2.4)

- Searching for a query that appears once returns exactly one match, at the correct line and rune column.
- Searching for a query that appears multiple times on the same line returns every occurrence, each at its own column, in left-to-right order.
- Searching for a query that appears across multiple lines returns matches in source order (line, then column within the line).
- Matching is case-insensitive: a query matches occurrences differing only in case.
- Overlapping occurrences (e.g. searching "aa" in "aaa") are all reported, not just non-overlapping ones.
- An empty query returns no matches.
- A query with no occurrences anywhere in the file returns no matches.
- A match's column is a rune offset, not a byte offset: a query appearing after a multi-byte character lands on the correct column regardless of that character's UTF-8 byte width.

## Node / tree construction and flattening (§3.1)

- A freshly-built root node is expanded and has its children loaded.
- `flatten()` on a root with no expansion beyond itself returns just the root.
- `flatten()` includes a child's own subtree only when every ancestor down to the root is expanded; collapsing an intermediate ancestor removes its entire subtree from the flattened list even if descendants are individually marked expanded.
- Loading a directory's children twice is a no-op the second time (doesn't re-list the filesystem or replace the children list).
- A directory whose listing raises an OS-level error (e.g. permission denied) ends up with zero children and a non-empty error string, not an exception.
- Directory entries are sorted directories-first, then case-insensitively by name.

## Expand / collapse / toggle (§3.1)

- Expanding a file is a no-op.
- Expanding a collapsed directory loads its children (if not already loaded) and marks it expanded.
- Collapsing marks a directory not-expanded regardless of prior state (idempotent).
- Toggle expands a collapsed directory and collapses an expanded one.

## `.git` and `.gitignore` exclusion (§3.2)

- A `.git` directory is never listed as a child, with or without a `.gitignore` present.
- With no `.gitignore` present, no additional filtering happens beyond the `.git` skip.
- A simple `.gitignore` pattern (e.g. `*.log`) excludes matching files from listing at any depth.
- A negation pattern (`!keep.log` after a broader `*.log` exclude) re-includes the negated path.
- A directory-only pattern (trailing `/`) does not exclude a same-named file.
- An anchored pattern (containing `/`, not just a trailing one) only matches from the root, not at arbitrary depth.
- Ignored directories are excluded from listing entirely — their contents must not be enumerable through the tree even after attempting to expand them (verifies "never even enumerated," not just "hidden after listing").

## `.dirtreeignore` exclusion (§3.2)

- With no `.dirtreeignore` present, no additional filtering happens beyond `.git` and `.gitignore`.
- A `.dirtreeignore` pattern uses the same syntax as `.gitignore` (glob, anchoring, directory-only, negation all behave identically).
- A path matching either the `.gitignore` set or the `.dirtreeignore` set is excluded (union, not intersection).
- A negation pattern in `.dirtreeignore` does not re-include a path excluded by a `.gitignore` pattern, and vice versa — negation precedence is scoped to a single file's own rule list, not across the two files.
- The background full-tree index (§4.1) respects `.dirtreeignore` exclusions the same way it respects `.gitignore` ones — this is app-wide filtering, not limited to quick open or jump to file.

## Right-arrow / left-arrow semantics (§3.4)

- Right on a collapsed directory expands it and returns the same node (selection doesn't move).
- Right on an expanded directory with children returns its first child.
- Right on an expanded directory with zero children returns the same node.
- Right on a file returns the same node (no-op).
- Left on an expanded directory collapses it and returns the same node.
- Left on a collapsed directory with a parent returns the parent.
- Left on a collapsed directory with no parent (root) returns the same node.
- Left on a file returns its parent **and** that parent ends up collapsed, in one call — this is the "jump to parent and close it" combined behavior; verify both the returned node and the parent's `expanded` state.
- Left on a file whose parent is the root behaves the same way (parent has no further parent, but still gets collapsed).

## Selection movement (§3.4)

- `move_selection` wraps forward past the last index back to index 0.
- `move_selection` wraps backward past index 0 to the last index.
- `move_selection` on a single-item list (count=1) always returns 0 regardless of delta.
- `move_selection` on an empty list (count=0) returns 0 without dividing by zero or raising.

## Path display and reveal (§3.1, §4.2)

- `relative_display_path` renders a path relative to the root as a POSIX (forward-slash) string regardless of host path-separator conventions.
- `reveal_path` on a deeply nested target expands every intermediate ancestor directory and returns the target node.
- `reveal_path` on a root-level (depth-1) target works without needing any intermediate expansion beyond the root itself.
- `reveal_path` on a path that doesn't exist under the root returns "not found" (null/None/equivalent) rather than raising.
- `reveal_path` on a path outside the root entirely (not a descendant) returns "not found" rather than raising or misbehaving.

## Background full-tree index (`list_all_paths`-equivalent) (§4.1)

- The index reaches into directories that are currently collapsed in the browser (i.e. it does not depend on or mutate the browser's expand/collapse state at all).
- Building the index does not mutate the browser's node objects (no shared state — assert the tree's structure is bit-for-bit unchanged after an index build).
- The index is sorted by root-relative slash-delimited path, case-insensitively.
- The index respects the same `.gitignore` exclusion rules as interactive listing.
- A symlink cycle does not cause infinite recursion or non-termination (construct a symlink pointing back to an ancestor and confirm the walk still terminates and doesn't duplicate/loop that subtree).

## Filtering / matching (tree-name and full-path variants) (§4.2)

- An empty query matches every candidate.
- A plain (non-wildcard) query is a case-insensitive substring match.
- A query containing `*`, `?`, or `[` is matched as a case-insensitive shell-glob against the *entire* candidate string, not as a substring.
- Filtering the flat full-path index matches on any path segment, including a directory-name component, not just the leaf/file name — e.g. a query matching a middle directory's name returns files nested under it.
- A query that legitimately matches the same relative path only once returns exactly one result even when the same basename recurs at multiple nesting depths elsewhere in the tree (regression case: verify the matcher isn't accidentally treating differently-nested-but-distinctly-pathed matches as duplicates of each other, and isn't failing to de-duplicate a genuinely repeated symlinked/aliased path either way — assert against a known-good expected set built directly from the fixture layout).

## Quick open and jump to file: single-action wiring (§4.2, §4.3)

- Opening quick open from the primary preview view and pressing Return on a match opens/reuses the corresponding open-files entry, closes quick open, and lands on the preview showing it.
- Opening quick open from the browser and pressing Return on a match behaves identically: opens/reuses the corresponding open-files entry and lands on the primary preview view showing it (not back on the browser).
- Opening jump to file from the browser and pressing Return on a match expands every ancestor down to it and leaves the browser open with the match selected — jump to file has no other trigger and no other action.
- Jump to file's resolution failure (path no longer exists) exits the overlay without changing browser selection.
- Escape from quick open returns to whichever screen it was opened from: the primary preview view's displayed entry (or empty state) unchanged if opened from there, or the browser unchanged (selection untouched) if opened from the browser — in neither case is anything opened.
- Escape from jump to file returns to the browser, unchanged, without changing selection.
- Neither overlay exposes the other's action: quick open never reveals in the browser, and jump to file never opens a file into the list.

## Content search matching (§9.1)

- An empty query returns no matches, and the returned result set is never nil (distinguishable from "not yet searched").
- A plain query is a case-insensitive substring match checked against file content, not just file names.
- A match reports the 1-based line number and text of the first line containing the query, not a later line even when multiple lines match.
- A file containing a NUL byte within the byte-capped read is never matched, even if the query text is literally present in its bytes (binary detection takes precedence, mirroring §2.2's binary-open check).
- A file that can't be read (permission denied, deleted mid-scan) is silently skipped rather than aborting the whole scan or surfacing an error.
- A match occurring only beyond the byte cap is not found — content search never reads past the same cap preview loading uses.
- Results are sorted by root-relative path, case-insensitively.
- An already-canceled context stops the scan before it reads any candidate, so a superseded query's scan does no wasted work once canceled.

## Layout computation (§5.1)

- `compute_tree_pane_width` returns at least the configured minimum even for a very short/empty node list.
- `compute_tree_pane_width` grows to fit the longest currently-visible label, up to the configured maximum, and clamps at that maximum for a pathologically long name.
- `should_split_view` returns true only when total width is at least browser-pane width + minimum preview width + the separator column; false just under that threshold (boundary-test both sides of the inequality).

## Indexing-delay / spinner-suppression logic (§5.2)

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
- Debug-only always-show mode is a build-time switch (`-tags spinnerdebug`), not something exercised by the normal automated test suite; manual verification is to build once without the tag and confirm the badge behaves normally (threshold-suppressed, then spinner, then completion message + fade), then build with `-tags spinnerdebug` on a small directory and confirm: the spinner appears immediately (threshold bypassed) even though real indexing finishes almost instantly, stays visible for the same minimum display duration as the non-debug path, then hands off to the same completion message and fade; quick open's and jump to file's indexing-blocked behavior is unaffected either way.
- The bottom-right badge's background is visually distinct from the default row background (a contrasting/accent color), confirmed by manual inspection in a real terminal alongside the other rendering-layer checks below.
- When the minimum-display-duration skip is set, the badge shows the completion message in full immediately, treating the moment the skip happened — not the index's actual completion time — as when indexing finished.
- The minimum-display-duration skip still shows the completion message in full even when the index's real completion time is already well past the completion message's entire display+fade window (e.g. it was masked for a long time by an artificially-held spinner) — this is the regression case for a bug where the badge briefly vanished the instant the picker overlay was opened instead of visibly transitioning.
- The minimum-display-duration skip does not bypass the completion message's own display+fade timing — it still fades out on schedule, counted forward from the moment of the skip.
- Opening quick open or jump to file while indexing is already done sets the badge's minimum-display-duration skip and records the elapsed-since-indexing-started value at that moment.
- A filesystem-change-triggered index rebuild resets the minimum-display-duration skip (and its recorded moment), so a fresh indexing cycle gets the flash-prevention floor back.
- The bottom-right badge renders identically whether the browser, quick open, or jump-to-file overlay is the currently active screen — this is a manual/rendering-layer check (see the notes below), since the corner badge itself is drawn by the terminal-rendering layer, not the underlying pure decision logic already covered above.

## Live refresh on filesystem changes (§6.1)

- Refreshing after a new file/directory appears on disk adds it to the corresponding loaded node's children.
- Refreshing after a file/directory is deleted removes it from the corresponding loaded node's children.
- Refreshing an unrelated part of the tree preserves an already-expanded directory's node identity, its expanded state, and its own already-loaded children (verified by object identity, not just equal content) — a refresh must not disturb state for anything that didn't change.
- Refreshing does not touch (and does not mark loaded) a directory that has never been expanded/loaded.
- Refreshing respects the same `.gitignore`/`.dirtreeignore` exclusion rules as initial listing for newly-appeared entries.
- If the currently-selected browser node still exists after a refresh, the selection-fallback helper returns that same node.
- If the currently-selected browser node was deleted by the change, the selection-fallback helper returns its nearest surviving ancestor.
- If an entire subtree containing the selection was deleted, the selection-fallback helper walks all the way up to the root (which always survives).
- The background index rebuilds and reflects a newly-created path once the rebuild completes.

## Manual / rendering-layer verification (not unit-testable)

The following require a real terminal (ideally inside a multiplexer like Zellij or tmux) and should be explicitly confirmed, and their status stated, whenever a change touching them is reported complete:

- Startup shows the empty preview state with the browser auto-opened on top of it.
- The browser's Return opens a file and displays it in the primary preview view without closing the browser, allowing several files to be queued before Escape/`b`.
- Selecting a binary or unreadable file from the browser (Return) or from quick open renders the corresponding failure message ("binary file, preview not available," or the OS error text) inline in that overlay without navigating away or adding an open-files entry.
- The open-files-list overlay (`Tab` from preview) correctly lists entries in insertion order, supports Return-to-display and `x`-to-remove, and its empty-list message renders when reachable.
- Shift-Up/Shift-Down reordering is confirmed against a real terminal/multiplexer, since Shift+arrow key delivery is more library/terminal-dependent than plain arrow keys; if the target terminal library can't reliably distinguish Shift-Up/Shift-Down from plain Up/Down, note the fallback keys actually used here rather than silently shipping non-functional reordering.
- Quick open's header legend correctly shows Return-to-open, and jump to file's header legend correctly shows Return-to-reveal; neither offers the other's action.
- Content search overlay (§9): `s` opens it from both the primary preview view (including its empty state) and the browser; typing (including a literal space) builds the query live and each keystroke's rescan doesn't visibly block input; Return on a match opens it into the preview and closes the overlay; Escape returns to whichever screen it was opened from unchanged; the bottom-right index badge renders in this overlay the same way it does in the browser, quick open, and jump-to-file overlays.
- `b` behaves as a toggle: pressing it again while the browser overlay is active closes it back to the primary preview view, the same as Escape.
- Quick open is not a toggle: typing `o` as part of a filter query (e.g. "go") filters normally rather than closing the overlay; only Escape closes it.
- `o` opens quick open from within the browser overlay too (not just the primary preview view), replacing the browser view; Escape from there returns to the browser, unchanged, while opening a match lands on the primary preview view instead (confirmed for both entry points).
- Pressing Escape at the primary preview view (no overlay active, no active find) does nothing — it does not quit; only `q` quits.
- Jump to file (`/` from within the browser) fully replaces the browser view while open, and Escape from it returns to the browser exactly as it was.
- Browser split-view vs. popup layout flips correctly on live resize, with the preview pane visible-but-inert in split view.
- The header/title bar shows the tree root path (abbreviated with `~` when under the home directory) alongside the keybinding legend in a wide terminal, and drops the root path (keeping just the legend) once the terminal is narrowed below the point where both fit — confirmed live on resize in both directions.
- The file title bar, directly above the preview content, shows the displayed entry's root-relative path (not its absolute path) whenever a file is open, in both the primary preview view and the split-view browser's preview pane, and disappears when no file is displayed.
- The global header/title bar's legend does not list goto-line (`g`) or any other file-specific action; the file title bar shows `[g] goto line  [/] find` right-aligned, in the primary preview view, whenever a file is displayed, but omits it (path only) in the split-view browser's read-only preview pane, since neither key does anything there while the browser owns input.
- In-file find (`/` from the primary preview view, SPEC.md §2.4): the prompt opens in the file title bar (not goto-line's bottom row); typing/Backspace edit the query; Escape cancels the prompt without touching any existing find state; Return executes the search, jumps to the first match at or after the top of the current viewport (wrapping to the very first match — with a wrap note — if none exists after that point), and closes the prompt.
- `n`/`N` step to the next/previous match, wrapping at either end and showing a "wrapped to top"/"wrapped to bottom" note that clears on the next non-wrapping step; both are no-ops with no active find or zero matches.
- The file title bar's find status shows the query and "current/total" match count while at least one match exists, "no matches" (with no `n`/`N` legend) when the query matched nothing, and this status — like the idle file title bar's legend — is shown only in the interactive primary preview view, not the split-view browser's read-only preview pane.
- Every match is highlighted in the preview content in a style distinct from syntax highlighting, with the current match visually distinguished from the rest; a match split across two wrapped rows by a mid-token wrap is highlighted correctly on both rows. Unlike the find status text, this highlighting is also visible in the split-view browser's read-only preview pane, since it's a passive visual rather than an interactive legend.
- Switching to a different open-files entry and back preserves the first entry's find state (query, matches, current index, wrap note) exactly, the same way scroll/goto-line state already does.
- Escape, while a find is active, clears it — query, matches, current index, wrap note, and highlighting all disappear, and the file title bar returns to its idle `[g] goto line  [/] find  [c] copy mode` legend; Escape with no active find remains a no-op (still doesn't quit).
- The active-find status always lists `[esc] clear` in its legend, alongside `[n] next  [N] prev` when there's at least one match, or alone (no `n`/`N`) when the query matched nothing — Escape still clears a zero-match find.
- Copy mode (`c` from the primary preview view): toggling it on strips the line-number gutter and all syntax-highlighting color from the preview content, leaving plain text at full pane width; toggling it off restores both immediately. The file title bar switches to a visually distinct style and its legend collapses to `[c] normal view` while copy mode is active, and back to the idle legend (which itself now includes `[c] copy mode`) once it's off.
- Copy mode wraps exactly like normal display (same word-wrap rules above), not clipped — confirm a genuinely long line (wider than the terminal) still wraps across multiple screen rows with copy mode on, exactly as it does with copy mode off, just without the gutter/color. (An earlier version clipped instead of wrapping in copy mode; that made anything past the pane's edge impossible to select at all, which is worse than a multi-row selection occasionally picking up a wrap-induced line break — confirm this regression doesn't recur.)
- In-file find's match highlighting remains visible while copy mode is active (both the current and other matches, still visually distinguished from each other) — copy mode only strips the gutter and syntax color, not find's overlay.
- Copy mode is tracked per open-files entry, not globally: switching to a different open file while it's active on the first one leaves the new file in normal mode, and switching back restores copy mode on the first exactly as it was left — the same independence scroll/goto-line/find state already have. It applies to the split-view browser layout's read-only preview pane too, not just the interactive primary preview view, whenever that pane is showing an entry that's in copy mode.
- The file title bar tags its content `[copy mode] ` whenever the displayed entry is in copy mode, regardless of whether the idle legend, an active find's status, or the find prompt is otherwise showing on that row — the tag is never hidden by another state taking over the row.
- Actually select-and-copy text from the preview (via the terminal/multiplexer's own mouse selection) with copy mode on, confirming the copied text is exactly the file's own characters with no gutter digits mixed in.
- Escape responsiveness (§6.3), resize handling via periodic polling (§6.2), and the spinner badge's visual distinctness (§5.2) per the checks above.
