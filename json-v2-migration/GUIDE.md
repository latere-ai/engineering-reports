# encoding/json to encoding/json/v2: migration rules

This is the brief every migrating and reviewing agent worked from, as it
stood at the end of the migration. "We" and "our" are the organization whose
repositories moved; "services we do not own" are everyone else's.

Toolchain: Go 1.27.3 (every repo pins `toolchain go1.27.3`). On this toolchain
`encoding/json` (v1) is already implemented on the v2 engine with v1 options,
so the migration changes behavior, not the engine. Everything below was checked
on this toolchain with probe programs; where this guide and your memory of the
v2 proposal disagree, this guide wins.

## Goal and wire policy

Every file stops importing `encoding/json`. Code uses `encoding/json/v2`
(imported as `json`) and `encoding/json/jsontext`. The JSON other systems read
and store keeps the same fields and values ("v2 defaults, fields pinned"):

Kept identical (pin it when v2 would differ):
- which fields are omitted (`omitempty` semantics), field names, number and
  string values, durations as integer nanoseconds;
- key order wherever bytes are stored, hashed, compared, or sent to model
  providers;
- `null` for nil slices and maps in payloads sent to services we do not own;
- leniency for input we do not produce (duplicate keys, invalid UTF-8,
  case-insensitive names).

Accepted v2 changes (do not pin, update tests that assert the old bytes):
- nil slices and maps encode as `[]` and `{}` in our own API responses,
  events and stored documents;
- `<`, `>`, `&` are no longer escaped (except JSON embedded in HTML/JS);
- object keys from Go maps are unsorted in HTTP responses to our own clients;
- struct field names match case-sensitively for requests from our own clients;
- json error message text; `MarshalWrite` writes no trailing newline in HTTP
  response bodies.

## Verified facts about Go 1.27.3

1. `json.RawMessage` is a type alias of `jsontext.Value`. Rename it; values
   cross module boundaries unchanged, so other repos' APIs still match.
2. The `format:` struct tag option is NOT supported by the standard library on
   Go 1.27: any field carrying `format:...` makes Marshal and Unmarshal of the
   whole struct fail at runtime. Never write `format:` tags (no `format:nano`,
   `format:emitnull`, `format:array`, `format:base64`).
3. `time.Duration` has no v2 representation: marshaling or unmarshaling one
   (a field, slice element, map value, or a value inside `any`) is a runtime
   error. See "Durations".
4. v1 `omitempty` omits false, 0, nil pointer, nil interface, and empty
   string/slice/map/array, and never omits a struct value. v2 `omitempty`
   omits a field whose encoded JSON is `null`, `""`, `{}` or `[]`. `omitzero`
   (supported by v1 and v2) omits the Go zero value, or where `IsZero()` says.
5. `json.MarshalWrite` writes no trailing newline. `json.MarshalEncode` on a
   `jsontext.NewEncoder(w, opts...)` writes the value plus `\n`, like v1
   `Encoder.Encode`, and indents when given `jsontext.WithIndent`.
6. `json.UnmarshalRead` reads the reader to EOF; trailing non-whitespace is an
   error. On empty input it returns an error for which
   `errors.Is(err, io.ErrUnexpectedEOF)` is true and `errors.Is(err, io.EOF)`
   is false.
7. In a stream, `json.UnmarshalDecode(dec, &v)` returns exactly `io.EOF` after
   the last value.
8. Call-site options do NOT reach v1-style `UnmarshalJSON([]byte)` or
   `MarshalJSON()` methods: the method's own inner `json.Unmarshal` runs with
   v2 defaults. Options do reach `UnmarshalJSONFrom(*jsontext.Decoder)` and
   `MarshalJSONTo(*jsontext.Encoder)` methods through the decoder/encoder.
9. `json.MatchCaseInsensitiveNames(true)` matches case-insensitively AND
   ignores `_` and `-` (`foo_bar` matches `FooBar`): slightly more lenient
   than v1.
10. `jsontext.AllowDuplicateNames(true)`: the last duplicate wins, as in v1.
11. Marshaling a `jsontext.Value` re-validates it: a raw value with duplicate
    keys or invalid UTF-8 makes the enclosing Marshal fail unless the call
    passes `jsontext.AllowDuplicateNames(true)` / `jsontext.AllowInvalidUTF8(true)`.
12. `jsontext.WithIndent("  ")` output equals v1 `MarshalIndent(v, "", "  ")`
    except that `<>&` are not escaped.
13. `jsontext.AllowInvalidUTF8(true)` replaces invalid bytes with U+FFFD on
    marshal and on unmarshal, as v1 did silently.

## Step 1: pin omitempty with the tag tool (mechanical, do it first)

Build the tool once (it is its own module), then run it from each module
root:

    (cd tools/jsontags && go build -o "$HOME/bin/jsontags" .)
    jsontags -w -report jsontags-report.json ./...

It rewrites `omitempty` by the field's Go kind so v2 output equals v1 output:

| Field kind | Tag change |
|---|---|
| bool, numbers, pointers, interfaces | `omitempty` -> `omitzero` |
| pointer to `time.Time` | unchanged (`omitempty` is exact under v2 here) |
| raw values (`jsontext.Value`) | `omitempty` -> `omitzero` |
| string kinds with a custom marshaler | `omitempty` -> `omitzero` |
| struct values (`time.Time` included) | `omitempty` removed (v1 never omitted them) |
| plain string, slice, map, array | unchanged (same in both) |

The rewrite is also neutral under v1. Run it BEFORE switching imports (it type-checks the module). It skips
generated files. Its report lists, for you to handle by hand:
- `durations`: struct fields holding `time.Duration`;
- `byte_arrays`: `[N]byte` fields (v1: array of numbers; v2: base64 string);
- `check`: fields where kind-based pinning may not be exact (type parameters,
  pointers to types with `IsZero`, slices/maps with custom marshalers);
- `untagged_samples`: exported fields without a json tag in tagged structs.

Do not hand-edit omitempty tags the tool already decided. Do not run the tool
twice after imports change, and never on another repo.

## Step 2: imports and mechanical renames

- `import "encoding/json"` becomes `import "encoding/json/v2"` (name stays
  `json`); add `"encoding/json/jsontext"` where used. Group per goimports.
- `json.RawMessage` -> `jsontext.Value` everywhere (types, conversions, tests).
- `json.MarshalIndent(v, "", ind)` -> `json.Marshal(v, jsontext.WithIndent(ind))`;
  a non-empty prefix adds `jsontext.WithIndentPrefix(p)`.
- `json.Valid(b)` -> `jsontext.Value(b).IsValid()`. It is stricter (rejects
  duplicate keys and invalid UTF-8); for foreign input pass the lenient options.
- `json.Compact(&buf, src)` -> `v := jsontext.Value(bytes.Clone(src)); err := v.Compact()`;
  `json.Indent(&buf, src, p, i)` -> `v.Indent(jsontext.WithIndentPrefix(p), jsontext.WithIndent(i))`.
- One-shot decode of a bounded body (request body, response body, file):
  `json.NewDecoder(r).Decode(&v)` -> `json.UnmarshalRead(r, &v, opts...)`.
  Never use UnmarshalRead on a long-lived stream (it blocks until EOF).
- Empty-body handling: code that treated `io.EOF` from `Decode` as "empty
  body" must keep that behavior: check `errors.Is(err, io.ErrUnexpectedEOF)`
  only if you are sure the input was empty, or better, read the body and test
  `len(bytes.TrimSpace(b)) == 0` first.
- `dec.DisallowUnknownFields()` -> pass `json.RejectUnknownMembers(true)`.
- One-shot encode:
  - HTTP response bodies: `json.NewEncoder(w).Encode(v)` -> `json.MarshalWrite(w, v, opts...)`.
  - Line-oriented output (CLI stdout, NDJSON, log lines, files people read,
    JSON-RPC over stdio): keep the newline with
    `json.MarshalEncode(jsontext.NewEncoder(w, opts...), v)`.
  - `enc.SetIndent(p, i)` -> `jsontext.WithIndentPrefix(p), jsontext.WithIndent(i)`
    on the encoder; `enc.SetEscapeHTML(false)` -> delete (v2 default). A v1
    Encoder without `SetEscapeHTML(false)` escaped HTML: see "HTML".
- Streams (several values on one reader/writer, NDJSON, SSE data lines read in
  a loop, websockets, JSON-RPC):
  - `dec := json.NewDecoder(r)` -> `dec := jsontext.NewDecoder(r, opts...)`;
    `dec.Decode(&v)` -> `json.UnmarshalDecode(dec, &v)`; end of stream is
    `err == io.EOF`.
  - `dec.Token()` -> `dec.ReadToken()` (a `jsontext.Token`; `tok.Kind()` is
    one of `{ } [ ] " 0 t f n`; `tok.String()` for strings).
  - `dec.More()` -> `dec.PeekKind() != ']'` inside an array (`'}'` in an object).
  - `dec.InputOffset()` -> `dec.InputOffset()`. `dec.Buffered()` has no
    equivalent: restructure.
  - `enc := json.NewEncoder(w)` -> `enc := jsontext.NewEncoder(w, opts...)`;
    `enc.Encode(v)` -> `json.MarshalEncode(enc, v)`.
  - Types `*json.Decoder` / `*json.Encoder` in fields and signatures become
    `*jsontext.Decoder` / `*jsontext.Encoder`.
- Errors: `*json.SyntaxError` -> `*jsontext.SyntacticError` (`ByteOffset`
  replaces `Offset`); `*json.UnmarshalTypeError`, `*json.InvalidUnmarshalError`,
  `*json.MarshalerError`, `*json.UnsupportedTypeError`, `*json.UnsupportedValueError`
  -> `*json.SemanticError` (`ByteOffset`, `JSONPointer`, `JSONKind`, `GoType`, `Err`).
  `JSONPointer` replaces v1's `Field` (it is `/a/b`, not `a.b`); rewrite any
  user-facing message built from it so it still names the field.
- `json.Marshaler` and `json.Unmarshaler` exist in v2 with the same methods;
  existing `MarshalJSON`/`UnmarshalJSON` methods keep working. Switch their
  bodies to v2 calls. Read fact 8: if a type with an `UnmarshalJSON` method is
  decoded from foreign input with lenient options, either repeat the options
  inside the method or convert it to `UnmarshalJSONFrom(dec *jsontext.Decoder)`
  that calls `json.UnmarshalDecode(dec, (*alias)(v))`.
- `json.Number` and `dec.UseNumber()` (v1 only): use the value's real type
  (`int64`, `uint64`, `float64`, `string`) when the schema is known; keep raw
  digits as `jsontext.Value` (parse with `strconv`) when precision matters
  (IDs above 2^53, money, pass-through tool arguments). For `map[string]any`
  decoded with UseNumber to preserve numbers, decode into
  `map[string]jsontext.Value` or a concrete type instead. Never change a
  number into a float64 where v1 code kept exact digits.

## Step 3: the semantic pass (wire policy)

Go through every remaining json call site and every serialized type. Decide by
where the bytes go and where they come from. Prefer a package-level options
value over repeating options, for example:

    // foreignJSON reads JSON we do not produce: model output, provider and
    // webhook payloads, user files. v1 accepted duplicate keys, invalid UTF-8
    // and any letter case; v2 rejects them unless asked.
    var foreignJSON = json.JoinOptions(
        jsontext.AllowDuplicateNames(true),
        jsontext.AllowInvalidUTF8(true),
        json.MatchCaseInsensitiveNames(true),
    )

a. Outbound to services we do not own (model providers, Stripe, mail,
   Kubernetes, cloud APIs, GitHub, Google, OAuth providers): pass
   `json.Deterministic(true), json.FormatNilSliceAsNull(true), json.FormatNilMapAsNull(true)`,
   and v1's escaping where the receiver compares bytes (see "Learned while
   landing").
   Some providers reject `[]` where they accepted `null` (OpenAI rejects
   `"tools": []`), and prompt caching needs byte-identical requests.
b. Stored, hashed, signed, cached by content, compared byte-for-byte, or
   written to files that are diffed or committed: `json.Deterministic(true)`.
   Golden tests: compare semantically or keep Deterministic in the test.
c. Embedded in HTML or JavaScript (script tags, inline JSON in templates,
   `template.JS`, data attributes): `jsontext.EscapeForHTML(true), jsontext.EscapeForJS(true)`.
d. Inbound we do not produce (model output incl. tool-call arguments and
   structured output, provider responses and streams, webhooks, user-written
   config/files, process or terminal output): the lenient options above.
   Untagged exported fields in decode targets match only the exact Go name
   in v2: either the call passes `json.MatchCaseInsensitiveNames(true)`, or
   (decode-only types) add the tag with the real wire name.
e. Marshaling strings that carry bytes we do not control (process and PTY
   output, file contents, model output, user text, third-party data, log
   lines): `jsontext.AllowInvalidUTF8(true)`, or the marshal fails at runtime
   where v1 wrote U+FFFD.
f. Embedding foreign `jsontext.Value`s (raw tool arguments, provider payloads)
   in a Marshal: `jsontext.AllowDuplicateNames(true), jsontext.AllowInvalidUTF8(true)`
   on that Marshal (fact 11).
g. Requests from our own clients (web, apps, CLI, SDK) and responses to them:
   v2 defaults. Keep `RejectUnknownMembers(true)` exactly where
   `DisallowUnknownFields` was.

Durations. Keep v1's wire (integer nanoseconds) without `format:` tags:
- A struct you own with `time.Duration` fields: give it methods that encode
  through an alias, so every caller in every repo gets the pinned form:

      func (s Spec) MarshalJSON() ([]byte, error) {
          type alias Spec
          return json.Marshal(struct {
              alias
              TTL int64 `json:"ttl,omitzero"`
          }{alias(s), int64(s.TTL)})
      }
      func (s *Spec) UnmarshalJSON(b []byte) error {
          type alias Spec
          var w struct {
              *alias
              TTL int64 `json:"ttl"`
          }
          w.alias = (*alias)(s)
          if err := json.Unmarshal(b, &w); err != nil {
              return err
          }
          s.TTL = time.Duration(w.TTL)
          return nil
      }

  (The shallower `TTL` shadows the embedded one, as in v1. Match each pinned
  field's original tag options, with omitempty -> omitzero.) Add a test that
  asserts the exact bytes.
- Durations inside values you cannot add methods to (`map[string]any`,
  third-party types, ad-hoc literals): convert at the call site
  (`int64(d)` or `d.Nanoseconds()`), or pass
  `json.WithMarshalers(json.MarshalToFunc(func(enc *jsontext.Encoder, d time.Duration) error { return enc.WriteToken(jsontext.Int(int64(d))) }))`
  and the matching `json.WithUnmarshalers(json.UnmarshalFromFunc(...))`.
- Search beyond the tool's report: untagged structs that get marshaled
  (config dumps, `--json` output), `map[string]any{"elapsed": time.Since(t)}`.

Byte arrays (`[N]byte`): v1 wrote an array of numbers; pin with a method or a
`[]int`-shaped conversion if any are serialized.

Merging: v2 unmarshal into a non-zero value replaces slices and merges into map
values; JSON `null` always zeroes the target. Check code that unmarshals over
pre-filled defaults; keep the old result.

Pointer-receiver `MarshalJSON` is now called for non-addressable values too
(map values, values passed by value). If such a method's output differs from
the plain struct encoding, v2 now emits the method's output: note each case.

Third-party types marshaled by our own calls: their tags were written for v1.
If one has omitempty on bool/number/pointer/struct fields, a Duration, or
needs case-insensitive input, our v2 call changes its wire. Check; if v1 output
must be kept and v2 has no way, report it instead of importing v1.

## Rules

- No `encoding/json` import may remain in any non-generated file, tests
  included, and no `//nolint:depguard`. A site that truly cannot be expressed
  without v1: report it, do not import v1.
- Generated files (`// Code generated ... DO NOT EDIT.`): change the generator
  or its template and regenerate with the repo's own command.
- Tests: update a test only when its expected bytes changed because of an
  accepted v2 change (`[]` vs `null`, unescaped `<`, key order, trailing
  newline, error text). If a test caught a pinned field changing, fix the code.
  Add byte-exact tests for each kind of pin you introduce (duration methods,
  omitzero on a wire type, nil-as-null on an outbound payload, lenient decode
  of duplicate keys / invalid UTF-8 / mixed case).
- Comments say what the code does and why, never migration status.
- Never `git add`, commit, push, branch or stash. Never touch another repo or
  any file outside your repo. (The coordinator commits.)
- Benchmark files (`json_bench_test.go`) measure this migration: before and
  after runs are paired by benchmark name. Migrate their imports and calls like
  any other test, with the same options the production path now uses, but keep
  every `BenchmarkJSON...` function name, every `b.Run` name, every input and
  every loop body otherwise unchanged. Never add, remove, rename or reorder a
  benchmark.

## Learned while landing

These were added after the first 27 repositories went through their gates.
Each one failed a gate at least once.

- **Map keys are unsorted.** v1 sorted the keys of a Go map; v2 does not.
  Every test that compares a body built from a map byte for byte, and every
  test that decodes a map and marshals it again, fails at random. A three-key
  map comes out in v1's order about one run in three, so one green run proves
  nothing; rerun such packages several times. Rerun them as separate
  processes, or check a `-count=3` failure against the code before the
  change: a test that leaves a fixed database key, a package counter or a
  process limit behind fails `-count=3` without any JSON in it. Fixes, by
  rule: a response or stored body with a fixed set of members becomes a
  struct whose fields are declared in sorted key order (its bytes then equal
  v1's); an error body, which a test compares to prove two refusals are
  indistinguishable, is written with `json.Deterministic(true)`; a test that
  re-marshals a map passes `json.Deterministic(true)`.
- **Escaping is part of the bytes a server compares.** v1 always escaped
  `<`, `>`, `&`, U+2028 and U+2029; v2 escapes none of them by default. The
  Kubernetes API server still encodes with v1's rules, and a JSON Patch
  `test` operation compares its value with the server's encoding byte for
  byte. A guarded patch of an annotation holding `a > b` sent as `"a > b"`
  fails against the stored `"a \u003e b"` on every attempt. Toward a server
  that compares bytes (Kubernetes, prompt caches), pass
  `jsontext.EscapeForHTML(true), jsontext.EscapeForJS(true)` as well.
- **Libraries first.** A migrated module that encodes a struct from an
  unmigrated library applies v2 semantics to that library's v1-era tags.
  Migrate, release and bump every module that other modules import before
  their consumers land. Do not write mirror marshalers for another module's
  types to work around it.
- **Fakes that send what no server sends.** Once a library reads with v2's
  defaults, a test's fake that writes a member twice, or answers with an
  untagged Go struct, fails where v1 read it. Fix the fake so it sends what
  the real server sends; keep the strict read.
- **Linters that only know v1.** `errchkjson` and `gosec` do not recognize v2
  calls, so a `//nolint:errchkjson` or `//nolint:gosec` on a line that now
  calls v2 is unused and fails `nolintlint`: remove it. `errorlint` flags
  `err == io.EOF` after `jsontext.Decoder.ReadToken` or `ReadValue`; write
  `errors.Is(err, io.EOF)`. `unconvert` flags `jsontext.Value(x)` when `x`
  already has that type.
- **The go directive.** v2 needs `go 1.27` in `go.mod`. Raising it turns on
  Go 1.27's modernizers (`for range N`, `embedlit`) in every file the change
  touches, and a library that supported older Go releases drops them: say so
  in its changelog.
- **Error text.** Go 1.27.3 words a type mismatch "unable to unmarshal"; v1
  said "cannot unmarshal". Tests that match error text change.
- **New files.** A new test file carries the same license notice as its
  siblings.
