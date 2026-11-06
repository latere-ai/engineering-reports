# The migration, step by step

This is the full record behind [the post](README.md): what ran, in what
order, what failed and how it was fixed. Times are UTC, from the evening of
2026-11-05 into the morning of 2026-11-06.

A few words recur:

- **The coordinator** is the Claude Code session that ran the migration: it
  surveyed the code, wrote the tools and briefs, started the agents, read
  their reports, and committed and pushed. An engineer made the decisions
  that needed a person.
- **The checks** are each repository's full CI suite: lint, license
  headers, coverage floors, the test suite, end-to-end suites, an image
  build. Our check runner, [lateregate](https://github.com/latere-ai/ci-gate),
  runs the same checks locally and in CI, so the coordinator ran them
  before every push.
- **A pin** is an option, tag or method that keeps v1's behavior where v2's
  default would differ.

Repositories are named by their role. The data files use each repository's
directory name; [data/names.tsv](data/names.tsv) maps one to the other.

## 0. Survey and design (17:07 to 17:21)

The aim was a codebase with no `encoding/json` import left, and a
measurement of what removing it changed.

The survey found 29 repositories with Go code, 39 Go modules (not counting
test fixtures), 7,361 Go files with 1.94 million lines (tests included),
and 2,327 files importing `encoding/json`, all counted before any benchmark
was added. Every repository pins `toolchain go1.27.3`.

Two findings shaped everything after:

- On Go 1.27 the v2 engine is the default (`internal/buildcfg` turns
  `JSONv2` on), and `encoding/json` itself is built from the `v2_*.go` files
  on top of it, with v1 options. Our code was already running on the new
  engine before we changed a line. `GOEXPERIMENT=nojsonv2` still builds the
  original v1 engine, which made a three-way comparison possible.
- `json.RawMessage` is a type alias of `jsontext.Value`. A repository that
  moves to v2 still matches the exported API of one that has not, so the
  repositories looked as if they could move in any order, in parallel.
  Section 3 shows why that held for the types but not for the bytes.

Toolchain behavior was checked with probe programs rather than taken from
memory: [tools/probe](tools/probe) reproduces each case, and
[tools/probe/output.txt](tools/probe/output.txt) is its output on Go
1.27.3. The facts that became rules (all in [GUIDE.md](GUIDE.md)):

1. The `format:` struct tag option is not supported by the standard
   library on Go 1.27. A field carrying one makes `Marshal` and `Unmarshal`
   of the whole struct fail at run time.
2. `time.Duration` has no v2 representation; marshaling one is an error.
3. v1's `omitempty` decides by the Go value; v2's decides by the encoded
   JSON. `omitzero` exists in both.
4. `MarshalWrite` writes no trailing newline; `MarshalEncode` on a
   `jsontext.Encoder` does.
5. `UnmarshalRead` on an empty body fails with `io.ErrUnexpectedEOF`, not
   `io.EOF`; `UnmarshalDecode` returns exactly `io.EOF` at the end of a
   stream.
6. Call-site options do not reach a v1-style `UnmarshalJSON([]byte)`
   method.
7. `MatchCaseInsensitiveNames` also ignores `_` and `-`.

### Wire policy

One decision needed a person: how much of the old JSON to keep. The
engineer chose v2's defaults, with fields pinned:

Kept identical: which fields are omitted, names and values, durations as
integer nanoseconds, key order wherever bytes are stored, hashed, compared
or sent to model providers, `null` for nil lists sent to services we do
not own, and leniency for input we do not produce (duplicate names,
invalid UTF-8, member names in any letter case).

Accepted: nil lists become `[]` in our own responses; `<`, `>` and `&` are
no longer escaped outside HTML; map keys are unsorted in API responses;
field names match case-sensitively for our own clients; error text
changes.

## 1. Benchmarks for the status quo (17:21 to 19:00)

The coordinator started one Claude Sonnet agent per repository (23 agents;
small repositories shared one) with a single brief: write `BenchmarkJSON...`
benchmarks for the JSON hot paths, called through the functions the service
uses, at realistic sizes, and change nothing else. They returned 100
benchmark files and 350 benchmark functions (762 results with
sub-benchmarks), each with a note on the production path it stands for.
The batch took 7.2 minutes, 569 tool calls and 2.85 million tokens.

Each repository then passed its full checks before the benchmarks were
committed on their own, so the commit that adds them is the "before" tree
of every comparison. Getting them merged took longer than writing them:

| Problem | Where | Fix |
|---|---|---|
| lint findings in the new files: `go fix` modernizations, import grouping, `gofmt`, a needless loop-variable copy, `errors.Is` | 12 repositories | applied the linter's own suggested change, or a hand fix, to the benchmark files only |
| a test that forbids a fixed container registry in any file | the sandbox host | a neutral example registry in the fixtures |
| end-to-end tests flaky under load (a stub server slow to answer) | the model gateway, the command-line tool | reran them alone |
| no Docker socket for Testcontainers | every repository with database tests | pointed `DOCKER_HOST` at the Podman machine |
| kind's `load docker-image` shells out to a `docker` binary | the model gateway | a `docker` wrapper over Podman that saves images under the name a Kubernetes node resolves |
| an install test that adds an `/etc/hosts` line with `sudo` | the model gateway | it needs a password, so it ran in CI instead; every other suite passed locally |
| other work changing the same checkout | the sandbox host | ran the checks in a clean, detached checkout of `origin/main` |
| a test that scans every file for company host names, which the local run of changed packages only never reached | the model gateway | CI caught it, and the test's example issuer became `login.example.com`. Since then every push also runs each module's whole `go test ./...` |

The checks ran 45 times for 29 repositories; 17 runs failed, for the
reasons above. None failed because a benchmark measured the wrong thing:
the fixes were style, fixtures and the machine.

## 2. Measuring the status quo (19:04 to 19:42)

[METHOD.md](METHOD.md) has the method. Two builds came from clean exports
of the benchmark commits: `base` (the Go 1.27 default) and `classic`
(`GOEXPERIMENT=nojsonv2`), measured in 2 rounds of 5 runs at
`-benchtime 100ms`, alternating per module. A first pass ran every
benchmark once under both builds: 30 modules, 762 results each, no
failures.

The run took 38 minutes. Before any code changed, its result was already
the first finding: on Go 1.27 our unchanged code is much faster than on
the old engine.

| `classic` to `base` | Geometric mean | 95% interval | Faster | Slower | No significant change |
|---|---|---|---|---|---|
| time per operation | 0.720 | 0.699 to 0.742 | 405 | 61 | 296 |
| bytes per operation | 0.873 | 0.847 to 0.896 | 542 | 191 | 29 |
| allocations per operation | 0.607 | 0.576 to 0.639 | 589 | 132 | 41 |

By repository the time ratio ran from 0.48 (the command-line tool, whose
benchmarks mostly decode API answers) to about 1.0 (the company website,
6 benchmarks, none significant). The raw data and statistics are in
[data/baseline](data/baseline).

Two things in this run shaped the final one. The machine was shared (a
load average around 10 on 18 cores), and the load moved between the
rounds: in some modules every build's runs in one round were about twice
as slow as in the other. The medians held, but the unpaired test then
called many real differences "no change" (the encrypted messaging server:
14 benchmarks, all about 0.6 to 0.8, none significant). The final
measurement therefore pairs the builds run by run (METHOD.md, "Order and
noise").

## 3. Migrating (19:42 to 21:27)

### Tags first, by tool

Before any agent started, [tools/jsontags](tools/jsontags) rewrote the
struct tags of every module so that each field encodes under v2 exactly as
it did under v1 (the table is in the tool's doc comment and in the post).
The first pass covered 29 modules in about 3 minutes.

### The agents

The repositories were split into work units: one per small or medium
repository and several per large one, with no two units owning the same
directory. Each unit went to a migrating agent and a reviewing agent
(Claude Sonnet), with [GUIDE.md](GUIDE.md) as the brief. Three
repositories had a release of other work going out that day; their units
started once each release was out.

| Batch | Work units | Agents | Wall time | Tool calls | Tokens |
|---|---|---|---|---|---|
| 25 repositories | 22 | 44 | 42.2 min | 1,977 | 6.95M |
| the git host | 1 | 2 | 20.8 min | 105 | 0.33M |
| the identity provider | 1 | 2 | 13.6 min | 63 | 0.27M |
| the API platform | 3 | 6 | 25.4 min | 292 | 1.03M |
| the model gateway | 2 | 4 | 23.3 min | 185 | 0.66M |

What the agents reported, summed:

- 2,406 files moved off `encoding/json`.
- Call sites: 3,138 `Unmarshal`, 1,789 `Marshal`, 2,125 raw values, 465
  one-shot request decodes, 304 response encodes.
- 496 pins, each with a test; 197 accepted changes, each named; 176 new
  tests.
- Reviewers: 136 findings, 107 fixed by the reviewers and 1 by the
  coordinator. The largest class was lenient input made strict by accident
  (a decode of foreign data that now refused duplicate names or invalid
  UTF-8).

### What no single agent could see

Reading all the reports together turned up four problems that crossed
repositories:

1. **A pointer to `time.Time`.** The tool's first rule moved every pointer
   from `omitempty` to `omitzero`. For `*time.Time` that drops a non-nil
   pointer to the zero time, which v1 kept; `omitempty` is exact there
   under v2. Two reviewers had noticed it locally. The tool got the rule
   and a test, and a script reverted 56 fields.
2. **Raw values.** A probe ([tools/rawprobe](tools/rawprobe)) showed that
   v2's `omitempty` on a raw value drops `null`, `{}`, `[]` and `""`, all
   of which v1 kept. `omitzero` is exact except for a non-nil empty value,
   which fails under both tags. 9 fields moved to `omitzero`.
3. **Dependency order.** A migrated module that encodes a struct from an
   unmigrated library applies v2's rules to the library's v1-era tags.
   Agents had written shadow marshalers to compensate in four places. Those
   were removed, and the libraries went first instead (step 4).
4. **The SDK's Go floor.** The public SDK supported Go 1.26 and 1.27; v2
   needs 1.27. The floor moved to 1.27, marked as breaking in its
   changelog.

## 4. Checking and merging (20:43 to 02:10 the next day)

Order: the public SDK first (released as v0.33.0), then the shared library
(v0.112.0), then every consumer, each raised to both releases in its own
migration commit. Each repository merged as one commit,
`json: move to encoding/json/v2`, pushed only after its full checks and the
whole `go test ./...` of every module passed ([scripts/land.sh](scripts/land.sh)).

This ran in three rounds plus stragglers:

| Round | When | Repositories pushed | Needed another fix first |
|---|---|---|---|
| 1, three at a time | 20:43 to 21:47 | the public SDK, the dependency dashboard, the prepaid credit library, the agent sessions interface, engineer onboarding, the shared library, the CI check runner, the command-line tool, the file storage service | 13 runs |
| 2, one at a time | 22:03 to 00:10 | the company website, the identity provider, the code for a book, the git host, the document parsing service, the LLM inference engine, the app hosting service, the encrypted messaging server, the app hosting API types, the adversarial code review engine | 8 runs |
| 3, one at a time | 00:10 to 02:10 | the model gateway, the sandbox host, the agent runtime, the reproducibility auditor, the agent evaluation service, the chat web app, the credential broker, the agent oversight service, the Mac and iPhone app core, the API platform | 5 runs |

The logs keep 67 check runs for the migration commits and their
follow-ups (a later run of the same repository in the same round
overwrote some): 35 passed, 31 failed and one was stopped. The model
gateway's release ran the checks three more times, and two of those lost a
host port to another kind cluster. By cause:

| Cause | Repositories | Fix |
|---|---|---|
| map keys unsorted in a test that compares bytes | 9: agent evaluation, app hosting, document parsing, git host, agent runtime, identity provider, reproducibility auditor, API platform, Mac and iPhone app core | the shared library's error writer sorts its details again (its v0.113.0); bodies with a fixed set of members became structs with fields in sorted order; maps marshaled again use `Deterministic`; reads compared as canonical JSON |
| linters that only know v1 (`nolintlint`, `errorlint`, `unconvert`) | 6: encrypted messaging, LLM inference, agent runtime, API platform, credential broker, reproducibility auditor | removed unused directives, `errors.Is(err, io.EOF)`, dropped redundant conversions |
| Go 1.27's modernizers, once the `go` directive moved | 4: public SDK, sandbox host, LLM inference, agent oversight | applied the suggested changes |
| a license notice missing on a new file | 4: adversarial code review, app hosting API types, engineer onboarding, company website | copied a sibling file's notice |
| strict reads meeting a test fake | 3: chat web app, credential broker, Mac and iPhone app core | the fakes send what the real servers send |
| a JSON Patch `test` value compared byte for byte by Kubernetes | the sandbox host | v1's escaping toward the cluster |
| coverage under a package's floor | the agent runtime | tests for the new decode and reply paths |
| `go mod tidy` in a nested module | the shared library, agent oversight | tidied every module, not only the root |
| the machine: local ports, kind host ports, the Podman disk | 5: identity provider, company website, sandbox host, git host, model gateway | one repository at a time, one retry for test, end-to-end and image failures only, 37 GB of dangling image layers pruned |

Three repositories needed one more push after their migration commit: the
app hosting service dropped its copies of the app manifest types once the
library holding them was released, the API platform moved to the model
gateway release that carries the migrated types, and the model gateway was
released, deployed by its own pipeline and checked live.

Other work kept arriving in the same checkouts all night. The merge script
refused to push any commit it had not written, a rebase onto that work had
to be resolved twice in `go.sum`, and a kind cluster started by other work
held a host port that the model gateway's install test needs, which held
its release back by two runs.

## 5. The ban (02:15)

Version v0.70.0 of the check runner adds `encoding/json` to the `depguard`
deny list in the lint configuration every repository shares, with
`encoding/json/v2` and `encoding/json/jsontext` allowed by name (depguard
denies by prefix). A count over every repository's `main` at release time
found no v1 import outside string literals in a linter's own test fixtures
and one `//go:build ignore` benchmark driver in the model gateway, which
moved to v2 the same morning. Each repository moves to a new runner
version through a daily job that first runs that repository's full checks
on it.

## 6. Looking for map-order flakes (03:26 to 04:54)

A test that compares map-built JSON byte for byte fails at random under v2,
so it can pass the single run CI gives it and merge anyway.
[scripts/flake.sh](scripts/flake.sh) reran every package whose test files
a migration commit changed, three times each, in the clean tree of that
commit: 426 packages in 32 modules of 29 repositories. 421 passed all three
runs.

Each of the other five was run again in the tree of the migration's parent
commit with `-count=3`, and in the migrated tree as separate processes
([data/flake/triage.txt](data/flake/triage.txt)). None is a migration flake:

| Package | Why `-count=3` failed | Before the migration |
|---|---|---|
| identity provider, `internal/handler` | a fixed organization id inserted into one database per process | fails the same way |
| document parsing service, `internal/convert` | a test leaves a 2^62 address-space limit on its own process | fails the same way |
| API platform, `internal/sessions` | queries count rows an earlier repeat left in the shared database | fails the same way |
| app hosting service, `internal/api` | a delivery name grows by one byte per call and passes its 128-byte limit by the third repeat; one run also leaves about 3,670 sockets in TIME_WAIT, so a run right after another runs out of local ports | fails the same way; the same socket count |
| sandbox host, `cmd/cellad` | three repeats of a long package pass Go's 10-minute default test timeout | not needed: three separate runs pass |

All five pass as separate runs in the migrated tree, the app hosting
service's from a quiet machine. The four that cannot repeat in one process
fail the same way on the code from before the migration.
