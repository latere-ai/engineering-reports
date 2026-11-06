# Migrating 1.9 million lines of Go to encoding/json/v2: what got faster, and what broke

Go 1.27 ships `encoding/json/v2`. Your code already runs on its engine
even if it never imports the new package, because in Go 1.27 the v1
`encoding/json` package is implemented on top of the v2 engine. We wanted
to know how much that upgrade alone gains, and whether moving the code
itself to the v2 API is worth the work. To find out, we moved all of our Go
code, 29 repositories with about 1.9 million lines of Go, and measured it
with 350 benchmarks of the JSON paths our services actually run. We wrote
the benchmarks before changing any code, so that every result from before
the move pairs with one from after.

Most of the speed came with Go 1.27 itself. We compared against the old
engine, the original v1 implementation, which Go 1.27 still builds when
you set `GOEXPERIMENT=nojsonv2`. With our code unchanged, the new engine
ran our benchmarks about 1.4 times as fast as the old one. Moving the code
to the v2 API then cut the time by
a further 17%, so the same work now runs about 1.7 times as fast as it did
on the old engine.

Rewriting the calls for the new API was the easy part. The work went into
deciding which parts of the JSON our services send, store and receive were
allowed to change, because v2's defaults produce different bytes from
v1's, and into finding the tests that had passed only because v1 sorted
map keys.

The whole migration took one night, a little over nine hours from the
first survey of the code to the last repository merged. AI agents made
most of the edits, and a repository merged only after its CI checks
passed. Of the 29 repositories, 21 are open source, and their migration
commits are [linked at the end](#the-repositories).

## The results

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="figures/engines-dark.svg">
  <img alt="For each repository, three dots: the old v1 engine (Go 1.27 with GOEXPERIMENT=nojsonv2) at 1x, the new engine with our code unchanged, and the new engine after our code moved to the v2 API. Over all 762 benchmarks the second takes 0.70x the time of the first, and the third 0.58x" src="figures/engines.svg">
</picture>

Over the 762 results of our 350 benchmarks, counting sub-benchmarks, the
new engine with our code unchanged takes about 30% less time than the old
engine and makes about 40% fewer allocations. Moving our code to the v2
API takes a further 17% off the time, so in all the code needs about 42%
less time than on the old engine, which makes it 1.7 times as fast, and
its allocations stay about 40% below the old engine's. The table has the
exact figures.

| Builds compared | Time per operation | Bytes per operation | Allocations per operation | Faster, slower, unchanged (time) |
|---|---|---|---|---|
| old engine to new engine, code unchanged | 0.702 (0.682 to 0.722) | 0.873 (0.847 to 0.897) | 0.608 (0.576 to 0.640) | 563, 145, 54 |
| new engine, code unchanged to code on the v2 API | 0.831 (0.817 to 0.845) | 0.944 (0.918 to 0.969) | 0.970 (0.954 to 0.987) | 505, 76, 181 |
| old engine to code on the v2 API | 0.583 (0.560 to 0.608) | 0.824 (0.790 to 0.856) | 0.590 (0.560 to 0.620) | 577, 135, 50 |

Each ratio is the later build's value divided by the earlier build's, as
the geometric mean over all benchmarks, with its 95% interval in
parentheses. A benchmark counts as faster or slower only when a sign test
over 10 rounds, in which the builds ran back to back, says so;
[How to read the numbers](#how-to-read-the-numbers) has the details.

Because the two steps multiply, we split the gain between them on a log
scale, and on that scale about two thirds of it came with Go 1.27 and one
third with our move to the v2 API. We did not measure each cause of that
third on its own. We believe most of it comes from work that the v1 API
still asks the new engine to do and that v2 skips by default, such as
matching member names in any letter case, escaping `<`, `>` and `&`, and
sorting map keys. On Go 1.27 both APIs run on the same engine and differ
in these options, so code that keeps v1's behavior also keeps its cost.

Some of the gain comes from code that got simpler. Where the old code
wrapped bytes already in memory in a v1 `Decoder` to read one value, the
new code makes a single `Unmarshal` call. In the chat web app that change
alone made decoding a medium-size event payload 3.5 times as fast, and a
small one 2.8 times as fast, while the decode still matches member names in
any letter case as before.

Keeping v1's bytes has a price, though. The benchmarks that got slower,
about one in ten, cluster where we chose to keep v1's output (step 1 of
[How we did it](#how-we-did-it) says where), and
[Where it got slower](#where-it-got-slower) names three causes.

To measure all this, we built the same benchmarks three ways: the old
engine (Go 1.27 with `GOEXPERIMENT=nojsonv2`), the new engine with our code
unchanged, and the new engine after our code moved to the v2 API. The 350
benchmark functions give 762 results with their sub-benchmarks, and the
figures label the old engine the classic engine. All of
them ran on one machine, an Apple M5 Pro with 18 cores and Go 1.27.3. Each
benchmark binary was built once per build, and in each of 10 rounds the
three builds ran one right after another, in an order that rotated from
round to round, so a busy minute on the machine slowed all three alike
instead of skewing one comparison.
[METHOD.md](METHOD.md) has the details, [data/](data) every raw `go test`
line, and [data/final/stats.json](data/final/stats.json) every
per-benchmark statistic.

## What we moved

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="figures/size-dark.svg">
  <img alt="Two bar charts side by side, one row per repository: lines of Go, and files that imported encoding/json, before the migration" src="figures/size.svg">
</picture>

The code is what a company building a platform for AI agents runs. It
includes an API platform, an agent runtime, a sandbox host on Kubernetes,
a model gateway, an identity provider, a git host, a public SDK, a
command-line tool, the Go core of a Mac and iPhone app, a shared library
they all use, and more than a dozen smaller services and libraries. In
all, that is 29 repositories and 39 Go modules with 1.9 million lines of
Go, tests included, and 2,327 files that imported `encoding/json`. The
largest repository has 277,000 lines of Go and the smallest about 700.

JSON is everywhere in this code, in HTTP APIs, events, documents stored in
Postgres, requests to model providers and the streams they answer with,
and JSON-RPC frames. Across all of it, the migration counted these call
sites and values:

| What the migration changed | Count |
|---|---:|
| `Unmarshal` calls | 3,138 |
| `Marshal` calls | 1,789 |
| raw JSON values (`json.RawMessage`) | 2,125 |
| one-shot request decodes | 465 |
| response encodes | 304 |

## How we did it

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="figures/timeline-dark.svg">
  <img alt="Timeline of the migration: one bar per step on an axis of hours since it began, from the survey to the final measurement, 10 hours in all" src="figures/timeline.svg">
</picture>

The migration was run by one engineer, Changkun Ou, who wrote this post,
and by one Claude Code session that directed the agents. The session
surveyed the code and wrote the tools, the task description for the
benchmark agents and the migration guide. It then handed each repository,
or each directory of a large one, to two Claude Sonnet agents, one to
migrate the code and one to review the result. Changkun made the decisions
that needed a person, chiefly which v1 behaviors to keep. Nothing merged
without passing the repository's full CI checks.

### 1. Decide which v1 behaviors to keep

v2 differs from v1 in more than its API, because its defaults are stricter
and its output is different. A nil slice encodes as `[]` instead of
`null`, map keys come out unsorted, `<`, `>` and `&` are not escaped,
field names match case-sensitively, and invalid UTF-8 is an error. Each of
these defaults is right for new code, and each can break something that
depends on the old bytes. For example, a client that stores a hash of a
response body sees the hash change when the same map comes back with its
keys in a different order, and some model providers reject `[]` where they
accepted `null` (OpenAI rejects `"tools": []`). So before any code moved,
we wrote down one policy for the JSON our services send, store and
receive, and held every change to it:

| Kept exactly as before | Accepted as v2's default |
|---|---|
| which fields are omitted, names and values | `[]` for a nil list in our own API responses |
| `time.Duration` as integer nanoseconds | no HTML escaping outside HTML |
| key order wherever bytes are stored, hashed, compared or sent to a model provider | unsorted map keys in API responses |
| `null` for a nil list sent to a service we don't own | case-sensitive names for our own clients |
| lenient input from anything we don't produce: duplicate names, invalid UTF-8, any letter case | new error text |

To keep a behavior, the code passes an explicit option at the call site
that holds v1's behavior there. Each package names its option sets once,
after where the JSON comes from or goes, and a test fails if one of those
options is lost. In a package that reads JSON from other systems and
stores records of its own, that looks like this:

```go
// foreignJSON reads what another system wrote, as leniently as v1 did.
var foreignJSON = json.JoinOptions(
	jsontext.AllowDuplicateNames(true),
	jsontext.AllowInvalidUTF8(true),
	json.MatchCaseInsensitiveNames(true),
)

// storedJSON writes a record whose bytes must be the same for the same value.
var storedJSON = json.JoinOptions(json.Deterministic(true), jsontext.AllowInvalidUTF8(true))

err := json.Unmarshal(body, &event, foreignJSON)
```

### 2. Benchmarks before code

We wrote the benchmarks and committed them on their own before changing
any code, so that the before and after comparison measures the migration
and nothing else. Agents, one per repository with small repositories
sharing one, added `BenchmarkJSON...` functions for each service's hot
paths, 350 functions in 100 files. Each benchmark calls the functions the
service actually uses, with data of realistic size. The migration kept
every benchmark's name, so each result from before pairs with exactly one
from after.

### 3. Test the toolchain's behavior with small programs

Every rule in the migration guide depends on how Go 1.27.3 actually
behaves, so we checked each behavior with a small probe program
([tools/probe](tools/probe), [tools/rawprobe](tools/rawprobe)) instead of
relying on memory. Some results were not what we expected:

- The `format:` struct tag option is not in the standard library's v2, and
  a field that carries one makes the whole struct fail to encode at run
  time.
- `time.Duration` has no default representation in v2, so encoding one is
  an error. Because `go vet` reports neither this nor a `format:` option,
  both problems appear only when the code runs; we asked for a check in
  [golang/go#82895](https://github.com/golang/go/issues/82895).
- v1's `omitempty` decides by the Go value (false, 0, nil, empty), while
  v2's decides by the JSON it would write (`null`, `""`, `{}`, `[]`). The
  same tag therefore omits different fields. A `false` bool or a `0` int
  tagged `omitempty`, for example, was omitted by v1 and is written by v2.
- For a raw JSON value, v2's `omitempty` drops `null`, `{}`, `[]` and
  `""`, all of which v1 kept, while `omitzero` behaves as v1 did. A
  non-nil, zero-length raw value is an error under either tag.
- `UnmarshalRead` of an empty body returns `io.ErrUnexpectedEOF`, not
  `io.EOF`, so code that treated `io.EOF` as an empty body needs another
  check. `MarshalWrite` writes no trailing newline, unlike v1's
  `Encoder.Encode`.
- Options passed at a call site don't reach a v1-style `UnmarshalJSON`
  method, because the method's own inner `Unmarshal` call runs with v2's
  defaults.

### 4. Make the tag changes mechanical

The `omitempty` difference touches thousands of struct fields, and the
right fix depends on each field's type, which makes it the kind of change
a person or a model gets subtly wrong. So a small tool
([tools/jsontags](tools/jsontags)) type-checks each module and rewrites
every tag, by the field's type, to the tag that encodes the same under v2:

| Field | Tag change |
|---|---|
| bool, number, interface, any other pointer | `omitempty` to `omitzero` |
| pointer to `time.Time` | unchanged |
| raw value | `omitempty` to `omitzero` |
| string with a custom marshaler | `omitempty` to `omitzero` |
| struct value | `omitempty` removed (v1 never omitted one) |
| plain string, slice, map, array | unchanged |

```go
// before                                  // after, the same bytes under v2
Retries int       `json:"retries,omitempty"`  Retries int       `json:"retries,omitzero"`
Meta    Meta      `json:"meta,omitempty"`     Meta    Meta      `json:"meta"`
Tags    []string  `json:"tags,omitempty"`     Tags    []string  `json:"tags,omitempty"`
```

The tool ran first in every repository, so the agents started from tags
that were already right.

### 5. One agent migrates, a second one reviews

Each repository, or each directory of a large one, went to a pair of
agents:

1. A migrating agent, which followed the [migration guide](GUIDE.md). It
   rewrote imports and calls, applied the policy above at each call site,
   added a test for every option that keeps v1's behavior, and reported
   each such option, each accepted change and each open question in a
   fixed form.
2. A reviewing agent, which read the whole diff looking for
   [11 kinds of mistake](GUIDE.md#the-reviewers-checklist) and fixed what
   it found. Examples are a lost option that kept v1's behavior, lenient
   input made strict, a raw value newly omitted, and a test weakened until
   it passed.

| Step | Agents | Wall time | Tokens |
|---|---|---|---|
| write the benchmarks | 23 | 7 min | 2.9M |
| migrate and review 25 repositories | 44 | 42 min | 7.0M |
| migrate and review three more, once their other releases were out | 10 | 14 to 25 min | 1.6M |
| migrate and review the model gateway | 4 | 23 min | 0.7M |

The reviewers found 136 problems, fixed 107 and left 28, each with a
reason, and the session fixed one more when it read all the reports
together. Most findings were about lenient input, where a decode of data
from another system had become strict about duplicate names or invalid
UTF-8 and would have refused input that v1 accepted.

### 6. Merge only through CI

Each repository merged as one commit, `json: move to encoding/json/v2`,
after its full CI checks passed: lint, license headers, coverage floors,
the whole test suite, end-to-end suites against real Postgres and kind
(Kubernetes in Docker) clusters, and an image build. The checks failed
often, and no repository was pushed until they passed. Of the 71 check
runs the logs keep for the migration commits and their follow-ups, 33 did
not pass (32 failed and one was stopped). Most of those failures came from
the code or its tests and were fixed there, while the ones the machine
caused, such as a port another process held or a full Podman disk, went
away once the repositories ran one at a time ([PROCESS.md](PROCESS.md)
tables the causes). Other work kept arriving in
the same repositories through the night, so the merge script
([scripts/land.sh](scripts/land.sh)) rebased onto it and never pushed a
commit it had not written.

### 7. Ban the v1 import

The last step was a lint rule that fails CI for any Go file that imports
`encoding/json`, with a message that names the replacement, so that new
code starts on v2. In a golangci-lint v2 configuration with `depguard`
enabled, the rule looks like this:

```yaml
linters:
  settings:
    depguard:
      rules:
        stdlib:
          list-mode: lax
          files: [$all]
          allow:  # depguard denies by prefix, so name the successors
            - encoding/json/v2
            - encoding/json/jsontext
          deny:
            - pkg: encoding/json
              desc: use encoding/json/v2 and encoding/json/jsontext; RawMessage is jsontext.Value
```

## What surprised us

**The order of the repositories mattered, even though the types matched.**
`json.RawMessage` is an alias of `jsontext.Value`, so a migrated library
still matches an unmigrated consumer's types, and the repositories looked
as if they could move in any order. The JSON they produced showed
otherwise. A
migrated service that encodes a struct from an unmigrated library applies
v2's `omitempty` to the library's v1-era tags, so fields that v1 omitted,
such as `false` and `0`, appear in the output. Agents saw this inside
their own repositories and wrote extra `MarshalJSON` methods on the
library's types to compensate. We removed those and went in dependency
order instead. The SDK and the shared library were migrated and released
first, and then each consumer was raised to the new versions in the same
commit as its own migration. That order had to cover every module that
another module imports, not only the ones we think of as libraries. A
third such module, which holds the manifest types of our app hosting
service, had been migrated but not released, and one consumer kept copies
of its types until we released it.

**Map order was the biggest class of test failures.** v1 sorts map keys
and v2 doesn't, so every test that compared a body built from a map byte
for byte started to fail at random. These included golden files, tests
that decode a map and marshal it again, and security tests asserting that
a refused request gets exactly the same response as a request for
something that doesn't exist. Those security tests compare bytes by
design. We fixed each kind of failure at its source rather than test by
test. The shared error writer
sorts the details list in an error body again, which costs little because
error bodies are a cold path. Response shapes that are fixed became
structs, whose fields always encode in the same order. Tests that encode a
map again ask for `json.Deterministic(true)`.

A map with a few keys often comes out in v1's order by chance, so such a
test passes some runs and fails others, and one passing run proves
nothing. After the migration we therefore ran, three times each, every
test package whose tests the migration had changed. Of 426 packages, 421
passed every
run, and none of the other five fails because of the migration. Four of
them fail the same way on the code from before the migration, because they
cannot run twice in one process; one, for example, inserts a fixed
organization id into its database. The fifth only runs past Go's 10-minute
test timeout when repeated. All five pass when each run is a separate
process.

**Escaping is part of the bytes a server compares.** v2 doesn't escape
`<`, `>` and `&`, which is harmless for anything that parses JSON, and we
had accepted it. Then one case in the sandbox host's end-to-end suite
failed on every run. In that case, a sandbox whose command held a shell
redirection could not be started again after a failure. The host guards
each write of a sandbox's record with a JSON Patch `test` operation that
holds the record as the host read it. The Kubernetes API server, whose
encoder still escapes the v1 way, compares that value byte for byte with
its own encoding. `"a > b"` and `"a \u003e b"` are the same string but
different bytes, so the guard never passed and the write was retried
until it gave up. JSON sent to the cluster now asks for v1's escaping, and
a test reproduces the failure on the fake clientset. Only one case in the
whole suite had a `>` in it, which is why only that case failed.

```go
// outboundJSON writes what the cluster's API reads, escaped as the API
// server's own encoder escapes, so a patch's test operation matches.
var outboundJSON = json.JoinOptions(
	json.Deterministic(true),
	json.FormatNilSliceAsNull(true),
	json.FormatNilMapAsNull(true),
	jsontext.AllowInvalidUTF8(true),
	jsontext.EscapeForHTML(true),
	jsontext.EscapeForJS(true),
)
```

**Strict reads changed what our tests saw.** Once the SDK and the shared
library read with v2's defaults, the tests of three repositories failed.
Two of them had test fakes, stub servers that sent what no server of ours
sends. One fixture wrote `created_at` twice, and a fake authorizer
answered with an untagged Go struct holding a `time.Duration`. v1 had read
both without complaint. The third was a test request that repeated a
member to check which copy a security check reads. That request is now
refused as malformed one layer earlier, before the check runs. We fixed
the fakes, let that test expect the earlier refusal, and kept strict reads
for the JSON our own services exchange.

**Case-sensitive names hid a silent failure in tests.** Some tests decoded
responses into untagged structs. v1 matched the field `Error` to `"error"`
without a tag, but v2 doesn't, so the field stayed empty and the test kept
passing while checking nothing. The reviewers added the tags.

**Two tag rules were wrong at first.** The tool first treated a pointer to
`time.Time` like any other pointer and moved it to `omitzero`. That drops
a non-nil pointer to the zero time, which v1 kept, and two reviewers
noticed it in their own repositories. The tool also left raw values on
`omitempty`, which under v2 drops a `null` or `{}` that v1 kept, as a
probe program ([tools/rawprobe](tools/rawprobe)) showed. Both rules were
fixed in the tool, and a script corrected 65 fields across the
repositories.

**Linters that only know v1 broke or went blind.** `errchkjson` and
`gosec`'s secret-marshaling check don't recognize v2 calls, so they no
longer check those calls, and the `//nolint` directives written for them became
unused and failed `nolintlint`. `errorlint` now flags `err != io.EOF`
after `jsontext.Decoder.ReadToken`, although it accepted the same
comparison after v1's `Decoder`. We reported all three:
[breml/errchkjson#90](https://github.com/breml/errchkjson/issues/90),
[securego/gosec#1820](https://github.com/securego/gosec/issues/1820) and
[polyfloyd/go-errorlint#117](https://codeberg.org/polyfloyd/go-errorlint/issues/117).
Raising the `go` directive to 1.27 also turned on Go 1.27's own
modernizers, the `go fix` rewrites of old idioms, in every file we
touched.

**A library that supports older Go releases has to drop the older ones.**
Our public SDK supported the two newest Go releases, 1.26 and 1.27.
Because `encoding/json/v2` exists only from Go 1.27, the SDK now requires
Go 1.27, and we released that as a breaking change.

## The numbers in detail

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="figures/per-repo-dark.svg">
  <img alt="For each repository, time per operation after the migration to the v2 API divided by time before it, with its 95% interval and its number of benchmarks; 0.83x over all 762" src="figures/per-repo.svg">
</picture>

Moving to the v2 API made 25 of the 29 repositories faster. We count a
repository as faster only when the whole 95% interval of the geometric
mean of its time ratios is below 1, so that the claim does not hinge on
which benchmarks the repository happens to have. The largest gains are in
the services whose hot paths carry model and tool traffic. The agent
runtime's benchmarks take nearly 40% less time than before, and those of
the credential broker and the chat web app about 30% less. Three small
repositories with six benchmarks each show no clear change, because the
95% interval of each still includes a ratio of 1, which means no change.
They are the company website, the code for a book and the prepaid credit
library, whose benchmarks take 1%, 11% and 3% less time on average.

The sandbox host is the exception. Twelve of its benchmarks got faster and
twelve slower, and overall it takes about 6% more time than before,
although its 95% interval also includes no change. To keep durations as integer
nanoseconds in the messages the host exchanges with its workers, the
migration wrapped two message types in v1-style `MarshalJSON` methods, and
encoding its state messages then took about 1.9 times as long as before.
A v1-style method returns bytes, which the v2 encoder must check and copy,
while v2's own `MarshalJSONTo` writes straight into the caller's encoder.
When we gave the state type a `MarshalJSONTo` method in a scratch copy of
the code, encoding took 1.26 times as long as before the migration instead
of 1.9 times. The shipped code still has the v1-style method, which
accounts for most of the slowdown; we have not traced the rest.

```go
// v1 style: returns bytes, which the v2 encoder then validates and copies.
func (s State) MarshalJSON() ([]byte, error) {
	type alias State
	return json.Marshal(struct {
		alias
		AutoStop int64 `json:"autoStop,omitzero"`
	}{alias(s), int64(s.AutoStop)})
}

// v2 style: writes straight into the caller's encoder.
func (s State) MarshalJSONTo(enc *jsontext.Encoder) error {
	type alias State
	return json.MarshalEncode(enc, struct {
		alias
		AutoStop int64 `json:"autoStop,omitzero"`
	}{alias(s), int64(s.AutoStop)})
}
```

In all, the migration added v1-style methods in three files, two of them
for the message types above. Everywhere else the agents wrote methods of
v2's kind, 32 of them.

### Where it got slower

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="figures/distribution-dark.svg">
  <img alt="Three histograms of every benchmark's ratio after the migration to the v2 API divided by before it: time, bytes and allocations per operation" src="figures/distribution.svg">
</picture>

The median benchmark takes about 14% less time after the move than
before, and the middle 80% of benchmarks range from nearly 40% less time
to 2% more. The slower benchmarks cluster where the code keeps v1's bytes
or behavior and pays for it, in three kinds of place. The first is sorted
map keys in a file that is stored and compared, such as a cache file whose
save now takes about 1.4 times as long. The second is the v1-style methods
above. The third is a `jsontext.Decoder` built for each small body. One
service builds that decoder because it reads only the first value of a
body, which `Unmarshal` cannot do. Another builds it only to refuse text
after the value, which v2's `Unmarshal` already does on its own, and that
request decode takes about 1.7 times as long as before:

```go
// Slower: a Decoder per request body, only to refuse trailing text.
dec := jsontext.NewDecoder(bytes.NewReader(body))
err := json.UnmarshalDecode(dec, &req)
// then check that dec has nothing left

// Faster, and the same check: Unmarshal refuses text after the value.
err = json.Unmarshal(body, &req)
```

Each of these slowdowns shows in a benchmark what keeping some piece of
v1's bytes or behavior costs, so we can now decide, case by case, whether
that piece is worth paying for.

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="figures/memory-dark.svg">
  <img alt="For each repository, bytes and allocations per operation after the migration to the v2 API divided by before it, in two panels" src="figures/memory.svg">
</picture>

Memory moved less than time, because most of the allocation savings had
already come with the new engine, which alone made about 40% fewer
allocations than the old one. Moving our code to the v2 API saved a
further 6% of the bytes and 3% of the allocations per operation, with the
intervals in [the table above](#the-results). Apart from the command-line
tool, the API platform and the git host saved the most bytes, about 30%
and 20%. The command-line tool is the outlier, with about 40% fewer bytes
but over a third more allocations.

## If you are planning your migration

1. **Upgrade to Go 1.27 first, and measure.** Two thirds of our gain came
   from the toolchain, with no code change. If JSON is a small share of
   your service's time, that may be all you need for now.
2. **Decide which v1 behaviors to keep before you change code.** List
   where bytes are stored, hashed, compared or sent to someone else's
   server, because a changed byte there can break a reader outside your
   own code. Keep v1's behavior in those places with named option sets
   and a test for each.
3. **Fix `omitempty` by type, with a tool.** The same tag omits different
   fields under v2, and thousands of fields is too many to do by hand.
4. **Go in dependency order.** Migrate and release every module that other
   modules import before its consumers, because until then a migrated
   consumer writes the library's types with v2's rules.
5. **Expect map order in your tests, and rerun them.** v2 doesn't sort map
   keys, and one passing run proves nothing about a random order.
6. **Look for other systems that compare bytes,** such as the Kubernetes
   API server's JSON Patch `test` operation, caches keyed by a body, and
   signatures over JSON. Those need v1's escaping or sorted keys, because
   the same value in different bytes no longer matches.
7. **Write custom marshalers the v2 way.** Use `MarshalJSONTo` and
   `UnmarshalJSONFrom` instead of v1-style methods, because they work on
   the caller's encoder and decoder and receive its options. Call
   `Unmarshal` instead of building a `Decoder` for bytes already in
   memory.
8. **Ban the v1 import when you're done,** so new code starts on v2.

## Looking back

For us the migration was worth it, although most of the speed came from Go
1.27 itself and the move to the v2 API supplied the remaining third of the
gain, a further 17% off the time. Besides speed, the move gave us JSON
behavior that is written down. Every place where our bytes must match
v1's now says so at the call site, with a test, instead of depending on a
default nobody chose. Most of the work went into that policy, the order
of the libraries, and the tests in nine repositories that had depended on
v1's sorted maps without saying so, and little of it into the new API
itself. The v1-style
methods in three files and one needless `Decoder` are still to be
converted, and the benchmarks will show what converting them is worth.

If you are planning a migration of your own, ran into something we did
not, or think we got a number wrong, we would like to hear about it. Write
to Changkun at `changkun at latere dot ai`, or find Changkun on the
[Gophers Slack](https://gophers.slack.com).

## The repositories

| Repository | Lines of Go before | Files importing `encoding/json` before | Migration commit |
|---|---:|---:|---|
| API platform (private) | 276,829 | 334 | private |
| agent runtime ([topos](https://github.com/latere-ai/topos)) | 204,682 | 261 | [ebc9718](https://github.com/latere-ai/topos/commit/ebc9718ea2c3c701617a3cdba26fa43df83630e5) |
| sandbox host ([cella](https://github.com/latere-ai/cella)) | 202,762 | 202 | [bbd93c1](https://github.com/latere-ai/cella/commit/bbd93c10bb39dcf1ff475111399649906d5e1c60) |
| model gateway ([lux](https://github.com/latere-ai/lux)) | 118,585 | 165 | [e424ca9](https://github.com/latere-ai/lux/commit/e424ca9f89c69188c5840de7177fa5b96c812bf1) |
| shared library ([pkg](https://github.com/latere-ai/pkg)) | 105,870 | 141 | [92321b8](https://github.com/latere-ai/pkg/commit/92321b8673ac8ef8fa5d380f9fd576a9a6a84d2e) |
| reproducibility auditor ([replichai](https://github.com/latere-ai/replichai)) | 96,144 | 138 | [a8610d8](https://github.com/latere-ai/replichai/commit/a8610d82bdace7091ee674b1058355cb7855d8b6) |
| app hosting service (private) | 84,847 | 113 | private |
| file storage service ([arca](https://github.com/latere-ai/arca)) | 81,798 | 47 | [ab4f6f0](https://github.com/latere-ai/arca/commit/ab4f6f0092d9a99fc52a9bcf23a28b3a38eaca35) |
| document parsing service ([lectio](https://github.com/latere-ai/lectio)) | 80,092 | 78 | [2080b21](https://github.com/latere-ai/lectio/commit/2080b21913c85d85146554f6f07d24171a784a40) |
| git host ([origo](https://github.com/latere-ai/origo)) | 79,313 | 86 | [481a0d3](https://github.com/latere-ai/origo/commit/481a0d3dc728144afa47fd45b275e09d40497b8d) |
| identity provider (private) | 74,208 | 128 | private |
| public Go SDK ([sdk-go](https://github.com/latere-ai/sdk-go)) | 73,227 | 130 | [7f0e0a6](https://github.com/latere-ai/sdk-go/commit/7f0e0a625ef9914e338a5196f709d5f75d224af5) |
| LLM inference engine ([forma](https://github.com/latere-ai/forma)) | 67,153 | 50 | [becdc84](https://github.com/latere-ai/forma/commit/becdc8473804188751657a522d02ef4e5bfa2e33) |
| Mac and iPhone app core (private) | 60,110 | 55 | private |
| agent evaluation service ([examen](https://github.com/latere-ai/examen)) | 56,965 | 90 | [d5a9827](https://github.com/latere-ai/examen/commit/d5a98273f8fa783f51c1e809357d9fef98f69e07) |
| command-line tool ([latere-cli](https://github.com/latere-ai/latere-cli)) | 55,487 | 88 | [00ffaf0](https://github.com/latere-ai/latere-cli/commit/00ffaf04cdcc58850be27a9c90866d6b5a16c6a7) |
| CI quality gate ([ci-gate](https://github.com/latere-ai/ci-gate)) | 44,994 | 19 | [6cdcd79](https://github.com/latere-ai/ci-gate/commit/6cdcd79a2e69c32b764a12688944145c4368c185) |
| chat web app (private) | 39,354 | 47 | private |
| credential broker ([pons](https://github.com/latere-ai/pons)) | 38,375 | 37 | [d249a6f](https://github.com/latere-ai/pons/commit/d249a6fc121bd18c098f35012382ca60a38a0bcf) |
| agent oversight service ([limen](https://github.com/latere-ai/limen)) | 32,558 | 39 | [782e890](https://github.com/latere-ai/limen/commit/782e89041653aa257e5125091b4a35fc21103bc1) |
| encrypted messaging ([sermo](https://github.com/latere-ai/sermo)) | 13,658 | 9 | [50eb374](https://github.com/latere-ai/sermo/commit/50eb37408df5e9c4098e27beb9999ca0fd3bdd20) |
| company website (private) | 11,820 | 7 | private |
| adversarial code review ([agon](https://github.com/latere-ai/agon)) | 10,609 | 15 | [3a523a0](https://github.com/latere-ai/agon/commit/3a523a0087ccce368420371a6fb02f2e2c489abb) |
| dependency dashboard (private) | 10,173 | 14 | private |
| prepaid credit library ([pay](https://github.com/latere-ai/pay)) | 7,915 | 5 | [0075461](https://github.com/latere-ai/pay/commit/00754619ca06a5134ffc5c6083bbeaab43aeaeba) |
| agent sessions interface ([managed-agents](https://github.com/latere-ai/managed-agents)) | 7,563 | 19 | [33131b5](https://github.com/latere-ai/managed-agents/commit/33131b5a4790df3f3fbf397933f5a15cc5d3479b) |
| code for a book ([ai-as-an-infrastructure](https://github.com/latere-ai/ai-as-an-infrastructure)) | 4,397 | 4 | [a0b02a2](https://github.com/latere-ai/ai-as-an-infrastructure/commit/a0b02a2bce6697c9de555c1ccaf86870a5ce75e7) |
| app hosting API types ([apps](https://github.com/latere-ai/apps)) | 2,837 | 4 | [8bbbf70](https://github.com/latere-ai/apps/commit/8bbbf708b9719c89725c20050743d60927cf924e) |
| engineer onboarding (private) | 709 | 2 | private |

Private repositories are counted in every figure, but their code is not
published.

## Method and data

### How to read the numbers

- The data files call the three builds `classic` (the old engine, Go 1.27
  with `GOEXPERIMENT=nojsonv2`), `base` (the new engine with our code
  unchanged) and `after` (the new engine after the code moved to the v2
  API), and the figures call the old engine the classic engine.
- A ratio is the later build's value divided by the earlier build's. We
  compare `classic` to `base`, `base` to `after`, and `classic` to
  `after`. A time ratio of 0.70 means 70% of the time, which is 30% less
  time, or about 1.4 times as fast.
- Each benchmark's ratio is the median of its 10 per-round ratios. A
  benchmark counts as faster or slower only if an exact sign test over the
  10 rounds says so at the 5% level (at least 9 of 10 rounds in the same
  direction).
- A repository's or the whole set's figure is the geometric mean of its
  benchmarks' ratios, with a 95% bootstrap interval over the benchmarks.
- Benchmarks measure JSON in isolation. A handler that spends most of its
  time in a database sees a smaller share.

### Reproduce it

The scripts in [scripts/](scripts) run against checkouts of the
repositories (`ROOT` names the directory that holds them); 21 of the 29 are
public. The raw results for all 29 are in [data/](data). The scripts are
zsh scripts for macOS. `scripts/land.sh` is published as a record of what
ran: it needs input files that are not in `data/`, so you cannot run it as
is.

1. `scripts/final-shas.sh` finds each repository's migration commit and its
   parent ([data/shas-before.txt](data/shas-before.txt),
   [data/shas-after.txt](data/shas-after.txt)).
2. `scripts/trees.sh trees/before data/shas-before.txt` and the same for
   `after` export clean trees of both.
3. `scripts/bench-paired.sh out 10 100ms base=trees/before
   classic=trees/before:nojsonv2 after=trees/after` builds and runs.
4. `go -C tools/analyze run . -paired -pairs classic:base,base:after,classic:after
   -out ../../stats.json base=../../out/base classic=../../out/classic
   after=../../out/after` computes the statistics (each tool is its own Go
   module, so `go -C` runs it from its directory; the paths resolve from
   there; point the arguments at `data/final/base` and so on to recompute
   our published statistics).
5. `uv run figures/render.py` draws the figures from `data/` with
   matplotlib and seaborn ([uv](https://docs.astral.sh/uv/) installs the
   pinned versions). It also measures every drawn label against every
   line, dot, bar and other label, and fails if any two touch.
6. `scripts/flake.sh trees/after data/shas-after.txt flake` reruns every
   test package the migration changed three times
   ([data/flake/](data/flake)).

[PROCESS.md](PROCESS.md) is the full record: every step, in order, with
every CI failure and its fix.
