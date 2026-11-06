# How the numbers were measured

## What runs

Every repository got a set of `BenchmarkJSON...` benchmarks before any code
changed. They encode and decode the types each service actually puts on the
wire (API responses, events, stored documents, model-provider requests and
responses, JSON-RPC frames) at realistic sizes, through the same functions the
service calls. They were committed on their own, before any code changed.

Each repository's migration is one commit, `json: move to encoding/json/v2`.
The final comparison measures that commit (`after`) against its parent
(`before`), so it isolates the migration from whatever else was merged
in between. In a repository that depends on our shared libraries, the same
commit also raises them to their migrated releases (the public SDK
v0.33.0; the shared library v0.112.0 or v0.113.0, which differ only in the
order of an error body's details; the app hosting API types v0.0.4 where
they are used). The library's own JSON code is then part of the `after`
side, as it is in production.

Three later commits are not in the comparison: the app hosting service
dropping its copies of the app manifest types, the API platform moving to
the migrated model gateway release, and the shared library's sorted error
details (its v0.113.0, measured only through the repositories that use
it).

Repositories are named by their role in the text and figures. The data
files use each repository's directory name; [data/names.tsv](data/names.tsv)
maps one to the other and says which repositories are public.

The migration commit changes the benchmark files exactly as it changes other
tests: imports and calls move to `encoding/json/v2` with the options the
production path now uses. Benchmark names, sub-benchmark names and inputs stay
the same, so each "before" result pairs with one "after" result by name.

## Three variants

| Variant | Tree | Build | What it is |
|---|---|---|---|
| `classic` | before | `GOEXPERIMENT=nojsonv2` | the v1 package on its original engine, as before Go 1.27 |
| `base` | before | default | the v1 package on the v2 engine, the Go 1.27 default, with our code unchanged |
| `after` | after | default | our code on the v2 API |

`classic` against `base` isolates what the Go 1.27 toolchain changed with no
code change. `base` against `after` isolates what our migration changed.

## Clean trees

Other work continued in the same checkouts while this ran. Each variant
therefore builds from `git archive` of an exact commit, never from a working
tree: `scripts/trees.sh` exports one directory per repository.

## Order and noise

The first measurement of the status quo (`scripts/bench.sh`, kept in
`data/baseline`) ran, for each round, for each module, every variant in
turn, five runs at a time. The machine was shared with other work (load
average around 10 on 18 cores), and the load moved between the two rounds:
some modules came out bimodal, with every variant's runs in one round about
twice as slow as in the other. Medians and ratios survived that, but an
unpaired test could not tell a real difference from a busy minute, and too
many benchmarks counted as "no change".

The final measurement (`scripts/bench-paired.sh`) is built so that load
cannot do that:

1. Each benchmark package's test binary is built once per variant, before
   any measurement, so no compile runs between samples.
2. There are 10 rounds. In each round, for each package, every variant runs
   once (`-test.count 1`), one right after the other. The order of the
   variants rotates from round to round, so none always runs first.
3. The i-th result of one variant therefore ran next to the i-th result of
   every other. If the machine is busy for a minute, the samples from that
   minute are slow for all variants alike, and the pair keeps its ratio.

The run log records the load average at the start of every round.

## The final run

The binaries were built from 02:14 to 02:17 UTC on 2026-11-06, and the 10
rounds ran from 02:17 to 03:08, about five minutes each. The load average
stayed between 2.1 and 4.5 on 18 cores, much quieter than during the
baseline. Every one of the 762 benchmarks built and ran in all three
variants in every round: 22,860 results, with no build failure and no
failed run ([data/final/run.log](data/final/run.log)).

The status-quo measurement from before the migration
([data/baseline](data/baseline)) and the final run agree on what the
toolchain alone did: `classic` to `base` time per operation was 0.720 then
and 0.702 now, allocations 0.607 and 0.608.

## Statistics

`tools/analyze` reads the raw `go test` output with `golang.org/x/perf/benchfmt`
and, for every benchmark and unit (time, bytes, allocations):

- summarizes each variant by its median with a 95% confidence interval, using
  `benchmath.AssumeNothing` (distribution-free, as `benchstat` does);
- compares two variants by pairing their results round by round: the ratio
  is the median of the 10 per-round ratios (to / from), and an exact
  two-sided sign test over the rounds decides significance at alpha = 0.05
  (10 of 10 rounds in one direction gives p = 0.002; 9 of 10, p = 0.021;
  8 of 10, p = 0.109, not significant);
- also reports the unpaired Mann-Whitney U test and the ratio of medians,
  as `benchstat` would, in every record;
- counts a benchmark as improved or regressed only when the paired
  difference is significant.

Per module and overall it reports the geometric mean of the ratios, the 10th,
50th and 90th percentiles, and a 95% percentile-bootstrap interval of the
geometric mean (4,000 resamples of the module's benchmarks). That interval
describes how much the result depends on which benchmarks a module happens to
have; the per-benchmark tests describe run-to-run noise.

`benchstat` output for the same files is in
[data/final/benchstat.txt](data/final/benchstat.txt), for anyone who prefers
to read the standard tool; it compares each variant with `classic`.

## Caveats

- One machine (Apple M5 Pro, 18 cores, 64 GB, macOS), one Go release
  (1.27.3). The relative results matter more than the absolute times.
- Benchmarks measure JSON work in isolation. A request handler that spends
  most of its time in a database will see a smaller share of the gain.
- The benchmarks were written for this study before any code changed, by
  agents of the same Claude Code session that later migrated the code
  ([PROCESS.md](PROCESS.md)). They were not tuned afterwards. The
  migration commit changed them only as the API required: raw values became `jsontext.Value`, `MarshalIndent` became `Marshal`
  with indentation options, and setup data is built with v2. No benchmark
  was added, removed or renamed between the two sides.
