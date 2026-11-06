# Moving Go code from encoding/json to encoding/json/v2: the rules

These are the rules our migrating and reviewing agents followed, updated
with what the migration taught us. They are written as the instructions we
gave each agent, and they work as a checklist for a person. Every fact in them
was checked on Go 1.27.3 with small probe programs ([tools/probe](tools/probe),
[tools/rawprobe](tools/rawprobe)); where they disagree with what you
remember of the v2 proposal, trust the probes.

Words used throughout:

- **We, our**: the code being migrated and the services it belongs to.
- **Services we do not own**: everyone else's APIs, and the files and
  messages they produce.
- **Pin**: an option at a call site, a struct tag or a method that keeps
  v1's behavior where v2's default would differ. Every pin gets a test that
  fails if it is lost.

On Go 1.27 the v1 package already runs on the v2 engine, with options that
reproduce v1. Moving to the v2 API therefore changes behavior, not the
engine: each difference below is a v1 behavior you either keep with a pin
or let go.

## The wire policy

Decide this before changing any code; every later rule depends on it. Ours,
which the rest of this guide assumes, keeps v2's defaults except for the
behaviors in the first list, which we pin.

Kept identical (pin it wherever v2 would differ):

- which fields are omitted (`omitempty` semantics), field names, number and
  string values, durations as integer nanoseconds;
- key order wherever bytes are stored, hashed, compared, or sent to model
  providers;
- `null` for nil slices and maps in payloads sent to services we do not own;
- leniency for input we do not produce: duplicate names, invalid UTF-8,
  member names in any letter case.

Accepted v2 changes (no pin; update the tests that assert the old bytes):

- nil slices and maps encode as `[]` and `{}` in our own API responses,
  events and stored documents;
- `<`, `>` and `&` are no longer escaped, except in JSON embedded in HTML or
  JavaScript;
- object keys from Go maps are unsorted in HTTP responses to our own
  clients;
- struct field names match case-sensitively in requests from our own
  clients;
- error message text changes, and `MarshalWrite` writes no trailing newline
  in HTTP response bodies.

If your policy differs, change this section and follow the rest with yours.

## Facts about Go 1.27.3

1. `json.RawMessage` is a type alias of `jsontext.Value`. Rename it; values
   cross module boundaries unchanged, so a migrated module's API still
   matches an unmigrated one's.
2. The `format:` struct tag option is not supported by the standard library
   on Go 1.27. A field carrying `format:...` makes `Marshal` and `Unmarshal`
   of the whole struct fail at run time. Never write `format:` tags (no
   `format:nano`, `format:emitnull`, `format:array`, `format:base64`).
3. `time.Duration` has no v2 representation: marshaling or unmarshaling one
   (a field, a slice element, a map value, or a value inside `any`) is a run
   time error. See the "Durations" paragraph in Step 3.
4. v1's `omitempty` omits false, 0, a nil pointer, a nil interface, and an
   empty string, slice, map or array, and never omits a struct value. v2's
   `omitempty` omits a field whose encoded JSON is `null`, `""`, `{}` or `[]`.
   `omitzero`, which both support, omits the Go zero value, or whatever
   `IsZero()` reports.
5. `json.MarshalWrite` writes no trailing newline. `json.MarshalEncode` on a
   `jsontext.NewEncoder(w, opts...)` writes the value and `\n`, like v1's
   `Encoder.Encode`, and indents when given `jsontext.WithIndent`.
6. `json.UnmarshalRead` reads to EOF, and text after the value is an error.
   On empty input it returns an error for which
   `errors.Is(err, io.ErrUnexpectedEOF)` is true and `errors.Is(err, io.EOF)`
   is false.
7. In a stream, `json.UnmarshalDecode(dec, &v)` returns exactly `io.EOF`
   after the last value.
8. Call-site options do not reach v1-style `UnmarshalJSON([]byte)` or
   `MarshalJSON()` methods: the method's own inner `json.Unmarshal` runs with
   v2's defaults. They do reach `UnmarshalJSONFrom(*jsontext.Decoder)` and
   `MarshalJSONTo(*jsontext.Encoder)` methods, through the decoder or
   encoder.
9. `json.MatchCaseInsensitiveNames(true)` matches case-insensitively and
   also ignores `_` and `-` (`foo_bar` matches `FooBar`), slightly more
   lenient than v1.
10. With `jsontext.AllowDuplicateNames(true)` the last duplicate wins, as in
    v1.
11. Marshaling a `jsontext.Value` validates it again: a raw value with
    duplicate names or invalid UTF-8 makes the enclosing `Marshal` fail
    unless the call passes `jsontext.AllowDuplicateNames(true)` and
    `jsontext.AllowInvalidUTF8(true)`.
12. `jsontext.WithIndent("  ")` output equals v1's
    `MarshalIndent(v, "", "  ")`, except that `<`, `>` and `&` are not
    escaped.
13. `jsontext.AllowInvalidUTF8(true)` replaces invalid bytes with U+FFFD on
    marshal and on unmarshal, as v1 did without being asked.

## Step 1: fix omitempty with the tag tool, before anything else

The tag tool is `jsontags` ([tools/jsontags](tools/jsontags)). Build it once
(it is its own module), then run it from each module's root:

    (cd tools/jsontags && go build -o "$HOME/bin/jsontags" .)
    jsontags -w -report jsontags-report.json ./...

It rewrites `omitempty` by the field's Go type, so that v2 writes what v1
wrote:

| Field type | Tag change |
|---|---|
| bool, numbers, pointers, interfaces | `omitempty` to `omitzero` |
| pointer to `time.Time` | unchanged (`omitempty` is exact under v2 here) |
| raw values (`jsontext.Value`) | `omitempty` to `omitzero` |
| string types with a custom marshaler | `omitempty` to `omitzero` |
| struct values (`time.Time` included) | `omitempty` removed (v1 never omitted them) |
| plain string, slice, map, array | unchanged (the same in both) |

The rewritten tags encode the same under v1 too, so the change is safe on
its own. The tool type-checks the module, so run it before the imports
change, and once per module: a second run would rewrite tags you have
since set on purpose. It skips generated files. Its report lists what it
leaves for you:

- `durations`: struct fields holding `time.Duration`;
- `byte_arrays`: `[N]byte` fields (v1 wrote an array of numbers, v2 a
  base64 string);
- `check`: fields where a rule by type may not be exact (type parameters,
  pointers to types with `IsZero`, slices or maps with custom marshalers);
- `untagged_samples`: exported fields without a json tag in tagged
  structs.

Don't change by hand a tag the tool has already decided.

## Step 2: imports and mechanical renames

- `import "encoding/json"` becomes `import "encoding/json/v2"` (the name
  stays `json`); add `"encoding/json/jsontext"` where used.
- `json.RawMessage` becomes `jsontext.Value` everywhere: types,
  conversions, tests.
- `json.MarshalIndent(v, "", ind)` becomes
  `json.Marshal(v, jsontext.WithIndent(ind))`; a non-empty prefix adds
  `jsontext.WithIndentPrefix(p)`.
- `json.Valid(b)` becomes `jsontext.Value(b).IsValid()`. It is stricter (it
  rejects duplicate names and invalid UTF-8); for foreign input pass the
  lenient options.
- `json.Compact(&buf, src)` becomes
  `v := jsontext.Value(bytes.Clone(src)); err := v.Compact()`, and
  `json.Indent(&buf, src, p, i)` becomes
  `v.Indent(jsontext.WithIndentPrefix(p), jsontext.WithIndent(i))`.
- A one-shot decode of a bounded body (a request body, a response body, a
  file): `json.NewDecoder(r).Decode(&v)` becomes
  `json.UnmarshalRead(r, &v, opts...)`. Never use `UnmarshalRead` on a
  long-lived stream: it blocks until EOF.
- For bytes already in memory, call `json.Unmarshal(b, &v, opts...)`. It
  already refuses text after the value; a `jsontext.Decoder` built only to
  check that costs about 1.7 times the time on a small body.
- Empty bodies: code that treated `io.EOF` from `Decode` as "empty body"
  must keep doing so. `UnmarshalRead` returns `io.ErrUnexpectedEOF` on
  empty input, so read the body first and test
  `len(bytes.TrimSpace(b)) == 0`.
- `dec.DisallowUnknownFields()` becomes the option
  `json.RejectUnknownMembers(true)`.
- One-shot encodes:
  - HTTP response bodies: `json.NewEncoder(w).Encode(v)` becomes
    `json.MarshalWrite(w, v, opts...)`.
  - Line-oriented output (CLI output, NDJSON, log lines, files people
    read, JSON-RPC over stdio): keep the newline with
    `json.MarshalEncode(jsontext.NewEncoder(w, opts...), v)`.
  - `enc.SetIndent(p, i)` becomes `jsontext.WithIndentPrefix(p),
    jsontext.WithIndent(i)` on the encoder. Delete
    `enc.SetEscapeHTML(false)`: not escaping is v2's default. A v1 encoder
    without `SetEscapeHTML(false)` escaped HTML; see "Step 3: where the bytes go".
- Streams (several values on one reader or writer: NDJSON, SSE data lines
  read in a loop, websockets, JSON-RPC):
  - `dec := json.NewDecoder(r)` becomes `dec := jsontext.NewDecoder(r,
    opts...)`, and `dec.Decode(&v)` becomes `json.UnmarshalDecode(dec, &v)`;
    the end of the stream is `err == io.EOF`.
  - `dec.Token()` becomes `dec.ReadToken()`, a `jsontext.Token`:
    `tok.Kind()` is one of `{ } [ ] " 0 t f n`, and `tok.String()` gives a
    string's value.
  - `dec.More()` becomes `dec.PeekKind() != ']'` inside an array (`'}'` in
    an object).
  - `dec.InputOffset()` stays `dec.InputOffset()`. `dec.Buffered()` has no
    equivalent; restructure the code.
  - `enc := json.NewEncoder(w)` becomes `enc := jsontext.NewEncoder(w,
    opts...)`, and `enc.Encode(v)` becomes `json.MarshalEncode(enc, v)`.
  - `*json.Decoder` and `*json.Encoder` in fields and signatures become
    `*jsontext.Decoder` and `*jsontext.Encoder`.
- Errors: `*json.SyntaxError` becomes `*jsontext.SyntacticError`
  (`ByteOffset` replaces `Offset`). `*json.UnmarshalTypeError`,
  `*json.InvalidUnmarshalError`, `*json.MarshalerError`,
  `*json.UnsupportedTypeError` and `*json.UnsupportedValueError` become
  `*json.SemanticError` (`ByteOffset`, `JSONPointer`, `JSONKind`, `GoType`,
  `Err`). `JSONPointer` replaces v1's `Field` and reads `/a/b`, not `a.b`:
  rewrite any message shown to users so it still names the field.
- `json.Marshaler` and `json.Unmarshaler` exist in v2 with the same
  methods, so existing `MarshalJSON` and `UnmarshalJSON` methods keep
  working once their bodies call v2. Prefer converting them to
  `MarshalJSONTo` and `UnmarshalJSONFrom` (see the "Durations" paragraph in Step 3 for the shape):
  call-site options reach those (fact 8), and they write into the caller's
  encoder instead of returning bytes it must validate and copy. One of our
  message types took 1.9 times as long with a v1-style method and 1.26
  times with `MarshalJSONTo`.
- `json.Number` and `dec.UseNumber()` exist only in v1. Use the value's
  real type (`int64`, `uint64`, `float64`, `string`) when the schema is
  known. Keep the raw digits as a `jsontext.Value`, parsed with `strconv`,
  when precision matters (IDs above 2^53, money, tool arguments passed
  through). Where a `map[string]any` was decoded with `UseNumber` to keep
  numbers exact, decode into `map[string]jsontext.Value` or a concrete type
  instead. Never turn a number into a `float64` where v1 code kept its exact
  digits.

## Step 3: where the bytes go

Go through every remaining call site and every type that is serialized, and
decide by where the bytes go and where they come from. Name option sets
once per package instead of repeating options:

    // foreignJSON reads JSON we do not produce: model output, provider and
    // webhook payloads, user files. v1 accepted duplicate names, invalid
    // UTF-8 and any letter case; v2 rejects them unless asked.
    var foreignJSON = json.JoinOptions(
        jsontext.AllowDuplicateNames(true),
        jsontext.AllowInvalidUTF8(true),
        json.MatchCaseInsensitiveNames(true),
    )

a. **Sent to services we do not own** (model providers, payment and mail
   APIs, Kubernetes, cloud APIs, OAuth providers): pass
   `json.Deterministic(true), json.FormatNilSliceAsNull(true),
   json.FormatNilMapAsNull(true)`, and v1's escaping where the receiver
   compares bytes (see "Learned in CI"). Some providers reject `[]` where
   they accepted `null` (OpenAI rejects `"tools": []`), and prompt caching
   needs byte-identical requests.
b. **Stored, hashed, signed, cached by content, compared byte for byte, or
   written to files that are diffed or committed**: `json.Deterministic(true)`.
   Golden tests compare semantically, or keep `Deterministic` in the test.
c. **Embedded in HTML or JavaScript** (script tags, inline JSON in
   templates, `template.JS`, data attributes):
   `jsontext.EscapeForHTML(true), jsontext.EscapeForJS(true)`.
d. **Read from anything we do not produce** (model output, including
   tool-call arguments and structured output, provider responses and
   streams, webhooks, user-written configuration and files, process or
   terminal output): the lenient options above. Untagged exported fields in
   decode targets match only the exact Go name in v2: either the call
   passes `json.MatchCaseInsensitiveNames(true)`, or, for types that are only
   decoded, add the tag with the real wire name.
e. **Strings that carry bytes we do not control** (process and terminal
   output, file contents, model output, user text, third-party data, log
   lines): `jsontext.AllowInvalidUTF8(true)`, or the marshal fails at run
   time where v1 wrote U+FFFD.
f. **Foreign `jsontext.Value`s embedded in a value we marshal** (raw tool
   arguments, provider payloads): `jsontext.AllowDuplicateNames(true),
   jsontext.AllowInvalidUTF8(true)` on that `Marshal` (fact 11).
g. **Requests from our own clients (web, apps, CLI, SDK) and responses to
   them**: v2's defaults. Keep `RejectUnknownMembers(true)` exactly where
   `DisallowUnknownFields` was.

**Durations.** Keep v1's wire form, integer nanoseconds, without `format:`
tags:

- A struct you own with `time.Duration` fields: give it methods that encode
  through an alias, so every caller in every module gets the pinned form:

      func (s Spec) MarshalJSONTo(enc *jsontext.Encoder) error {
          type alias Spec
          return json.MarshalEncode(enc, struct {
              alias
              TTL int64 `json:"ttl,omitzero"`
          }{alias(s), int64(s.TTL)})
      }

      func (s *Spec) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
          type alias Spec
          var w struct {
              *alias
              TTL int64 `json:"ttl"`
          }
          w.alias = (*alias)(s)
          if err := json.UnmarshalDecode(dec, &w); err != nil {
              return err
          }
          s.TTL = time.Duration(w.TTL)
          return nil
      }

  The shallower `TTL` shadows the embedded one, as in v1. Give each pinned
  field its original tag options, with `omitempty` changed to `omitzero`,
  and add a test that asserts the exact bytes.
- Durations inside values you cannot add methods to (`map[string]any`,
  third-party types, ad hoc literals): convert at the call site (`int64(d)`
  or `d.Nanoseconds()`), or pass
  `json.WithMarshalers(json.MarshalToFunc(func(enc *jsontext.Encoder, d time.Duration) error { return enc.WriteToken(jsontext.Int(int64(d))) }))`
  and the matching `json.WithUnmarshalers(json.UnmarshalFromFunc(...))`.
- Look beyond the tool's report: untagged structs that get marshaled
  (configuration dumps, `--json` output), and literals such as
  `map[string]any{"elapsed": time.Since(t)}`.

**Byte arrays** (`[N]byte`): v1 wrote an array of numbers. If any are
serialized, pin them with a method or a conversion to an `[]int`-shaped
value.

**Merging.** v2's unmarshal into a non-zero value replaces slices and
merges into map values, and JSON `null` always zeroes the target. Check
code that unmarshals over pre-filled defaults, and keep the old result.

**Pointer-receiver marshalers.** A pointer-receiver `MarshalJSON` is now
called for values that are not addressable too (map values, values passed
by value). If such a method's output differs from the plain struct
encoding, v2 now writes the method's output: note each case.

**Third-party types marshaled by our calls.** Their tags were written for
v1. If one has `omitempty` on bool, number, pointer or struct fields, a
`time.Duration`, or needs case-insensitive input, our v2 call changes its
wire form. Check each; if v1's output must be kept and v2 offers no way,
report it instead of importing v1.

## Rules

- No `encoding/json` import may remain in any file that is not generated,
  tests included, and no `//nolint:depguard` to hide one. A site that truly
  cannot be written without v1 is reported, not left on v1.
- Generated files (`// Code generated ... DO NOT EDIT.`): change the
  generator or its template and regenerate with the repository's own
  command.
- Tests: update a test only when its expected bytes changed because of an
  accepted v2 change (`[]` for `null`, an unescaped `<`, key order, a
  trailing newline, error text). If a test caught a pinned field changing,
  fix the code. Add byte-exact tests for each kind of pin you introduce
  (duration methods, `omitzero` on a wire type, nil as `null` in an
  outbound payload, lenient decoding of duplicate names, invalid UTF-8 and
  mixed case).
- Comments say what the code does and why, never that it was migrated.
- Benchmarks written to measure the migration pair their before and after
  runs by name. Migrate their imports and calls like any other test, with
  the options the production path now uses, but keep every benchmark
  function name, every `b.Run` name, every input and every loop body
  otherwise unchanged. Never add, remove, rename or reorder a benchmark.

When an agent does this work, add:

- Leave version control to the person or session directing the agents,
  who will commit: no `git add`,
  commit, push, branch or stash.
- Touch only the repository, or the directory, you were given.
- Report every pin, every accepted change and every open question, with
  its file and line.

## The reviewer's checklist

A second agent read each migration's whole diff, looking for these 11 kinds
of mistake, and fixed or reported what it found. The list works as well for
a person reviewing a v2 migration.

1. An `encoding/json` import left in place, or a `//nolint:depguard` hiding
   one.
2. A `time.Duration` that reaches `Marshal` or `Unmarshal` without a pin
   (fact 3), or any `format:` tag (fact 2).
3. Output that another system reads, or that is stored, written without
   `json.Deterministic(true)`, or without `json.FormatNilSliceAsNull(true)`
   and `json.FormatNilMapAsNull(true)` where the receiver expects `null`
   for a nil slice or map (Step 3).
4. Input from outside the repository decoded without the leniency v1 had
   for invalid UTF-8, duplicate names and case-insensitive names (facts 9,
   10 and 13), or a v1-style `UnmarshalJSON` method that drops the options
   its caller passed (fact 8).
5. `json.UnmarshalRead` on a stream that stays open, which waits for EOF
   (fact 6); a decode loop that no longer stops on `io.EOF` (fact 7); or a
   rewrite of a v1 `Token` and `More` loop that checks the wrong token
   kinds.
6. A trailing newline lost where something reads line by line: command
   output, NDJSON, JSON-RPC over stdio, files people read (fact 5).
7. Empty-body handling that relied on v1's `Decode` returning `io.EOF` for
   an empty body (fact 6).
8. `json.Number` replaced by `float64` where the exact digits mattered.
9. A field whose omission changed in a way the tag tool did not cover
   (fact 4 and Step 1), or a change reported as accepted that the wire
   policy says to pin.
10. A test weakened until it passed, or a pin with no byte-exact test.
11. Error handling that checks for the wrong v2 error type, or an error
    message that lost the name of the field that failed.

## Learned in CI

These rules were added after the first repositories went through CI; each
one comes from a check run that failed.

- **Map keys are unsorted.** v1 sorted the keys of a Go map; v2 does not.
  Every test that compares a body built from a map byte for byte, and every
  test that decodes a map and marshals it again, fails at random. A map
  with a few keys often comes out in v1's order by chance, so such a test
  passes some runs and fails others, and one green run proves nothing.
  Rerun such packages several times, as separate processes.
  A `-count=3` failure needs a check against the code from before the
  change. A test that leaves a fixed database key, a package-level counter
  or a process limit behind fails `-count=3` with no JSON in it. The
  fixes, by rule: a response or stored body with a fixed set
  of members becomes a struct whose fields are declared in sorted key order
  (its bytes then equal v1's); an error body, which a test compares to prove
  two refusals are indistinguishable, is written with
  `json.Deterministic(true)`; a test that marshals a map again passes
  `json.Deterministic(true)`.
- **Escaping is part of the bytes a server compares.** v1 always escaped
  `<`, `>`, `&`, U+2028 and U+2029; v2 escapes none of them by default. The
  Kubernetes API server still encodes with v1's rules, and a JSON Patch
  `test` operation compares its value with the server's encoding byte for
  byte. A guarded patch of an annotation holding `a > b`, sent as
  `"a > b"`, fails against the stored `"a \u003e b"` on every attempt.
  Toward a server that compares bytes (Kubernetes, prompt caches), pass
  `jsontext.EscapeForHTML(true), jsontext.EscapeForJS(true)` as well.
- **Libraries first.** A migrated module that encodes a struct from an
  unmigrated library applies v2's rules to that library's v1-era tags.
  Migrate and release every module that other modules import, and raise its
  consumers to that release, before the consumers merge. Don't write mirror
  marshalers for another module's types to work around it.
- **Fakes that send what no server sends.** Once a library reads with v2's
  defaults, a test's fake that writes a member twice, or answers with an
  untagged Go struct, fails where v1 read it. Fix the fake so it sends what
  the real server sends, and keep the strict read.
- **Linters that only know v1.** `errchkjson` and `gosec` do not recognize
  v2 calls, so a `//nolint:errchkjson` or `//nolint:gosec` on a line that
  now calls v2 is unused and fails `nolintlint`: remove it. `errorlint`
  flags `err == io.EOF` and `err != io.EOF` after `jsontext.Decoder.ReadToken` or `ReadValue`;
  write `errors.Is(err, io.EOF)`. `unconvert` flags `jsontext.Value(x)` when
  `x` already has that type.
- **The go directive.** v2 needs `go 1.27` in `go.mod`. Raising it turns on
  Go 1.27's modernizers (`for range N`, `embedlit`) in every file the
  change touches, and a library that supported older Go releases drops
  them: say so in its changelog.
- **Error text.** Go 1.27.3 words a type mismatch "unable to unmarshal";
  v1 said "cannot unmarshal". Tests that match error text must change.
- **License headers.** If your checks require a license notice in every
  file, a new test file needs one too.
