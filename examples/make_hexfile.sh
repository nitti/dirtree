#!/usr/bin/env bash
# Generates a synthetic binary fixture for manually exercising the hex
# view (internal/ui/views/hexview.go, SPEC.md §2.1a): reasonably sized
# (256KB, several screens' worth of scrolling) so Page Up/Down and
# goto-offset are worth trying, with a few known ASCII markers dropped in
# at fixed offsets so goto-offset and hex-view find both have something
# predictable to jump to. See examples/README.md for the offsets/markers
# to try against it.
#
# Usage: make_hexfile.sh <output-dir>
set -euo pipefail

out="${1:?usage: make_hexfile.sh <output-dir>}"
mkdir -p "$out"

size=262144
path="$out/sample.bin"

dd if=/dev/urandom of="$path" bs=1024 count=$((size / 1024)) status=none

# A NUL byte at offset 0 guarantees dirtree's binary check (a NUL byte in
# the leading capped peek, SPEC.md §2.2) routes this file into the hex
# view rather than /dev/urandom happening not to produce one early
# enough on its own.
printf '\x00' | dd of="$path" bs=1 count=1 seek=0 conv=notrunc status=none

printf 'HEXNEEDLE_START' | dd of="$path" bs=1 seek=64 conv=notrunc status=none
printf 'HEXNEEDLE_MID' | dd of="$path" bs=1 seek=$((size / 2)) conv=notrunc status=none
printf 'HEXNEEDLE_END' | dd of="$path" bs=1 seek=$((size - 64)) conv=notrunc status=none

echo "wrote $path ($(wc -c <"$path" | tr -d ' ') bytes)"
echo "markers: HEXNEEDLE_START at offset 0x40, HEXNEEDLE_MID at $(printf '0x%x' $((size / 2))), HEXNEEDLE_END at $(printf '0x%x' $((size - 64)))"
