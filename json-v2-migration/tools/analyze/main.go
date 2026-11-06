// Command analyze reads go test -bench output for several variants and writes
// per-benchmark statistics (median, 95% CI, Mann-Whitney U p-value, ratio)
// plus per-module and overall geometric means.
//
// Usage: analyze -out stats.json -pairs base:after,classic:base [-paired] NAME=DIR...
// Each pair is FROM:TO, and a ratio is the TO value divided by the FROM value.
// Each DIR holds one <module>.txt per module, as written by bench.sh or
// bench-paired.sh.
//
// With -paired, the i-th result of one variant is taken to have run next to
// the i-th result of another, as bench-paired.sh writes them: each
// comparison then also carries the median of the per-round ratios and an
// exact two-sided sign test over the rounds, and that test, not the
// Mann-Whitney U test, decides whether a benchmark counts as changed.
package main

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"flag"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"golang.org/x/perf/benchfmt"
	"golang.org/x/perf/benchmath"
)

type key struct{ Module, Pkg, Name, Unit string }

type variantStat struct {
	N      int       `json:"n"`
	Median float64   `json:"median"`
	Lo     float64   `json:"lo"`
	Hi     float64   `json:"hi"`
	Values []float64 `json:"values"`
}

type cmp struct {
	From  string  `json:"from"`
	To    string  `json:"to"`
	Ratio float64 `json:"ratio"` // to/from of medians
	P     float64 `json:"p"`     // Mann-Whitney U
	Sig   bool    `json:"significant"`
	// Set with -paired: the median of to_i/from_i over the rounds, and the
	// sign test's p over the rounds in which the two differ.
	PairedRatio float64 `json:"paired_ratio,omitzero"`
	PairedP     float64 `json:"paired_p,omitzero"`
	Rounds      int     `json:"rounds,omitzero"`
}

type record struct {
	Module   string                 `json:"module"`
	Pkg      string                 `json:"pkg"`
	Name     string                 `json:"name"`
	Unit     string                 `json:"unit"`
	Variants map[string]variantStat `json:"variants"`
	Cmps     []cmp                  `json:"comparisons"`
}

type agg struct {
	From, To      string
	Unit          string
	Module        string
	N             int
	Geomean       float64
	GeoLo, GeoHi  float64 // 95% percentile-bootstrap interval of the geomean
	Improved      int
	Regressed     int
	NoChange      int
	Q10, Q50, Q90 float64
}

func main() {
	out := flag.String("out", "stats.json", "output file")
	pairs := flag.String("pairs", "", "comma-separated from:to variant pairs")
	alpha := flag.Float64("alpha", 0.05, "significance level")
	paired := flag.Bool("paired", false, "the i-th results of two variants ran side by side; decide by the sign test")
	flag.Parse()

	data := map[key]map[string][]float64{}
	var variants []string
	for _, arg := range flag.Args() {
		name, dir, ok := strings.Cut(arg, "=")
		if !ok {
			fail("bad variant %q", arg)
		}
		variants = append(variants, name)
		files, err := filepath.Glob(filepath.Join(dir, "*.txt"))
		if err != nil {
			fail("%v", err)
		}
		for _, f := range files {
			module := strings.TrimSuffix(filepath.Base(f), ".txt")
			fh, err := os.Open(f)
			if err != nil {
				fail("%v", err)
			}
			r := benchfmt.NewReader(fh, f)
			for r.Scan() {
				res, ok := r.Result().(*benchfmt.Result)
				if !ok {
					continue
				}
				pkg := res.GetConfig("pkg")
				full := string(res.Name.Full())
				// strip the -GOMAXPROCS suffix so variants line up
				if i := strings.LastIndex(full, "-"); i > 0 && strings.Trim(full[i+1:], "0123456789") == "" {
					full = full[:i]
				}
				for _, v := range res.Values {
					k := key{module, pkg, full, v.Unit}
					if data[k] == nil {
						data[k] = map[string][]float64{}
					}
					data[k][name] = append(data[k][name], v.Value)
				}
			}
			if err := r.Err(); err != nil {
				fail("%s: %v", f, err)
			}
			fh.Close()
		}
	}

	var ps [][2]string
	for _, p := range strings.Split(*pairs, ",") {
		if p == "" {
			continue
		}
		a, b, ok := strings.Cut(p, ":")
		if !ok {
			fail("bad pair %q", p)
		}
		ps = append(ps, [2]string{a, b})
	}

	thr := benchmath.DefaultThresholds
	thr.CompareAlpha = *alpha
	var recs []record
	ratios := map[[4]string][]float64{} // from,to,unit,module -> ratios
	counts := map[[4]string]*[3]int{}
	keys := make([]key, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		if a.Module != b.Module {
			return a.Module < b.Module
		}
		if a.Pkg != b.Pkg {
			return a.Pkg < b.Pkg
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.Unit < b.Unit
	})
	for _, k := range keys {
		vs := data[k]
		rec := record{Module: k.Module, Pkg: k.Pkg, Name: k.Name, Unit: k.Unit, Variants: map[string]variantStat{}}
		samples := map[string]*benchmath.Sample{}
		for _, name := range variants {
			vals := vs[name]
			if len(vals) == 0 {
				continue
			}
			// NewSample sorts its slice in place; the paired test needs
			// the values in the order the rounds ran.
			s := benchmath.NewSample(slices.Clone(vals), &thr)
			samples[name] = s
			sum := benchmath.AssumeNothing.Summary(s, 0.95)
			rec.Variants[name] = variantStat{N: len(vals), Median: sum.Center, Lo: sum.Lo, Hi: sum.Hi, Values: s.Values}
		}
		for _, p := range ps {
			a, b := samples[p[0]], samples[p[1]]
			if a == nil || b == nil {
				continue
			}
			c := benchmath.AssumeNothing.Compare(a, b)
			ma, mb := rec.Variants[p[0]].Median, rec.Variants[p[1]].Median
			ratio := math.NaN()
			if ma > 0 {
				ratio = mb / ma
			} else if ma == 0 && mb == 0 {
				ratio = 1
			}
			sig := c.P < c.Alpha
			cm := cmp{From: p[0], To: p[1], Ratio: ratio, P: c.P, Sig: sig}
			if *paired {
				cm.PairedRatio, cm.PairedP, cm.Rounds = pairedTest(vs[p[0]], vs[p[1]])
				if cm.Rounds > 0 {
					sig = cm.PairedP < *alpha
					cm.Sig = sig
					ratio = cm.PairedRatio
				}
			}
			rec.Cmps = append(rec.Cmps, cm)
			for _, m := range []string{k.Module, "ALL"} {
				ak := [4]string{p[0], p[1], k.Unit, m}
				if !math.IsNaN(ratio) && ratio > 0 {
					ratios[ak] = append(ratios[ak], ratio)
				}
				if counts[ak] == nil {
					counts[ak] = &[3]int{}
				}
				switch {
				case !sig || ratio == 1:
					counts[ak][2]++
				case better(k.Unit, ratio):
					counts[ak][0]++
				default:
					counts[ak][1]++
				}
			}
		}
		recs = append(recs, rec)
	}

	var aggs []agg
	for ak, rs := range ratios {
		sort.Float64s(rs)
		logSum := 0.0
		for _, r := range rs {
			logSum += math.Log(r)
		}
		c := counts[ak]
		lo, hi := bootGeomean(rs, ak)
		aggs = append(aggs, agg{From: ak[0], To: ak[1], Unit: ak[2], Module: ak[3], N: len(rs),
			Geomean: math.Exp(logSum / float64(len(rs))), GeoLo: lo, GeoHi: hi,
			Improved: c[0], Regressed: c[1], NoChange: c[2],
			Q10: q(rs, 0.1), Q50: q(rs, 0.5), Q90: q(rs, 0.9)})
	}
	sort.Slice(aggs, func(i, j int) bool {
		a, b := aggs[i], aggs[j]
		if a.From+a.To != b.From+b.To {
			return a.From+a.To < b.From+b.To
		}
		if a.Unit != b.Unit {
			return a.Unit < b.Unit
		}
		return a.Module < b.Module
	})

	doc := map[string]any{"variants": variants, "pairs": ps, "alpha": *alpha, "records": recs, "aggregates": aggs}
	// A bound benchmath cannot compute (too few or tied samples) is not a
	// number JSON can carry; it is written as null.
	finite := json.MarshalToFunc(func(enc *jsontext.Encoder, f float64) error {
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return enc.WriteToken(jsontext.Null)
		}
		return enc.WriteToken(jsontext.Float(f))
	})
	b, err := json.Marshal(doc, jsontext.WithIndent(" "), json.Deterministic(true), json.WithMarshalers(finite))
	if err != nil {
		fail("%v", err)
	}
	if err := os.WriteFile(*out, b, 0o644); err != nil {
		fail("%v", err)
	}
	for _, a := range aggs {
		if a.Module == "ALL" {
			fmt.Printf("%s->%s %-10s n=%4d geomean=%.3f [%.3f, %.3f] improved=%d regressed=%d nochange=%d p10=%.3f p50=%.3f p90=%.3f\n",
				a.From, a.To, a.Unit, a.N, a.Geomean, a.GeoLo, a.GeoHi, a.Improved, a.Regressed, a.NoChange, a.Q10, a.Q50, a.Q90)
		}
	}
}

// pairedTest pairs the i-th value of from with the i-th value of to. It
// returns the median of the ratios to/from, the exact two-sided sign-test p
// over the pairs that differ, and the number of pairs. Unequal or empty
// samples have no pairing and return zero pairs.
func pairedTest(from, to []float64) (ratio, p float64, n int) {
	if len(from) == 0 || len(from) != len(to) {
		return 0, 0, 0
	}
	var rs []float64
	less, more := 0, 0
	for i := range from {
		if from[i] <= 0 {
			continue
		}
		rs = append(rs, to[i]/from[i])
		switch {
		case to[i] < from[i]:
			less++
		case to[i] > from[i]:
			more++
		}
	}
	if len(rs) == 0 {
		return 0, 0, 0
	}
	sort.Float64s(rs)
	return q(rs, 0.5), signTest(less, more), len(rs)
}

// signTest is the exact two-sided binomial test of k successes in k+m
// trials against one half.
func signTest(k, m int) float64 {
	n := k + m
	if n == 0 {
		return 1
	}
	small := min(k, m)
	tail := 0.0
	for i := 0; i <= small; i++ {
		tail += math.Exp(lchoose(n, i) - float64(n)*math.Ln2)
	}
	return math.Min(1, 2*tail)
}

func lchoose(n, k int) float64 {
	a, _ := math.Lgamma(float64(n + 1))
	b, _ := math.Lgamma(float64(k + 1))
	c, _ := math.Lgamma(float64(n - k + 1))
	return a - b - c
}

// bootGeomean returns the 95% percentile-bootstrap interval of the geometric
// mean of rs. The interval describes spread across benchmarks, not run-to-run
// noise inside one benchmark. The seed comes from the group key, so reruns on
// the same data give the same interval.
func bootGeomean(rs []float64, ak [4]string) (float64, float64) {
	if len(rs) < 2 {
		return math.NaN(), math.NaN()
	}
	logs := make([]float64, len(rs))
	for i, r := range rs {
		logs[i] = math.Log(r)
	}
	var seed uint64
	for _, c := range strings.Join(ak[:], "|") {
		seed = seed*131 + uint64(c)
	}
	rng := rand.New(rand.NewPCG(seed, 0x6a736f6e7632))
	const n = 4000
	means := make([]float64, n)
	for b := range means {
		sum := 0.0
		for range logs {
			sum += logs[rng.IntN(len(logs))]
		}
		means[b] = sum / float64(len(logs))
	}
	sort.Float64s(means)
	return math.Exp(q(means, 0.025)), math.Exp(q(means, 0.975))
}

// better reports whether a ratio (new/old) is an improvement for the unit.
func better(unit string, ratio float64) bool {
	if strings.HasSuffix(unit, "/s") {
		return ratio > 1
	}
	return ratio < 1
}

func q(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return math.NaN()
	}
	i := p * float64(len(sorted)-1)
	lo := int(math.Floor(i))
	hi := int(math.Ceil(i))
	return sorted[lo] + (sorted[hi]-sorted[lo])*(i-float64(lo))
}

func fail(f string, a ...any) {
	fmt.Fprintf(os.Stderr, f+"\n", a...)
	os.Exit(1)
}
