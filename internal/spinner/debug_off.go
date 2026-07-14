//go:build !spinnerdebug

package spinner

// DebugAlwaysShow bypasses the status badge's perceptibility threshold
// (but not the completed-index check), so the spinner appears
// immediately when indexing starts instead of waiting out the
// threshold — letting both the spinner and the completion
// message/fade-out that follows it be exercised on demand instead of
// waiting for a genuinely slow index. It is a build-time switch, not a
// runtime flag: this file supplies the default (off) behavior compiled
// into every ordinary build. The `spinnerdebug` build tag (see
// debug_on.go) flips it on and must never be used for a shipped build.
const DebugAlwaysShow = false
