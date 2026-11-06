// Command figures draws the post's charts as SVG, each in a light and a dark
// variant (name.svg and name-dark.svg), from the analyzer output, the repo
// census and the timeline. Repositories and modules are labeled by the role
// column of the names table, so a reader who does not know the codebase
// can read them.
//
// Usage:
//
//	figures -stats data/final/stats.json -census data/census-pre.tsv \
//	    -timeline data/timeline.tsv -names data/names.tsv -out figures
package main

import (
	"bufio"
	"encoding/json/v2"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type theme struct {
	name, surface, ink, ink2, muted, grid, axis, s1, s2 string
}

// Values from the dataviz reference palette; slots 1 and 2 pass every
// categorical check in both modes (validate_palette.js).
var themes = []theme{
	{"light", "#fcfcfb", "#0b0b0b", "#52514e", "#898781", "#e1e0d9", "#c3c2b7", "#2a78d6", "#eb6834"},
	{"dark", "#1a1a19", "#f0efec", "#c3c2b7", "#898781", "#2c2c2a", "#383835", "#3987e5", "#d95926"},
}

const font = `system-ui, -apple-system, "Segoe UI", sans-serif`

type agg struct {
	From, To, Unit, Module string
	N                      int
	Geomean, GeoLo, GeoHi  float64
	Improved, Regressed    int
	NoChange               int
	Q10, Q50, Q90          float64
}

type cmp struct {
	From        string  `json:"from"`
	To          string  `json:"to"`
	Ratio       float64 `json:"ratio"`
	P           float64 `json:"p"`
	Sig         bool    `json:"significant"`
	PairedRatio float64 `json:"paired_ratio"`
}

// ratio is the paired ratio when the run paired its rounds, else the ratio
// of medians.
func (c cmp) ratio() float64 {
	if c.PairedRatio > 0 {
		return c.PairedRatio
	}
	return c.Ratio
}

type record struct {
	Module string `json:"module"`
	Pkg    string `json:"pkg"`
	Name   string `json:"name"`
	Unit   string `json:"unit"`
	Cmps   []cmp  `json:"comparisons"`
}

type stats struct {
	Records    []record `json:"records"`
	Aggregates []agg    `json:"aggregates"`
}

func main() {
	statsPath := flag.String("stats", "", "analyzer output of the final A/B run")
	censusPath := flag.String("census", "", "census TSV of the repos before the migration")
	timelinePath := flag.String("timeline", "", "timeline TSV: start, end, label")
	namesPath := flag.String("names", "", "names TSV: key, role, repository, visibility")
	out := flag.String("out", "figures", "output directory")
	flag.Parse()
	if *namesPath != "" {
		for _, row := range readTSV(*namesPath) {
			if len(row) < 2 {
				fail(fmt.Errorf("names: a row without a role: %q", row))
			}
			names[row[0]] = row[1]
		}
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		fail(err)
	}
	var st stats
	if *statsPath != "" {
		b, err := os.ReadFile(*statsPath)
		if err != nil {
			fail(err)
		}
		if err := json.Unmarshal(b, &st); err != nil {
			fail(err)
		}
	}
	for _, t := range themes {
		suffix := ""
		if t.name == "dark" {
			suffix = "-dark"
		}
		write := func(name, svg string) {
			if svg == "" {
				return
			}
			if err := os.WriteFile(filepath.Join(*out, name+suffix+".svg"), []byte(svg), 0o644); err != nil {
				fail(err)
			}
		}
		if *censusPath != "" {
			write("size", sizeChart(t, readTSV(*censusPath)))
		}
		if *timelinePath != "" {
			write("timeline", timelineChart(t, readTSV(*timelinePath)))
		}
		if *statsPath != "" {
			write("per-repo", perRepo(t, st))
			write("distribution", distribution(t, st))
			write("engines", engines(t, st))
			write("memory", memory(t, st))
		}
	}
}

// ---- data helpers

func readTSV(path string) [][]string {
	f, err := os.Open(path)
	if err != nil {
		fail(err)
	}
	defer f.Close()
	var rows [][]string
	sc := bufio.NewScanner(f)
	first := true
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if first {
			first = false
			continue // header
		}
		rows = append(rows, strings.Split(line, "\t"))
	}
	if err := sc.Err(); err != nil {
		fail(err)
	}
	return rows
}

func atof(s string) float64 {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		fail(fmt.Errorf("number %q: %w", s, err))
	}
	return v
}

// names maps a repository or module key to the role it is labeled by.
var names = map[string]string{}

// display is the label of a repository or module: its role when the names
// table has one, else its key with "/" restored.
func display(module string) string {
	if r, ok := names[module]; ok {
		return r
	}
	return strings.ReplaceAll(module, "_", "/")
}

func aggsFor(st stats, from, to, unit string) map[string]agg {
	m := map[string]agg{}
	for _, a := range st.Aggregates {
		if a.From == from && a.To == to && a.Unit == unit {
			m[a.Module] = a
		}
	}
	return m
}

func ratios(st stats, from, to, unit string) []float64 {
	var rs []float64
	for _, r := range st.Records {
		if r.Unit != unit {
			continue
		}
		for _, c := range r.Cmps {
			if r := c.ratio(); c.From == from && c.To == to && r > 0 && !math.IsNaN(r) {
				rs = append(rs, r)
			}
		}
	}
	return rs
}

// ---- SVG primitives

type canvas struct {
	b    strings.Builder
	t    theme
	w, h float64
}

func newCanvas(t theme, w, h float64, title, subtitle string) *canvas {
	c := &canvas{t: t, w: w, h: h}
	fmt.Fprintf(&c.b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %g %g" width="%g" height="%g" font-family='%s' role="img">`, w, h, w, h, font)
	fmt.Fprintf(&c.b, `<title>%s</title>`, esc(title))
	fmt.Fprintf(&c.b, `<rect width="%g" height="%g" rx="8" fill="%s"/>`, w, h, t.surface)
	c.text(24, 30, title, 15, t.ink, "start", "600")
	for i, l := range strings.Split(subtitle, "\n") {
		if l != "" {
			c.text(24, 50+16*float64(i), l, 12, t.ink2, "start", "400")
		}
	}
	return c
}

// extra is the height a text's lines after the first add, at 16px each.
func extra(s string) float64 { return 16 * float64(strings.Count(s, "\n")) }

func (c *canvas) done() string { c.b.WriteString(`</svg>`); return c.b.String() }

func (c *canvas) text(x, y float64, s string, size float64, fill, anchor, weight string) {
	fmt.Fprintf(&c.b, `<text x="%.1f" y="%.1f" font-size="%g" fill="%s" text-anchor="%s" font-weight="%s">%s</text>`,
		x, y, size, fill, anchor, weight, esc(s))
}

func (c *canvas) tick(x, y float64, s string, anchor string) {
	fmt.Fprintf(&c.b, `<text x="%.1f" y="%.1f" font-size="11" fill="%s" text-anchor="%s" font-variant-numeric="tabular-nums">%s</text>`,
		x, y, c.t.muted, anchor, esc(s))
}

// head labels a column or an axis: what the rows or values are.
func (c *canvas) head(x, y float64, s, anchor string) {
	c.text(x, y, s, 11, c.t.ink2, anchor, "600")
}

// axisTitle says what a position on the horizontal axis measures.
func (c *canvas) axisTitle(x, y float64, s string) {
	c.text(x, y, s, 12, c.t.ink2, "middle", "400")
}

func (c *canvas) line(x1, y1, x2, y2 float64, stroke string, width float64) {
	fmt.Fprintf(&c.b, `<line x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f" stroke="%s" stroke-width="%g" stroke-linecap="round"/>`,
		x1, y1, x2, y2, stroke, width)
}

// hbar draws a horizontal bar from x0 to x1: square at the baseline, 4px
// rounded at the data end.
func (c *canvas) hbar(x0, x1, y, h float64, fill, tip string) {
	r := math.Min(4, math.Min(h/2, math.Abs(x1-x0)/2))
	if x1 < x0+0.5 {
		x1 = x0 + 0.5
	}
	fmt.Fprintf(&c.b, `<path d="M%.1f %.1f H%.1f A%g %g 0 0 1 %.1f %.1f V%.1f A%g %g 0 0 1 %.1f %.1f H%.1f Z" fill="%s"><title>%s</title></path>`,
		x0, y, x1-r, r, r, x1, y+r, y+h-r, r, r, x1-r, y+h, x0, fill, esc(tip))
}

// vbar draws a column from the baseline y0 up to y1, rounded on top.
func (c *canvas) vbar(x, w, y0, y1 float64, fill, tip string) {
	if y0-y1 < 0.5 {
		return
	}
	r := math.Min(4, math.Min(w/2, (y0-y1)/2))
	fmt.Fprintf(&c.b, `<path d="M%.1f %.1f V%.1f A%g %g 0 0 1 %.1f %.1f H%.1f A%g %g 0 0 1 %.1f %.1f V%.1f Z" fill="%s"><title>%s</title></path>`,
		x, y0, y1+r, r, r, x+r, y1, x+w-r, r, r, x+w, y1+r, y0, fill, esc(tip))
}

// dot draws a marker with a 2px surface ring.
func (c *canvas) dot(x, y float64, fill, tip string) {
	fmt.Fprintf(&c.b, `<circle cx="%.1f" cy="%.1f" r="5" fill="%s" stroke="%s" stroke-width="2"><title>%s</title></circle>`,
		x, y, fill, c.t.surface, esc(tip))
}

// legend draws swatch + name pairs left to right starting at x.
func (c *canvas) legend(x, y float64, names []string, colors []string) {
	for i, n := range names {
		fmt.Fprintf(&c.b, `<circle cx="%.1f" cy="%.1f" r="5" fill="%s"/>`, x+5, y-4, colors[i])
		c.text(x+16, y, n, 12, c.t.ink2, "start", "400")
		x += 16 + float64(len(n))*6.6 + 22
	}
}

func esc(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return r.Replace(s)
}

// ---- scales

type logScale struct{ lo, hi, x0, x1 float64 }

func (s logScale) at(v float64) float64 {
	return s.x0 + (math.Log(v)-math.Log(s.lo))/(math.Log(s.hi)-math.Log(s.lo))*(s.x1-s.x0)
}

var logTicks = []float64{0.1, 0.2, 0.25, 1.0 / 3, 0.5, 2.0 / 3, 0.8, 1, 1.25, 1.5, 2, 3, 4, 5, 10}

func ratioLabel(v float64) string {
	switch {
	case math.Abs(v-1.0/3) < 1e-9:
		return "0.33×"
	case math.Abs(v-2.0/3) < 1e-9:
		return "0.67×"
	}
	return strconv.FormatFloat(v, 'f', -1, 64) + "×"
}

// ratioDomain pads the data range and keeps 1 inside it.
func ratioDomain(vals []float64) (float64, float64) {
	lo, hi := 1.0, 1.0
	for _, v := range vals {
		if v > 0 && !math.IsNaN(v) && !math.IsInf(v, 0) {
			lo = math.Min(lo, v)
			hi = math.Max(hi, v)
		}
	}
	return lo / 1.15, hi * 1.15
}

func pct(r float64) string {
	d := (r - 1) * 100
	if math.Abs(d) < 0.5 {
		return "±0%"
	}
	return fmt.Sprintf("%+.0f%%", d)
}

// ---- charts

// Every chart names both of its axes: a header over the row labels says
// what a row is, and a title under the tick labels says what the horizontal
// position measures.

// sizeChart ranks repositories by lines of Go; the column on the right gives
// the count of files that imported encoding/json.
func sizeChart(t theme, rows [][]string) string {
	type r struct {
		repo         string
		lines, files float64
	}
	var rs []r
	var totalLines, totalFiles float64
	for _, row := range rows {
		x := r{row[0], atof(row[4]), atof(row[5])}
		if x.files == 0 {
			continue
		}
		rs = append(rs, x)
		totalLines += x.lines
		totalFiles += x.files
	}
	sort.Slice(rs, func(i, j int) bool { return rs[i].lines > rs[j].lines })
	const left, right, top, rowH = 170.0, 150.0, 100.0, 20.0
	w := 760.0
	bottom := top + rowH*float64(len(rs))
	h := bottom + 52
	c := newCanvas(t, w, h, "Lines of Go and encoding/json imports, by repository",
		fmt.Sprintf("%d repositories, %s lines of Go and %s files importing encoding/json, counted before the migration",
			len(rs), thousands(totalLines), thousands(totalFiles)))
	maxV := rs[0].lines
	scale := func(v float64) float64 { return left + v/maxV*(w-left-right-56) }
	for _, k := range []float64{0, 50e3, 100e3, 150e3, 200e3, 250e3} {
		if k > maxV {
			break
		}
		x := scale(k)
		c.line(x, top-6, x, bottom, t.grid, 1)
		c.tick(x, bottom+16, compact(k), "middle")
	}
	c.head(left-10, top-12, "Repository", "end")
	c.head(w-right+14, top-26, "Files importing", "start")
	c.head(w-right+14, top-12, "encoding/json", "start")
	for i, x := range rs {
		y := top + rowH*float64(i)
		c.text(left-10, y+14, display(x.repo), 12, t.ink2, "end", "400")
		c.hbar(left, scale(x.lines), y+4, 12, t.s1, fmt.Sprintf("%s: %s lines of Go, %s files importing encoding/json", display(x.repo), thousands(x.lines), thousands(x.files)))
		c.text(scale(x.lines)+6, y+14, compact(x.lines), 11, t.ink2, "start", "400")
		c.text(w-right+14, y+14, thousands(x.files), 12, t.ink, "start", "400")
	}
	c.line(left, top-6, left, bottom, t.axis, 1)
	c.axisTitle((left+w-right-56)/2, bottom+36, "Lines of Go in the repository, tests included")
	return c.done()
}

// timelineChart draws the steps of the work on a wall-clock axis in hours
// from the first start.
func timelineChart(t theme, rows [][]string) string {
	type ph struct {
		start, end time.Time
		label      string
	}
	var ps []ph
	var t0, t1 time.Time
	for _, row := range rows {
		s, err := time.Parse(time.RFC3339, row[0])
		if err != nil {
			fail(err)
		}
		e, err := time.Parse(time.RFC3339, row[1])
		if err != nil {
			fail(err)
		}
		ps = append(ps, ph{s, e, row[2]})
		if t0.IsZero() || s.Before(t0) {
			t0 = s
		}
		if e.After(t1) {
			t1 = e
		}
	}
	const left, right, top, rowH = 270.0, 70.0, 92.0, 24.0
	w := 840.0
	bottom := top + rowH*float64(len(ps))
	h := bottom + 52
	total := t1.Sub(t0).Hours()
	c := newCanvas(t, w, h, "Timeline of the migration",
		fmt.Sprintf("%s of wall-clock time from the start of the migration to the final measurement; bars are the steps",
			hm(t1.Sub(t0))))
	scale := func(d time.Duration) float64 { return left + d.Hours()/total*(w-left-right) }
	step := 1.0
	switch {
	case total > 12:
		step = 2
	case total < 4:
		step = 0.5
	}
	for k := 0.0; k <= total+1e-9; k += step {
		x := left + k/total*(w-left-right)
		c.line(x, top-6, x, bottom, t.grid, 1)
		c.tick(x, bottom+16, fmt.Sprintf("%gh", k), "middle")
	}
	c.head(left-10, top-12, "Step", "end")
	for i, p := range ps {
		y := top + rowH*float64(i)
		c.text(left-10, y+16, p.label, 12, t.ink2, "end", "400")
		x0, x1 := scale(p.start.Sub(t0)), scale(p.end.Sub(t0))
		c.hbar(x0, math.Max(x1, x0+3), y+6, 12, t.s1, fmt.Sprintf("%s: %s", p.label, hm(p.end.Sub(p.start))))
		c.text(math.Max(x1, x0+3)+6, y+16, hm(p.end.Sub(p.start)), 11, t.ink2, "start", "400")
	}
	c.axisTitle((left+w-right)/2, bottom+36,
		fmt.Sprintf("Hours since the migration began, %s UTC on %s", t0.Format("15:04"), t0.Format("January 2, 2006")))
	return c.done()
}

type pt struct{ v, lo, hi float64 }

// dotChart is one row per label with one marker per series on a log ratio
// axis, a reference line at 1 and optional interval lines.
type dotChart struct {
	title, subtitle string
	rowHead         string // what a row is, over the row labels
	axis            string // what the horizontal position measures
	note            string // optional lines under the axis title
	countHead       string // optional column on the right: its header
	counts          []int  // and one number per row
	series          []string
	labels          []string
	vals            [][]pt
}

func dotRows(t theme, d dotChart) string {
	const left, rowH = 210.0, 22.0
	right := 50.0
	if d.counts != nil {
		right = 120 // room for the count column
	}
	top := 86.0 + extra(d.subtitle)
	if len(d.series) > 1 {
		top += 24 // room for the legend
	}
	w := 760.0
	bottom := top + rowH*float64(len(d.labels))
	h := bottom + 52
	if d.note != "" {
		h += 18 + extra(d.note)
	}
	c := newCanvas(t, w, h, d.title, d.subtitle)
	colors := []string{t.s1, t.s2}
	if len(d.series) > 1 {
		c.legend(24, 74+extra(d.subtitle), d.series, colors)
	}
	var all []float64
	for _, row := range d.vals {
		for _, p := range row {
			all = append(all, p.v, p.lo, p.hi)
		}
	}
	lo, hi := ratioDomain(all)
	s := logScale{lo, hi, left, w - right}
	for _, k := range logTicks {
		if k < lo || k > hi {
			continue
		}
		x := s.at(k)
		stroke := t.grid
		if k == 1 {
			stroke = t.axis
		}
		c.line(x, top-8, x, bottom, stroke, 1)
		c.tick(x, bottom+16, ratioLabel(k), "middle")
	}
	c.head(left-10, top-12, d.rowHead, "end")
	if d.counts != nil {
		c.head(w-24, top-12, d.countHead, "end")
	}
	for i, label := range d.labels {
		y := top + rowH*float64(i) + rowH/2
		c.text(left-10, y+4, label, 12, t.ink2, "end", "400")
		if d.counts != nil {
			c.text(w-24, y+4, strconv.Itoa(d.counts[i]), 12, t.ink, "end", "400")
		}
		for j, p := range d.vals[i] {
			if p.v <= 0 || math.IsNaN(p.v) {
				continue
			}
			dy := 0.0
			if len(d.series) > 1 {
				dy = float64(j)*6 - 3
			}
			if !math.IsNaN(p.lo) && !math.IsNaN(p.hi) && p.lo > 0 {
				c.line(s.at(p.lo), y+dy, s.at(p.hi), y+dy, colors[j], 2)
			}
			tip := fmt.Sprintf("%s, %s: %s (%s)", label, d.series[j], ratioLabel(round3(p.v)), pct(p.v))
			c.dot(s.at(p.v), y+dy, colors[j], tip)
		}
	}
	c.axisTitle((left+w-right)/2, bottom+36, d.axis)
	for i, l := range strings.Split(d.note, "\n") {
		c.text((left+w-right)/2, bottom+54+16*float64(i), l, 11, t.muted, "middle", "400")
	}
	return c.done()
}

func round3(v float64) float64 { return math.Round(v*1000) / 1000 }

// moduleOrder sorts modules by a geomean, best first, with ALL last.
func moduleOrder(m map[string]agg) []string {
	var keys []string
	for k := range m {
		if k != "ALL" {
			keys = append(keys, k)
		}
	}
	// Equal geomeans (modules that allocate the same before and after) fall
	// back to the key, so the same data draws the same figure every run.
	sort.Slice(keys, func(i, j int) bool {
		if gi, gj := m[keys[i]].Geomean, m[keys[j]].Geomean; gi != gj {
			return gi < gj
		}
		return keys[i] < keys[j]
	})
	if _, ok := m["ALL"]; ok {
		keys = append(keys, "ALL")
	}
	return keys
}

func labelOf(k string) string {
	if k == "ALL" {
		return "all benchmarks together"
	}
	return display(k)
}

const intervalNote = "Lines show the 95% bootstrap interval over the benchmarks in each row."

func perRepo(t theme, st stats) string {
	m := aggsFor(st, "base", "after", "sec/op")
	if len(m) == 0 {
		return ""
	}
	keys := moduleOrder(m)
	var labels []string
	var counts []int
	var vals [][]pt
	for _, k := range keys {
		a := m[k]
		labels = append(labels, labelOf(k))
		counts = append(counts, a.N)
		vals = append(vals, []pt{{a.Geomean, a.GeoLo, a.GeoHi}})
	}
	return dotRows(t, dotChart{
		title:     "Change in time per operation after the migration, by repository",
		subtitle:  "Each dot: time after the migration ÷ time before, geometric mean over the repository's benchmarks",
		rowHead:   "Repository",
		axis:      "Time after ÷ time before, log scale; left of 1× is faster",
		note:      intervalNote,
		countHead: "Benchmarks",
		counts:    counts,
		series:    []string{"time after ÷ time before"},
		labels:    labels,
		vals:      vals,
	})
}

func engines(t theme, st stats) string {
	base := aggsFor(st, "classic", "base", "sec/op")
	after := aggsFor(st, "classic", "after", "sec/op")
	if len(base) == 0 || len(after) == 0 {
		return ""
	}
	keys := moduleOrder(after)
	var labels []string
	var vals [][]pt
	for _, k := range keys {
		labels = append(labels, labelOf(k))
		b, a := base[k], after[k]
		vals = append(vals, []pt{{b.Geomean, math.NaN(), math.NaN()}, {a.Geomean, math.NaN(), math.NaN()}})
	}
	return dotRows(t, dotChart{
		title:    "Time per operation against Go's classic JSON engine, by repository",
		subtitle: "1× is encoding/json on its original engine (GOEXPERIMENT=nojsonv2). Each dot is the geometric mean\nover the repository's benchmarks of its time ÷ the time on that engine.",
		rowHead:  "Repository",
		axis:     "Time ÷ time on the classic engine, log scale; left of 1× is faster",
		series:   []string{"Go 1.27 default, our code unchanged (v1 API on the v2 engine)", "After the migration (v2 API)"},
		labels:   labels,
		vals:     vals,
	})
}

func memory(t theme, st stats) string {
	bytes := aggsFor(st, "base", "after", "B/op")
	allocs := aggsFor(st, "base", "after", "allocs/op")
	if len(bytes) == 0 || len(allocs) == 0 {
		return ""
	}
	keys := moduleOrder(allocs)
	var labels []string
	var vals [][]pt
	for _, k := range keys {
		labels = append(labels, labelOf(k))
		b, a := bytes[k], allocs[k]
		vals = append(vals, []pt{{b.Geomean, b.GeoLo, b.GeoHi}, {a.Geomean, a.GeoLo, a.GeoHi}})
	}
	return dotRows(t, dotChart{
		title:    "Change in memory per operation after the migration, by repository",
		subtitle: "Each dot: the value after the migration ÷ the value before, geometric mean over the repository's\nbenchmarks. A benchmark that allocates nothing before and after counts as 1×.",
		rowHead:  "Repository",
		axis:     "After ÷ before, log scale; left of 1× is less memory",
		note:     intervalNote,
		series:   []string{"Bytes allocated per operation", "Allocations per operation"},
		labels:   labels,
		vals:     vals,
	})
}

// distribution draws one histogram per unit of the per-benchmark ratios on a
// log2 axis, as small multiples sharing both axes.
func distribution(t theme, st stats) string {
	// left says what a ratio under 1 means; less and more count the
	// benchmarks on each side.
	units := []struct{ unit, name, left, less, more string }{
		{"sec/op", "Time per operation", "faster", "faster", "slower"},
		{"B/op", "Bytes per operation", "fewer bytes", "fewer", "more"},
		{"allocs/op", "Allocations per operation", "fewer allocations", "fewer", "more"},
	}
	const binW = 0.125 // log2 units, about 9% per bin
	// The axis spans the data in whole log2 units, at most 1/16x to 4x.
	lo, hi := 0.0, 0.0
	for _, u := range units {
		for _, r := range ratios(st, "base", "after", u.unit) {
			lo = math.Min(lo, math.Floor(math.Log2(r)))
			hi = math.Max(hi, math.Ceil(math.Log2(r)))
		}
	}
	lo, hi = math.Max(lo, -4), math.Min(math.Max(hi, 1), 2)
	nb := int((hi - lo) / binW)
	type panel struct {
		name, left, less, more string
		n, better, worse       int
		counts                 []int
	}
	var ps []panel
	maxC := 1
	for _, u := range units {
		rs := ratios(st, "base", "after", u.unit)
		p := panel{name: u.name, left: u.left, less: u.less, more: u.more, n: len(rs), counts: make([]int, nb)}
		for _, r := range rs {
			i := int(math.Floor((math.Log2(r) - lo) / binW))
			p.counts[max(0, min(nb-1, i))]++
			if r < 0.99 {
				p.better++
			} else if r > 1.01 {
				p.worse++
			}
		}
		for _, n := range p.counts {
			maxC = max(maxC, n)
		}
		ps = append(ps, p)
	}
	step := niceStep(maxC, 4)
	yMax := float64((maxC + step - 1) / step * step)
	const sub = "A benchmark's ratio is its value after the migration ÷ its value before. Each bar counts the benchmarks whose ratio falls in its range;\nthe outermost bars also hold everything beyond them."
	const panelW, panelH, gap, yAxisW = 300.0, 170.0, 44.0, 40.0
	top := 100.0 + extra(sub)
	w := 24 + yAxisW + 3*panelW + 2*gap + 24
	h := top + panelH + 76
	c := newCanvas(t, w, h, "Change per benchmark after the migration", sub)
	base := top + panelH
	yAt := func(n float64) float64 { return base - n/yMax*panelH }
	c.head(24, top-30, "Number of benchmarks", "start")
	for p, pn := range ps {
		x0 := 24 + yAxisW + float64(p)*(panelW+gap)
		for k := step; float64(k) <= yMax; k += step {
			c.line(x0, yAt(float64(k)), x0+panelW, yAt(float64(k)), t.grid, 1)
			if p == 0 {
				c.tick(x0-6, yAt(float64(k))+4, strconv.Itoa(k), "end")
			}
		}
		if p == 0 {
			c.tick(x0-6, base+4, "0", "end")
		}
		c.head(x0, top-10, pn.name, "start")
		bw := panelW / float64(nb)
		for i, n := range pn.counts {
			l0, l1 := lo+float64(i)*binW, lo+float64(i+1)*binW
			tip := fmt.Sprintf("%s: %d benchmarks between %s and %s", pn.name, n, ratioLabel(round3(math.Exp2(l0))), ratioLabel(round3(math.Exp2(l1))))
			c.vbar(x0+float64(i)*bw+1, bw-2, base, yAt(float64(n)), t.s1, tip)
		}
		c.line(x0, base, x0+panelW, base, t.axis, 1)
		tickStep := 1.0
		if hi-lo > 4 {
			tickStep = 2
		}
		for lk := math.Ceil(lo/tickStep) * tickStep; lk <= hi; lk += tickStep {
			x := x0 + (lk-lo)/(hi-lo)*panelW
			anchor := "middle"
			switch lk {
			case lo:
				anchor = "start"
			case hi:
				anchor = "end"
			}
			c.line(x, base, x, base+4, t.axis, 1)
			c.tick(x, base+17, pow2Label(lk), anchor)
		}
		x1 := x0 + (0-lo)/(hi-lo)*panelW
		c.line(x1, top, x1, base, t.axis, 1)
		c.axisTitle(x0+panelW/2, base+36, "After ÷ before, log scale; left of 1× is "+pn.left)
		c.text(x0+panelW/2, base+54, fmt.Sprintf("%d benchmarks: %d %s, %d %s, %d within 1%%", pn.n, pn.better, pn.less, pn.worse, pn.more, pn.n-pn.better-pn.worse), 11, t.muted, "middle", "400")
	}
	return c.done()
}

// pow2Label labels the tick at 2^k: 1/4×, 1×, 4×.
func pow2Label(k float64) string {
	if k < 0 {
		return fmt.Sprintf("1/%g×", math.Exp2(-k))
	}
	return fmt.Sprintf("%g×", math.Exp2(k))
}

// niceStep is the smallest of 1, 2, 2.5 and 5 times a power of ten that
// splits 0 to maxV into at most n steps.
func niceStep(maxV, n int) int {
	for p := 1; ; p *= 10 {
		for _, m := range []int{p, 2 * p, 5 * p / 2, 5 * p} {
			if m > 0 && (maxV+m-1)/m <= n {
				return m
			}
		}
	}
}

// ---- formatting

func thousands(v float64) string {
	s := strconv.FormatInt(int64(math.Round(v)), 10)
	var out []byte
	for i := range len(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, s[i])
	}
	return string(out)
}

func compact(v float64) string {
	if v >= 1000 {
		return strconv.FormatFloat(math.Round(v/1000), 'f', -1, 64) + "K"
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

func hm(d time.Duration) string {
	d = d.Round(time.Minute)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	switch {
	case h == 0:
		return fmt.Sprintf("%dm", m)
	case m == 0:
		return fmt.Sprintf("%dh", h)
	}
	return fmt.Sprintf("%dh %02dm", h, m)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
