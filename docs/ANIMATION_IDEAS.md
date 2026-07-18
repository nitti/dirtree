# Animation ideas

This is a brainstorm, not a spec. None of this is decided or scheduled — it's a running list of animation ideas to draw from, checked against the principles in `specs/SPEC.md` §5.3. Nothing here should be treated as required behavior; if and when one of these is actually built, its real behavior belongs in `specs/SPEC.md` (with acceptance criteria in `specs/TESTING.md`), same as the existing delayed loading indicator (§5.2).

## Reference implementation

The delayed loading indicator: threshold-suppressed spinner → minimum-display floor → completion message → left-to-right anchored fade-out (`specs/SPEC.md` §5.2, sequencing logic in `internal/spinner`, fade timing in `internal/toast`). It's the model every idea below is checked against.

## Ideas

- **Generalize the completion-message fade into a shared "toast" primitive — done.** The fade math moved out of `internal/spinner` into `internal/toast` (`Decide`), which `spinner.BadgeDecision` now calls; SPEC.md §5.3 documents it as the shared primitive. Of the two other events named here as candidates:
  - **Transient errors — done.** A live-refresh-detected permission-denied on a directory now shows as a bottom-right toast (SPEC.md §6.1), reusing the same timing/styling as the completion message, distinguished only by color. See `internal/tree.RefreshTree`'s newly-erroring-path return value and `App.showErrorToast`.
  - **Copy-mode / clipboard confirmation — not built.** Deliberately dropped for now: there's no clipboard-copy action in dirtree yet (copy mode, §2.1, only strips formatting for manual terminal selection), so there's nothing to confirm. Revisit if/when a copy-to-system-clipboard action is actually added — the toast primitive is ready for it whenever that happens.
- **Content search scan progress.** Content search (§9) already reuses the indexing badge's spinner once a scan has been running long enough to be perceptible. If a total candidate-file count is known up front, the glyph could become a determinate fraction (e.g. `search 42%`) instead of an indeterminate spinner — still elapsed/state-driven (principle: elapsed-time-driven, not frame-driven), just richer information once it's available.
- **Tree expand/collapse — considered and likely rejected.** A brief "unfold" transition (children rendered at partial height for one frame before settling) was considered and doesn't hold up well against the principles: a text-cell tree view has no sub-cell granularity to animate smoothly, a keyboard-driven browser's expectation is that expand/collapse is instant, and — the deciding factor — the state change is already fully communicated by the expand marker flipping and the children appearing/disappearing, so an unfold transition would be "informative, not decorative" in name only. Recorded here so it isn't re-proposed without this context; an instant toggle is very likely the right long-term answer, not a placeholder.
- **Selection/cursor movement — no animation, by design.** Instant jump on every navigation key, no easing or trailing effect. This isn't a gap to fill later; zero-latency selection feedback is core to what makes a keyboard-driven tool feel responsive, and it's the clearest example of "informative, not decorative," "never block the UI," and "subtle, not flashy" all pulling in the same direction: nothing about *how* the cursor got somewhere is information the user needs, only *where* it ended up — so the correct amount of animation here is none.

## Open questions

- For content search's determinate-progress idea: is a total candidate count actually known early enough in the scan to make a percentage meaningful, or would it jump around enough to be worse than the current indeterminate spinner? Needs a look at `internal/search` before committing to the idea.
