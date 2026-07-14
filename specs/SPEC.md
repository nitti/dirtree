# dirtree — behavioral specification

This document specifies observable behavior only. Any data structure, module split, or algorithm name below is a *description of behavior*, not an implementation mandate — implement it however is idiomatic in the chosen language, as long as the described behavior holds.

## 1. CLI

```
dirtree [path]
```

- `path` is optional, positional, defaults to `.` (the process's current working directory).
- The path is resolved to an absolute path at startup.
- If the resolved path is not a directory, exit immediately with a non-zero status and a one-line error to stderr (e.g. `dirtree: not a directory: <path>`). Do not enter the terminal UI.
- No other flags are required for behavioral parity with the prototype. Additional flags (e.g. `--version`, `--help`) are at the implementer's discretion but must not change default behavior.

## 2. Core data model

The tool operates over a lazily-loaded tree of filesystem entries rooted at the resolved start path.

Each entry (a "node") has:

- an absolute path
- a display name (the path's basename; for the root, if the basename is empty — e.g. `/` — use the full path string instead)
- a depth (root is depth 0, children are parent depth + 1)
- a parent reference (root has none)
- whether it's a directory
- an expanded/collapsed state (directories only; starts collapsed except the root, which starts expanded)
- a lazily-populated children list (`null`/`None`/unset until first loaded; loading is idempotent — loading an already-loaded node's children is a no-op)
- an optional error string, set when the node's directory entries could not be listed (e.g. permission denied); on error, treat the node as having zero children rather than propagating an exception, and render the error inline (see §9).

**Root node initialization:** on startup, build the root node, load its `.gitignore` (see §3), load its children, and mark it expanded.

**Flattening (visible list):** the UI operates over the currently-visible, depth-first, pre-order flattening of the tree: the root, then — if the root is expanded — each child's own flattening, recursively. A node's subtree contributes to this list only while every ancestor down to the root is expanded. This is "progressive disclosure": collapsed directories hide their descendants from the visible list (and from on-screen rendering) but the descendants still exist in the lazily-loaded tree once visited.

## 3. Traversal and ignore rules

Three independent exclusion mechanisms apply identically to (a) normal directory listing/expansion and (b) the background full-tree index used by jump mode (§6):

1. **Always skip `.git`** (exact name match on any path segment's immediate entry, not a pattern) — unconditionally, regardless of `.gitignore` contents. Git internals are never useful to browse or search and can be large.
2. **`.gitignore`-aware filtering**, best-effort:
   - At root-node construction, look for a `.gitignore` file directly in the root path. If present and readable, parse it into an ignore-pattern set (see the matching algorithm below). If absent, unreadable, or pattern-parsing isn't implemented for a given rule, treat it as "no additional patterns" — never crash or block startup because of a malformed `.gitignore`.
   - When listing a directory's entries (or walking the full tree for the index), drop any entry that matches the loaded pattern set, so an ignored directory's contents are never even enumerated (not just hidden after listing).
   - Only the root's `.gitignore` needs to be honored. (The prototype did not walk nested `.gitignore` files in subdirectories; parity does not require it, though an implementation may choose to go further.)
3. **`.dirtreeignore`-aware filtering**, an application-specific exclusion list independent of `.gitignore`, applied identically everywhere `.gitignore` is (tree listing, index walk — never just jump mode; a path either exists in the app or it doesn't):
   - At root-node construction, look for a `.dirtreeignore` file directly in the root path, alongside the `.gitignore` lookup. Same syntax, same best-effort tolerance (missing/unreadable/malformed → "no additional patterns," never crash or block startup), same "only the root's file is honored" scope.
   - A candidate path is excluded if it matches *either* the `.gitignore` pattern set *or* the `.dirtreeignore` pattern set. The two files' patterns don't interact with each other — a `!negation` in one cannot re-include a path the other excluded; negation precedence (§3's "later rules override earlier ones") only applies within a single file's own rule list.
   - Rationale: this exists for exclusions that are about dirtree's own browsing noise (e.g. build output you don't want fuzzy-findable) rather than about what git tracks, so it shouldn't require touching a repo's actual `.gitignore` (or exist in a repo without one at all).

**Minimal gitignore pattern semantics to implement** (a small, dependency-free subset — this does not need to be a full `git check-ignore` reimplementation, but must at least cover):
   - Blank lines and lines starting with `#` are ignored (comments).
   - A pattern with no `/` matches the basename at any depth (e.g. `*.log` matches `a.log` and `sub/a.log`).
   - A pattern containing `/` (other than a trailing slash) is anchored to the root (matches only starting from the tree root, not at arbitrary depth).
   - A trailing `/` means "match directories only."
   - A leading `!` negates a previous match (re-includes a path that an earlier pattern excluded). Later rules override earlier ones, matching git's actual precedence.
   - Glob wildcards `*`, `?`, `[seq]` behave as shell globbing (not needing to also support `**` recursive-glob semantics for parity, though it's a reasonable enhancement).
   - When testing a candidate path, match it against its path relative to the tree root, POSIX-slash-delimited; append a trailing `/` to the tested string when the candidate is itself a directory, so directory-only patterns work.

## 4. Directory listing order

Within a directory, entries are sorted directories-first, then case-insensitively by name.

## 5. Navigation semantics

State: a `selected` index into the current flattened/visible list, and a `scroll` offset (topmost visible row index) used only for rendering (§10).

- **Up / Down**: move `selected` by ∓1, **wrapping around** at both ends (moving up from the first visible row selects the last; moving down from the last selects the first).
- **Right**:
  - On a directory that is currently collapsed: expand it (load children if not already loaded) and mark it expanded. Selection does not move.
  - On a directory that is currently expanded and has at least one child: move selection to its first child.
  - On a directory that is expanded with zero children, or on a file: no-op.
- **Left**:
  - On a directory that is currently expanded: collapse it in place. Selection does not move (stays on the now-collapsed directory).
  - On a directory that is currently collapsed and has a parent: move selection to its parent.
  - On a directory that is collapsed with no parent (i.e. the root): no-op.
  - On a file: move selection to its parent directory **and collapse that parent**, in the same keypress (i.e. "go up and close" in one step, not two).
- After any structural change (expand/collapse/move), if the previously-focused node object is still present in the newly-flattened list, keep it selected (find it by identity, not by recomputing an index formula) — this matters because expand/collapse changes how many rows precede a given node.

## 6. Background full-tree index (for jump mode)

Jump mode (§7) needs to search the *entire* tree regardless of what's currently expanded, but building that list can be slow on a large tree if done synchronously when jump mode is first opened. So:

- Immediately at startup (after the root node is built), kick off building a **background index**: a flat, recursively-walked list of every path under the root (files and directories, root itself excluded), applying the same skip/ignore rules as §3, sorted by its root-relative slash-delimited display path, case-insensitively.
- This must run concurrently with the UI being immediately usable (i.e. the interactive tree view must not block waiting for the index).
- The index-building walk must guard against symlink cycles (track resolved/canonical real paths already visited; do not re-descend into one already seen).
- The index-building process must not share mutable state with the interactive tree's node objects — it operates on raw paths only, so no locking is required between the UI thread/task and the indexing thread/task. (This is a correctness requirement, not a style preference: the prototype found that reusing the same mutable node objects across threads was a real data-race hazard and redesigned around it. Whatever concurrency model the implementation language provides — OS threads, green threads, async tasks — the two must not touch each other's mutable state.)
- Track whether indexing has completed and how much time has elapsed since it started; both are needed for the delayed-loading-indicator behavior in §7 and §10.

## 6a. Live refresh on filesystem changes

The tree, and the background index (§6), must stay current as files and directories are added, moved, or deleted on disk underneath the running session — the user should not have to restart `dirtree` to see a change made by another process (an editor, `git checkout`, a build, etc.).

- The implementation must watch, at minimum, every directory that has already been loaded (§2's lazy-loading sense: the root at startup, plus any directory the user has expanded or that jump mode has revealed since). A directory never visited does not need to be watched — it will simply reflect current disk state whenever it is eventually loaded, same as today.
- Watching must not block or slow down interactive use; detected changes are applied asynchronously, the same way the background index (§6) is built without blocking the UI.
- Because change notifications can arrive in rapid bursts (an editor's save-via-temp-file-and-rename is often 2-3 raw events; a multi-file operation like `git checkout` can be dozens), the implementation should coalesce a burst into a single refresh rather than re-scanning once per raw event — a short debounce window (on the order of a few hundred milliseconds) is sufficient and keeps this from being a performance or flicker problem on active directories.
- **Applying a refresh:** re-list each already-loaded directory's contents and merge the result into the existing tree by path, rather than discarding and rebuilding it wholesale:
  - An entry whose path is unchanged (same path, still the same kind — file vs. directory) keeps its existing node identity, and therefore keeps its expanded/collapsed state and any already-loaded subtree, exactly as if it had never been touched.
  - An entry that no longer exists on disk is removed from the tree.
  - A newly-appeared entry is added as a new node (collapsed if a directory, per §2's default), sorted into place per §4.
  - This mirrors §5's existing "keep the previously-focused node selected by identity, not by index" rule — a refresh must not disturb selection or disclosure state for anything that didn't actually change.
- **Selection after a refresh:** if the currently-selected node was deleted by the change, selection falls back to the nearest ancestor still present in the tree (walking up from the deleted node); the root is always present, so this always terminates somewhere visible.
- The background index (§6) must eventually reflect the same change (so jump mode doesn't keep offering deleted paths or miss new ones). Re-triggering an index rebuild after a live-refresh is treated exactly like the initial index build for purposes of the delayed-loading-indicator (§10): a fast rebuild stays invisible, a slow one on a very large tree shows the same spinner/"indexing…" treatment a fresh build would.
- This is best-effort: if the underlying OS change-notification facility is unavailable or exhausted (e.g. a platform inotify-instance/watch-count limit), the implementation must degrade gracefully — continue running with the tree simply not auto-refreshing — rather than failing startup or crashing.

## 7. Jump mode (fuzzy finder)

Triggered by a dedicated key (`/` in the prototype) from the main tree view. While active:

- The screen switches to a **flat list view**: every entry from the background index (§6), rendered as its root-relative, slash-delimited path (not the tree's indented/marker style) — this is what makes it distinct from the tree view, and lets a query match on any path segment, not just a leaf name.
- A query string starts empty and accumulates/removes characters as the user types/backspaces. It is shown in the header (see §10).
- **Matching**: an empty query matches everything. A non-empty query:
  - If it contains any shell-wildcard character (`*`, `?`, `[`), it's matched via case-insensitive shell-glob matching against the *entire* candidate string (root-relative slash path for jump mode; bare name for any other filtering use).
  - Otherwise, it's a case-insensitive **substring** match against the same candidate string. (This is deliberate: most quick-jump queries are typed as plain fragments, not `*fragment*`, and requiring wildcard wrapping for the common case would be worse UX.)
  - Matching runs against the *entire tree's* index, not just currently-expanded/visible nodes — jump mode is global regardless of the interactive tree's current disclosure state.
- **While the background index has not finished building**: matches are empty/unavailable; render the delayed loading indicator described in §10 instead of "no matches" (don't claim there are no matches when you simply haven't looked yet).
- A `selected` index into the current match list, with wraparound cycling: advance forward (Tab, or Down) or backward (Shift-Tab, or Up).
- **Enter**, if there is at least one match: resolve the selected match's path in the interactive tree — expanding every ancestor directory along the path from the root down to the match (regardless of each ancestor's prior expanded/collapsed state) so the match becomes visible in the main tree view — then exit jump mode with that node selected and scrolled into view. If the resolution fails (e.g. the path no longer exists — deleted after indexing but before the jump), exit jump mode without changing tree selection.
- **Backspace**: remove the last character of the query; reset match-selection to the first match and reset scroll.
- **Escape**: cancel jump mode and return to the tree view unchanged (query and match selection are discarded).
- Typing any other printable character appends it to the query (reset match-selection and scroll to the top, since the match set changes).

## 8. File preview

Triggered by a dedicated key (Space in the prototype) from the main tree view, only when the current selection is a file (no-op on directories).

- **Reading**: read up to a fixed byte cap (1,000,000 bytes in the prototype — pick something in that neighborhood; it exists to keep memory/latency bounded on huge files, not to be a hard product requirement of that exact number) from the start of the file.
  - If a read error occurs (permission denied, etc.), show a single explanatory line instead of crashing.
  - If the read bytes contain a NUL byte, treat the file as binary and show a single "binary file, preview not available" line instead of attempting to render it.
  - Otherwise, decode as UTF-8, replacing invalid sequences rather than failing, and split into lines. If the file's actual size exceeds the byte cap, append a line noting the content was truncated at that many bytes.
  - An empty result set becomes a single empty line (so the preview always has at least one row to render).
- **Syntax highlighting** (best-effort, must not require a runtime dependency):
  - Attempt to categorize each line's text into a small fixed set of display categories: `comment`, `string`, `number`, `keyword`, `function`, `operator`, falling back to `text` for anything uncategorized (identifiers, punctuation, whitespace).
  - This does not need a general-purpose lexer framework. A pragmatic dependency-free approach: pick a lexer/rule-set by file extension (and/or a shebang-line sniff for extensionless scripts) from a small built-in table covering common languages actually present in `homeserver` and sibling repos (at minimum: shell, Python, Go, YAML, JSON, Markdown, Dockerfile, HCL/Terraform, CUE) — regex or hand-written scanning per language is acceptable; there is no requirement to match a general tokenizer's exact token boundaries, only to produce a reasonable, stable per-line categorization.
  - If no rule-set matches the file, or highlighting fails for any reason, fall back to rendering the whole file as plain `text` — never let a highlighting failure block the preview from showing.
  - Highlighting produces, per source line, an ordered list of `(text_fragment, category)` segments that concatenate back to the original line.
- **Line wrapping**: each source line's segments are wrapped to fit a target display width (in columns), splitting fragments mid-token as needed so no wrapped row exceeds the width. Wrapping must be recomputed whenever the available width changes (e.g. terminal resize, or split/popup layout switching — see §9). Each wrapped row remembers which source line it came from; only a source line's *first* wrapped row carries that line's number (continuation rows are unnumbered), so the gutter (§10) only prints a number once per source line.
- **Line-number gutter**: reserve a fixed-width column (wide enough for the largest line number in the file) plus a short separator, printed to the left of content on every row; continuation rows print blank space in the number column instead of repeating or incrementing.
- **Scrolling**: Up/Down scroll by one display row; Page Up/Page Down scroll by one viewport height; scrolling is clamped so it never goes negative or past the point where the last display row would leave the viewport.
- **Goto-line** (`g` key): prompt for a numeric line number at the bottom of the preview area; Enter jumps the preview's scroll position to that source line's first display row (clamped to `[1, total source lines]`); Escape cancels the prompt without changing scroll; only digit and backspace input is accepted while the prompt is open.
- **Closing**: `q` or Escape closes the preview and returns to the tree view, wherever the tree selection was left.

## 9. Layout: split view vs. popup

The preview can render in one of two layouts, chosen every frame from the current terminal dimensions (not decided once at preview-open time, so a live resize can flip between them):

- **Split view** (wide terminal): the tree pane occupies the left side of the screen (below the header), sized just wide enough to fit the longest currently-visible tree row's rendered label (indentation + expand marker + name) at the current disclosure state, clamped to a sane minimum and maximum so one long name can't crowd out the preview, and the preview pane occupies the remaining width on the right, at full height below the header row. The preview pane's *own width* is capped at a fixed maximum (120 columns in the prototype, arrived at after iteration — treat it as configurable/tunable, not sacred); once the terminal is wide enough that the preview would exceed that cap, the *extra* width goes to growing the tree pane instead of stretching the preview further. This is a deliberate distinction: bound the preview window's width, not the width of the wrapped content within it — capping content width was tried first and rejected because it wasted the freed space instead of giving it to the tree pane.
  - The tree pane in split view is read-only (renders the current selection highlight but does not accept navigation keys — all keys go to the preview until it's closed) and is visually separated from the preview by a vertical rule.
- **Popup** (narrow terminal): a centered, bordered floating window over the (unmodified, last-rendered) tree view, sized to the terminal minus a fixed margin, with its own title (the file name) and a footer hint line.
- **Threshold**: split view is used when the total terminal width is at least the computed tree-pane width plus a minimum usable preview width (40 columns in the prototype) plus one separator column; otherwise fall back to popup. Recompute this every frame.

## 10. Rendering conventions

- **Header/title bar**: a single full-width row at the very top of the screen, rendered with a background contrasting from the normal row background *and* from the reverse-video selection highlight (so the title bar is never visually indistinguishable from a selected row directly beneath it — the prototype initially made this mistake using plain reverse-video for both and had to give the title bar a distinct fixed color pair instead). Content:
  - Tree view: the root path and a short keybinding legend.
  - Jump mode: the literal query typed so far (e.g. `/foo`) and a short keybinding legend.
  - Preview (split view only; popup uses its own window title/footer instead): the file name being previewed and its keybinding legend.
- **Selected row**: rendered in reverse video (or an equivalent single, consistent "selected" visual treatment) relative to unselected rows.
- **Directory expand/collapse marker**: a small glyph before the name distinguishing expanded vs. collapsed directories (e.g. a down-caret vs. right-caret); files get equivalent blank spacing so names still align vertically.
- **Indentation**: each row is indented proportionally to its depth in the tree, so the hierarchy is visually legible.
- **Per-node error indicator**: if a node failed to list its children (see §2), append a bracketed short error string after its name.
- **Delayed loading indicator** (indexing spinner / badge): while the background index (§6) has not finished, an animated spinner is available for use in two places — a small floating badge anchored to the bottom-right corner of the tree view, and, in jump mode, replacing the match list entirely with an "indexing…" message. In both cases, **suppress the indicator entirely until indexing has been running for at least a short threshold** (250ms in the prototype). This is a deliberate UX fix: on a small tree, indexing finishes in a handful of milliseconds, and briefly flashing a spinner for genuinely instant work reads as more distracting than informative — so nothing is shown at all until it's clear the wait is actually perceptible. During that sub-threshold grace period in jump mode specifically, render neither the spinner nor a "no matches" message (since indexing not being done yet is a different state from "genuinely zero matches") — just leave the match-list area blank.
  - Compute the spinner glyph by cycling a small fixed set of animation frames at a fixed rate (10 frames/sec in the prototype) driven by elapsed wall-clock time since indexing started, not by frame count, so its speed is independent of render/poll rate.
  - The bottom-right badge is rendered with a background that contrasts with the surrounding tree rows (an accent color in the prototype), the same "must read as visually distinct" rule the header bar (above) follows — not plain/default-styled text sitting over the tree, which is easy to miss in a busy view.
- **Completion message** (bottom-right badge only): once indexing finishes, replace the spinner badge with a transient "indexing complete" message, shown in full for a short display duration (2 seconds in the prototype), then faded out over a shorter fade duration (on the order of a few hundred ms) by disappearing left-to-right — the message's earliest characters vanish first while its right edge stays anchored at the same screen position the spinner badge occupied — until nothing remains. This only applies to the badge; jump mode's replacement of the match list is not affected, since once indexing is done the real match list should render immediately rather than being delayed by a completion message.
- **Debug-only always-show mode**: a build-time-only switch (never a runtime flag, and never enabled in a shipped build) that bypasses only the bottom-right badge's perceptibility threshold above, not the completed-index check — so the spinner appears the instant indexing starts, indexing still runs to completion normally, and the completion message and fade-out are reached right after, letting both animations be watched on demand without needing to reproduce a genuinely slow index. It additionally holds the spinner on screen for a minimum duration (2 seconds in the prototype) even when indexing genuinely finishes sooner — real directories usually index in microseconds, so without a floor the spinner would flash a single frame and vanish before it's visible — by treating indexing as not-yet-done until that minimum has elapsed, then handing off to the completion message as normal. This must not affect jump mode's indexing-blocked state, since forcing that indefinitely would make jump mode permanently unusable in a debug build.

## 11. Resize handling

The terminal must be treated as capable of changing size at any time, live, without necessarily receiving a dedicated resize notification — the primary target usage is inside terminal multiplexers (Zellij and similar) that do not reliably deliver resize events to the child process. Therefore:

- The main input-wait must not block indefinitely; it must wake on a short periodic timeout (100ms in the prototype) even with no input, so layout can be recomputed and redrawn on that cadence regardless of whether an explicit resize signal fired.
- Every draw must recompute layout (row/column counts, split-vs-popup decision, pane widths, line-wrap widths) from the terminal's *current* actual dimensions queried fresh each frame — never cache dimensions across frames and trust a resize event to invalidate the cache as the sole mechanism.
- If the implementation's terminal library does deliver an explicit resize event/signal, treat it as a hint to redraw immediately rather than the only trigger to do so.

## 12. Escape-key responsiveness

Terminal input libraries commonly buffer a short delay after receiving an Escape byte, to disambiguate a bare Escape keypress from the start of a longer escape sequence (arrow keys, function keys, etc.). Left at defaults, this can make Escape feel like it takes up to ~1 second to register. The implementation must configure its terminal library's escape-sequence timeout down to a short value (on the order of 10–25ms) at startup, before entering the main loop, so Escape reads as instant. (In the prototype, this was set via both an environment variable read at import time and an explicit API call at runtime, to cover two different code paths that read the timeout at different times — whatever the chosen library's equivalent mechanism is, set it early and set it redundantly if the library offers more than one knob for it.)

## 13. Keybindings summary

| Key | Context | Effect |
|---|---|---|
| Up / Down | tree view | move selection, wraps at both ends |
| Right | tree view | expand a collapsed dir, or descend into an expanded dir's first child |
| Left | tree view | collapse an expanded dir; move a collapsed dir's selection to its parent; on a file, jump to parent and collapse it |
| Space | tree view, file selected | open preview (split or popup per §9) |
| `/` | tree view | enter jump mode |
| `q` / Escape | tree view | quit |
| (typing) | jump mode | append to query, filters live |
| Backspace | jump mode | remove last query character |
| Tab / Down | jump mode | next match (wraps) |
| Shift-Tab / Up | jump mode | previous match (wraps) |
| Enter | jump mode | jump to selected match in tree view, disclosing ancestors; exits jump mode |
| Escape | jump mode | cancel, return to tree view unchanged |
| Up / Down | preview | scroll one row |
| Page Up / Page Down | preview | scroll one viewport height |
| `g` | preview | prompt for a line number, jump to it |
| `q` / Escape | preview | close preview, return to tree view |

Escape is the universal "get me out of here" key everywhere it's listed above: closes whatever overlay/mode is currently active, or quits if none is active.

## 14. Explicitly out of scope for parity

- Mouse support was deliberately removed from the prototype (terminal multiplexer interception made it unreliable) and is **not required**. If an implementation wants to add it back for environments where it works, it must not be the only way to perform any action — full keyboard parity per §13 is required regardless.
- Exact color choices, exact spinner glyph set, and exact numeric tuning constants (byte cap, pane width cap, badge delay, spinner fps, min/max tree-pane width) are not required to match the prototype's literal values — they're implementation-tunable. What's required is the *behavior* those constants produce (bounded, sane, non-flashing, non-jarring), not the specific numbers.
