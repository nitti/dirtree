//go:build spinnerdebug

package spinner

// See debug_off.go for what this switches. Built only via
// `go build -tags spinnerdebug` (or `go run -tags spinnerdebug`) — a
// dev-only way to watch the spinner animate without needing a
// genuinely slow directory to index. Never build a release/shipped
// binary with this tag set.
const DebugAlwaysShow = true
