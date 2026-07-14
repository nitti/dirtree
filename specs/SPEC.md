# dirtree — behavioral specification

This document specifies observable behavior only. Any data structure, module split, or algorithm name below is a *description of behavior*, not an implementation mandate — implement it however is idiomatic in the chosen language, as long as the described behavior holds.

## 1. CLI

```
dirtree [path]
```

- `path` is optional, positional, defaults to `.` (the process's current working directory).
- The path is resolved to an absolute path at startup.
- If the resolved path is not a directory, exit immediately with a non-zero status and a one-line error to stderr (e.g. `dirtree: not a directory: <path>`). Do not enter the terminal UI.
- On successful startup, the primary view is the file preview (§10), initially empty (no files open), with the tree explorer overlay (§5, §10) automatically opened on top of it — see §10 for the full view model.
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
- an optional error string, set when the node's directory entries could not be listed (e.g. permission denied); on error, treat the node as having zero children rather than propagating an exception, and render the error inline (see §11).

**Root node initialization:** on startup, build the root node, load its `.gitignore` (see §3), load its children, and mark it expanded.

**Flattening (visible list):** the tree explorer operates over the currently-visible, depth-first, pre-order flattening of the tree: the root, then — if the root is expanded — each child's own flattening, recursively. A node's subtree contributes to this list only while every ancestor down to the root is expanded. This is "progressive disclosure": collapsed directories hide their descendants from the visible list (and from on-screen rendering) but the descendants still exist in the lazily-loaded tree once visited.

## 3. Traversal and ignore rules

Three independent exclusion mechanisms apply identically to (a) normal directory listing/expansion and (b) the background full-tree index used by the jump/fuzzy-picker mode (§7):

1. **Always skip `.git`** (exact name match on any path segment's immediate entry, not a pattern) — unconditionally, regardless of `.gitignore` contents. Git internals are never useful to browse or search and can be large.
2. **`.gitignore`-aware filtering**, best-effort:
   - At root-node construction, look for a `.gitignore` file directly in the root path. If present and readable, parse it into an ignore-pattern set (see the matching algorithm below). If absent, unreadable, or pattern-parsing isn't implemented for a given rule, treat it as "no additional patterns" — never crash or block startup because of a malformed `.gitignore`.
   - When listing a directory's entries (or walking the full tree for the index), drop any entry that matches the loaded pattern set, so an ignored directory's contents are never even enumerated (not just hidden after listing).
   - Only the root's `.gitignore` needs to be honored. (The prototype did not walk nested `.gitignore` files in subdirectories; parity does not require it, though an implementation may choose to go further.)
3. **`.dirtreeignore`-aware filtering**, an application-specific exclusion list independent of `.gitignore`, applied identically everywhere `.gitignore` is (tree listing, index walk — never just the fuzzy picker; a path either exists in the app or it doesn't):
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

## 5. Tree explorer navigation semantics

The tree explorer is the overlay used to browse and open files (see §10 for when it's shown). Its internal navigation model is unchanged from a plain tree browser:

State: a `selected` index into the current flattened/visible list, and a `scroll` offset (topmost visible row index) used only for rendering (§11).

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
- **Space**, when the current selection is a file (no-op on directories): open the file (§9's open semantics). If the result is "opened," close the tree explorer overlay and display it in the primary preview view — this is the default, immediate "open and go" action. If the result is "binary," see §9's binary-file signaling — the explorer stays open instead.
- **`a`**, when the current selection is a file (no-op on directories): open the file (§9's open semantics) exactly as Space does, but on an "opened" result leave the tree explorer overlay open and selection unchanged, so several files can be queued up in a row before returning to the preview. A "binary" result behaves the same as it does for Space (§9) — the explorer already stays open either way, so the only visible effect is the inline message.
- **`/`**: open the jump/fuzzy-picker overlay (§7) with this tree explorer as its entry context (default action on Enter: reveal-in-tree).
- **Escape**: close the tree explorer overlay and return to the primary preview view unchanged (whatever was displayed, or the empty state, stays as it was).

## 6. Background full-tree index (for the jump/fuzzy-picker mode)

The jump/fuzzy-picker mode (§7) needs to search the *entire* tree regardless of what's currently expanded, but building that list can be slow on a large tree if done synchronously when it's first opened. So:

- Immediately at startup (after the root node is built), kick off building a **background index**: a flat, recursively-walked list of every path under the root (files and directories, root itself excluded), applying the same skip/ignore rules as §3, sorted by its root-relative slash-delimited display path, case-insensitively.
- This must run concurrently with the UI being immediately usable (i.e. the interactive tree explorer must not block waiting for the index).
- The index-building walk must guard against symlink cycles (track resolved/canonical real paths already visited; do not re-descend into one already seen).
- The index-building process must not share mutable state with the interactive tree's node objects — it operates on raw paths only, so no locking is required between the UI thread/task and the indexing thread/task. (This is a correctness requirement, not a style preference: the prototype found that reusing the same mutable node objects across threads was a real data-race hazard and redesigned around it. Whatever concurrency model the implementation language provides — OS threads, green threads, async tasks — the two must not touch each other's mutable state.)
- Track whether indexing has completed and how much time has elapsed since it started; both are needed for the delayed-loading-indicator behavior in §7 and §11.

## 6a. Live refresh on filesystem changes

The tree, and the background index (§6), must stay current as files and directories are added, moved, or deleted on disk underneath the running session — the user should not have to restart `dirtree` to see a change made by another process (an editor, `git checkout`, a build, etc.).

- The implementation must watch, at minimum, every directory that has already been loaded (§2's lazy-loading sense: the root at startup, plus any directory the user has expanded or that the jump/fuzzy-picker mode has revealed since). A directory never visited does not need to be watched — it will simply reflect current disk state whenever it is eventually loaded, same as today.
- Watching must not block or slow down interactive use; detected changes are applied asynchronously, the same way the background index (§6) is built without blocking the UI.
- Because change notifications can arrive in rapid bursts (an editor's save-via-temp-file-and-rename is often 2-3 raw events; a multi-file operation like `git checkout` can be dozens), the implementation should coalesce a burst into a single refresh rather than re-scanning once per raw event — a short debounce window (on the order of a few hundred milliseconds) is sufficient and keeps this from being a performance or flicker problem on active directories.
- **Applying a refresh:** re-list each already-loaded directory's contents and merge the result into the existing tree by path, rather than discarding and rebuilding it wholesale:
  - An entry whose path is unchanged (same path, still the same kind — file vs. directory) keeps its existing node identity, and therefore keeps its expanded/collapsed state and any already-loaded subtree, exactly as if it had never been touched.
  - An entry that no longer exists on disk is removed from the tree.
  - A newly-appeared entry is added as a new node (collapsed if a directory, per §2's default), sorted into place per §4.
  - This mirrors §5's existing "keep the previously-focused node selected by identity, not by index" rule — a refresh must not disturb selection or disclosure state for anything that didn't actually change.
- **Selection after a refresh:** if the currently-selected node in the tree explorer was deleted by the change, selection falls back to the nearest ancestor still present in the tree (walking up from the deleted node); the root is always present, so this always terminates somewhere visible. A file that's currently open in the open-files list (§9) is unaffected by tree selection fallback — its list entry and any loaded preview content simply become stale if the underlying file is deleted (see §9's removal semantics for what happens if the user then tries to act on it).
- The background index (§6) must eventually reflect the same change (so the jump/fuzzy-picker mode doesn't keep offering deleted paths or miss new ones). Re-triggering an index rebuild after a live-refresh is treated exactly like the initial index build for purposes of the delayed-loading-indicator (§11): a fast rebuild stays invisible, a slow one on a very large tree shows the same spinner/"indexing…" treatment a fresh build would.
- This is best-effort: if the underlying OS change-notification facility is unavailable or exhausted (e.g. a platform inotify-instance/watch-count limit), the implementation must degrade gracefully — continue running with the tree simply not auto-refreshing — rather than failing startup or crashing.

## 7. Jump / fuzzy-picker mode

A single overlay and a single shared matcher/index power two related workflows: revealing a path in the tree explorer, and opening a file into the preview. Which one is the default action depends on where the overlay was opened from; either action is always reachable regardless of entry point.

**Entry points:**

- From the **tree explorer** overlay (§5), via `/`: default action on Enter is **reveal-in-tree** (today's exact prototype behavior).
- From the **primary preview view** (§10), via `/` — including while it's showing the empty state — default action on Enter is **open-into-list** (§9).

**While active**, regardless of entry point:

- The screen the overlay was opened from is fully replaced by a **flat list view**: every entry from the background index (§6), rendered as its root-relative, slash-delimited path (not the tree's indented/marker style) — this is what makes it distinct from the tree explorer, and lets a query match on any path segment, not just a leaf name.
- A query string starts empty and accumulates/removes characters as the user types/backspaces. It is shown in the header (see §11).
- **Matching**: an empty query matches everything. A non-empty query:
  - If it contains any shell-wildcard character (`*`, `?`, `[`), it's matched via case-insensitive shell-glob matching against the *entire* candidate string.
  - Otherwise, it's a case-insensitive **substring** match against the same candidate string. (This is deliberate: most quick-jump queries are typed as plain fragments, not `*fragment*`, and requiring wildcard wrapping for the common case would be worse UX.)
  - Matching runs against the *entire tree's* index, not just currently-expanded/visible nodes or currently-open files — this mode is global regardless of any other UI state.
- **While the background index has not finished building**: matches are empty/unavailable; render the delayed loading indicator described in §11 instead of "no matches" (don't claim there are no matches when you simply haven't looked yet).
- A `selected` index into the current match list, with wraparound cycling: advance forward (Tab, or Down) or backward (Shift-Tab, or Up).
- **Enter**, if there is at least one match: perform this entry point's default action (below) on the selected match. Reveal-in-tree always exits the overlay; open-into-list exits the overlay only on an "opened" result, per §9's binary-file signaling.
- **Space**, if there is at least one match: perform the *other* action (below) on the selected match, with the same exit behavior as Enter's for whichever action that is. (I.e. Enter and Space always together cover both actions; which key maps to which action is the only thing that changes with entry point.)
- **Backspace**: remove the last character of the query; reset match-selection to the first match and reset scroll.
- **Escape**: cancel the overlay and return to whichever screen it was opened from, unchanged (query and match selection are discarded; no action is performed).
- Typing any other printable character appends it to the query (reset match-selection and scroll to the top, since the match set changes).

**The two actions:**

- **Reveal-in-tree**: resolve the selected match's path in the interactive tree — expanding every ancestor directory along the path from the root down to the match (regardless of each ancestor's prior expanded/collapsed state) so the match becomes visible in the tree explorer — then leave the tree explorer overlay open (opening it first if the picker was entered from preview) with that node selected and scrolled into view. If resolution fails (e.g. the path no longer exists — deleted after indexing but before the jump), exit the overlay without changing tree selection, landing back on the tree explorer (opening it if needed) unchanged.
- **Open-into-list**: open the selected match's path per §9's open semantics (reusing an existing open-files entry if the path is already open). If the result is "opened," close the tree explorer overlay if it was open, landing on the primary preview view with that file displayed. If the result is "binary," see §9's binary-file signaling — this overlay stays open with the message shown inline instead of exiting.

## 8. File preview

Each entry in the open-files list (§9) has its own preview content and its own scroll/goto-line state, computed and tracked independently — the mechanics below apply per open file, not globally.

- **Reading**: read up to a fixed byte cap (1,000,000 bytes in the prototype — pick something in that neighborhood; it exists to keep memory/latency bounded on huge files, not to be a hard product requirement of that exact number) from the start of the file. This happens once, when the file is opened (§9); an already-open entry's content is not re-read on every display. Binary-ness (§9's binary check) is determined from this same read, before deciding whether to create an open-files entry at all — a file found to be binary never reaches the rest of this section, since §9 short-circuits the open before an entry is created; the reading/highlighting/wrapping/scrolling described below only ever applies to non-binary entries that did get created.
  - If a read error occurs (permission denied, etc.), show a single explanatory line instead of crashing.
  - Otherwise, decode as UTF-8, replacing invalid sequences rather than failing, and split into lines. If the file's actual size exceeds the byte cap, append a line noting the content was truncated at that many bytes.
  - An empty result set becomes a single empty line (so the preview always has at least one row to render).
- **Syntax highlighting** (best-effort, must not require a runtime dependency):
  - Attempt to categorize each line's text into a small fixed set of display categories: `comment`, `string`, `number`, `keyword`, `function`, `operator`, falling back to `text` for anything uncategorized (identifiers, punctuation, whitespace).
  - This does not need a general-purpose lexer framework. A pragmatic dependency-free approach: pick a lexer/rule-set by file extension (and/or a shebang-line sniff for extensionless scripts) from a small built-in table covering common languages actually present in `homeserver` and sibling repos (at minimum: shell, Python, Go, YAML, JSON, Markdown, Dockerfile, HCL/Terraform, CUE) — regex or hand-written scanning per language is acceptable; there is no requirement to match a general tokenizer's exact token boundaries, only to produce a reasonable, stable per-line categorization.
  - If no rule-set matches the file, or highlighting fails for any reason, fall back to rendering the whole file as plain `text` — never let a highlighting failure block the preview from showing.
  - Highlighting produces, per source line, an ordered list of `(text_fragment, category)` segments that concatenate back to the original line.
- **Line wrapping**: each source line's segments are wrapped to fit a target display width (in columns), splitting fragments mid-token as needed so no wrapped row exceeds the width. Wrapping must be recomputed whenever the available width changes (e.g. terminal resize, or split/popup layout switching — see §10). Each wrapped row remembers which source line it came from; only a source line's *first* wrapped row carries that line's number (continuation rows are unnumbered), so the gutter (§11) only prints a number once per source line.
- **Line-number gutter**: reserve a fixed-width column (wide enough for the largest line number in the file) plus a short separator, printed to the left of content on every row; continuation rows print blank space in the number column instead of repeating or incrementing.
- **Scrolling**: Up/Down scroll by one display row; Page Up/Page Down scroll by one viewport height; scrolling is clamped so it never goes negative or past the point where the last display row would leave the viewport. Scroll position is stored per open-files entry (§9) and restored exactly when switching back to that entry.
- **Goto-line** (`g` key): prompt for a numeric line number at the bottom of the preview area; Enter jumps the current entry's scroll position to that source line's first display row (clamped to `[1, total source lines]`); Escape cancels the prompt without changing scroll; only digit and backspace input is accepted while the prompt is open.
- **Empty state**: when the open-files list has no entries (fresh startup, or the last entry was just closed), the preview view renders a short explanatory message (e.g. hinting at `e` to browse or `/` to search) instead of gutter/content rows, and none of the scrolling/goto-line keys apply.

## 9. Open files list

The open-files list is the primary state the rest of the UI operates on: an ordered collection of files the user has opened during this session, plus which one (if any) is currently displayed in the primary preview view (§10).

- **Ordering**: insertion order. A newly-opened file (one whose resolved absolute path is not already in the list) is appended to the end. Opening a file whose resolved absolute path already matches an existing entry does not create a duplicate or change that entry's position — see "open semantics" below.
- **Per-entry state**: absolute path, loaded preview content (§8's read/highlight results, loaded once at open time), and independent scroll/goto-line state (§8). Exactly one entry, or none, is the "displayed" entry at any time.
- **Open semantics** (used by §5's Space/`a` and §7's open-into-list action): given a path,
  - if an entry for that resolved absolute path already exists in the list, do not read the file again or move the entry — just mark it as the displayed entry, preserving its existing scroll/goto state. (A file that is binary never has an entry, per the next bullet, so any existing entry is by construction non-binary and safe to display as-is.)
  - otherwise, read up to the byte cap (§8) from the start of the file to determine binary-ness: if the read bytes contain a NUL byte, the open is a **binary result** — do not create an entry, do not change the currently-displayed entry (if any), and return that result to the caller instead of a displayable file. See "Binary-file signaling" below for what each caller does with it.
  - otherwise (readable and not binary), continue reading/highlighting the file (§8), append a new entry at the end of the list with scroll reset to the top, and mark it as the displayed entry. This is an **opened result**.
- **Displaying** an entry (making it the one shown in the primary preview view) never changes list order.
- **Binary-file signaling**: an open call's result is one of "opened" (an entry now exists and is displayed) or "binary" (nothing changed). The two callers each handle a binary result by staying exactly where they were and surfacing a single "binary file, preview not available" message directly, in whatever form fits that context — the open-files list and primary preview view are untouched in this case, since no entry was created:
  - From the tree explorer overlay (§5's Space or `a`): the explorer does **not** close (even for Space, whose normal behavior is to close and display) and selection does not move; the message is shown inline in the explorer (e.g. a transient status/footer line) instead.
  - From the jump/fuzzy-picker overlay's open-into-list action (§7): the overlay does **not** exit and match selection does not move; the message is shown inline (e.g. in place of the header's keybinding legend, or an equivalent status line) instead of landing on the preview.
  - The message is advisory only and does not block further input — the user can immediately navigate elsewhere or attempt to open a different file.

### Open-files list overlay

Triggered by `Tab` from the primary preview view (including the empty state). While active:

- The screen switches to a list view of every current open-files entry, in list order, each rendered as its root-relative, slash-delimited path (same path-rendering convention as the jump/fuzzy-picker mode, §7), with the currently-displayed entry (if any) marked distinctly.
- A `selected` index into the list, with wraparound cycling on Up/Down (same wrap semantics as §5).
- **Enter**: mark the selected entry as displayed, then close the overlay, returning to the primary preview view showing it.
- **`x`**: remove the selected entry from the list.
  - If the removed entry was not the displayed entry, the displayed entry is unaffected; only the list itself shrinks, and overlay selection is clamped to the nearest remaining index (preferring the entry that was next after the removed one, falling back to the new last entry if the removed entry was last).
  - If the removed entry *was* the displayed entry, the displayed entry becomes the adjacent surviving entry (the one after it in list order, or the one before it if the removed entry was last); overlay selection follows the same entry.
  - If the list becomes empty as a result, there is no displayed entry; closing the overlay (or its own auto-close, see below) lands on the primary preview view's empty state (§8), which in turn auto-opens the tree explorer overlay exactly as it does on startup (§1).
  - The overlay itself stays open after an `x` removal (it does not auto-close), so multiple entries can be removed in a row; it only auto-closes if the removal just emptied the list entirely, per the previous bullet.
- **Escape**: close the overlay and return to the primary preview view unchanged — whatever was displayed before opening the overlay is still displayed (removals already performed via `x`, if any, are not undone; only the "make this one displayed" action of Enter is what Escape skips).
- If the list is empty when the overlay is opened (a degenerate case reachable by escaping out of the auto-opened tree explorer on a fresh, file-less session and then pressing Tab), render an explanatory "no open files" message instead of a list, and only Escape is meaningful.

## 10. View model and overlay layout

The **primary preview view** (§8) is the default screen: on startup it shows the empty state with the tree explorer automatically opened on top of it (§1); once at least one file has been opened, it shows that file (or whichever was most recently made the displayed entry) whenever no overlay is active.

Three overlays exist, each opened from the primary preview view or (for the jump/fuzzy-picker mode) also from the tree explorer, and each closed by Escape back to whatever was showing before it: the **tree explorer** (§5), the **jump/fuzzy-picker mode** (§7), and the **open-files list** (§9). Only one overlay is active at a time.

**Tree explorer layout** (the only overlay with a dual layout; the jump/fuzzy-picker and open-files-list overlays always fully replace the screen they were opened from, per §7 and §9) is chosen every frame from the current terminal dimensions (not decided once at open-time, so a live resize can flip between them):

- **Split view** (wide terminal): the tree explorer occupies the left side of the screen (below the header), sized just wide enough to fit the longest currently-visible tree row's rendered label (indentation + expand marker + name) at the current disclosure state, clamped to a sane minimum and maximum so one long name can't crowd out the preview, and the primary preview view — still visible and still showing whatever file (or empty state) it had — occupies the remaining width on the right, at full height below the header row. The preview pane's *own width* is capped at a fixed maximum (120 columns in the prototype, arrived at after iteration — treat it as configurable/tunable, not sacred); once the terminal is wide enough that the preview would exceed that cap, the *extra* width goes to growing the tree explorer pane instead of stretching the preview further. This is a deliberate distinction: bound the preview window's width, not the width of the wrapped content within it — capping content width was tried first and rejected because it wasted the freed space instead of giving it to the tree explorer pane.
  - The preview pane in split view is read-only while the tree explorer overlay is active (renders its current content but does not accept scrolling/goto-line keys — all keys go to the tree explorer until it's closed) and is visually separated from the tree explorer by a vertical rule.
- **Popup** (narrow terminal): a centered, bordered floating window over the (unmodified, last-rendered) primary preview view, sized to the terminal minus a fixed margin, with its own title (the tree's root path) and a footer hint line.
- **Threshold**: split view is used when the total terminal width is at least the computed tree-explorer-pane width plus a minimum usable preview width (40 columns in the prototype) plus one separator column; otherwise fall back to popup. Recompute this every frame.

## 11. Rendering conventions

- **Header/title bar**: a single full-width row at the very top of the screen, rendered with a background contrasting from the normal row background *and* from the reverse-video selection highlight (so the title bar is never visually indistinguishable from a selected row directly beneath it — the prototype initially made this mistake using plain reverse-video for both and had to give the title bar a distinct fixed color pair instead). Content:
  - Primary preview view: the displayed file's name (or an empty-state hint if none), and a short keybinding legend.
  - Tree explorer overlay: the root path and a short keybinding legend.
  - Jump/fuzzy-picker overlay: the literal query typed so far (e.g. `/foo`) and a short keybinding legend reflecting which action is bound to Enter vs. Space for the current entry point (§7).
  - Open-files-list overlay: a short label (e.g. "open files") and a short keybinding legend.
- **Selected row**: rendered in reverse video (or an equivalent single, consistent "selected" visual treatment) relative to unselected rows.
- **Directory expand/collapse marker**: a small glyph before the name distinguishing expanded vs. collapsed directories (e.g. a down-caret vs. right-caret); files get equivalent blank spacing so names still align vertically.
- **Indentation**: each row is indented proportionally to its depth in the tree, so the hierarchy is visually legible.
- **Per-node error indicator**: if a node failed to list its children (see §2), append a bracketed short error string after its name.
- **Delayed loading indicator** (indexing spinner / badge): while the background index (§6) has not finished, an animated spinner is available for use in two places — a small floating badge anchored to the bottom-right corner of the screen, rendered in *both* the tree explorer overlay and the jump/fuzzy-picker overlay (so the badge's sequence below stays visible/watchable whichever one is currently showing), and, in the jump/fuzzy-picker overlay specifically, also replacing the match list entirely with an "indexing…" message. In both cases, **suppress the indicator entirely until indexing has been running for at least a short threshold** (250ms in the prototype). This is a deliberate UX fix: on a small tree, indexing finishes in a handful of milliseconds, and briefly flashing a spinner for genuinely instant work reads as more distracting than informative — so nothing is shown at all until it's clear the wait is actually perceptible. During that sub-threshold grace period in the jump/fuzzy-picker overlay specifically, render neither the spinner nor a "no matches" message in the match-list area (since indexing not being done yet is a different state from "genuinely zero matches") — just leave the match-list area blank; the corner badge follows its own sequence below independent of the match-list area's blank state.
  - Compute the spinner glyph by cycling a small fixed set of animation frames at a fixed rate (10 frames/sec in the prototype) driven by elapsed wall-clock time since indexing started, not by frame count, so its speed is independent of render/poll rate.
  - The bottom-right badge is rendered with a background that contrasts with the surrounding rows (an accent color in the prototype), the same "must read as visually distinct" rule the header bar (above) follows — not plain/default-styled text sitting over the view, which is easy to miss in a busy screen.
- **Bottom-right badge sequence**: the badge (unlike the jump/fuzzy-picker overlay's match-list-area indicator, which is just shown/hidden per the threshold above) runs through a full sequence once indexing has crossed the perceptibility threshold, identically whether the tree explorer or jump/fuzzy-picker overlay is currently on screen:
  1. **Spinner**: shown as soon as the threshold is crossed, animating as described above.
  2. **Minimum display duration**: the spinner stays on screen for at least a short minimum duration (1 second in the prototype, measured from when indexing started) even if indexing genuinely finishes sooner — real directories often index in microseconds, so without this floor the spinner could cross the threshold and finish in the same frame, an unreadable flash rather than a perceptible indicator.
  3. **Completion message**: once both indexing has actually finished and the minimum display duration has elapsed, the spinner is replaced with a transient "indexing complete" message, shown in full for a short display duration (2 seconds in the prototype).
  4. **Fade-out**: the completion message then fades out over a shorter fade duration (on the order of a few hundred ms) by disappearing left-to-right — its earliest characters vanish first while its right edge stays anchored at the same screen position the spinner occupied — until nothing remains.
  - If indexing finishes before ever crossing the perceptibility threshold, none of this sequence runs at all — the spinner was never shown, so announcing its completion would be exactly the flashing-chrome-for-instant-work problem the threshold exists to avoid.
  - **Opening the jump/fuzzy-picker overlay short-circuits the minimum display duration**: if the user opens it while indexing is already done, they've directly seen indexing is ready — the overlay renders the real match list immediately once done, per §7. Continuing to hold the badge on step 1's spinner for the rest of step 2's minimum duration at that point would be actively misleading (claiming to still be working on something the user just saw finish), not a perceptibility safeguard, so opening the overlay while already done drops the minimum-display-duration floor entirely and treats *the moment the overlay was opened* — not the index's actual (possibly long-past) completion time — as when indexing finished, restarting steps 3/4 fresh from there. Because the badge renders in that overlay too, this transition is immediately visible right there without needing to return to the tree explorer first. This distinction matters in particular for the debug-only always-show mode below: without it, the index's real completion time could already be well past the completion message's entire display+fade window (having been masked the whole time by the artificially-held spinner), and using it directly would make the badge vanish the instant the overlay opened instead of visibly transitioning to the completion message. This short-circuit resets the next time a new indexing cycle starts (e.g. a live-refresh-triggered rebuild, §6a), since the flash-prevention floor is meaningful again for that fresh run.
  - This whole sequence is specific to the badge; the jump/fuzzy-picker overlay's replacement of the match list is not affected by any of it — once indexing is actually done, the real match list should render immediately rather than being delayed by a completion message.
- **Debug-only always-show mode**: a build-time-only switch (never a runtime flag, and never enabled in a shipped build) that bypasses only step 1's perceptibility threshold — the spinner appears the instant indexing starts — while every other step of the sequence above (the minimum display duration, the completion message, the fade-out) behaves exactly as it otherwise would. This lets the full sequence be watched on demand without needing to reproduce a genuinely slow index. This must not affect the jump/fuzzy-picker overlay's indexing-blocked state, since forcing that indefinitely would make it permanently unusable in a debug build.

## 12. Resize handling

The terminal must be treated as capable of changing size at any time, live, without necessarily receiving a dedicated resize notification — the primary target usage is inside terminal multiplexers (Zellij and similar) that do not reliably deliver resize events to the child process. Therefore:

- The main input-wait must not block indefinitely; it must wake on a short periodic timeout (100ms in the prototype) even with no input, so layout can be recomputed and redrawn on that cadence regardless of whether an explicit resize signal fired.
- Every draw must recompute layout (row/column counts, split-vs-popup decision, pane widths, line-wrap widths) from the terminal's *current* actual dimensions queried fresh each frame — never cache dimensions across frames and trust a resize event to invalidate the cache as the sole mechanism.
- If the implementation's terminal library does deliver an explicit resize event/signal, treat it as a hint to redraw immediately rather than the only trigger to do so.

## 13. Escape-key responsiveness

Terminal input libraries commonly buffer a short delay after receiving an Escape byte, to disambiguate a bare Escape keypress from the start of a longer escape sequence (arrow keys, function keys, etc.). Left at defaults, this can make Escape feel like it takes up to ~1 second to register. The implementation must configure its terminal library's escape-sequence timeout down to a short value (on the order of 10–25ms) at startup, before entering the main loop, so Escape reads as instant. (In the prototype, this was set via both an environment variable read at import time and an explicit API call at runtime, to cover two different code paths that read the timeout at different times — whatever the chosen library's equivalent mechanism is, set it early and set it redundantly if the library offers more than one knob for it.)

## 14. Keybindings summary

| Key | Context | Effect |
|---|---|---|
| Up / Down | preview | scroll one row |
| Page Up / Page Down | preview | scroll one viewport height |
| `g` | preview | prompt for a line number, jump to it |
| `e` | preview | open tree explorer overlay |
| `Tab` | preview | open the open-files list overlay |
| `/` | preview | open jump/fuzzy-picker overlay (default action: open-into-list) |
| `q` / Escape | preview, no overlay active | quit |
| Up / Down | tree explorer | move selection, wraps at both ends |
| Right | tree explorer | expand a collapsed dir, or descend into an expanded dir's first child |
| Left | tree explorer | collapse an expanded dir; move a collapsed dir's selection to its parent; on a file, jump to parent and collapse it |
| Space | tree explorer, file selected | open file, close explorer, display it |
| `a` | tree explorer, file selected | open file, keep explorer open |
| `/` | tree explorer | open jump/fuzzy-picker overlay (default action: reveal-in-tree) |
| Escape | tree explorer | close overlay, return to preview unchanged |
| (typing) | jump/fuzzy-picker | append to query, filters live |
| Backspace | jump/fuzzy-picker | remove last query character |
| Tab / Down | jump/fuzzy-picker | next match (wraps) |
| Shift-Tab / Up | jump/fuzzy-picker | previous match (wraps) |
| Enter | jump/fuzzy-picker | perform this entry point's default action on the selected match, exit overlay |
| Space | jump/fuzzy-picker | perform the other action on the selected match, exit overlay |
| Escape | jump/fuzzy-picker | cancel, return to whichever screen it was opened from, unchanged |
| Up / Down | open-files list | move selection, wraps at both ends |
| Enter | open-files list | display selected entry, close overlay |
| `x` | open-files list | remove selected entry from the list |
| Escape | open-files list | close overlay, return to preview unchanged |

Escape is the universal "get me out of here" key everywhere it's listed above: closes whatever overlay is currently active, or quits if none is active (i.e. when the primary preview view itself is on screen).

## 15. Explicitly out of scope for parity

- Mouse support was deliberately removed from the prototype (terminal multiplexer interception made it unreliable) and is **not required**. If an implementation wants to add it back for environments where it works, it must not be the only way to perform any action — full keyboard parity per §14 is required regardless.
- Exact color choices, exact spinner glyph set, and exact numeric tuning constants (byte cap, pane width cap, badge delay, spinner fps, min/max tree-explorer-pane width) are not required to match the prototype's literal values — they're implementation-tunable. What's required is the *behavior* those constants produce (bounded, sane, non-flashing, non-jarring), not the specific numbers.
