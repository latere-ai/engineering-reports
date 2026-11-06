// Command probe prints how encoding/json (v1) and encoding/json/v2 differ on
// the cases that decided the migration rules. Each line is one case: the
// input, what v1 produced, and what v2 produced with default options.
//
// Run it with the toolchain under test: go run .
package main

import (
	"bytes"
	jsonv1 "encoding/json"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"runtime"
	"strings"
	"time"
)

type omit struct {
	I int       `json:"i,omitempty"`
	B bool      `json:"b,omitempty"`
	P *string   `json:"p,omitempty"`
	T time.Time `json:"t,omitempty"`
	S struct{}  `json:"s,omitempty"`
}

type nils struct {
	Items []int
	M     map[string]int
}

type ttl struct {
	TTL time.Duration `json:"ttl"`
}

type formatted struct {
	TTL time.Duration `json:"ttl,format:nano"`
}

type named struct {
	UserID string `json:"user_id"`
}

type legacy struct{ seen string }

// UnmarshalJSON is a v1-style method; call-site options do not reach the
// json.Unmarshal inside it.
func (l *legacy) UnmarshalJSON(b []byte) error {
	var m map[string]string
	err := json.Unmarshal(b, &m)
	l.seen = fmt.Sprint(m, " err=", err)
	return nil
}

func main() {
	fmt.Println("toolchain:", runtime.Version())
	row := func(name string, v1, v2 any) {
		fmt.Printf("\n%s\n  v1: %v\n  v2: %v\n", name, v1, v2)
	}
	show := func(b []byte, err error) string {
		if err != nil {
			return "error: " + err.Error()
		}
		return string(b)
	}

	var raw jsonv1.RawMessage
	row("json.RawMessage", "reflects as "+fmt.Sprintf("%T", raw), "jsontext.Value is the same type (an alias), so an API that takes one takes the other")

	row("omitempty on a zero struct {i int; b bool; p *string; t time.Time; s struct{}}",
		show(jsonv1.Marshal(omit{})), show(json.Marshal(omit{})))

	row("nil slice and nil map", show(jsonv1.Marshal(nils{})), show(json.Marshal(nils{})))

	row("time.Duration field", show(jsonv1.Marshal(ttl{TTL: 1500 * time.Millisecond})), show(json.Marshal(ttl{TTL: 1500 * time.Millisecond})))

	row("format:nano tag option (standard library)", "ignored by v1", show(json.Marshal(formatted{TTL: time.Second})))

	row("HTML-significant characters", show(jsonv1.Marshal("a<b && c>d")), show(json.Marshal("a<b && c>d")))

	m := map[string]int{"b": 2, "a": 1, "c": 3}
	row("map key order", show(jsonv1.Marshal(m)), show(json.Marshal(m))+" (unsorted unless json.Deterministic(true))")

	var a, b named
	errA := jsonv1.Unmarshal([]byte(`{"USER_ID":"u1"}`), &a)
	errB := json.Unmarshal([]byte(`{"USER_ID":"u1"}`), &b)
	row(`field name case: {"USER_ID":"u1"} into json:"user_id"`,
		fmt.Sprintf("%q err=%v", a.UserID, errA), fmt.Sprintf("%q err=%v", b.UserID, errB))

	var d1, d2 map[string]int
	errA = jsonv1.Unmarshal([]byte(`{"a":1,"a":2}`), &d1)
	errB = json.Unmarshal([]byte(`{"a":1,"a":2}`), &d2)
	row("duplicate names", fmt.Sprintf("%v err=%v", d1, errA), fmt.Sprintf("%v err=%v", d2, errB))

	row("invalid UTF-8 in a string", show(jsonv1.Marshal("bad\xffbyte")), show(json.Marshal("bad\xffbyte")))

	var buf bytes.Buffer
	_ = jsonv1.NewEncoder(&buf).Encode(1)
	v1enc := fmt.Sprintf("%q", buf.String())
	buf.Reset()
	_ = json.MarshalWrite(&buf, 1)
	w := fmt.Sprintf("MarshalWrite %q", buf.String())
	buf.Reset()
	_ = json.MarshalEncode(jsontext.NewEncoder(&buf), 1)
	row("trailing newline", "Encoder.Encode "+v1enc, w+fmt.Sprintf(", MarshalEncode %q", buf.String()))

	var x any
	errA = jsonv1.NewDecoder(strings.NewReader("")).Decode(&x)
	errB = json.UnmarshalRead(strings.NewReader(""), &x)
	row("empty body", fmt.Sprintf("Decoder.Decode: %v (io.EOF: %v)", errA, errors.Is(errA, io.EOF)),
		fmt.Sprintf("UnmarshalRead: %v (io.EOF: %v, io.ErrUnexpectedEOF: %v)", errB, errors.Is(errB, io.EOF), errors.Is(errB, io.ErrUnexpectedEOF)))

	var l legacy
	errB = json.Unmarshal([]byte(`{"k":"v","k":"w"}`), &l, jsontext.AllowDuplicateNames(true))
	row("call-site option and a v1-style UnmarshalJSON method", "n/a",
		fmt.Sprintf("AllowDuplicateNames(true) at the call, inside the method: %s (outer err=%v)", l.seen, errB))
}
