// Command rawprobe prints how v1 and v2 treat a raw JSON value in a struct
// field tagged omitempty or omitzero, for these inputs: nil, empty (non-nil,
// zero length), null, {}, [], "" and a value with surrounding spaces. Its
// output (output.txt) fixed the tag rules for raw values.
//
// Run it with the toolchain under test: go run .
package main

import (
	jsonv1 "encoding/json"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
)

type E struct {
	V jsontext.Value `json:"v,omitempty"`
}
type Z struct {
	V jsontext.Value `json:"v,omitzero"`
}
type N struct {
	V jsontext.Value `json:"v"`
}
type V1 struct {
	V jsonv1.RawMessage `json:"v,omitempty"`
}

func main() {
	cases := map[string]jsontext.Value{"nil": nil, "empty": {}, "null": jsontext.Value("null"), "obj": jsontext.Value("{}"), "arr": jsontext.Value("[]"), "str": jsontext.Value(`""`), "sp": jsontext.Value(" {} ")}
	for _, k := range []string{"nil", "empty", "null", "obj", "arr", "str", "sp"} {
		v := cases[k]
		a, ea := jsonv1.Marshal(V1{V: jsonv1.RawMessage(v)})
		b, eb := json.Marshal(E{V: v})
		c, ec := json.Marshal(Z{V: v})
		d, ed := json.Marshal(N{V: v})
		fmt.Printf("%-5s v1-omitempty=%s %v | v2-omitempty=%s %v | v2-omitzero=%s %v | v2-plain=%s %v\n", k, a, ea, b, eb, c, ec, d, ed)
	}
}
