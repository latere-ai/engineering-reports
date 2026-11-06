# Moving 29 repositories to encoding/json/v2 in a day

Go 1.27 shipped `encoding/json/v2`. At Latere AI we moved every Go
repository we have to it: 29 repositories, 39 modules, 1.94 million lines
of Go and 2,327 files that imported `encoding/json`. We measured before
and after, with 350 benchmarks of our real JSON paths. The whole move,
from the first survey to the last repository on `main`, took a little over nine hours, overnight.

This is how we did it, what it bought us, and what surprised us on the
way.

## The results first

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="figures/engines-dark.svg">
  <img alt="Geometric mean time per operation of 762 JSON benchmarks under three builds: the classic v1 engine (GOEXPERIMENT=nojsonv2), Go 1.27's default, and our code on the v2 API" src="figures/engines.svg">
</picture>

- **The toolchain alone made our JSON code 1.4× faster.** In Go 1.27 the
  v1 package runs on the v2 engine by default. With no code change, the
  geometric mean time of our 762 benchmark results fell to 70% of what it
  was on the old engine (`GOEXPERIMENT=nojsonv2`), and allocations fell to
  61%.
- **Moving the code to the v2 API cut the time by a further 17%**
  (geometric mean 0.831, 95% interval 0.817 to 0.845): 505 benchmarks got
  faster and 76 got slower, each one significant over 10 paired rounds; 181
  did not change.
- **From the old engine to our migrated code: 58%** of the time and 59% of
  the allocations, 1.7× faster overall.

About two thirds of the gain came with Go 1.27 itself, and one third with
the move. Most of that third is work the v1 API keeps doing on the new
engine, which v2 lets a caller drop: matching member names in any letter
case, escaping `<`, `>` and `&`, sorting map keys. Code changed too: where
a v1 `Decoder` read one value from bytes already in memory, one
`Unmarshal` call does it now, and that alone made a chat service's event
decode 3.5 times faster, with its case-insensitive matching kept. Our wire
policy put some of v1's work back where the bytes matter, and that is where
the 76 slower benchmarks are (see below).

The numbers come from one machine (Apple M5 Pro, 18 cores, Go 1.27.3), with
each benchmark package's binary built once per variant and the variants
run side by side in 10 rotating rounds. [METHOD.md](METHOD.md) has the
details, [data/](data) every raw `go test` line, and
[data/final/stats.json](data/final/stats.json) every per-benchmark
statistic.

## What we moved

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="figures/size-dark.svg">
  <img alt="Lines of Go per repository, and the files in each that imported encoding/json" src="figures/size.svg">
</picture>

The repositories are services (an API platform, an identity provider, an
agent runtime, a sandbox host, a model gateway and a dozen more), the
shared libraries they all use, a public SDK, a CLI, the Go core of a Mac
app, and the code of a book. The largest has 277,000 lines of Go; the
smallest, about 700. JSON is everywhere in them: HTTP APIs, events, documents
stored in Postgres, model-provider requests and streams, JSON-RPC frames.

Across them the agents that did the work counted 3,138 `Unmarshal` calls,
1,789 `Marshal` calls, 2,125 raw JSON values (`json.RawMessage`), 465
one-shot request decodes and 304 response encodes.

## How we did it

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="figures/timeline-dark.svg">
  <img alt="Timeline of the migration, from the request to the final measurement" src="figures/timeline.svg">
</picture>

### 1. Decide what the wire may change

v2 differs from v1 in more than its API. Its defaults are stricter and its
output differs: nil slices encode as `[]`, map keys are not sorted, `<`,
`>` and `&` are not escaped, field names match case-sensitively, invalid
UTF-8 is an error. Each of those is right for new code, and each can break
a client, a stored digest or a third party. So before any code moved we
wrote down one policy, and every change was held to it:

| Kept exactly as before | Accepted as v2's default |
|---|---|
| which fields are omitted, names and values | `[]` for a nil list in our own API responses |
| `time.Duration` as integer nanoseconds | no HTML escaping outside HTML |
| key order wherever bytes are stored, hashed, compared or sent to a model provider | unsorted map keys in API responses |
| `null` for a nil list sent to a service we don't own | case-sensitive names for our own clients |
| lenient input from anything we don't produce: duplicate names, invalid UTF-8, any letter case | new error text |

"Kept" means an option at the call site (`json.Deterministic(true)`,
`json.FormatNilSliceAsNull(true)`, `jsontext.AllowInvalidUTF8(true)` and so
on), named once per package, with a test that fails if the pin is lost.

### 2. Benchmarks before code

We wrote the benchmarks before changing a line. One agent per repository
added `BenchmarkJSON...` functions for that service's hot paths, through
the functions the service actually calls, at realistic sizes. Then we
committed them on their own. The result: 100 files, 350 benchmark
functions, 762 results with sub-benchmarks. Benchmark names stay the same
through the migration, so every before result pairs with one after result.

### 3. Check the toolchain, don't remember it

Every behavior a rule depended on was checked with a probe program on Go
1.27.3 ([tools/probe](tools/probe), [tools/rawprobe](tools/rawprobe)). Some
results were not what we expected:

- The `format:` tag option is not in the standard library's v2. A field
  carrying one makes the whole struct fail to encode, at run time.
- `time.Duration` has no default v2 representation; encoding one is an
  error.
- v1's `omitempty` goes by the Go kind (false, 0, nil, empty); v2's goes by
  the JSON (`null`, `""`, `{}`, `[]`). The same tag omits different fields.
- For a raw value, v2 `omitempty` drops `null`, `{}`, `[]` and `""`, which v1
  kept. `omitzero` matches v1, except that a non-nil empty value is an error
  under both tags.
- `UnmarshalRead` of an empty body returns `io.ErrUnexpectedEOF`, not
  `io.EOF`. `MarshalWrite` writes no trailing newline.
- Options passed at a call site don't reach a v1-style `UnmarshalJSON`
  method.

### 4. Make the tag changes mechanical

The `omitempty` difference touches thousands of struct fields, and it is
the kind of change a person or a model gets subtly wrong. So a small tool
([tools/jsontags](tools/jsontags)) type-checks each module and rewrites
every tag by the field's kind, to the tag that encodes identically under
v2:

| Field | Tag change |
|---|---|
| bool, number, pointer, interface | `omitempty` to `omitzero` |
| pointer to `time.Time` | unchanged |
| raw value | `omitempty` to `omitzero` |
| string with a custom marshaler | `omitempty` to `omitzero` |
| struct value | `omitempty` removed (v1 never omitted one) |
| plain string, slice, map, array | unchanged |

The tool ran first in every repository, so the agents started from tags
that were already right.

### 5. One agent migrates, a second one reviews

We split the repositories into 29 scopes: one for each small or
medium repository, and the large ones split by directory. Each scope went
through two Sonnet agents in a workflow:

1. A migrating agent with a guide ([GUIDE.md](GUIDE.md)): rewrite imports
   and calls, apply the wire policy at each call site, add a test for every
   pin, and report every pin, accepted change and open question in a fixed
   schema.
2. A reviewing agent that read the whole diff against 11 classes of
   mistakes (a lost pin, lenient input made strict, a raw value newly
   omitted, a weakened test, and so on) and fixed what it found.

| | Scopes | Agents | Wall time | Tokens |
|---|---|---|---|---|
| benchmarks | 23 | 23 | 7 min | 2.9M |
| migration, wave 1 | 22 | 44 | 42 min | 7.0M |
| three repositories that had a release going out first | 5 | 10 | 14 to 25 min | 1.6M |
| the model gateway, after other work on it was out | 2 | 4 | 23 min | 0.7M |

The reviewers found 136 problems and fixed 107 of them; reading their
reports together, we fixed one more. The rest they judged and left, each
with a reason. Most were about lenient input: a decode of foreign data that
had become strict about duplicate names or invalid UTF-8.

### 6. Land through the gate

Each repository landed as one commit, `json: move to encoding/json/v2`,
only after its full gate passed: lint, license notices, coverage floors,
the whole test suite, end-to-end suites against real Postgres and kind
clusters, and an image build. The logs record 71 gate runs for the 29
migration commits. 34 failed, and each failure was fixed in the code or the
test before the push; the causes are tabled in [PROCESS.md](PROCESS.md).
Other work kept landing in the same repositories all night, so the landing
script rebased onto it and never pushed a commit it had not written.

### 7. Ban the v1 import

The last step was a rule in our shared gate: a Go file that imports
`encoding/json` fails the gate, with a message that names the v2
replacement. New code starts on v2.

## What surprised us

**The order of the repositories mattered, though the API said it
wouldn't.** `json.RawMessage` is an alias of `jsontext.Value`, so a
migrated library still matches an unmigrated consumer's types, and the
repositories looked like they could move in any order. The wire bytes
told a different story. A migrated service that encodes a struct from an
unmigrated library applies v2's `omitempty` to the library's v1-era tags,
and fields that used to be omitted (`false`, `0`) appear. Agents saw it
locally and wrote shadow marshalers to compensate. We removed those and
went in dependency order: the shared library and the SDK first, released,
then every consumer raised to the new versions in the same commit as its
own migration. "The libraries" turned out to mean every module that another
module imports: a third one, the manifest types of our app host, had been
migrated but not released, and one consumer kept copies of its types until
we released it.

**Map order was the biggest class of test failures.** v1 sorts map keys;
v2 doesn't. Every test that compared a map-built body byte for byte failed
at random: golden files, tests that re-marshal a decoded map, and, the
interesting ones, security tests asserting that a refused request reads
exactly like a request for something that doesn't exist. Those are
byte-equality tests by design. We fixed the rule rather than each test:
the shared error writer sorts its details again (error bodies are a cold
path), response shapes that are fixed became structs, and tests that
re-encode a map ask for `Deterministic`.

**Escaping is part of the bytes a server compares.** v2 doesn't escape
`<`, `>` and `&`, which is harmless for anything that parses JSON, and we
had accepted it. Then one case in the sandbox host's end-to-end suite failed
on every run: a sandbox whose command held a shell redirection could not be
started again after a failure. The host guards each write of a sandbox's
record with a JSON Patch `test` operation holding the record as it read it,
and the Kubernetes API server, whose encoder still escapes the v1 way,
compares that value byte for byte with its own encoding. `a > b` and
`a \u003e b` are the same string and different bytes, so the guard never
passed and the write was retried until it gave up. JSON sent to the cluster
now asks for v1's escaping (`jsontext.EscapeForHTML(true)`,
`jsontext.EscapeForJS(true)`), and a test reproduces the failure on the fake
clientset. Only one case in the whole suite had a `>` in it.

**Strict reads changed what our tests saw.** Once the SDK and the shared
library read with v2's defaults, three repositories' tests failed. Two had
fakes that sent what no server of ours sends: a fixture that wrote
`created_at` twice, and a fake authorizer that answered with an untagged Go
struct holding a `time.Duration`. v1 had read both without complaint. The
third was a test request that repeated a member to check which copy a
security check reads; the request is now refused as malformed one layer
earlier, before the check runs. We fixed the fakes, let that test expect the
earlier refusal, and kept strict reads for our own wire.

**Case-sensitive names hid a silent failure in tests.** Some tests decoded
responses into untagged structs. v1 matched `Error` to `"error"` without a
tag; v2 doesn't, so the field stayed empty and the test kept passing while
checking nothing. The reviewers tagged them.

**Two tag rules were wrong at first, and only a probe showed it.** We had
first treated a pointer to `time.Time` like any pointer and moved it to
`omitzero`. That drops a non-nil pointer to the zero time, which v1 kept.
We also left raw values on `omitempty`, which under v2 drops a `null` or
`{}` that v1 kept. The probe caught both, the tool was fixed, and a script
corrected 65 fields across the repositories.

**Linters that only know v1 went quiet.** `errchkjson` and `gosec`'s
secret-marshaling check don't recognize v2 calls, so their `//nolint`
directives became unused and failed `nolintlint`. `errorlint` now flags
`err != io.EOF` after `jsontext.Decoder.ReadToken`, which v1's `Decoder`
didn't need. Raising the `go` directive to 1.27 turned on Go 1.27's own
modernizers in files we touched.

**The public SDK needed a new Go floor.** Our SDK supported the two newest
Go releases. `encoding/json/v2` exists only from 1.27, so the floor moved to
Go 1.27, released as a breaking change with a note on when the two-release
window resumes.

**The machine was the last bottleneck.** Running gates in parallel exhausted
the ephemeral port range (12,147 sockets in `TIME_WAIT`), two kind
clusters fought over the same host port, and the Podman VM's disk filled
with 37 GB of dangling build layers. The last repositories landed one at a
time.

## The numbers in detail

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="figures/per-repo-dark.svg">
  <img alt="Geometric mean change in time per operation for each repository: toolchain only, and toolchain plus migration" src="figures/per-repo.svg">
</picture>

Every repository but one got faster. The largest gains are in the services
whose hot paths carry model and tool traffic: the agent runtime (topos)
went to 0.62 of its time, the tool gateway (pons) to 0.71, the chat backend
(faber) to 0.72. The sandbox host (cella) is the exception, at 1.06. To keep
durations as integer nanoseconds on its worker protocol, the migration
wrapped two message types in v1-style `MarshalJSON` methods. Under v2 each
such method encodes into a buffer of its own, which the encoder then checks
again, and the state messages that carry those types take twice as long.
v2's own `MarshalJSONTo`, which writes into the caller's encoder, keeps the
bytes without the second pass. The migration added v1-style methods in
three files, two of them these; everywhere else the agents wrote 32 methods
of v2's kind.

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="figures/distribution-dark.svg">
  <img alt="Distribution of per-benchmark ratios of time per operation" src="figures/distribution.svg">
</picture>

Per benchmark the median ratio is 0.86, and the middle 80% run from 0.62
to 1.02. The slower benchmarks cluster where the wire policy keeps v1's
bytes or leniency, and pays for it: sorted map keys in a file that is
stored and compared (a cache file, 1.36 times the time), the v1-style
methods above, and a `jsontext.Decoder` kept where only the first value of
the input counts, on bodies of a few hundred bytes. Each of those pins is
deliberate and has a test; the price is now measured.

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="figures/memory-dark.svg">
  <img alt="Geometric mean change in bytes and allocations per operation" src="figures/memory.svg">
</picture>

Memory moved less than time. Against Go 1.27's default, the move saved 6%
of the bytes and 3% of the allocations per operation (geometric means 0.944
and 0.970, intervals 0.918 to 0.969 and 0.954 to 0.987); most of the
allocation savings had already come with the toolchain, which alone brought
allocations to 0.61 of the old engine's. The platform and the code host
(origo) saved the most bytes. The CLI is the outlier: 0.60 of the bytes in
1.36 times as many allocations.

### How to read them

- A ratio is after / before: 0.70 means the after side takes 70% of the
  time, 30% less.
- Each benchmark's ratio is the median of its 10 per-round ratios. A
  benchmark counts as faster or slower only if an exact sign test over the
  10 rounds says so at the 5% level (at least 9 of 10 rounds in the same
  direction).
- A module's or the whole set's figure is the geometric mean of its
  benchmarks' ratios, with a 95% bootstrap interval over the benchmarks.
- Benchmarks measure JSON in isolation. A handler that spends most of its
  time in a database sees a smaller share.

## Reproduce it

Everything here can be rerun from the repositories at the recorded
commits, with the scripts in [scripts/](scripts) (`ROOT` names the directory
that holds the repositories' checkouts):

1. `scripts/final-shas.sh` finds each repository's migration commit and its
   parent ([data/shas-before.txt](data/shas-before.txt),
   [data/shas-after.txt](data/shas-after.txt)).
2. `scripts/trees.sh trees/before data/shas-before.txt` and the same for
   `after` export clean trees of both.
3. `scripts/bench-paired.sh out 10 100ms base=trees/before
   classic=trees/before:nojsonv2 after=trees/after` builds and runs.
4. `go run ./tools/analyze -paired -pairs classic:base,base:after,classic:after
   -out stats.json base=out/base classic=out/classic after=out/after`
   computes the statistics, and `go run ./tools/figures -stats stats.json
   -census data/census-pre.tsv -timeline data/timeline.tsv` draws the
   figures.

[PROCESS.md](PROCESS.md) is the full log of the day: every step, every gate
failure and its fix.
