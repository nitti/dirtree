# dirtree — behavioral specification

This document specifies observable behavior only. Any data structure, module split, or algorithm name below is a *description of behavior*, not an implementation mandate — implement it however is idiomatic in the chosen language, as long as the described behavior holds.

The sections below are ordered to mirror the app itself: start at the primary view (§2), then each way of getting a new file into it — the browser (§3) and quick open / jump to file (§4) — then how it's all laid out and rendered on screen (§5), then the ambient system behaviors that apply everywhere (§6), and finally the keybindings recap (§7).

## 1. CLI

```
dirtree [path]
```

- `path` is optional, positional, defaults to `.` (the process's current working directory).
- The path is resolved to an absolute path at startup.
- If the resolved path is not a directory, exit immediately with a non-zero status and a one-line error to stderr (e.g. `dirtree: not a directory: <path>`). Do not enter the terminal UI.
- On successful startup, the primary view is the file preview (§2.1), initially empty (no files open), with the browser overlay (§3) automatically opened on top of it — see §5.1 for the full view model.
- No other flags are required for behavioral parity with the prototype. Additional flags (e.g. `--version`, `--help`) are at the implementer's discretion but must not change default behavior.

## 2. Primary view: file preview and open-files list

This is the screen the app centers on: a single file preview, backed by an ordered list of every file the user has opened during the session. §2.1 covers the mechanics of rendering one file's content; §2.2 covers the list that holds multiple open files and how opening/displaying/closing one works; §2.3 covers the overlay used to switch between them.

### 2.1 File preview

Each entry in the open-files list (§2.2) has its own preview content and its own scroll/goto-line state, computed and tracked independently — the mechanics below apply per open file, not globally.

- **Reading**: read up to a fixed byte cap (1,000,000 bytes in the prototype — pick something in that neighborhood; it exists to keep memory/latency bounded on huge files, not to be a hard product requirement of that exact number) from the start of the file. This happens once, when the file is opened (§2.2); an already-open entry's content is not re-read on every display. Both a read failure (permission denied, path no longer exists, etc.) and binary-ness (§2.2's checks) are determined from this same read, before deciding whether to create an open-files entry at all — a file that fails to read or is found to be binary never reaches the rest of this section, since §2.2 short-circuits the open before an entry is created in either case; the highlighting/wrapping/scrolling described below only ever applies to entries that did get created, i.e. files that were successfully read and are not binary.
  - Decode as UTF-8, replacing invalid sequences rather than failing, and split into lines. If the file's actual size exceeds the byte cap, append a line noting the content was truncated at that many bytes.
  - **Tab expansion**: each line's tab characters are expanded to spaces up to the next tab-stop column (8 columns in the prototype, tab stops resetting at the start of every line), so a tab-indented file (a Go source file, for instance) renders with the same visual alignment a terminal would normally give it, rather than each tab occupying a single narrow column — this expansion happens once at read time, so highlighting, wrapping, line-number gutter alignment, and in-file find's column coordinates (§2.4) all operate on the already-expanded text with no separate tab-width bookkeeping of their own.
  - An empty result set becomes a single empty line (so the preview always has at least one row to render).
- **Syntax highlighting** (best-effort, must not require a *runtime* dependency — a statically-linked library compiled into the binary is fine, per the Language and Dependencies section of `CLAUDE.md`):
  - Attempt to categorize each line's text into a small fixed set of display categories: `comment`, `string`, `number`, `keyword`, `function`, `operator`, falling back to `text` for anything uncategorized (identifiers, punctuation, whitespace, and — for prose/markup lexers whose native token vocabulary doesn't map onto this code-oriented set — headings/emphasis/link text, which are folded onto the nearest existing category rather than left unhighlighted).
  - Language detection is by file extension/basename (and/or a shebang-line sniff for extensionless scripts), covering whatever languages the underlying lexer implementation supports; there is no requirement to match a general tokenizer's exact token boundaries, only to produce a reasonable, stable per-line categorization. A hand-written per-language rule table is one acceptable implementation; a vendored general-purpose lexer library (e.g. Go's `chroma`) mapped down onto the fixed category set above is another.
  - If no lexer/rule-set matches the file, or highlighting fails for any reason, fall back to rendering the whole file as plain `text` — never let a highlighting failure block the preview from showing.
  - Highlighting produces, per source line, an ordered list of `(text_fragment, category)` segments that concatenate back to the original line.
- **Line wrapping**: each source line's segments are word-wrapped to fit a target display width (in columns): a row only ever breaks in the middle of a word as a last resort, when the word doesn't fit within a full-width row even on its own — otherwise the break lands at the end of a run of spaces (those triggering spaces are dropped entirely rather than reappearing as leading whitespace on the next row — real trailing whitespace at a line's actual end is unaffected, only a wrap-induced break drops anything) or immediately after a dash/hyphen (kept on the row it ends, since the dash itself is the visual break marker, same as hyphenated prose). No wrapped row ever exceeds the width. Wrapping must be recomputed whenever the available width changes (e.g. terminal resize, or split/popup layout switching — see §5.1); copy mode (below) wraps the same way. Each wrapped row remembers which source line it came from; only a source line's *first* wrapped row carries that line's number (continuation rows are unnumbered), so the gutter (§5.2) only prints a number once per source line.
- **Line-number gutter**: reserve a fixed-width column (wide enough for the largest line number in the file) plus a short separator, printed to the left of content on every row; continuation rows print blank space in the number column instead of repeating or incrementing.
- **Copy mode** (`c` key, toggle, SPEC.md §7): strips the line-number gutter entirely (content occupies the full pane width instead) and renders every line in plain text, without syntax-highlighting color, so a terminal mouse selection dragged across the preview grabs nothing but the file's own characters — no gutter digits mixed in, and (though this was never actually a risk, since tcell applies color via terminal escape sequences outside the text stream, not literal codes a selection could copy) no ambiguity about it either. Copy mode still word-wraps exactly like normal display (above) — an earlier version instead disabled wrapping and clipped a line at the pane's edge, on the theory that wrapping's extra screen rows could pick up a line break a multi-row selection wouldn't otherwise have; that traded a cosmetic problem for a worse one, since anything past the clipped edge was never drawn at all and so could not be selected by any means regardless of technique. Word-wrapping keeps every character of a line visible and selectable somewhere on screen, which matters more than a multi-row selection occasionally picking up an extra line break at a wrap point — an inherent limitation of any fixed-width terminal grid that copy mode does not attempt to fully solve. In-file find's match highlighting (§2.4) remains visible in copy mode — it isn't literal selectable text any more than syntax color is, and staying visible is more useful than not. Copy mode is tracked per open-files entry (§2.2), independent across files like scroll/goto-line/find state above — switching to a different open file does not carry the setting over, and switching back restores whatever it was left at for that file. It applies wherever that entry's preview is drawn, including the split-view browser layout's read-only pane (§5.1). The file title bar (§5.2) renders in a visually distinct style while the displayed entry is in copy mode, and its content is tagged `[copy mode]` regardless of which of the file title bar's other states (idle, active find, find prompt) is otherwise showing, so its on/off state for the current file is always legible, not just from the legend text.
- **Scrolling**: Up/Down scroll by one display row; Page Up/Page Down scroll by one viewport height; scrolling is clamped so it never goes negative or past the point where the last display row would leave the viewport. Scroll position is stored per open-files entry (§2.2) and restored exactly when switching back to that entry.
- **Goto-line** (`g` key): prompt for a numeric line number at the bottom of the preview area; Return jumps the current entry's scroll position to that source line's first display row (clamped to `[1, total source lines]`); Escape cancels the prompt without changing scroll; only digit and backspace input is accepted while the prompt is open.
- **Empty state**: when the open-files list has no entries (fresh startup, or the last entry was just closed), the preview view renders a short explanatory message (e.g. hinting at `b` to browse or `o` to quick-open) instead of gutter/content rows, and none of the scrolling/goto-line keys apply.

### 2.2 Open files list

The open-files list is the primary state the rest of the UI operates on: an ordered collection of files the user has opened during this session, plus which one (if any) is currently displayed in the primary preview view (§2.1).

- **Ordering**: insertion order by default, but user-rearrangable — see the open-files list overlay's (§2.3) reorder keys. A newly-opened file (one whose resolved absolute path is not already in the list) is appended to the end regardless of any manual reordering already done. Opening a file whose resolved absolute path already matches an existing entry does not create a duplicate or change that entry's position — see "open semantics" below.
- **Per-entry state**: absolute path, loaded preview content (§2.1's read/highlight results, loaded once at open time), and independent scroll/goto-line state (§2.1). Exactly one entry, or none, is the "displayed" entry at any time.
- **Open semantics** (used by §3.4's Return and §4.2's quick open): given a path,
  - if an entry for that resolved absolute path already exists in the list, do not read the file again or move the entry — just mark it as the displayed entry, preserving its existing scroll/goto state. (A file that failed to open never has an entry, per the next bullets, so any existing entry is by construction previously-successful and safe to display as-is.)
  - otherwise, attempt to read up to the byte cap (§2.1) from the start of the file:
    - if the read itself fails (permission denied, the path no longer exists, or any other OS-level read error), the open is a **failed result** carrying a short explanatory message (e.g. "permission denied") — do not create an entry, do not change the currently-displayed entry (if any), and return that result to the caller instead of a displayable file.
    - otherwise, if the read bytes contain a NUL byte, the file is binary — also a **failed result**, carrying the message "binary file, preview not available," with the same "no entry created" handling as above.
    - See "Open-failure signaling" below for what each caller does with a failed result.
  - otherwise (read succeeded and the content is not binary), continue reading/highlighting the file (§2.1), append a new entry at the end of the list with scroll reset to the top, and mark it as the displayed entry. This is an **opened result**.
- **Displaying** an entry (making it the one shown in the primary preview view) never changes list order.
- **Open-failure signaling**: an open call's result is one of "opened" (an entry now exists and is displayed) or "failed," carrying a short explanatory message — a read error and a binary file are both failed results, distinguished only by their message text; there is no separate binary-specific code path from here on. The two callers each handle a failed result by staying exactly where they were and surfacing its message directly, in whatever form fits that context — the open-files list and primary preview view are untouched in this case, since no entry was created:
  - From the browser overlay (§3.4's Return): the browser does **not** close and selection does not move; the message is shown inline in the browser (e.g. a transient status/footer line) instead.
  - From the quick open overlay (§4.2): the overlay does **not** exit and match selection does not move; the message is shown inline (e.g. in place of the header's keybinding legend, or an equivalent status line) instead of landing on the preview.
  - The message is advisory only and does not block further input — the user can immediately navigate elsewhere or attempt to open a different file.

### 2.3 Open-files list overlay

Triggered by `Tab` from the primary preview view (including the empty state). While active:

- The screen switches to a list view of every current open-files entry, in list order, each rendered as its root-relative, slash-delimited path (same path-rendering convention as quick open and jump to file, §4), with the currently-displayed entry (if any) marked distinctly.
- A `selected` index into the list, with wraparound cycling on Up/Down (same wrap semantics as §3.4).
- **Return**: mark the selected entry as displayed, then close the overlay, returning to the primary preview view showing it.
- **`x`**: remove the selected entry from the list.
  - If the removed entry was not the displayed entry, the displayed entry is unaffected; only the list itself shrinks, and overlay selection is clamped to the nearest remaining index (preferring the entry that was next after the removed one, falling back to the new last entry if the removed entry was last).
  - If the removed entry *was* the displayed entry, the displayed entry becomes the adjacent surviving entry (the one after it in list order, or the one before it if the removed entry was last); overlay selection follows the same entry.
  - If the list becomes empty as a result, there is no displayed entry; closing the overlay (or its own auto-close, see below) lands on the primary preview view's empty state (§2.1), which in turn auto-opens the browser overlay exactly as it does on startup (§1).
  - The overlay itself stays open after an `x` removal (it does not auto-close), so multiple entries can be removed in a row; it only auto-closes if the removal just emptied the list entirely, per the previous bullet.
- **Shift-Up / Shift-Down**: move the selected entry one position toward the top/bottom of the list, swapping it with its current neighbor; overlay selection follows the moved entry so repeated presses keep walking it further. Unlike Up/Down's navigation wraparound, reordering does **not** wrap — Shift-Up on the first entry and Shift-Down on the last entry are both no-ops. This only changes list order (§2.2); it does not change which entry is displayed, and does not affect any entry's stored scroll/goto state. The reordered position persists for the rest of the session (until the entry is removed or the app exits) exactly like an insertion-order position would.
- **Escape**: close the overlay and return to the primary preview view unchanged — whatever was displayed before opening the overlay is still displayed (removals already performed via `x`, if any, are not undone; only the "make this one displayed" action of Return is what Escape skips).
- If the list is empty when the overlay is opened (a degenerate case reachable by escaping out of the auto-opened browser on a fresh, file-less session and then pressing Tab), render an explanatory "no open files" message instead of a list, and only Escape is meaningful.

### 2.4 In-file find

Triggered by `/` from the primary preview view (§2.1), a no-op if there's no displayed entry (the empty state has nothing to search). This is a `less`-style search within the currently-displayed file's own content — distinct from quick open/jump to file (§4, which match *paths*, never read a file's content) and content search (§9, which matches content but across every file in the tree, not within one already-open file). Find state — query, matches, which one is current, and the wrap note below — is tracked per open-files entry (§2.2), independently of any other open file, the same way scroll and goto-line state already are; switching to a different entry and back preserves it exactly.

- **Prompt**: `/` opens a query prompt in the file title bar (§5.2) — not the goto-line prompt's bottom row, since this is itself a file-specific action per that section's convention. Any printable character is accepted (unlike goto-line's digits-only prompt), Backspace removes the last character, Escape cancels the prompt without changing the entry's existing find state (if any), and Return executes the search:
  - Every case-insensitive substring match of the query is located across the entry's lines, in source order (line, then column within the line).
  - The match at or after the source line currently at the top of the viewport becomes the current match ("search forward from here," the same convention `less` uses); if none exists at or after that point, the current match wraps to the very first match in the file and the wrap note (below) is set.
  - The view scrolls just enough to bring the current match into view (only if it isn't already visible, the same "minimal scroll" rule the browser uses for its own selection, §5.2) — not necessarily to the top of the viewport.
  - An empty query, or a query with zero matches, still records the query and clears any prior matches (so the file title bar reflects "no matches" rather than silently doing nothing).
- **`n`**: move to the next match, wrapping to the first match if the current one is the last. A no-op if there's no active find or it has zero matches.
- **`N`**: move to the previous match, wrapping to the last match if the current one is the first. Same no-op conditions as `n`.
- **Wrap note**: stepping with `n`/`N` past either end of the match list sets a transient note ("wrapped to top" or "wrapped to bottom" depending on direction) that's displayed until the next `n`/`N` step, cleared immediately if that next step doesn't itself wrap.
- **Highlighting**: every match is highlighted in the preview content itself, in a style distinct from syntax highlighting and from each other — the current match rendered more prominently than the rest, so it's unambiguous which one `n`/`N` will move from next. Highlighting persists even in the split-view browser layout's read-only preview pane (§5.1), since it's a passive visual, unlike the file title bar's find status/legend, which (like goto-line's legend) is shown only where the preview is actually interactive.
- **Status display**: while a find is active (query non-empty), the file title bar's right-aligned legend area (§5.2) shows the query, the current match's position and total count (e.g. "3/12"), the wrap note if set, and a keybinding legend that always includes `esc` (clear, below) plus — only when there's at least one match — `n`/`N`; a zero-match search shows "no matches" instead of a position/count, with no `n`/`N` in the legend since there's nothing to step between, but `esc` is still shown since it still clears a zero-match find.
- **Escape**, when a find is active: clears it (query, matches, current index, and wrap note), returning the file title bar to its idle content (§5.2) and removing all highlighting. Escape otherwise remains inert at the primary view (§7) — this is its only effect there, it still never quits (only `q` does), and it's a no-op when there's no active find to clear, so it can't be mistaken for a general-purpose "back out" key.

## 3. Browser

The browser is the overlay used to browse the filesystem and open a file into the primary view (§2) — see §5.1 for when it's shown relative to the preview. §3.1–§3.3 cover the supporting data it operates on (the node model, the ignore rules that filter it, and how it's sorted); §3.4 covers the browser's own navigation and open actions.

### 3.1 Core data model

The tool operates over a lazily-loaded tree of filesystem entries rooted at the resolved start path.

Each entry (a "node") has:

- an absolute path
- a display name (the path's basename; for the root, if the basename is empty — e.g. `/` — use the full path string instead)
- a depth (root is depth 0, children are parent depth + 1)
- a parent reference (root has none)
- whether it's a directory
- an expanded/collapsed state (directories only; starts collapsed except the root, which starts expanded)
- a lazily-populated children list (`null`/`None`/unset until first loaded; loading is idempotent — loading an already-loaded node's children is a no-op)
- an optional error string, set when the node's directory entries could not be listed (e.g. permission denied); on error, treat the node as having zero children rather than propagating an exception, and render the error inline (see §5.2).

**Root node initialization:** on startup, build the root node, load its `.gitignore` (see §3.2), load its children, and mark it expanded.

**Flattening (visible list):** the browser operates over the currently-visible, depth-first, pre-order flattening of the tree: the root, then — if the root is expanded — each child's own flattening, recursively. A node's subtree contributes to this list only while every ancestor down to the root is expanded. This is "progressive disclosure": collapsed directories hide their descendants from the visible list (and from on-screen rendering) but the descendants still exist in the lazily-loaded tree once visited.

### 3.2 Traversal and ignore rules

Three independent exclusion mechanisms apply identically to (a) normal directory listing/expansion and (b) the background full-tree index used by quick open and jump to file (§4.1):

1. **Always skip `.git`** (exact name match on any path segment's immediate entry, not a pattern) — unconditionally, regardless of `.gitignore` contents. Git internals are never useful to browse or search and can be large.
2. **`.gitignore`-aware filtering**, best-effort:
   - At root-node construction, look for a `.gitignore` file directly in the root path. If present and readable, parse it into an ignore-pattern set (see the matching algorithm below). If absent, unreadable, or pattern-parsing isn't implemented for a given rule, treat it as "no additional patterns" — never crash or block startup because of a malformed `.gitignore`.
   - When listing a directory's entries (or walking the full tree for the index), drop any entry that matches the loaded pattern set, so an ignored directory's contents are never even enumerated (not just hidden after listing).
   - Only the root's `.gitignore` needs to be honored. (The prototype did not walk nested `.gitignore` files in subdirectories; parity does not require it, though an implementation may choose to go further.)
3. **`.dirtreeignore`-aware filtering**, an application-specific exclusion list independent of `.gitignore`, applied identically everywhere `.gitignore` is (browser listing, index walk — never just quick open or jump to file; a path either exists in the app or it doesn't):
   - At root-node construction, look for a `.dirtreeignore` file directly in the root path, alongside the `.gitignore` lookup. Same syntax, same best-effort tolerance (missing/unreadable/malformed → "no additional patterns," never crash or block startup), same "only the root's file is honored" scope.
   - A candidate path is excluded if it matches *either* the `.gitignore` pattern set *or* the `.dirtreeignore` pattern set. The two files' patterns don't interact with each other — a `!negation` in one cannot re-include a path the other excluded; negation precedence (this section's "later rules override earlier ones") only applies within a single file's own rule list.
   - Rationale: this exists for exclusions that are about dirtree's own browsing noise (e.g. build output you don't want fuzzy-findable) rather than about what git tracks, so it shouldn't require touching a repo's actual `.gitignore` (or exist in a repo without one at all).

**Minimal gitignore pattern semantics to implement** (a small, dependency-free subset — this does not need to be a full `git check-ignore` reimplementation, but must at least cover):
   - Blank lines and lines starting with `#` are ignored (comments).
   - A pattern with no `/` matches the basename at any depth (e.g. `*.log` matches `a.log` and `sub/a.log`).
   - A pattern containing `/` (other than a trailing slash) is anchored to the root (matches only starting from the tree root, not at arbitrary depth).
   - A trailing `/` means "match directories only."
   - A leading `!` negates a previous match (re-includes a path that an earlier pattern excluded). Later rules override earlier ones, matching git's actual precedence.
   - Glob wildcards `*`, `?`, `[seq]` behave as shell globbing (not needing to also support `**` recursive-glob semantics for parity, though it's a reasonable enhancement).
   - When testing a candidate path, match it against its path relative to the tree root, POSIX-slash-delimited; append a trailing `/` to the tested string when the candidate is itself a directory, so directory-only patterns work.

### 3.3 Directory listing order

Within a directory, entries are sorted directories-first, then case-insensitively by name.

### 3.4 Navigation semantics

State: a `selected` index into the current flattened/visible list, and a `scroll` offset (topmost visible row index) used only for rendering (§5.2).

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
- **Return**, when the current selection is a file (no-op on directories): open the file (§2.2's open semantics) and display it in the primary preview view, leaving the browser overlay open and selection unchanged, so several files can be queued up in a row without leaving the browser. If the result is "failed" (read error or binary), see §2.2's open-failure signaling — the browser stays open regardless, so the only visible effect is the inline message.
- **`/`**: open the jump-to-file overlay (§4.3) to reveal/select a file within the browser itself. Jump to file replaces the browser view while it's active, exactly like any other overlay taking over the screen it was opened from (§5.1); Escape returns to the browser unchanged.
- **`o`**: open quick open (§4.2), replacing the browser view while it's active; Escape returns to the browser unchanged, while opening a match lands on the primary preview view instead (§4.2).
- **`b` / Escape**: close the browser overlay and return to the primary preview view unchanged (whatever was displayed, or the empty state, stays as it was). `b` is the same key that opens the browser from the primary preview view (§7) — pressing it again while the browser is open closes it, i.e. it's a toggle.

## 4. Quick open and jump to file

Two overlays share a single background index and matcher but are otherwise single-purpose: **quick open**, reachable from both the primary preview view and the browser, opens a matched path into the open-files list; **jump to file**, reachable only from within the browser, reveals/selects a matched path inside the browser itself. (An earlier revision of this spec described one shared overlay whose Return/Space mapping flipped depending on where it was opened from — that dual-action design has been replaced by these two single-action overlays; there is no longer a way to open-into-list from jump to file, nor to reveal-in-browser from quick open.) §4.1 covers the background index they both search; §4.2 covers quick open; §4.3 covers jump to file.

### 4.1 Background full-tree index

Both overlays need to search the *entire* tree regardless of what's currently expanded, but building that list can be slow on a large tree if done synchronously when it's first opened. So:

- Immediately at startup (after the root node is built), kick off building a **background index**: a flat, recursively-walked list of every path under the root (files and directories, root itself excluded), applying the same skip/ignore rules as §3.2, sorted by its root-relative slash-delimited display path, case-insensitively.
- This must run concurrently with the UI being immediately usable (i.e. the browser must not block waiting for the index).
- The index-building walk must guard against symlink cycles (track resolved/canonical real paths already visited; do not re-descend into one already seen).
- The index-building process must not share mutable state with the interactive tree's node objects — it operates on raw paths only, so no locking is required between the UI thread/task and the indexing thread/task. (This is a correctness requirement, not a style preference: the prototype found that reusing the same mutable node objects across threads was a real data-race hazard and redesigned around it. Whatever concurrency model the implementation language provides — OS threads, green threads, async tasks — the two must not touch each other's mutable state.)
- Track whether indexing has completed and how much time has elapsed since it started; both are needed for the delayed-loading-indicator behavior in §4.2/§4.3 and §5.2.

**Matching**, shared by both overlays: an empty query matches everything. A non-empty query:

- If it contains any shell-wildcard character (`*`, `?`, `[`), it's matched via case-insensitive shell-glob matching against the *entire* candidate string.
- Otherwise, it's a case-insensitive **substring** match against the same candidate string. (This is deliberate: most quick-jump queries are typed as plain fragments, not `*fragment*`, and requiring wildcard wrapping for the common case would be worse UX.)
- Matching runs against the *entire tree's* index, not just currently-expanded/visible nodes or currently-open files — both overlays are global regardless of any other UI state.

### 4.2 Quick open

Triggered by `o`, from either the primary preview view (§2.1), including while it's showing the empty state, or from within the browser overlay (§3.4), replacing whichever screen it was opened from. Unlike the browser, quick open is not a toggle on its trigger key — `o` is a live filter character once the overlay is open (a common enough letter that overloading it as a close key would break ordinary typing) — so Escape returns to whichever screen it was opened from (the primary preview view, or the browser), unchanged; opening a match, by contrast, always lands on the primary preview view regardless of entry point, the same as content search (§9.2).

- The primary preview view is fully replaced by a **flat list view**: every entry from the background index (§4.1), rendered as its root-relative, slash-delimited path (not the browser's indented/marker style) — this is what makes it distinct from the browser, and lets a query match on any path segment, not just a leaf name.
- A query string starts empty and accumulates/removes characters as the user types/backspaces. It is shown in the header (see §5.2).
- **While the background index has not finished building**: matches are empty/unavailable; render the delayed loading indicator described in §5.2 instead of "no matches" (don't claim there are no matches when you simply haven't looked yet).
- A `selected` index into the current match list, with wraparound cycling: advance forward (Tab, or Down) or backward (Shift-Tab, or Up).
- **Return**, if there is at least one match: open the selected match's path into the open-files list, per §2.2's open semantics (reusing an existing open-files entry if the path is already open). If the result is "opened," close the overlay, landing on the primary preview view with that file displayed. If the result is "failed" (read error or binary), see §2.2's open-failure signaling — the overlay stays open with the message shown inline instead of exiting.
- **Backspace**: remove the last character of the query; reset match-selection to the first match and reset scroll.
- **`o` / Escape**: cancel the overlay and return to the primary preview view, unchanged (query and match selection are discarded; no action is performed).
- Typing any other printable character appends it to the query (reset match-selection and scroll to the top, since the match set changes).

### 4.3 Jump to file

Triggered by `/` from within the browser overlay (§3.4) — jump to file has no entry point of its own from the primary preview view; the browser must already be open. While active, jump to file **replaces the browser view** exactly the way any overlay replaces the screen it was opened from (§5.1): the browser's own row list is not shown while jump to file is on screen.

- The screen is fully replaced by the same flat list view described in §4.2, rendered from the same background index (§4.1).
- Query, matching, and the delayed-loading-indicator behavior while indexing isn't done are identical to quick open (§4.2).
- A `selected` index into the current match list, with the same wraparound cycling as §4.2.
- **Return**, if there is at least one match: resolve the selected match's path in the interactive tree — expanding every ancestor directory along the path from the root down to the match (regardless of each ancestor's prior expanded/collapsed state) so the match becomes visible — then close jump to file, leaving the browser open with that node selected and scrolled into view. If resolution fails (e.g. the path no longer exists — deleted after indexing but before the jump), close jump to file without changing browser selection, landing back on the unchanged browser.
- **Backspace**: remove the last character of the query; reset match-selection to the first match and reset scroll.
- **Escape**: cancel jump to file and return to the browser, unchanged (query and match selection are discarded; no action is performed, browser selection is untouched).
- Typing any other printable character appends it to the query (reset match-selection and scroll to the top, since the match set changes), including `/` itself — jump to file does not treat `/` specially once it's open.

## 5. Visual layout and rendering

### 5.1 View model and overlay layout

The **primary preview view** (§2.1) is the default screen: on startup it shows the empty state with the browser automatically opened on top of it (§1); once at least one file has been opened, it shows that file (or whichever was most recently made the displayed entry) whenever no overlay is active.

Four overlays exist, and each is closed by Escape back to whatever was showing before it: the **browser** (§3), opened from the primary preview view; **quick open** (§4.2), opened from either the primary preview view or the browser; **jump to file** (§4.3), opened only from within the browser; and the **open-files list** (§2.3), opened from the primary preview view. Only one overlay is active at a time. The browser is also a toggle on the key that opens it (`b`, §7): pressing that same key again while the browser is open closes it, equivalent to Escape. Quick open is not a toggle on `o` — once it's open, `o` is a live filter character like any other, so only Escape closes it (§4.2).

**Browser layout** (the only overlay with a dual layout; quick open, jump to file, and the open-files-list overlay always fully replace the screen they were opened from, per §4 and §2.3 — jump to file replaces the *browser's* view specifically, since that's always where it's opened from) is chosen every frame from the current terminal dimensions (not decided once at open-time, so a live resize can flip between them):

- **Split view** (wide terminal): the browser occupies the left side of the screen (below the header), sized just wide enough to fit the longest currently-visible row's rendered label (indentation + expand marker + name) at the current disclosure state, clamped to a sane minimum and maximum so one long name can't crowd out the preview, and the primary preview view — still visible and still showing whatever file (or empty state) it had — occupies the remaining width on the right, at full height below the header row. The preview pane's *own width* is capped at a fixed maximum (120 columns in the prototype, arrived at after iteration — treat it as configurable/tunable, not sacred); once the terminal is wide enough that the preview would exceed that cap, the *extra* width goes to growing the browser pane instead of stretching the preview further. This is a deliberate distinction: bound the preview window's width, not the width of the wrapped content within it — capping content width was tried first and rejected because it wasted the freed space instead of giving it to the browser pane.
  - The preview pane in split view is read-only while the browser overlay is active (renders its current content but does not accept scrolling/goto-line keys — all keys go to the browser until it's closed) and is visually separated from the browser by a vertical rule.
- **Popup** (narrow terminal): a centered, bordered floating window over the (unmodified, last-rendered) primary preview view, sized to the terminal minus a fixed margin, with its own title (the tree's root path, rendered the same abbreviated way as the header/title bar's root path, §5.2) and a footer hint line.
- **Threshold**: split view is used when the total terminal width is at least the computed browser-pane width plus a minimum usable preview width (40 columns in the prototype) plus one separator column; otherwise fall back to popup. Recompute this every frame.

### 5.2 Rendering conventions

- **Header/title bar**: a single full-width row at the very top of the screen, rendered with a background contrasting from the normal row background *and* from the reverse-video selection highlight (so the title bar is never visually indistinguishable from a selected row directly beneath it — the prototype initially made this mistake using plain reverse-video for both and had to give the title bar a distinct fixed color pair instead). Content:
  - Primary preview view and browser overlay: the tree root path — never the path of whatever file happens to be open (see the separate per-file title bar below) — left-aligned, and a short keybinding legend, right-aligned, with at least one space of separation between them. The root path is abbreviated with common shell shortcuts where applicable (e.g. `~/dirtree` instead of `/Users/x/dirtree`) to save space, and is recomputed, and re-fit, every frame so a live resize is reflected immediately. If the terminal is too narrow to fit both the root path and the legend (with that minimum separation), the root path is dropped entirely, leaving just the left-aligned legend (the legend is what makes the app usable and always takes priority) — recomputed every frame so a live resize can bring the root path back once there's room again. This legend lists only app-wide actions (navigation between views/overlays, quitting); actions specific to whichever file is currently displayed (e.g. goto-line) do not belong here — see the file title bar below.
  - Quick open overlay: the literal query typed so far and a short keybinding legend (Return opens the selected match, `o`/Escape cancels).
  - Jump-to-file overlay: the literal query typed so far and a short keybinding legend (Return reveals the selected match in the browser, Escape cancels).
  - Open-files-list overlay: a short label (e.g. "open files") and a short keybinding legend.
- **File title bar**: a second full-width row, directly above the preview content and visually distinct from both the header/title bar above it and the preview content below it, shown only while a file is displayed (the empty state has no file title bar, since its explanatory hint already occupies the content area) — present wherever the preview content itself is rendered: the primary preview view and the split-view browser layout's preview pane (§5.1). Left-aligned, it renders that entry's root-relative, slash-delimited path (the same path-rendering convention as quick open/jump to file, §4, and the open-files list, §2.3) — this is where the open file's own identity lives now that the header/title bar above is reserved for the tree root. Right-aligned (same fit/drop rule as the header/title bar above, keyed off this row's own width), it renders a keybinding legend of actions specific to the displayed file — goto-line (`g`), in-file find (`/`, §2.4), and copy mode (`c`, §2.1) — but is the designated home for any file-specific action added in the future, so the header/title bar's legend stays app-wide-only. This legend is only shown where the preview is actually interactive: it's omitted (path only) in the split-view browser layout's preview pane, since that pane is read-only while the browser owns input (§5.1) and advertising a key that currently does nothing would be misleading. While in-file find has an active query, this same row's left/right split instead shows the in-file find prompt (while typing) or its live status (query, match position/count, wrap note, and a next/previous legend — §2.4) in place of the path and goto-line/find legend, in both cases only where the preview is interactive; the split-view browser's read-only pane still shows just the path even with an active find, though the matches themselves remain highlighted there regardless (§2.4's highlighting is passive and always shown). While the displayed entry is in copy mode, the left side is tagged `[copy mode] ` ahead of the path (or ahead of whichever other left-side content — the find prompt or an active find's status — is otherwise showing there, so the tag stays legible no matter which of this row's other states copy mode happens to be layered under), the legend shows `[c] normal view` in place of `[c] copy mode` in the idle case specifically (goto-line and find are additionally omitted there, though still reachable by key, the same way arrow-key scrolling is never listed either), and the row itself renders in a visually distinct style — this style change, unlike the legend text, is not gated on interactivity, since copy mode's plain-rendering effect on the preview content itself applies everywhere that entry's preview is drawn, interactive or not (§2.1).
- **Selected row**: rendered in reverse video (or an equivalent single, consistent "selected" visual treatment) relative to unselected rows.
- **Directory expand/collapse marker**: a small glyph before the name distinguishing expanded vs. collapsed directories (e.g. a down-caret vs. right-caret); files get equivalent blank spacing so names still align vertically.
- **Indentation**: each row is indented proportionally to its depth in the tree, so the hierarchy is visually legible.
- **Per-node error indicator**: if a node failed to list its children (see §3.1), append a bracketed short error string after its name.
- **Delayed loading indicator** (indexing spinner / badge): while the background index (§4.1) has not finished, an animated spinner is available for use in two places — a small floating badge anchored to the bottom-right corner of the screen, rendered in the browser overlay and in *both* the quick open and jump-to-file overlays (so the badge's sequence below stays visible/watchable whichever one is currently showing), and, in quick open and jump to file specifically, also replacing the match list entirely with an "indexing…" message. The content search overlay (§9) renders the same bottom-right badge (it depends on the same background index for its candidate set) but has its own "indexing…"/"searching…" results-area placeholder per §9.1, distinct from quick open's/jump to file's — and unlike theirs, content search's own scan (not just the background index) also gets an animated spinner glyph once it's been running long enough to be perceptible, since a content scan can itself be slow (a broad regex over a large tree) independently of whether the index is done. In both cases, **suppress the indicator entirely until the relevant work has been running for at least a short threshold** (250ms in the prototype). This is a deliberate UX fix: on a small tree, indexing finishes in a handful of milliseconds, and briefly flashing a spinner for genuinely instant work reads as more distracting than informative — so nothing is shown at all until it's clear the wait is actually perceptible. During that sub-threshold grace period in quick open or jump to file specifically, render neither the spinner nor a "no matches" message in the match-list area (since indexing not being done yet is a different state from "genuinely zero matches") — just leave the match-list area blank; the corner badge follows its own sequence below independent of the match-list area's blank state.
  - Compute the spinner glyph by cycling a small fixed set of animation frames at a fixed rate (10 frames/sec in the prototype) driven by elapsed wall-clock time since indexing started, not by frame count, so its speed is independent of render/poll rate.
  - The bottom-right badge is rendered with a background that contrasts with the surrounding rows (an accent color in the prototype), the same "must read as visually distinct" rule the header bar (above) follows — not plain/default-styled text sitting over the view, which is easy to miss in a busy screen.
- **Bottom-right badge sequence**: the badge (unlike quick open's or jump to file's match-list-area indicator, which is just shown/hidden per the threshold above) runs through a full sequence once indexing has crossed the perceptibility threshold, identically whether the browser, quick open, jump-to-file, or content search overlay is currently on screen:
  1. **Spinner**: shown as soon as the threshold is crossed, animating as described above.
  2. **Minimum display duration**: the spinner stays on screen for at least a short minimum duration (1 second in the prototype, measured from when indexing started) even if indexing genuinely finishes sooner — real directories often index in microseconds, so without this floor the spinner could cross the threshold and finish in the same frame, an unreadable flash rather than a perceptible indicator.
  3. **Completion message**: once both indexing has actually finished and the minimum display duration has elapsed, the spinner is replaced with a transient "indexing complete" message, shown in full for a short display duration (2 seconds in the prototype).
  4. **Fade-out**: the completion message then fades out over a shorter fade duration (on the order of a few hundred ms) by disappearing left-to-right — its earliest characters vanish first while its right edge stays anchored at the same screen position the spinner occupied — until nothing remains.
  - If indexing finishes before ever crossing the perceptibility threshold, none of this sequence runs at all — the spinner was never shown, so announcing its completion would be exactly the flashing-chrome-for-instant-work problem the threshold exists to avoid.
  - **Opening quick open or jump to file short-circuits the minimum display duration**: if the user opens either while indexing is already done, they've directly seen indexing is ready — the overlay renders the real match list immediately once done, per §4.2/§4.3. Continuing to hold the badge on step 1's spinner for the rest of step 2's minimum duration at that point would be actively misleading (claiming to still be working on something the user just saw finish), not a perceptibility safeguard, so opening either overlay while already done drops the minimum-display-duration floor entirely and treats *the moment the overlay was opened* — not the index's actual (possibly long-past) completion time — as when indexing finished, restarting steps 3/4 fresh from there. Because the badge renders in that overlay too, this transition is immediately visible right there without needing to return to the browser first. This distinction matters in particular for the debug-only always-show mode below: without it, the index's real completion time could already be well past the completion message's entire display+fade window (having been masked the whole time by the artificially-held spinner), and using it directly would make the badge vanish the instant the overlay opened instead of visibly transitioning to the completion message. This short-circuit resets the next time a new indexing cycle starts (e.g. a live-refresh-triggered rebuild, §6.1), since the flash-prevention floor is meaningful again for that fresh run.
  - This whole sequence is specific to the badge; quick open's and jump to file's replacement of the match list is not affected by any of it — once indexing is actually done, the real match list should render immediately rather than being delayed by a completion message.
- **Debug-only always-show mode**: a build-time-only switch (never a runtime flag, and never enabled in a shipped build) that bypasses only step 1's perceptibility threshold — the spinner appears the instant indexing starts — while every other step of the sequence above (the minimum display duration, the completion message, the fade-out) behaves exactly as it otherwise would. This lets the full sequence be watched on demand without needing to reproduce a genuinely slow index. This must not affect quick open's or jump to file's indexing-blocked state, since forcing that indefinitely would make either permanently unusable in a debug build.

### 5.3 Animation principles

The delayed loading indicator above (§5.2) is, at time of writing, the only animated UI element in the spec, but any future one must be designed against the same principles rather than as a one-off:

- **Never block the UI.** No animation may gate input handling, redrawing, or any other work on reaching a particular frame or on a sleep/timer of its own. An animation's state (spinner glyph, fade-out progress, or anything else) is derived fresh at draw time from `(elapsed, done)`-shaped state, the same way §6.2 requires layout itself to be recomputed fresh every frame from current terminal dimensions rather than cached — an animation is just another quantity computed that way, never a separate blocking wait the draw loop has to synchronize with.
- **Informative, not decorative.** An animation must be earning its place by communicating something the user actually needs to know — that background work is in progress, that it just finished, that state changed — not exist for visual polish on its own. If removing it would leave the user no worse informed, it doesn't belong; a dense text UI has no room for chrome that isn't pulling its weight (see `docs/ANIMATION_IDEAS.md`'s tree-expand/collapse and selection-movement examples of this principle ruling an animation out entirely).
- **Elapsed-time-driven, not frame-driven.** An animation's position is a pure function of wall-clock time elapsed since it started, not of how many draws have happened, so its apparent speed is independent of render/poll rate (including §6.2's periodic resize-poll cadence) — and so its logic can be unit-tested with synthetic durations, without a real terminal or real elapsed wall-clock time, per `specs/TESTING.md` and CLAUDE.md's testing discipline.
- **Perceptibility threshold.** Nothing animates for work that resolves before a human would notice — suppress an indicator entirely until the underlying operation has been running for at least a short threshold. Briefly flashing chrome on and then immediately off for genuinely instant work reads as more distracting than informative, not as reassurance that something happened.
- **Minimum-display floor once shown.** An animation that has crossed the perceptibility threshold commits to staying legible for at least a short minimum duration even if the underlying work finishes sooner — without this floor, work that happens to finish just after the threshold could show the indicator for a single unreadable frame instead of a perceptible one.
- **Anchored, directional motion.** Motion reads as happening at a fixed screen position (e.g. §5.2's fade-out disappearing left-to-right while its right edge stays anchored where the spinner was) rather than shifting other content; an animation must never itself trigger a layout recompute.
- **Subtle, not flashy.** Default to the least motion and visual intensity that still communicates the state change — rapid blinking, saturated color flashes, or jarring transitions are distracting in a dense text UI, not more noticeable in a useful way. This applies even where an animation exists specifically to be noticed (e.g. the corner badge): §5.2's contrast treatment — a background that reads as visually distinct from surrounding rows — is what makes it noticeable, not intensity, saturation, or motion; color alone should also be avoided as the sole distinguishing signal, since terminal color support and themes vary.

## 6. System behaviors

These apply across every view/overlay above rather than belonging to any one of them.

### 6.1 Live refresh on filesystem changes

The tree, and the background index (§4.1), must stay current as files and directories are added, moved, or deleted on disk underneath the running session — the user should not have to restart `dirtree` to see a change made by another process (an editor, `git checkout`, a build, etc.).

- The implementation must watch, at minimum, every directory that has already been loaded (§3.1's lazy-loading sense: the root at startup, plus any directory the user has expanded or that jump to file has revealed since). A directory never visited does not need to be watched — it will simply reflect current disk state whenever it is eventually loaded, same as today.
- Watching must not block or slow down interactive use; detected changes are applied asynchronously, the same way the background index (§4.1) is built without blocking the UI.
- Because change notifications can arrive in rapid bursts (an editor's save-via-temp-file-and-rename is often 2-3 raw events; a multi-file operation like `git checkout` can be dozens), the implementation should coalesce a burst into a single refresh rather than re-scanning once per raw event — a short debounce window (on the order of a few hundred milliseconds) is sufficient and keeps this from being a performance or flicker problem on active directories.
- **Applying a refresh:** re-list each already-loaded directory's contents and merge the result into the existing tree by path, rather than discarding and rebuilding it wholesale:
  - An entry whose path is unchanged (same path, still the same kind — file vs. directory) keeps its existing node identity, and therefore keeps its expanded/collapsed state and any already-loaded subtree, exactly as if it had never been touched.
  - An entry that no longer exists on disk is removed from the tree.
  - A newly-appeared entry is added as a new node (collapsed if a directory, per §3.1's default), sorted into place per §3.3.
  - This mirrors §3.4's existing "keep the previously-focused node selected by identity, not by index" rule — a refresh must not disturb selection or disclosure state for anything that didn't actually change.
- **Selection after a refresh:** if the currently-selected node in the browser was deleted by the change, selection falls back to the nearest ancestor still present in the tree (walking up from the deleted node); the root is always present, so this always terminates somewhere visible. A file that's currently open in the open-files list (§2.2) is unaffected by browser selection fallback — its list entry and any loaded preview content simply become stale if the underlying file is deleted (see §2.3's removal semantics for what happens if the user then tries to act on it).
- The background index (§4.1) must eventually reflect the same change (so quick open and jump to file don't keep offering deleted paths or miss new ones). Re-triggering an index rebuild after a live-refresh is treated exactly like the initial index build for purposes of the delayed-loading-indicator (§5.2): a fast rebuild stays invisible, a slow one on a very large tree shows the same spinner/"indexing…" treatment a fresh build would.
- This is best-effort: if the underlying OS change-notification facility is unavailable or exhausted (e.g. a platform inotify-instance/watch-count limit), the implementation must degrade gracefully — continue running with the tree simply not auto-refreshing — rather than failing startup or crashing.

### 6.2 Resize handling

The terminal must be treated as capable of changing size at any time, live, without necessarily receiving a dedicated resize notification — the primary target usage is inside terminal multiplexers (Zellij and similar) that do not reliably deliver resize events to the child process. Therefore:

- The main input-wait must not block indefinitely; it must wake on a short periodic timeout (100ms in the prototype) even with no input, so layout can be recomputed and redrawn on that cadence regardless of whether an explicit resize signal fired.
- Every draw must recompute layout (row/column counts, split-vs-popup decision, pane widths, line-wrap widths) from the terminal's *current* actual dimensions queried fresh each frame — never cache dimensions across frames and trust a resize event to invalidate the cache as the sole mechanism.
- If the implementation's terminal library does deliver an explicit resize event/signal, treat it as a hint to redraw immediately rather than the only trigger to do so.

### 6.3 Escape-key responsiveness

Terminal input libraries commonly buffer a short delay after receiving an Escape byte, to disambiguate a bare Escape keypress from the start of a longer escape sequence (arrow keys, function keys, etc.). Left at defaults, this can make Escape feel like it takes up to ~1 second to register. The implementation must configure its terminal library's escape-sequence timeout down to a short value (on the order of 10–25ms) at startup, before entering the main loop, so Escape reads as instant. (In the prototype, this was set via both an environment variable read at import time and an explicit API call at runtime, to cover two different code paths that read the timeout at different times — whatever the chosen library's equivalent mechanism is, set it early and set it redundantly if the library offers more than one knob for it.)

## 7. Keybindings summary

| Key | Context | Effect |
|---|---|---|
| Up / Down | preview | scroll one row |
| Page Up / Page Down | preview | scroll one viewport height |
| `g` | preview | prompt for a line number, jump to it |
| `/` | preview, file displayed | open the in-file find prompt in the file title bar |
| `n` | preview, active find | jump to the next match, wraps |
| `N` | preview, active find | jump to the previous match, wraps |
| Escape | preview, active find | clear the find (query, matches, highlighting) |
| `c` | preview | toggle copy mode (strip the gutter and syntax color, for clean mouse selection) |
| `b` | preview | open browser overlay (toggle: closes it again if already open) |
| `Tab` | preview | open the open-files list overlay |
| `o` | preview | open quick open overlay |
| `s` | preview | open content search overlay |
| `q` | preview, no overlay active | quit |
| (typing) | find prompt | append to query |
| Backspace | find prompt | remove last query character |
| Return | find prompt | execute the search, jump to the current match, close the prompt |
| Escape | find prompt | cancel the prompt, existing find state (if any) unchanged |
| Up / Down | browser | move selection, wraps at both ends |
| Right | browser | expand a collapsed dir, or descend into an expanded dir's first child |
| Left | browser | collapse an expanded dir; move a collapsed dir's selection to its parent; on a file, jump to parent and collapse it |
| Return | browser, file selected | open file, display it, browser stays open |
| `/` | browser | open jump-to-file overlay, replacing the browser view |
| `o` | browser | open quick open overlay, replacing the browser view |
| `s` | browser | open content search overlay |
| `b` / Escape | browser | close overlay, return to preview unchanged |
| (typing) | quick open | append to query, filters live |
| Backspace | quick open | remove last query character |
| Tab / Down | quick open | next match (wraps) |
| Shift-Tab / Up | quick open | previous match (wraps) |
| Return | quick open | open the selected match into the open-files list, exit overlay (always lands on the primary preview view, regardless of entry point) |
| Escape | quick open | cancel, return to whichever screen it was opened from (primary preview view or browser), unchanged |
| (typing) | jump to file | append to query, filters live |
| Backspace | jump to file | remove last query character |
| Tab / Down | jump to file | next match (wraps) |
| Shift-Tab / Up | jump to file | previous match (wraps) |
| Return | jump to file | reveal the selected match in the browser, exit overlay |
| Escape | jump to file | cancel, return to the browser unchanged |
| Up / Down | open-files list | move selection, wraps at both ends |
| Shift-Up / Shift-Down | open-files list | move selected entry toward top/bottom of the list (no wraparound) |
| Return | open-files list | display selected entry, close overlay |
| `x` | open-files list | remove selected entry from the list |
| Escape | open-files list | close overlay, return to preview unchanged |
| (typing, including space) | content search | append to query, rescans in the background, resets disclosure to expanded-by-default |
| Backspace | content search | remove last query character, rescans, resets disclosure to expanded-by-default |
| Tab / Down | content search | next row — file or hit — in the flattened list (wraps) |
| Shift-Tab / Up | content search | previous row in the flattened list (wraps) |
| Left | content search | collapse the selected row's file, hiding its hit rows |
| Right | content search | expand the selected file row if it's collapsed |
| Return | content search | open the selected row's file, jump to its line, leave the overlay open |
| Ctrl+R | content search | toggle substring/regex matching mode, rescans |
| Ctrl+U | content search | clear the query and results |
| Escape | content search | leave the overlay, returning to whichever screen it was opened from — query, results, selection, mode, and disclosure state are all preserved, not discarded |

Escape is the universal "get me out of here" key for every overlay listed above: it closes whatever overlay is currently active, returning to whatever was showing before it. Escape otherwise has **no effect** when no overlay is active (i.e. when the primary preview view itself is on screen) — it deliberately does not quit, so a reflexive Escape press can't discard the session's open-files state; `q` is the only way to quit. Its sole exception there is clearing an active in-file find (§2.4), a deliberately narrow "way out" for a state that would otherwise persist indefinitely; that still isn't a general-purpose back-out action — Escape stays inert with no active find. `b` is additionally a toggle on its own overlay (browser): pressing `b` again while the browser is active has the same effect as Escape. Quick open is deliberately not a toggle on `o` — it's a live text filter, and `o` is far too common a letter to double as a close key without breaking ordinary typing — so Escape is the only way to close it. Content search is the one overlay where Escape does not reset the overlay's own state (§9.2) — it only leaves the screen; the state itself persists until the user explicitly clears the query.

## 8. Explicitly out of scope for parity

- Mouse support was deliberately removed from the prototype (terminal multiplexer interception made it unreliable) and is **not required**. If an implementation wants to add it back for environments where it works, it must not be the only way to perform any action — full keyboard parity per §7 is required regardless.
- Exact color choices, exact spinner glyph set, and exact numeric tuning constants (byte cap, pane width cap, badge delay, spinner fps, min/max browser-pane width) are not required to match the prototype's literal values — they're implementation-tunable. What's required is the *behavior* those constants produce (bounded, sane, non-flashing, non-jarring), not the specific numbers.

## 9. Content search

Content search is a separate overlay from quick open and jump to file (§4): where those match *paths* by substring/glob and never read a file's content, content search matches a plain string against each candidate file's *content* and lists the files that contain it. It has its own trigger key (`s`) rather than overloading `/`, `o`, or the shared finder, and is not part of the "single background index/matcher, two single-purpose overlays" design of §4 — it's a third, independent overlay with its own matching rule.

### 9.1 Triggering and background scanning

- Entry points: `s` from the primary preview view (§2.1, including its empty state) and `s` from the browser overlay (§3.4). Both entry points behave identically once the overlay is open — there is no per-entry-point default-action distinction (see §9.2).
- The candidate set is every non-directory entry in the background full-tree index (§4.1) — the same index quick open and jump to file search, so content search automatically honors the same `.gitignore`/`.dirtreeignore` exclusions (§3.2) and reflects the same live-refresh-triggered rebuilds (§6.1). A query typed before the index has finished building is held pending until it completes (rendered as `indexing…`), the same way quick open and jump to file defer to an unfinished index (§4.2/§4.3).
- Each candidate file is read up to the same byte cap used for preview loading (§2.1), and a capped read containing a NUL byte is treated as binary and never matched — the same check §2.2 uses to detect a binary open. Content search never reports a match inside content it wouldn't be able to preview anyway, and (like preview loading) never reads past the cap, so a match occurring only beyond the cap is not found.
- Matching has two modes, toggled by the user (§9.2): **substring mode** (the default) is a case-insensitive plain-text substring match, checked line by line against the capped read; **regex mode** compiles the query as a case-insensitive regular expression and matches it line by line the same way. Content search has no glob-wildcard mode — the wildcard-vs-substring switch shared by quick open/jump to file (§4.1) is specific to path matching and is unrelated to content search's substring-vs-regex mode.
- In regex mode, an invalid pattern (fails to compile) is reported inline as an error in the results area and never triggers a scan — the same "don't run work you already know is wrong" discipline as the empty-query case below, just for a different reason.
- A file may match on more than one line; every matching line is kept (in source order) as that file's hits, and all of them are exposed in the result list (§9.2) — this is a full per-line results list, not just a first-example lookup.
- Scanning always runs in a background goroutine (mirroring §4.1's non-blocking discipline), never on the UI/input thread: each keystroke that changes the query or mode cancels any still-running scan for the previous query/mode and starts a new one over the full candidate set, so a scan — however slow, e.g. a broad regex over a large tree — never blocks keystrokes, only its own result arrives late. Only the most recently started scan's result is ever applied; a result from a canceled/superseded scan is discarded.
- An empty query performs no scan and shows no results — this is deliberately different from quick open/jump to file's "empty query matches everything" (§4.1): their empty-query match is a free lookup against an already-built in-memory path list, while scanning every candidate file's content on an empty query would mean reading the entire tree for no useful result.
- While a scan for the current query is in flight, or the background index itself isn't done yet, the results area shows a `searching…`/`indexing…` placeholder instead of "no matches" — the same "don't claim zero results before you've actually looked" rule §5.2 uses for quick open's/jump to file's own delayed-loading state. Once a scan has been running long enough to be perceptible, `searching…` gains an animated spinner glyph, the same threshold-suppressed feedback the background-index badge uses (§5.2) — nothing flashes for a scan that finishes in a blink, but a genuinely slow one (large tree, broad regex) gets visible, non-jarring feedback that it's still working rather than looking stuck.

### 9.2 Mode behavior

- The screen is fully replaced by a header row (title — `search`, or `search (regex)` while regex mode is on — plus the keybinding legend), a dedicated query-input row directly below it showing the query as typed (e.g. `> needle`), and below that a two-level list view: one **file row** per matching file (its root-relative, slash-delimited path plus its total hit count), each disclosing its own **hit rows** underneath — one per matching line in that file, showing the 1-based line number and that line's (trimmed) text. Every file row starts expanded (all of its hits visible) as soon as results arrive; nothing needs to be manually opened to see them. The query lives in its own row, separate from the legend, so a long query and a long legend never have to compete for the same line, and is rendered with a background distinct from the plain-background results list below it, so the "you're typing here" row reads at a glance.
- Each file row shows a disclosure indicator (expanded vs. collapsed) reflecting its own independent expand/collapse state. **Left** collapses the file the current selection belongs to (hiding its hit rows and moving the selection to that file's own row, if it wasn't already there); **Right** expands the currently-selected file row if it's collapsed. Collapsing/expanding one file never affects any other file's disclosure state.
- A query string starts empty and accumulates/removes characters as the user types/backspaces, shown on its own row (above). Because a content-search query is literal text rather than a path fragment, the space bar always types a literal space into the query — unlike the finder overlays, no printable key is overloaded to double as an action key; every action below is on a non-printable key instead.
- **Ctrl+R** toggles between substring mode (default) and regex mode (§9.1), re-running the scan for the current query under the new mode. The active mode is shown in the header title.
- A `selected` index into the current flattened row list (file rows plus, for expanded files, their hit rows), with wraparound cycling: advance forward (Tab, or Down) or backward (Shift-Tab, or Up). Collapsing a file removes its hit rows from this list; expanding it reinserts them, always in source order.
- **Return**, if there is at least one row: open the selected row's file into the open-files list per §2.2's open semantics (reusing an existing open-files entry if the path is already open), then jump the preview's scroll to that row's line — the selected hit's line if a hit row is selected, otherwise the file's first (lowest-line-number) hit if a file row is selected — the same jump-to-line behavior goto-line uses (§2.1). Unlike quick open/jump to file, opening a result here does **not** close the overlay — the primary preview view updates underneath it, but content search stays on screen, so several hits can be opened in a row without re-entering the search each time; Escape (below) is what leaves the overlay once you're done. A "failed" result (read error or binary — e.g. the file changed or was removed between scanning and opening) leaves the overlay open with the message shown inline instead, per §2.2's open-failure signaling — the same outcome as a successful open, just with a message instead of a moved preview.
- **Ctrl+U**: clear the query and results back to empty — the explicit way to reset a search, since (per Escape below) leaving the overlay no longer does this itself. The regex-mode toggle is left as-is; it's a standing preference, not part of the query being cleared.
- **Backspace**: remove the last character of the query, cancel any in-flight scan for the old query, and start a new one for the shortened query (or, if the query is now empty, show no results without scanning). Also resets every file's disclosure state back to expanded-by-default for the new result set.
- **Escape**: leave the overlay, returning to whichever screen it was opened from. Unlike quick open/jump to file, this does **not** discard anything — the query, results, in-flight scan, selection, mode, and each file's disclosure state are left exactly as they were, so reopening the overlay (`s`) resumes right where it was left. The query and results are only ever reset by the user explicitly clearing them (Ctrl+U, backspacing the query to empty, or typing a new one).
- Typing any other printable character, including space, appends it to the query, cancels any in-flight scan for the old query, starts a new one, and (like Backspace) resets disclosure state to expanded-by-default for the new result set.
