#!/usr/bin/env bash
# Times dirtree's content search against common external content-search
# tools (ripgrep, grep, ag) over the same directory/query, and appends a
# CSV row per tool run to data/bench-results.csv so results accumulate
# locally across invocations instead of being lost. That file lives under
# data/, which is fully .gitignore'd, so nothing captured here is ever
# committed. See examples/README.md.
#
# Usage: compare_search.sh <dir> <query>
set -euo pipefail

dir="${1:?usage: compare_search.sh <dir> <query>}"
query="${2:?usage: compare_search.sh <dir> <query>}"

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
results_csv="$script_dir/data/bench-results.csv"
mkdir -p "$(dirname "$results_csv")"
if [ ! -f "$results_csv" ]; then
	echo "timestamp,tool,dir,query,elapsed_seconds" > "$results_csv"
fi

timestamp="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

run_and_record() {
	local tool="$1"
	shift
	local start end elapsed
	start=$(date +%s.%N)
	# Zero matches is a valid outcome, not a failure; grep/rg/ag all exit
	# non-zero for it, which set -e would otherwise treat as a script
	# error and abort the remaining tool comparisons.
	"$@" >/dev/null || true
	end=$(date +%s.%N)
	elapsed=$(awk -v a="$start" -v b="$end" 'BEGIN { printf "%.3f", b - a }')
	printf "%-10s %ss\n" "$tool" "$elapsed"
	echo "$timestamp,$tool,$dir,$query,$elapsed" >> "$results_csv"
}

echo "Comparing content search over $dir for \"$query\":"
echo

run_and_record dirtree go run "$script_dir/../cmd/searchbench" -dir "$dir" -query "$query"

if command -v rg >/dev/null 2>&1; then
	run_and_record ripgrep rg -i -c -- "$query" "$dir"
else
	echo "ripgrep (rg) not installed, skipping"
fi

run_and_record grep grep -riIc -- "$query" "$dir"

if command -v ag >/dev/null 2>&1; then
	run_and_record ag ag -i -- "$query" "$dir"
else
	echo "ag (the_silver_searcher) not installed, skipping"
fi

echo
echo "Results appended to $results_csv"
