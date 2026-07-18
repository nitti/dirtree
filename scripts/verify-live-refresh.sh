#!/usr/bin/env bash
# Manual verification helper for SPEC.md §6a (live refresh).
#
# Builds dirtree, sets up a scratch directory with a couple of
# subdirectories, then launches a background "mutator" that adds,
# renames, and deletes files/directories on a loop while you drive
# dirtree in the foreground. Watch the tree pane (and jump mode, and the
# background-index spinner badge) update on their own as the mutator
# runs — that's what you're checking.
#
# Usage:
#   scripts/verify-live-refresh.sh
#
# Ctrl-C or `q`/Escape to quit dirtree; the mutator is cleaned up
# automatically on exit.

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
scratch="$(mktemp -d -t dirtree-live-refresh)"

cleanup() {
  if [[ -n "${mutator_pid:-}" ]] && kill -0 "$mutator_pid" 2>/dev/null; then
    kill "$mutator_pid" 2>/dev/null || true
    wait "$mutator_pid" 2>/dev/null || true
  fi
  rm -rf "$scratch"
}
trap cleanup EXIT INT TERM

echo "Building dirtree..."
( cd "$repo_root" && go build -o "$scratch/.dirtree-bin" ./cmd/dirtree )

# Seed some initial structure so there's something to expand/browse
# before the mutator starts changing things underneath you.
mkdir -p "$scratch/tree/alpha" "$scratch/tree/beta/nested"
echo "seed" > "$scratch/tree/alpha/keep.txt"
echo "seed" > "$scratch/tree/beta/nested/keep.txt"

# The mutator: every 2s, do one of add-file / add-dir / rename / delete
# inside $scratch/tree, so a session watching the tree sees a steady
# trickle of each kind of change SPEC.md §6a is supposed to handle.
mutate_loop() {
  local i=0
  while true; do
    sleep 2
    i=$((i + 1))
    case $((i % 4)) in
      0)
        echo "created at iteration $i" > "$scratch/tree/new-file-$i.txt"
        ;;
      1)
        mkdir -p "$scratch/tree/new-dir-$i/inner"
        echo "hi" > "$scratch/tree/new-dir-$i/inner/f.txt"
        ;;
      2)
        # rename/move: pick the most recent new-file if any exist
        local latest
        latest="$(ls -t "$scratch/tree"/new-file-*.txt 2>/dev/null | head -n1 || true)"
        if [[ -n "$latest" ]]; then
          mv "$latest" "$scratch/tree/renamed-$i.txt"
        fi
        ;;
      3)
        # delete: remove the oldest new-dir if any exist
        local oldest
        oldest="$(ls -td "$scratch/tree"/new-dir-* 2>/dev/null | tail -n1 || true)"
        if [[ -n "$oldest" ]]; then
          rm -rf "$oldest"
        fi
        ;;
    esac
  done
}

mutate_loop &
mutator_pid=$!

cat <<EOF

Scratch tree: $scratch/tree
Mutator running in background (pid $mutator_pid), one change every ~2s:
  - creates new-file-N.txt
  - creates new-dir-N/inner/f.txt
  - renames the most recent new-file-*.txt -> renamed-N.txt
  - deletes the oldest new-dir-*

What to check while dirtree is running, with NO manual refresh/restart:
  1. New files/dirs appear in the tree on their own within ~1s.
  2. Renames show up as the old name disappearing and the new one appearing.
  3. Deletes remove the entry; if you had it selected, selection should
     jump to its parent (or further up) instead of crashing or freezing.
  4. Expand a directory (e.g. beta/nested), then leave it expanded while
     the mutator changes an unrelated part of the tree (e.g. alpha) --
     the expanded directory should stay expanded and NOT be disturbed.
  5. Open jump mode ("/") and search for "renamed" or "new-dir" -- results
     should reflect changes made *after* you opened dirtree, not just
     what existed at startup.
  6. If a lot changes at once, you may briefly see the indexing spinner
     (bottom-right badge / "indexing..." in jump mode) while the
     background index rebuilds -- that's expected, not a bug.

Press Enter to launch dirtree against the scratch tree...
EOF
read -r

"$scratch/.dirtree-bin" "$scratch/tree"

echo "dirtree exited; cleaning up scratch tree and mutator."
