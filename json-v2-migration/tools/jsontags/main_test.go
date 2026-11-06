package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The probe module declares one field of every kind the tool decides on.
// Its test marshals the same values with v1 before the rewrite and with v2
// after it; the two outputs must be byte-identical.
const probeTypes = `package probe

import (
	"encoding/json"
	"time"
)

type Level int

func (l Level) MarshalJSON() ([]byte, error) { return []byte("\"lvl\""), nil }

type Name string

func (n Name) MarshalText() ([]byte, error) {
	if n == "" {
		return []byte("anon"), nil
	}
	return []byte(n), nil
}

type Inner struct {
	X int ` + "`json:\"x,omitempty\"`" + `
}

type T struct {
	B    bool           ` + "`json:\"b,omitempty\"`" + `
	I    int            ` + "`json:\"i,omitempty\"`" + `
	F    float64        ` + "`json:\"f,omitempty\"`" + `
	S    string         ` + "`json:\"s,omitempty\"`" + `
	P    *string        ` + "`json:\"p,omitempty\"`" + `
	PI   *Inner         ` + "`json:\"pi,omitempty\"`" + `
	A    any            ` + "`json:\"a,omitempty\"`" + `
	St   Inner          ` + "`json:\"st,omitempty\"`" + `
	Tm   time.Time      ` + "`json:\"tm,omitempty\"`" + `
	PT   *time.Time     ` + "`json:\"pt,omitempty\"`" + `
	R    json.RawMessage ` + "`json:\"r,omitempty\"`" + `
	Sl   []int          ` + "`json:\"sl,omitempty\"`" + `
	M    map[string]int ` + "`json:\"m,omitempty\"`" + `
	L    Level          ` + "`json:\"l,omitempty\"`" + `
	N    Name           ` + "`json:\"n,omitempty\"`" + `
	Both int            ` + "`json:\"both,omitempty,omitzero\"`" + `
	Str  int64          ` + "`json:\"str,omitempty,string\"`" + `
}
`

const probeTest = `package probe

import (
	jsonv1 "encoding/json"
	"encoding/json/v2"
	"os"
	"testing"
	"time"
)

func TestDump(t *testing.T) {
	e := ""
	var zt time.Time
	values := []T{
		{},
		{P: &e, PI: &Inner{}, A: 0, St: Inner{X: 1}, Sl: []int{}, M: map[string]int{}, PT: &zt, R: jsonv1.RawMessage("{}")},
		{B: true, I: 1, F: 0.5, S: "s", L: 2, N: "x", Both: 3, Str: 7, Tm: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), R: jsonv1.RawMessage("null")},
	}
	var out []byte
	for _, v := range values {
		var b []byte
		var err error
		if os.Getenv("V2") == "" {
			b, err = jsonv1.Marshal(v)
		} else {
			// nil-as-null is a separate decision from omission; pin it here
			// so the comparison isolates what the tags control.
			b, err = json.Marshal(v, json.FormatNilSliceAsNull(true), json.FormatNilMapAsNull(true))
		}
		if err != nil {
			t.Fatal(err)
		}
		out = append(append(out, b...), '\n')
	}
	if err := os.WriteFile(os.Getenv("OUT"), out, 0o644); err != nil {
		t.Fatal(err)
	}
}
`

func TestRewriteKeepsV1Output(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the tool and a probe module")
	}
	dir := t.TempDir()
	tool := filepath.Join(dir, "jsontags")
	run(t, ".", "go", "build", "-o", tool, ".")

	mod := filepath.Join(dir, "probe")
	write(t, filepath.Join(mod, "go.mod"), "module probe\n\ngo 1.27.0\n")
	write(t, filepath.Join(mod, "types.go"), probeTypes)
	write(t, filepath.Join(mod, "probe_test.go"), probeTest)

	v1 := filepath.Join(dir, "v1.txt")
	v2 := filepath.Join(dir, "v2.txt")
	runEnv(t, mod, []string{"OUT=" + v1}, "go", "test", "-count=1", "-run", "TestDump", ".")
	run(t, mod, tool, "-w", "./...")
	runEnv(t, mod, []string{"OUT=" + v2, "V2=1"}, "go", "test", "-count=1", "-run", "TestDump", ".")

	a, err := os.ReadFile(v1)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(v2)
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Errorf("v2 output after the rewrite differs from v1 output before it\nv1:\n%s\nv2:\n%s", a, b)
	}
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func run(t *testing.T, dir string, name string, args ...string) {
	t.Helper()
	runEnv(t, dir, nil, name, args...)
}

func runEnv(t *testing.T, dir string, env []string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
}
