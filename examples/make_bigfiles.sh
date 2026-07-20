#!/usr/bin/env bash
# Generates synthetic files for manually exercising content search's
# streaming scan (internal/search, spec §9.1) against sizes and failure
# modes that real-world repos don't reliably provide on demand. See
# examples/README.md for the search terms to try against each one.
#
# Usage: make_bigfiles.sh <output-dir>
set -euo pipefail

out="${1:?usage: make_bigfiles.sh <output-dir>}"
mkdir -p "$out"

filler() {
	# Repeating filler text, not zero bytes, so the file doesn't look
	# binary and gzip/sniffing tools don't take a shortcut. yes is wrapped
	# in its own subshell with `|| true` because head closing the pipe
	# early sends yes SIGPIPE, which pipefail would otherwise treat as a
	# failure of the whole pipeline under `set -e`.
	(yes "the quick brown fox jumps over the lazy dog" || true) | head -c "$1"
}

# Just past preview's 1MB read cap (internal/preview.DefaultByteCap) - the
# threshold content search itself used to truncate at before it moved to
# streaming. Marker sits shortly after that offset.
{ filler 1100000; echo "NEEDLE_PAST_OLD_CAP"; } > "$out/past_old_cap.txt"

# Comfortably large (~50MB), marker near the very end, to confirm a full
# streamed scan of an ordinary "big log file"-sized target completes and
# finds a late match.
{ filler 50000000; echo "NEEDLE_MID_SIZE"; } > "$out/mid_size.txt"

# Large enough (~300MB) that a slow disk or heavy system load may cause
# scanFile's per-file timeout to trip before reaching the marker - useful
# for manually confirming the timeout path renders as an inline Issue
# instead of silently truncating. Whether it actually times out depends on
# scan speed; if it doesn't, that's fine too, it's just a bigger streaming
# test.
{ filler 300000000; echo "NEEDLE_LARGE"; } > "$out/large.txt"

# Unreadable file, to manually confirm a permission-denied candidate is
# listed with its OS error shown inline instead of being silently dropped.
echo "NEEDLE_UNREADABLE" > "$out/unreadable.txt"
chmod 000 "$out/unreadable.txt"

echo "Generated in $out:"
ls -lh "$out"
