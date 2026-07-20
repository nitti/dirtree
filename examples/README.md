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

`DIRTREE_BENCH_QUERY` defaults to `TODO` if unset — match count doesn't affect scan cost, so any non-empty query works. This only measures; picking (or, as below, validating) `maxConcurrentScans`/`perFileTimeout` in `internal/search/search.go` from the results is still a manual step, done once here and re-checked whenever the scan path changes meaningfully.

### Sample results: concurrency

5 runs of `DIRTREE_BENCH_DIR=examples/data/linux go test ./internal/search/... -run=^$ -bench=. -benchtime=1x -count=5`, averaged, against a full Linux kernel checkout (94,732 files), Apple M4 Pro / macOS — illustrative, not a guarantee for other machines or trees, but a large enough tree and enough repetitions that the trend is meaningful, not single-run noise:

| `maxConcurrentScans` | avg files/sec | gain over half |
|---|---|---|
| 1 | 8,045 | — |
| 2 | 15,422 | +92% |
| 4 | 26,301 | +71% |
| 8 | 37,548 | +43% |
| 16 (current default) | 40,520 | +8% |
| 32 | 43,647 | +8% |
| 64 | 44,717 | +2% |
| 128 | 44,263 | -1% (noise) |

The knee is at 8→16: every doubling up to 8 buys 40%+ more throughput, but 8→16 only buys ~8%, and everything past 16 is under ~8% (64→128 is flat-to-negative, within run-to-run noise). **16 sits right at that knee** — validated as the default rather than picked by guesswork.

### Sample results: per-file latency (informs `perFileTimeout`)

`perFileTimeout` isn't a throughput question — it's "how long can one file's scan run before it's almost certainly a pathological case, not a normal one." `cmd/searchbench -per-file-stats` times each candidate individually instead of one aggregate scan:

```
go run ./cmd/searchbench -dir examples/data/linux -query TODO -per-file-stats
```

Against the same Linux checkout (94,831 files, one file scanned per call):

| Percentile | Latency |
|---|---|
| p50 | 63µs |
| p90 | 226µs |
| p99 | 537µs |
| p999 | 2.25ms |
| max (single slowest real file) | 38.7ms — a large generated GPU register-definition header, not an outlier bug |

Cross-checked against a synthetic ~300MB file (`make -C examples bigfiles`, `large.txt`): **~600ms** to fully scan. The current `perFileTimeout` default of **5s** is validated by this data too: it's ~100x the real-world p99, and still gives roughly an 8x margin over "a user deliberately searches one legitimately huge (~300MB) file" — generous enough to never fire on anything normal, while still bounding a genuinely stalled/pathological scan (a hung network mount, a multi-GB file) to a few seconds instead of indefinitely.

## Comparing against other tools

`compare_search.sh` times dirtree's search against `ripgrep`, `grep`, and `ag` (whichever are installed) over the same directory/query, appending one CSV row per tool per run to `data/bench-results.csv` (gitignored, local-only) so results accumulate across repeated runs instead of being lost:

```
make -C examples compare DIR=data/linux QUERY=EXPORT_SYMBOL
```

### Sample results

Same Linux checkout, two queries, all three external tools installed (`brew install ripgrep the_silver_searcher`), single run each, wall-clock via `compare_search.sh`:

| Query | dirtree | `ripgrep` | `ag` | `grep -riI` |
|---|---|---|---|---|
| `EXPORT_SYMBOL` | 2.95s | 1.76s | 2.45s | 32.67s |
| `TODO` | 2.93s | 2.38s | 2.22s | 26.99s |

dirtree lands in the same tier as `ag` (a purpose-built, C-based search tool) and is ~1.2-1.7x behind `ripgrep` — very respectable for a Go implementation that isn't specifically optimized as a grep replacement and also collects full line text/numbers for every hit, not just counts. All three beat plain `grep -r` by roughly an order of magnitude, which is really a statement about `grep`'s lack of parallelism/optimized I/O rather than anything specific to dirtree. Take this as one data point on one machine, not a general benchmark claim — the comparison exists so it's easy to regenerate and check again after any change to the scan path.
