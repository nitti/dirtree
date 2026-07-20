# examples/

Test fixtures for manually exercising dirtree against trees much larger than this repo itself: many-thousands-of-files real-world source trees, plus synthetic files sized to hit content search's streaming-scan behavior (spec §9.1) on purpose.

Everything here is fetched or generated into `data/`, which is entirely `.gitignore`'d — nothing under `examples/data/` is ever committed to dirtree's own history. Repos are pulled as plain ZIP snapshots (`curl` + `unzip`) rather than `git clone`, so there's no `.git` history or submodule bookkeeping to manage, just a directory of files.

## Usage

```
make -C examples list      # see available targets
make -C examples linux     # Linux kernel source, ~80k files, deep nesting
make -C examples llvm      # LLVM monorepo, larger and deeper than linux
make -C examples cpython   # CPython source, smaller, quick to fetch
make -C examples bigfiles  # synthetic large files with known search markers
make -C examples all       # everything above
make -C examples clean     # remove all of data/
```

Then point dirtree at whichever tree you want, e.g. `dirtree examples/data/linux`.

## Example search terms

Real repos, once fetched:

| Tree | Term | What it exercises |
|---|---|---|
| `linux` | `EXPORT_SYMBOL` | thousands of hits across thousands of files — result-list scroll/paging |
| `linux` | `TODO` | many small, scattered matches |
| `llvm` | `LLVMContext` | a mid-size symbol across a very deep tree |
| `cpython` | `PyObject_GC_New` | a quick, small-scale sanity check |

`bigfiles`, once generated (see `make_bigfiles.sh` for exact sizes and rationale):

| File | Marker | What it exercises |
|---|---|---|
| `past_old_cap.txt` (~1.1MB) | `NEEDLE_PAST_OLD_CAP` | a match just past the preview panel's 1MB read cap — confirms search isn't capped the same way |
| `mid_size.txt` (~50MB) | `NEEDLE_MID_SIZE` | an ordinary "big log file"-sized target, marker near the end |
| `large.txt` (~300MB) | `NEEDLE_LARGE` | large enough that a slow disk/heavy load may trip the per-file scan timeout — confirms the timeout renders as an inline issue rather than a silent truncation |
| `unreadable.txt` | `NEEDLE_UNREADABLE` | `chmod 000`'d — confirms a permission-denied candidate is listed with its OS error shown inline instead of being silently dropped |

## Benchmarking

`internal/search`'s `maxConcurrentScans` and `perFileTimeout` vars were picked by judgment, not measurement. `go test`'s benchmark support (`internal/search/search_bench_test.go`) can measure `search.Run`'s throughput at different `maxConcurrentScans` values against a real tree:

```
make -C examples bench-linux      # fetches linux (if needed), then benchmarks against it
make -C examples bench-bigfiles   # generates bigfiles (if needed), then benchmarks against it
```

Or point it at any directory directly:

```
DIRTREE_BENCH_DIR=/path/to/dir DIRTREE_BENCH_QUERY=term make bench   # from the repo root
```

`DIRTREE_BENCH_QUERY` defaults to `TODO` if unset — match count doesn't affect scan cost, so any non-empty query works. This only measures; picking new defaults for `maxConcurrentScans`/`perFileTimeout` in `internal/search/search.go` based on the results is still a manual follow-up step.

### Sample results

One run of `DIRTREE_BENCH_DIR=examples/data/cpython make bench` against a fresh CPython checkout (5,896 files), Apple M4 Pro / macOS, single run per concurrency level (`-benchtime=1x`) — illustrative, not a guarantee for other machines or trees:

| `maxConcurrentScans` | time | files/sec |
|---|---|---|
| 1 | 544ms | 10,846 |
| 2 | 293ms | 20,104 |
| 4 | 206ms | 28,679 |
| 8 | 196ms | 30,091 |
| 16 (current default) | 190ms | 31,020 |
| 32 | 212ms | 27,842 |
| 64 | 174ms | 33,813 |
| 128 | 159ms | 37,115 |

The big jump is 1→4 (concurrency pays off immediately on an I/O-bound scan); past 8–16 the gains flatten and get noisy (32 dipping below 16 here is almost certainly run-to-run variance, not a real regression — see the single-run caveat above). Nothing here suggests the current default of 16 is meaningfully wrong, but it also doesn't rule out that something higher (64–128) is measurably better on a bigger tree or a slower disk; that's the kind of question this harness exists to let someone answer with real numbers instead of guessing.

## Comparing against other tools

`compare_search.sh` times dirtree's search against `ripgrep`, `grep`, and `ag` (whichever are installed) over the same directory/query, appending one CSV row per tool per run to `data/bench-results.csv` (gitignored, local-only) so results accumulate across repeated runs instead of being lost:

```
make -C examples compare DIR=data/linux QUERY=EXPORT_SYMBOL
```

### Sample results

Same CPython checkout as above (5,896 files), three queries, `ripgrep`/`ag` not installed on this machine so only `grep -riI` is shown (single run each, wall-clock via `compare_search.sh`):

| Query | dirtree | `grep -riI` |
|---|---|---|
| `import` | 0.43s | 1.82s |
| `TODO` | 0.27s | 1.81s |
| `PyObject_GC_New` | 0.29s | 1.93s |

dirtree comes out ~4-7x faster than `grep -r` here despite also doing more work per match (collecting line numbers/text for every hit across every file, not just a count). Take this as one data point on one machine, not a broad claim against `grep`'s implementation in general — the comparison exists so this kind of number is easy to regenerate and check again after any change to the scan path, not as a one-time bragging point.
