# The migration, step by step

This is the full record: what ran, in what order, what failed and how it was
fixed. The post ([README.md](README.md)) tells the story; this file is the
log behind it. Times are UTC, from 2026-11-05 into 2026-11-06.

## 0. Survey and design (17:07 to 17:21)

The request: move every Go repository in the organization from
`encoding/json` to `encoding/json/v2`, and measure performance before and
after.

The survey found 29 repositories with Go code, 39 Go modules (not counting
test fixtures), 7,361 Go files with 1.94 million lines (tests included), and
2,327 files importing `encoding/json`, all counted before any benchmark was
added. Every repository pins
`toolchain go1.27.3`.

Two findings shaped everything after:

- On Go 1.27 the v2 engine is the default (`internal/buildcfg`: `JSONv2` on),
  and `encoding/json` itself is built from the `v2_*.go` files on top of it
  with v1 options. Our code was already running on the new engine before we
  changed a line. `GOEXPERIMENT=nojsonv2` still builds the original v1
  engine, which made a three-way comparison possible.
- `json.RawMessage` is a type alias of `jsontext.Value`. A repository that
  moves to v2 still matches the exported API of one that has not, so the 29
  repositories could move in any order, in parallel.

Toolchain behavior was checked with probe programs rather than taken from
memory; [tools/probe](tools/probe) reproduces each case and
[tools/probe/output.txt](tools/probe/output.txt) is its output on Go 1.27.3.
The facts that became rules (all in [GUIDE.md](GUIDE.md)):

1. The `format:` struct tag option is not supported by the standard library
   on Go 1.27. A field carrying one makes Marshal and Unmarshal of the whole
   struct fail at run time.
2. `time.Duration` has no v2 representation; marshaling one is an error.
3. v1 `omitempty` decides by Go kind; v2 `omitempty` decides by the encoded
   JSON. `omitzero` exists in both.
4. `MarshalWrite` writes no trailing newline; `MarshalEncode` on a
   `jsontext.Encoder` does.
5. `UnmarshalRead` on an empty body fails with `io.ErrUnexpectedEOF`, not
   `io.EOF`; `UnmarshalDecode` returns exactly `io.EOF` at the end of a
   stream.
6. Call-site options do not reach a v1-style `UnmarshalJSON([]byte)` method.
7. `MatchCaseInsensitiveNames` also ignores `_` and `-`.

### Wire policy

One decision needed the owner: how much of the old JSON to keep. The answer
was "v2 defaults, fields pinned":

Kept identical: which fields are omitted, names and values, durations as
integer nanoseconds, key order wherever bytes are stored, hashed, compared
or sent to model providers, `null` for nil lists sent to services we do not
own, and leniency for input we do not produce (duplicate keys, invalid
UTF-8, case-insensitive names).

Accepted: nil lists become `[]` in our own responses; `<`, `>` and `&` are
no longer escaped outside HTML; map keys are unsorted in API responses;
field names match case-sensitively for our own clients; error text changes.

## 1. Benchmarks for the status quo (17:21 to 19:00)

A workflow ran one Sonnet agent per repository (23 agents; small
repositories shared one) with a single brief: write `BenchmarkJSON...`
benchmarks for the JSON hot paths, through the functions the service calls,
at realistic sizes, and change nothing else. They returned 100 benchmark
files and 350 benchmark functions (762 results with sub-benchmarks), each
with a note on the production path it stands for. The workflow took 7.2
minutes, 569 tool calls and 2.85 million tokens.

Each repository then went through its full gate (`go tool lateregate`) before
the benchmarks were committed on their own, so the commit that adds them is
the "before" tree of every comparison. Landing took longer than writing:

| Problem | Repositories | Fix |
|---|---|---|
| lint findings in the new files: `go fix` modernizations, import grouping, `gofmt`, a needless loop-variable copy, `errors.Is` | 12 | applied the gate's own diff, or a hand fix, to the benchmark files only |
| a test that forbids a fixed image registry in any file | cella | neutral example registry in the fixtures |
| flaky end-to-end tests under load (a stub server late to answer) | lux, latere-cli | rerun alone |
| no Docker socket; Testcontainers needs one | all with database tests | pointed `DOCKER_HOST` at the Podman machine |
| kind's `load docker-image` shells out to a `docker` binary | lux | a `docker` wrapper over Podman that saves images under the name a Kubernetes node resolves |
| the install walk adds an `/etc/hosts` line with `sudo` | lux | needs a password; the walk ran in CI instead, every other suite passed locally |
| another session editing the same checkout | cella | gated in a clean detached worktree of `origin/main` |
| a test that reads every file for company host names, which the local gate's changed-only suite never reaches | lux | CI caught it; the test issuer became `login.example.com`. Since then every push also runs the module's whole `go test ./...` |

The gate ran 45 times for 29 repositories; 17 runs failed for the reasons
above. None failed because a benchmark measured the wrong thing; the fixes
were style, fixtures and the machine.

## 2. Measuring the status quo (19:04 to 19:42)

See [METHOD.md](METHOD.md) for the method. Two variants built from clean
exports of the benchmark commits: `base` (Go 1.27 default) and `classic`
(`GOEXPERIMENT=nojsonv2`), 2 rounds of 5 runs at `-benchtime 100ms`,
alternating per module. A smoke run first executed every benchmark once
under both builds: 30 modules, 762 results each, no failures.

The run took 38 minutes. Its result, before any code changed, was already
the first finding: on Go 1.27 our unchanged code is much faster than on
the old engine.

| `classic` to `base` | Geometric mean | 95% interval | Faster | Slower | No significant change |
|---|---|---|---|---|---|
| time per operation | 0.720 | 0.699 to 0.742 | 405 | 61 | 296 |
| bytes per operation | 0.873 | 0.847 to 0.896 | 542 | 191 | 29 |
| allocations per operation | 0.607 | 0.576 to 0.639 | 589 | 132 | 41 |

By repository the time ratio ran from 0.48 (the CLI, whose benchmarks are
mostly decoding API answers) to about 1.0 (the company site, 6 benchmarks,
none significant). The raw data and statistics are in
[data/baseline](data/baseline).

Two things in this run shaped the final one. The machine was shared (load
average around 10 on 18 cores), and the load moved between the rounds: in
some modules every variant's runs in one round were about twice as slow as
in the other. The medians held, but the unpaired test then called many
real differences "no change" (sermo: 14 benchmarks, all about 0.6 to 0.8,
none significant). The final measurement therefore pairs the variants run
by run (METHOD.md, "Order and noise").

## 3. Migrating (19:42 to 21:27)

### Tags first, by tool

Before any agent started, [tools/jsontags](tools/jsontags) rewrote the
struct tags of every module so that each field encodes under v2 exactly as
it did under v1 (the table is in the tool's doc comment and in the post).
The first pass covered 29 modules in about 3 minutes.

### Workflows

The repositories were split into scopes, one per small or medium
repository and several per large one, with no two scopes owning the same
directory. Each scope ran through a migrating agent and a reviewing agent
(Sonnet), with [GUIDE.md](GUIDE.md) as the brief. Three repositories had a
release of other work going out that day; their scopes started once each
release was out.

| Workflow | Scopes | Agents | Wall time | Tool calls | Tokens |
|---|---|---|---|---|---|
| wave 1 | 22 | 44 | 42.2 min | 1,977 | 6.95M |
| origo | 1 | 2 | 20.8 min | 105 | 0.33M |
| auth | 1 | 2 | 13.6 min | 63 | 0.27M |
| platform | 3 | 6 | 25.4 min | 292 | 1.03M |
| lux | 2 | 4 | 23.3 min | 185 | 0.66M |

What the agents reported, summed:

- 2,406 files moved off `encoding/json`.
- Call sites: 3,138 `Unmarshal`, 1,789 `Marshal`, 2,125 raw values, 465
  one-shot request decodes, 304 response encodes.
- 496 pins (an option that keeps v1's bytes or leniency), each with a
  test; 197 accepted changes, each named; 176 new tests.
- Reviewers: 136 findings, 107 fixed by the reviewers and 1 by the
  coordinator. The largest class was lenient input
  made strict by accident (a decode of foreign data that now refused
  duplicate names or invalid UTF-8).

### What no single agent could see

Reading all the reports together turned up four problems that crossed
repositories:

1. **A pointer to `time.Time`.** The tool's first rule moved every
   pointer from `omitempty` to `omitzero`. For `*time.Time` that drops a
   non-nil pointer to the zero time, which v1 kept; `omitempty` is exact
   there under v2. Two reviewers had noticed it locally. The tool got the
   rule and a test, and a script reverted 56 fields.
2. **Raw values.** A probe ([tools/rawprobe](tools/rawprobe)) showed that
   v2 `omitempty` on a raw value drops `null`, `{}`, `[]` and `""`, all of
   which v1 kept. `omitzero` is exact except for a non-nil empty value,
   which fails under both tags. 9 fields moved to `omitzero`.
3. **Dependency order.** A migrated module that encodes a struct from an
   unmigrated library applies v2 semantics to the library's v1-era tags.
   Agents had written shadow marshalers to compensate in four places. Those
   were removed; the libraries went first instead (step 4).
4. **The SDK's Go floor.** The public SDK supported Go 1.26 and 1.27; v2
   needs 1.27. The floor moved to 1.27, marked as breaking in its
   changelog.

## 4. Landing (20:43 to 02:10 the next day)

Order: the SDK (`latere.ai/x/sdk-go`, released v0.33.0), then the shared
library (`latere.ai/x/pkg`, released v0.112.0), then every consumer, each
raised to both releases in its own migration commit. Each landed only
through its full gate plus the whole `go test ./...` of every module.

The landing ran in three rounds plus stragglers, each repository as one
commit, `json: move to encoding/json/v2`, pushed only after its gate and the
whole `go test ./...` of every module passed.

| Round | When | Repositories pushed | Gate failures fixed before the next round |
|---|---|---|---|
| 1, three lanes in parallel | 20:43 to 21:47 | sdk-go, census, pay, managed-agents, onboarding, pkg, ci-gate, latere-cli, arca | 13 |
| 2, one at a time | 22:03 to 00:10 | latere-ai, auth, ai-as-an-infrastructure, origo, lectio, forma, insula, sermo, apps, agon | 8 |
| 3, one at a time | 00:10 to 02:10 | lux, cella, topos, replichai, examen, faber, pons, limen, gradus, platform | 3 |

The logs record 71 gate runs for the migration commits; 34 of them
failed. By cause:

| Cause | Repositories | Fix |
|---|---|---|
| map keys unsorted in a byte-compared test | examen, insula, lectio, origo, topos, auth, replichai, platform, gradus | pkg's error writer deterministic (v0.113.0); fixed-shape bodies became structs with members in sorted order; re-marshaled maps use `Deterministic`; reads compared as canonical JSON |
| linters that only know v1 (`nolintlint`, `errorlint`, `unconvert`) | sermo, forma, topos, platform, pons, replichai | removed unused directives, `errors.Is(err, io.EOF)`, dropped redundant conversions |
| Go 1.27 modernizers after the `go` directive moved | sdk-go, cella, forma, limen | applied the gate's own diff |
| license notice on a new file | agon, apps, onboarding, latere-ai | copied a sibling file's notice |
| strict reads meeting a test fake | faber, pons, gradus | the fakes send what the real servers send |
| JSON Patch `test` value compared byte for byte by Kubernetes | cella | v1's escaping toward the cluster |
| coverage under a package's floor | topos | tests for the new decode and reply paths |
| `go mod tidy` in a nested module | pkg, limen | tidied every module, not only the root |
| the machine: ports, kind host ports, the Podman disk | auth, latere-ai, cella, origo, lux | one landing at a time, a retry for suite, e2e and image failures only, 37 GB of dangling layers pruned |

Three repositories needed one more push after their migration commit:
insula dropped its copies of the app manifest types once the apps library
was released (06975a7), platform moved its lux pin to the release that
carries the migrated manifest types (cd72250), and lux was released as
v0.22.0, deployed by its own pipeline with a live check.

Other sessions kept working in the same checkouts all night. The landing
script refused to push over any commit it had not written, a rebase onto
their work was resolved twice in `go.sum` (a ci-gate bump landed first), and
a kind cluster another session started held a host port that lux's install
walk needs, which delayed the lux release by two runs.

## 5. The ban (02:15)

ci-gate v0.70.0 adds `encoding/json` to the shared `depguard` deny list,
with `encoding/json/v2` and `encoding/json/jsontext` allowed by name. A
census over every repository's `main` at release time found no v1 import
outside string literals in a linter's own test fixtures and one
`//go:build ignore` benchmark driver. Repositories take the new ci-gate
through the daily bump job, which runs each repository's whole bar on it
before it moves the pin.

