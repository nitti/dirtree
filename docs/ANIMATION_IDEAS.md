# Animation ideas

This is a brainstorm, not a spec. None of this is decided or scheduled — it's a running list of animation ideas to draw from, checked against the principles in `specs/SPEC.md` §5.3. Nothing here should be treated as required behavior; if and when one of these is actually built, its real behavior belongs in `specs/SPEC.md` (with acceptance criteria in `specs/TESTING.md`), same as the existing delayed loading indicator (§5.2).

## Reference implementation

The only animation currently in the spec is the delayed loading indicator: threshold-suppressed spinner → minimum-display floor → completion message → left-to-right anchored fade-out (`specs/SPEC.md` §5.2, logic in `internal/spinner`). It's the model every idea below is checked against.

## Ideas

- **Generalize the completion-message fade into a shared "toast" primitive.** `internal/spinner`'s `Completion`/`BadgeDecision` already implement "show a transient message, then fade it out left-to-right from an anchor point" as pure functions of elapsed time. At least two other events want exactly that treatment:
  - **Copy-mode / clipboard confirmation** — a transient "copied" message near wherever the action was triggered.
  - **Transient errors** — e.g. a live-refresh-detected permission-denied on a directory, or another non-fatal error that doesn't fit the per-node inline error indicator (§5.2).
  This is the highest-value idea on this list: no new animation mechanic, just factoring the existing one so three features share it instead of each reimplementing the same math.
- **Content search scan progress.** Content search (§9) already reuses the indexing badge's spinner once a scan has been running long enough to be perceptible. If a total candidate-file count is known up front, the glyph could become a determinate fraction (e.g. `search 42%`) instead of an indeterminate spinner — still elapsed/state-driven (principle: elapsed-time-driven, not frame-driven), just richer information once it's available.
- **Tree expand/collapse — considered and likely rejected.** A brief "unfold" transition (children rendered at partial height for one frame before settling) was considered and doesn't hold up well against the principles: a text-cell tree view has no sub-cell granularity to animate smoothly, and a keyboard-driven browser's expectation is that expand/collapse is instant, not eased. Recorded here so it isn't re-proposed without this context; an instant toggle is very likely the right long-term answer, not a placeholder.
- **Selection/cursor movement — no animation, by design.** Instant jump on every navigation key, no easing or trailing effect. This isn't a gap to fill later; zero-latency selection feedback is core to what makes a keyboard-driven tool feel responsive, and it's the clearest example of the "never block the UI" and "restrained, not garish" principles pulling in the same direction: the correct amount of animation here is none.

## Open questions

- Does a "toast" primitive belong in `internal/spinner` (renamed to something more general), or as a new small package that `internal/spinner`'s badge logic itself is rebuilt on top of? Worth deciding at implementation time, not speculatively now.
- For content search's determinate-progress idea: is a total candidate count actually known early enough in the scan to make a percentage meaningful, or would it jump around enough to be worse than the current indeterminate spinner? Needs a look at `internal/search` before committing to the idea.
