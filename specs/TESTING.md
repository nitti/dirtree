# dirtree — acceptance criteria

These are the behaviors the pure-logic layer's unit suite must lock down — no terminal/rendering tests, since that layer isn't practically unit-testable. Treat each bullet as a required test case in the implementation, regardless of test framework. Group names below mirror the spec sections in `SPEC.md` they correspond to; they are not meant to prescribe file/module layout.

Groups are ordered the same way `SPEC.md` is: primary view (§2) first, then the browser (§3, including jump to file, §4.3), then quick open (§4.1, §4.2), then layout/rendering (§5), then the system behaviors that cut across all of them (§6).

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
- An index's page (0-based) is its position divided by the page size, for a range of indices spanning several pages including exact page boundaries (e.g. the last index of one page and the first index of the next).
- The total page count for a given entry count is always at least 1 (including zero entries), and rounds up for a partial final page.
- A page's index bounds correctly reflect a short final page (fewer than a full page's worth of entries) as well as a full one.
- Jumping to the next/previous page from an index lands on the target page's first entry (position 0 on that page), not on the same relative position carried over from the source page.
- Jumping past the last page or before the first page is a no-op (clamped, not wrapped) — the index is left unchanged.
- Jumping to the next/previous page on an empty list is a no-op.
- Selecting a digit (0-9) resolves to the entry at that position on the current page, for a digit that has a corresponding entry.
- Selecting a digit past the current page's last row (e.g. a short final page) reports no entry, distinguishing it from a valid position 0.
- A bulk page-reorder (Shift-Page-Up/Shift-Page-Down) moves the entry up to a full page's worth of positions toward the top/bottom, and the displayed entry (if it was the one moved) follows it.
- A bulk page-reorder that would cross a list end stops at that end rather than being a no-op or wrapping around — moving as many positions as are actually available.

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
- The background full-tree index (§4.1) respects `.dirtreeignore` exclusions the same way it respects `.gitignore` ones — this is app-wide filtering, not limited to quick open.

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

## Path display (§3.1, §4.2)

- `relative_display_path` renders a path relative to the root as a POSIX (forward-slash) string regardless of host path-separator conventions.

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

## Quick open: single-action wiring (§4.2)

- Opening quick open from the primary preview view and pressing Return on a match opens/reuses the corresponding open-files entry, closes quick open, and lands on the preview showing it.
- Opening a deeply-nested match via quick open (`tree.RevealPath`, §4.2) expands every intermediate ancestor directory and moves the browser's selection to the opened file, confirmed by then opening the browser (`b`) and observing the file's row visible and selected with no further navigation needed.
- Escape from quick open returns to the primary preview view's displayed entry (or empty state) unchanged — nothing is opened.
- `o` has no effect while the browser overlay is active, and `s` has no effect while the browser or quick open overlays are active: browse, quick open, and content search are mutually exclusive (§5.1), so reaching one from another requires closing back to the primary preview view first (Escape, or `b` for the browser).
- Page Up/Page Down in quick open move the selection by the match list's own visible height, clamped at the first/last match rather than wrapping (unlike Tab/Shift-Tab); covered by `TestMoveSelectionClamped*` in `internal/tree`, the pure function quick open's Page Up/Down handling is built on.

## Jump to file: in-browser incremental jump (§4.3)

- Candidate matching is scoped to the browser's current flattened row list (§3.1): a file inside a currently-collapsed directory is not a candidate even if its name matches, and becomes one as soon as that directory is expanded.
- Matching is a case-insensitive prefix match against each candidate's leaf name, not its full path — a query matching only a directory segment further up the path (not the row's own name) does not match that row.
- Both files and directories are eligible candidates.
- Typing a character that produces at least one match moves the browser's selection to the first match in display (top-to-bottom) order.
- Typing a character that produces zero matches leaves the browser's selection wherever it currently was (does not move it, does not clear it).
- Backspace recomputes matches against the shortened query the same way (jump to new first match, or leave selection in place if none match).
- With more than one match, Tab (or Down) moves the selection to the next match in display order, wrapping from the last match back to the first; Shift-Tab (or Up) moves to the previous match, wrapping the other way.
- With only one match, Tab/Shift-Tab is a no-op (nothing to cycle to).
- Return leaves jump mode without moving the selection any further and without performing the browser's open action.
- Escape restores the browser's selection (and scroll) to exactly what it was immediately before `/` was pressed, discarding the query, regardless of how much typing/cycling happened in between.
- While jump mode is active, keys that are otherwise browser commands (`o`, `s`, `b`) are treated as ordinary query characters instead of triggering their normal browser action.
- Jump to file never expands or collapses any directory itself — its candidate set is a pure function of the tree's expand/collapse state as it already was when `/` was pressed (plus whatever narrower state jump mode's own cycling leaves it in, which never changes disclosure).

## Content search matching (§9.1)

- An empty query returns no matches (no error), and the returned result set is never nil (distinguishable from "not yet searched").
- In substring mode (the default), a plain query is a case-insensitive substring match checked against file content, not just file names.
- A file result reports every matching line (not just the first), in source order, each with its 1-based line number and text.
- A file containing a NUL byte within the byte-capped read is never matched, even if the query text is literally present in its bytes (binary detection takes precedence, mirroring §2.2's binary-open check).
- A file that can't be read (permission denied, deleted mid-scan) is silently skipped rather than aborting the whole scan or surfacing an error.
- A match occurring only beyond the byte cap is not found — content search never reads past the same cap preview loading uses.
- Results are sorted by root-relative path, case-insensitively.
- An already-canceled context stops the scan before it reads any candidate, so a superseded query's scan does no wasted work once canceled.
- In regex mode, a query is compiled and matched as a case-insensitive regular expression rather than a literal substring, matching every line the pattern matches (e.g. `foo\d+` matches "foo1" and "foo22" but not "foobar").
- In substring mode, a query containing regex metacharacters (e.g. `foo(bar)`) is matched completely literally — it is never interpreted as a pattern.
- In regex mode, an invalid pattern returns a non-nil error and no results, without scanning any candidate (verified by using a candidate that would otherwise match, and confirming zero reads/matches occurred).

## Toast primitive (fade timing) (§5.3)

- A toast is shown in full throughout the display-duration window (including at its start and just under its end).
- A toast starts fading (a nonzero, growing hidden-prefix count) once elapsed time reaches the display duration, and is fully hidden once elapsed time reaches display duration + fade duration (boundary-test both transitions).
- A zero fade duration hides the toast immediately once the display duration elapses, rather than dividing by zero or leaving it stuck fading forever.

## Indexing-delay / spinner-suppression logic (§5.2)

- The loading indicator is hidden whenever indexing is already marked done, regardless of elapsed time.
- The loading indicator is hidden while indexing is still running but elapsed time is under the configured delay threshold.
- The loading indicator is shown once indexing is still running and elapsed time has reached/exceeded the configured delay threshold (boundary-test at exactly the threshold).
- Spinner frame selection is deterministic given the same elapsed time (same input → same glyph, not randomized).
- Spinner frame advances (cycles to a different glyph) as elapsed time increases across at least one full frame-interval step.
- The completion message's shown/fading/hidden timing itself is covered by the toast-primitive group above (BadgeDecision delegates to it directly); what's specific to the badge and tested here is the sequencing layered on top of that timing:
- The bottom-right badge shows no completion message at all when indexing finished before the perceptibility threshold — i.e. time-to-completion, not time-since-completion, is what's checked against the threshold — since the spinner never showed for that instant a run in the first place.
- Once indexing has crossed the perceptibility threshold but finishes before the minimum display duration has elapsed, the badge keeps showing the spinner (not the completion message) until that minimum duration is reached (boundary-test at exactly the minimum).
- Once indexing has run at least as long as the minimum display duration before finishing, the badge shows the completion message immediately upon completion (no extension needed).
- The bottom-right badge stops showing anything once the completion message's display+fade window has fully elapsed.
- Debug-only always-show mode is a build-time switch (`-tags spinnerdebug`), not something exercised by the normal automated test suite; manual verification is to build once without the tag and confirm the badge behaves normally (threshold-suppressed, then spinner, then completion message + fade), then build with `-tags spinnerdebug` on a small directory and confirm: the spinner appears immediately (threshold bypassed) even though real indexing finishes almost instantly, stays visible for the same minimum display duration as the non-debug path, then hands off to the same completion message and fade; quick open's indexing-blocked behavior is unaffected either way.
- The bottom-right badge's background is visually distinct from the default row background (a contrasting/accent color), confirmed by manual inspection in a real terminal alongside the other rendering-layer checks below.
- When the minimum-display-duration skip is set, the badge shows the completion message in full immediately, treating the moment the skip happened — not the index's actual completion time — as when indexing finished.
- The minimum-display-duration skip still shows the completion message in full even when the index's real completion time is already well past the completion message's entire display+fade window (e.g. it was masked for a long time by an artificially-held spinner) — this is the regression case for a bug where the badge briefly vanished the instant the picker overlay was opened instead of visibly transitioning.
- The minimum-display-duration skip does not bypass the completion message's own display+fade timing — it still fades out on schedule, counted forward from the moment of the skip.
- Opening quick open while indexing is already done sets the badge's minimum-display-duration skip and records the elapsed-since-indexing-started value at that moment.
- A filesystem-change-triggered index rebuild resets the minimum-display-duration skip (and its recorded moment), so a fresh indexing cycle gets the flash-prevention floor back.
- The bottom-right badge renders identically whether the browser or quick open overlay is the currently active screen (jump to file has no indexing-dependent state and doesn't render the badge itself, though it's layered on top of the browser which does) — this is a manual/rendering-layer check (see the notes below), since the corner badge itself is drawn by the terminal-rendering layer, not the underlying pure decision logic already covered above.

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
- Refreshing a directory that newly fails to list (e.g. loses read permission) reports that directory's path as newly-erroring.
- Refreshing a directory that already had a listing error does not re-report it as newly-erroring.

## Manual / rendering-layer verification (not unit-testable)

The following require a real terminal (ideally inside a multiplexer like Zellij or tmux) and should be explicitly confirmed, and their status stated, whenever a change touching them is reported complete:

- Startup shows the empty preview state with the browser auto-opened on top of it.
- Making an already-loaded, watched directory unreadable (e.g. `chmod 000` it while dirtree is running, with its row visible in the browser) triggers a live refresh that briefly flashes that row's background red, distinct from the green post-open flash and from selection's reverse-video; the row's inline `[error]` text (already covered elsewhere in this list) remains visible after the flash itself fades, rather than disappearing along with it.
- The browser's Return opens a file and displays it in the primary preview view without closing the browser, allowing several files to be queued before Escape/`b`.
- Selecting a binary or unreadable file from the browser (Return) or from quick open renders the corresponding failure message ("binary file, preview not available," or the OS error text) inline in that overlay without navigating away or adding an open-files entry.
- The open-files-list overlay (`Tab` from preview) correctly lists entries in insertion order, supports Return-to-display and `x`-to-remove, and its empty-list message renders when reachable.
- Shift-Up/Shift-Down reordering is confirmed against a real terminal/multiplexer, since Shift+arrow key delivery is more library/terminal-dependent than plain arrow keys; if the target terminal library can't reliably distinguish Shift-Up/Shift-Down from plain Up/Down, note the fallback keys actually used here rather than silently shipping non-functional reordering.
- Quick open's header shows a bold, all-caps "QUICK OPEN" mode label and legend correctly showing Return-to-open, while the query lives on its own row directly beneath the header (not sharing a row with the legend), rendered with a background distinct from the match list below it — the same input-row convention content search uses (§9.2).
- Quick open's match list is files-only (§4.2): a directory never appears as its own row, confirmed by querying a directory's exact name and seeing zero rows (not the directory itself) while a query matching one of its files still returns that file; a query that matches both a directory name and files under it (e.g. a common path segment) returns only the files, the directory contributing no row of its own.
- The browser overlay renders full-screen at every terminal width; its own header/title bar shows a bold, all-caps "BROWSE" mode label in place of the tree root path, alongside the browser's own legend.
- The browser's open-file feedback (§2.2, §5.2): a row for a file already in the open-files list (whether opened from the browser itself, quick open, content search, or earlier in the session) shows a lasting "●" indicator inline next to its expand/collapse marker, and this never appears on directory rows; opening a file via Return from the browser briefly flashes that row in a distinct style before it fades back to normal — the same on-open confirmation content search's own file rows give (§9.2) — and the flash is purely visual, never blocking or delaying subsequent input (queuing up several more files to open in a row immediately after).
- Jump to file (`/` from within the browser): the browser's row list stays fully visible while typing (no overlay/popup obscures it); the header row switches from the "BROWSE" mode label and its legend to a bold, all-caps "JUMP" mode label plus a Tab/Return/Escape legend while active, and back to "BROWSE" on Return/Escape; the literal query never appears in the header itself — it renders on its own input row (`/query`) directly below the header instead, confined to the terminal's full width, the same convention quick open and content search use; every currently-matching row gets a visible highlight distinct from the cursor's own selection highlight; typing/backspacing visibly moves the cursor to the live first match, and Tab/Shift-Tab visibly cycles it among matches when there's more than one; a query typed with no matches leaves the cursor visibly in place rather than jumping anywhere; typing a letter that's otherwise a browser command (`b`) while jump mode is active is confirmed to type into the query instead of triggering that command.
- Content search overlay (§9): `s` opens it from the primary preview view (including its empty state); the query lives on its own row directly beneath the header (not sharing a row with the legend), rendered with a background distinct from the results list below it; typing (including a literal space) builds the query live and each keystroke's rescan doesn't visibly block input — confirmed by typing further characters while a scan over a large tree is still in flight and seeing them register immediately, not queued up behind the scan; the bottom-right index badge renders in this overlay the same way it does in the browser and quick open overlays.
- Content search's two-level result list (§9.2): each matching file appears once as its own row with a disclosure indicator and hit count, expanded by default so all of its hit rows (line number + trimmed text) are visible immediately with no action needed; Left collapses the selected row's file and Right re-expands it, and collapsing one file never affects any other file's disclosure state; Tab/Down and Shift-Tab/Up move the selection through the flattened file+hit row list, including into and out of a file's hit rows, with wraparound at either end.
- Content search's open actions (§9.2): Return on a file row opens that file and jumps to its first (lowest-line-number) hit; Return on a hit row opens that file and jumps to that specific hit's line — confirmed by checking the preview's scroll position lands on the target line, not just that the file opened; Return leaves the overlay open afterward (confirmed by opening two different hits in a row via Return without the overlay closing in between), and Escape is what closes it back to the primary preview view.
- Content search's open action also reveals the opened path in the browser (§9.2, same `tree.RevealPath` mechanism as quick open, §4.2): opening a deeply-nested hit expands every intermediate ancestor directory and moves the browser's selection to it, confirmed by then opening the browser (`b`) and observing the file visible and selected.
- Content search's open-file feedback (§9.2): a file row for a path already in the open-files list (whether opened from this overlay, the browser, quick open, or earlier in the session) shows a lasting "●" indicator next to its disclosure marker, and this indicator never appears on hit rows; pressing Return on a row briefly flashes that file's row in a distinct style before it fades back to normal — confirmed by opening a hit row specifically and observing the flash lands on its parent file row, not the hit row itself — and the flash is purely visual, never blocking or delaying subsequent input (typing, navigating, or opening another result immediately after).
- Content search persistence (§9.2): typing a query, then pressing Escape, then reopening with `s` shows the exact same query, results, selection, mode, and per-file disclosure state as before Escape was pressed — nothing is reset just by leaving and returning to the overlay; Ctrl+U, backspacing the query to empty, or typing a new query all reset the result set (and its disclosure state back to expanded-by-default).
- Content search's regex mode (§9.1, §9.2): Ctrl+R toggles it on/off (bold, all-caps header mode label switches from "SEARCH" to "SEARCH (REGEX)" and back) and immediately rescans the current query under the new mode; a regex query (e.g. `foo\d+`) matches lines a literal substring search wouldn't, and the same query switched back to substring mode is matched completely literally instead; an invalid pattern (e.g. an unclosed group) shows an inline "invalid regex" message in the results area instead of a scan or a crash, and correcting the pattern clears the error and resumes scanning.
- Content search's slow-scan feedback (§9.1): confirmed on a large enough tree (or a query broad enough to be slow) that the results area shows "searching…" and then, once the scan has run long enough to be perceptible, an animated spinner glyph alongside it — mirroring the background-index badge's own threshold-suppressed spinner (§5.2) — and that keystrokes (typing further, arrow-key movement, Escape) remain immediately responsive throughout, confirming the scan never blocks the input thread.
- `b` behaves as a toggle: pressing it again while the browser overlay is active closes it back to the primary preview view, the same as Escape.
- Quick open is not a toggle: typing `o` as part of a filter query (e.g. "go") filters normally rather than closing the overlay; only Escape closes it.
- Browse, quick open, and content search are mutually exclusive (§5.1): `o` and `s` have no effect while the browser overlay is active, `o` has no effect while content search is active, and `s` has no effect while quick open is active — confirmed by pressing each in the "wrong" overlay and observing no mode switch, then confirming the same key does work once back at the primary preview view (Escape, or `b` from the browser).
- Pressing Escape at the primary preview view (no overlay active, no active find) does nothing — it does not quit; only `q` quits.
- The browser overlay renders full-screen at every terminal width, confirmed live across a resize from narrow to wide and back.
- The primary preview view's header/title bar shows the tree root path (abbreviated with `~` when under the home directory) alongside the keybinding legend in a wide terminal, and drops the root path (keeping just the legend) once the terminal is narrowed below the point where both fit — confirmed live on resize in both directions. Wherever a mode label (`BROWSE`, `QUICK OPEN`, `SEARCH`/`SEARCH (REGEX)`, `JUMP`) stands in for the root path instead, the same narrow-terminal drop rule applies to the label, and the label itself renders bold while the legend sharing its row stays normal weight.
- The file title bar, directly above the preview content, shows the displayed entry's root-relative path (not its absolute path) whenever a file is open, in both the primary preview view and underneath the open-files-list overlay, and disappears when no file is displayed.
- The global header/title bar's legend does not list goto-line (`g`) or any other file-specific action; the file title bar shows `[g] goto line  [/] find` right-aligned, in the primary preview view, whenever a file is displayed, but omits it (path only) underneath the open-files-list overlay, since neither key does anything there while that overlay owns input.
- In-file find (`/` from the primary preview view, SPEC.md §2.4): the prompt opens in the file title bar (not goto-line's bottom row); typing/Backspace edit the query; Escape cancels the prompt without touching any existing find state; Return executes the search, jumps to the first match at or after the top of the current viewport (wrapping to the very first match — with a wrap note — if none exists after that point), and closes the prompt.
- `n`/`N` step to the next/previous match, wrapping at either end and showing a "wrapped to top"/"wrapped to bottom" note that clears on the next non-wrapping step; both are no-ops with no active find or zero matches.
- The file title bar's find status shows the query and "current/total" match count while at least one match exists, "no matches" (with no `n`/`N` legend) when the query matched nothing, and this status — like the idle file title bar's legend — is shown only in the interactive primary preview view, not underneath the open-files-list overlay.
- Every match is highlighted in the preview content in a style distinct from syntax highlighting, with the current match visually distinguished from the rest; a match split across two wrapped rows by a mid-token wrap is highlighted correctly on both rows. Unlike the find status text, this highlighting is also visible underneath the open-files-list overlay, since it's a passive visual rather than an interactive legend.
- Switching to a different open-files entry and back preserves the first entry's find state (query, matches, current index, wrap note) exactly, the same way scroll/goto-line state already does.
- Escape, while a find is active, clears it — query, matches, current index, wrap note, and highlighting all disappear, and the file title bar returns to its idle `[g] goto line  [/] find  [c] copy mode` legend; Escape with no active find remains a no-op (still doesn't quit).
- The active-find status always lists `[esc] clear` in its legend, alongside `[n] next  [N] prev` when there's at least one match, or alone (no `n`/`N`) when the query matched nothing — Escape still clears a zero-match find.
- Copy mode (`c` from the primary preview view): toggling it on strips the line-number gutter and all syntax-highlighting color from the preview content, leaving plain text at full pane width; toggling it off restores both immediately. The file title bar switches to a visually distinct style and its legend collapses to `[c] normal view` while copy mode is active, and back to the idle legend (which itself now includes `[c] copy mode`) once it's off.
- Copy mode wraps exactly like normal display (same word-wrap rules above), not clipped — confirm a genuinely long line (wider than the terminal) still wraps across multiple screen rows with copy mode on, exactly as it does with copy mode off, just without the gutter/color. (An earlier version clipped instead of wrapping in copy mode; that made anything past the pane's edge impossible to select at all, which is worse than a multi-row selection occasionally picking up a wrap-induced line break — confirm this regression doesn't recur.)
- In-file find's match highlighting remains visible while copy mode is active (both the current and other matches, still visually distinguished from each other) — copy mode only strips the gutter and syntax color, not find's overlay.
- Copy mode is tracked per open-files entry, not globally: switching to a different open file while it's active on the first one leaves the new file in normal mode, and switching back restores copy mode on the first exactly as it was left — the same independence scroll/goto-line/find state already have. It applies underneath the open-files-list overlay too, not just the interactive primary preview view, whenever that view is showing an entry that's in copy mode.
- The file title bar tags its content `[copy mode] ` whenever the displayed entry is in copy mode, regardless of whether the idle legend, an active find's status, or the find prompt is otherwise showing on that row — the tag is never hidden by another state taking over the row.
- Actually select-and-copy text from the preview (via the terminal/multiplexer's own mouse selection) with copy mode on, confirming the copied text is exactly the file's own characters with no gutter digits mixed in.
- Escape responsiveness (§6.3), resize handling via periodic polling (§6.2), and the spinner badge's visual distinctness (§5.2) per the checks above.
