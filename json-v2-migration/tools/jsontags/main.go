// Command jsontags pins encoding/json v1 omitempty semantics in struct tags
// so that the same types encode identically under encoding/json/v2, and lists
// the fields v2 treats differently in ways a tag cannot fix.
//
// v1 omitempty is decided by the Go kind: false, 0, nil pointer, nil
// interface, len 0 string/slice/map/array; struct values are never omitted.
// v2 omitempty is decided by the encoded JSON (null, "", {}, []). The rewrite
// per kind is exact under v2 and behavior-neutral under v1, which supports
// omitzero too:
//
//	bool, numbers, pointers, interfaces  omitempty -> omitzero
//	pointer to time.Time                  unchanged (same in both)
//	raw values (jsontext.Value)           omitempty -> omitzero
//	string with a custom marshaler       omitempty -> omitzero
//	struct values (time.Time included)    omitempty removed (v1 never omitted)
//	plain string, slice, map, array       unchanged (same in both)
//
// Usage: jsontags [-w] [-report file] [packages]   (run from the module root)
package main

import (
	"bytes"
	"encoding/json/v2"
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/token"
	"go/types"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/tools/go/packages"
)

type edit struct {
	start, end int
	text       string
}

type finding struct {
	Pos    string `json:"pos"`
	Struct string `json:"struct,omitempty"`
	Field  string `json:"field"`
	Type   string `json:"type"`
	Note   string `json:"note"`
}

type report struct {
	Module          string    `json:"module"`
	OmitemptyTags   int       `json:"omitempty_tags"`
	ToOmitzero      int       `json:"to_omitzero"`
	Dropped         int       `json:"dropped_on_struct"`
	Unchanged       int       `json:"unchanged"`
	Durations       []finding `json:"durations"`
	ByteArrays      []finding `json:"byte_arrays"`
	Check           []finding `json:"check"`
	UntaggedFields  int       `json:"untagged_exported_fields_in_tagged_structs"`
	UntaggedSamples []finding `json:"untagged_samples"`
	FilesChanged    []string  `json:"files_changed"`
}

var jsonKey = regexp.MustCompile(`(^|\s)json:"([^"]*)"`)

func main() {
	write := flag.Bool("w", false, "rewrite files in place")
	out := flag.String("report", "", "write the JSON report to this file")
	flag.Parse()
	patterns := flag.Args()
	if len(patterns) == 0 {
		patterns = []string{"./..."}
	}
	cfg := &packages.Config{
		Mode:  packages.NeedName | packages.NeedFiles | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo | packages.NeedModule,
		Tests: true,
	}
	pkgs, err := packages.Load(cfg, patterns...)
	if err != nil {
		fmt.Fprintln(os.Stderr, "load:", err)
		os.Exit(2)
	}
	if n := packages.PrintErrors(pkgs); n > 0 {
		fmt.Fprintf(os.Stderr, "%d package errors; continuing with what type-checked\n", n)
	}

	var rep report
	edits := map[string][]edit{}
	seenTag := map[string]bool{}   // file:offset of a tag already decided
	seenField := map[string]bool{} // file:offset of a field already reported
	for _, p := range pkgs {
		if rep.Module == "" && p.Module != nil {
			rep.Module = p.Module.Path
		}
		for _, f := range p.Syntax {
			fname := p.Fset.File(f.Pos()).Name()
			if ast.IsGenerated(f) {
				continue
			}
			ast.Inspect(f, func(n ast.Node) bool {
				st, ok := n.(*ast.StructType)
				if !ok {
					return true
				}
				structName := enclosingName(f, st)
				hasJSON := false
				for _, fld := range st.Fields.List {
					if fld.Tag != nil && strings.Contains(fld.Tag.Value, `json:"`) {
						hasJSON = true
					}
				}
				for _, fld := range st.Fields.List {
					tv, ok := p.TypesInfo.Types[fld.Type]
					if !ok || tv.Type == nil {
						continue
					}
					t := tv.Type
					pos := p.Fset.Position(fld.Pos())
					key := fmt.Sprintf("%s:%d", fname, pos.Offset)
					name := fieldName(fld)
					jsonVal, rawTag, tagOK := jsonTag(fld)
					if jsonVal == "-" {
						continue
					}
					if !seenField[key] && hasJSON {
						seenField[key] = true
						fd := finding{Pos: fmt.Sprintf("%s:%d", rel(fname), pos.Line), Struct: structName, Field: name, Type: types.TypeString(t, qual(p.Types))}
						if containsDuration(t, 0) {
							fd.Note = "time.Duration has no v2 default representation; pin to int64 nanoseconds"
							rep.Durations = append(rep.Durations, fd)
						}
						if isByteArray(t) {
							fd.Note = "[N]byte is base64 in v2, a number array in v1"
							rep.ByteArrays = append(rep.ByteArrays, fd)
						}
						if !tagOK && len(fld.Names) > 0 && ast.IsExported(name) {
							rep.UntaggedFields++
							if len(rep.UntaggedSamples) < 40 {
								fd.Note = "no json tag: v2 matches the Go name case-sensitively on decode"
								rep.UntaggedSamples = append(rep.UntaggedSamples, fd)
							}
						}
					}
					if !tagOK {
						continue
					}
					opts := strings.Split(jsonVal, ",")
					if !hasOpt(opts[1:], "omitempty") {
						continue
					}
					tkey := fmt.Sprintf("%s:%d", fname, p.Fset.Position(fld.Tag.Pos()).Offset)
					if seenTag[tkey] {
						continue
					}
					seenTag[tkey] = true
					rep.OmitemptyTags++
					action, note := decide(t)
					if note != "" {
						rep.Check = append(rep.Check, finding{Pos: fmt.Sprintf("%s:%d", rel(fname), pos.Line), Struct: structName, Field: name, Type: types.TypeString(t, qual(p.Types)), Note: note})
					}
					var newOpts []string
					switch action {
					case "omitzero":
						rep.ToOmitzero++
						newOpts = replaceOpt(opts, "omitempty", "omitzero")
					case "drop":
						rep.Dropped++
						newOpts = removeOpt(opts, "omitempty")
					default:
						rep.Unchanged++
						continue
					}
					newVal := strings.Join(newOpts, ",")
					newTag := jsonKey.ReplaceAllStringFunc(rawTag, func(m string) string {
						sub := jsonKey.FindStringSubmatch(m)
						return sub[1] + `json:"` + newVal + `"`
					})
					lit := requote(fld.Tag.Value, newTag)
					s := p.Fset.Position(fld.Tag.Pos()).Offset
					e := p.Fset.Position(fld.Tag.End()).Offset
					edits[fname] = append(edits[fname], edit{s, e, lit})
				}
				return true
			})
		}
	}

	for fname, es := range edits {
		rep.FilesChanged = append(rep.FilesChanged, rel(fname))
		if !*write {
			continue
		}
		src, err := os.ReadFile(fname)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		sort.Slice(es, func(i, j int) bool { return es[i].start > es[j].start })
		for _, e := range es {
			src = append(src[:e.start], append([]byte(e.text), src[e.end:]...)...)
		}
		formatted, err := format.Source(src)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: gofmt after rewrite: %v\n", fname, err)
			os.Exit(1)
		}
		if err := os.WriteFile(fname, formatted, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	sort.Strings(rep.FilesChanged)
	fmt.Printf("%s: omitempty=%d omitzero=%d dropped=%d unchanged=%d check=%d durations=%d bytearrays=%d untagged=%d files=%d\n",
		rep.Module, rep.OmitemptyTags, rep.ToOmitzero, rep.Dropped, rep.Unchanged, len(rep.Check), len(rep.Durations), len(rep.ByteArrays), rep.UntaggedFields, len(rep.FilesChanged))
	if *out != "" {
		b, err := json.Marshal(rep, json.Deterministic(true))
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		var buf bytes.Buffer
		buf.Write(b)
		if err := os.WriteFile(*out, buf.Bytes(), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
}

// decide returns what to do with omitempty on a field of type t, and a note
// when the field needs a person to look at it.
func decide(t types.Type) (action, note string) {
	if _, ok := t.(*types.TypeParam); ok {
		return "keep", "type parameter: kind unknown, v1 and v2 omitempty may differ"
	}
	custom := hasMethod(t, "MarshalJSON") || hasMethod(t, "MarshalText") || hasMethod(t, "MarshalJSONTo")
	switch u := t.Underlying().(type) {
	case *types.Basic:
		switch {
		case u.Info()&(types.IsBoolean|types.IsNumeric) != 0:
			return "omitzero", isZeroNote(t)
		case u.Info()&types.IsString != 0:
			if custom {
				return "omitzero", "string kind with a custom marshaler: omitzero keeps v1 (omit only \"\")" + isZeroSuffix(t)
			}
			return "keep", ""
		}
		return "keep", ""
	case *types.Pointer:
		if isTime(u.Elem()) {
			// v2 omitempty omits nil (null) and keeps every non-nil time,
			// which always encodes as a non-empty string: exactly v1.
			// omitzero would also drop a pointer to the zero time.
			return "keep", ""
		}
		if hasMethod(u.Elem(), "IsZero") {
			return "omitzero", "pointer to a type with IsZero: omitzero also omits a non-nil pointer whose target IsZero; v1 kept it"
		}
		return "omitzero", ""
	case *types.Interface:
		return "omitzero", ""
	case *types.Struct:
		if custom {
			return "drop", "struct with a custom marshaler: v1 never omitted it; omitempty removed"
		}
		return "drop", ""
	case *types.Slice, *types.Map:
		if isRawValue(t) {
			// v2 omitempty drops a raw null, {}, [] or "", which v1 kept;
			// omitzero omits only nil, as v1 did for every valid value. A
			// non-nil empty value fails to marshal in v2 under either tag.
			return "omitzero", ""
		}
		if custom {
			return "keep", "slice/map with a custom marshaler: v2 omits by the encoded JSON, v1 by len"
		}
		return "keep", ""
	}
	return "keep", ""
}

func isRawValue(t types.Type) bool {
	n, ok := types.Unalias(t).(*types.Named)
	return ok && n.Obj().Pkg() != nil && n.Obj().Pkg().Path() == "encoding/json/jsontext" && n.Obj().Name() == "Value"
}

func isTime(t types.Type) bool {
	n, ok := t.(*types.Named)
	return ok && n.Obj().Pkg() != nil && n.Obj().Pkg().Path() == "time" && n.Obj().Name() == "Time"
}

func isZeroNote(t types.Type) string {
	if hasMethod(t, "IsZero") {
		return "type has IsZero: omitzero uses it, v1 used the zero value"
	}
	return ""
}

func isZeroSuffix(t types.Type) string {
	if hasMethod(t, "IsZero") {
		return "; type has IsZero, which omitzero uses"
	}
	return ""
}

func hasMethod(t types.Type, name string) bool {
	for _, tt := range []types.Type{t, types.NewPointer(t)} {
		ms := types.NewMethodSet(tt)
		for i := 0; i < ms.Len(); i++ {
			if ms.At(i).Obj().Name() == name {
				return true
			}
		}
	}
	return false
}

func containsDuration(t types.Type, depth int) bool {
	if depth > 3 {
		return false
	}
	if n, ok := t.(*types.Named); ok && n.Obj().Pkg() != nil && n.Obj().Pkg().Path() == "time" && n.Obj().Name() == "Duration" {
		return true
	}
	if hasMethod(t, "MarshalJSON") || hasMethod(t, "MarshalJSONTo") {
		return false
	}
	switch u := t.(type) {
	case *types.Pointer:
		return containsDuration(u.Elem(), depth+1)
	case *types.Slice:
		return containsDuration(u.Elem(), depth+1)
	case *types.Array:
		return containsDuration(u.Elem(), depth+1)
	case *types.Map:
		return containsDuration(u.Elem(), depth+1)
	}
	return false
}

func isByteArray(t types.Type) bool {
	a, ok := t.Underlying().(*types.Array)
	if !ok {
		return false
	}
	b, ok := a.Elem().Underlying().(*types.Basic)
	return ok && b.Kind() == types.Byte && !hasMethod(t, "MarshalJSON") && !hasMethod(t, "MarshalText")
}

func jsonTag(fld *ast.Field) (val, raw string, ok bool) {
	if fld.Tag == nil {
		return "", "", false
	}
	raw, err := strconv.Unquote(fld.Tag.Value)
	if err != nil {
		return "", "", false
	}
	m := jsonKey.FindStringSubmatch(raw)
	if m == nil {
		return "", raw, false
	}
	return m[2], raw, true
}

func requote(orig, tag string) string {
	if strings.HasPrefix(orig, "`") && !strings.Contains(tag, "`") {
		return "`" + tag + "`"
	}
	return strconv.Quote(tag)
}

func hasOpt(opts []string, o string) bool {
	for _, x := range opts {
		if x == o {
			return true
		}
	}
	return false
}

func replaceOpt(opts []string, from, to string) []string {
	out := []string{opts[0]}
	have := hasOpt(opts[1:], to)
	for _, x := range opts[1:] {
		if x == from {
			if have {
				continue
			}
			out = append(out, to)
			continue
		}
		out = append(out, x)
	}
	return out
}

func removeOpt(opts []string, o string) []string {
	out := []string{opts[0]}
	for _, x := range opts[1:] {
		if x != o {
			out = append(out, x)
		}
	}
	return out
}

func fieldName(f *ast.Field) string {
	if len(f.Names) > 0 {
		return f.Names[0].Name
	}
	return types.ExprString(f.Type)
}

func enclosingName(f *ast.File, st *ast.StructType) string {
	name := ""
	ast.Inspect(f, func(n ast.Node) bool {
		if ts, ok := n.(*ast.TypeSpec); ok && ts.Type == st {
			name = ts.Name.Name
			return false
		}
		return name == ""
	})
	return name
}

func qual(p *types.Package) types.Qualifier {
	return func(other *types.Package) string {
		if other == p {
			return ""
		}
		return other.Name()
	}
}

var cwd, _ = os.Getwd()

func rel(p string) string {
	if strings.HasPrefix(p, cwd+string(os.PathSeparator)) {
		return p[len(cwd)+1:]
	}
	return p
}

var _ = token.NoPos
