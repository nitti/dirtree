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

## Comparing against other tools

`compare_search.sh` times dirtree's search against `ripgrep`, `grep`, and `ag` (whichever are installed) over the same directory/query, appending one CSV row per tool per run to `data/bench-results.csv` (gitignored, local-only) so results accumulate across repeated runs instead of being lost:

```
make -C examples compare DIR=data/linux QUERY=EXPORT_SYMBOL
```
