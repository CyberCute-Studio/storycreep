// Package benchgate compares the server's benchmarks of a change with those
// of its base, and finds the regressions that fail the build (PRD §9.1, plan
// note N52).
//
// CI's benchmark job runs the benchmarks of a pull request's base and of its
// head on one runner, in turn, several times each, so that both are measured
// on the same hardware, whatever hardware GitHub gives the job, and a drift of
// the machine during the job falls on both. Each run of a benchmark is a
// sample of each of its metrics. A metric whose unit is a time, where lower
// is better, ns/op or a unit that ends in -ms or -ns, as p95-ms, regresses
// when the median of the head's samples is more than Threshold above the
// base's, and a Mann-Whitney U test finds the two sets of samples apart, at a
// p-value under Alpha, so that the noise of a shared runner does not fail the
// build. Other metrics are reported, and gate nothing. A benchmark that only
// the head has is new, and one that only the base has is gone; neither is
// compared.
package benchgate

import (
	"bufio"
	"fmt"
	"io"
	"maps"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// The defaults of Options: a regression of more than 20% fails the build
// (PRD §9.1), when a test finds it at p < 0.05.
const (
	DefaultThreshold = 0.20
	DefaultAlpha     = 0.05
)

// Options are the bounds of a regression.
type Options struct {
	// Threshold is how much higher than the base's a median may be, as a
	// fraction of it, before it is a regression.
	Threshold float64
	// Alpha is the p-value under which a difference is not noise.
	Alpha float64
}

// Metric names a metric of a benchmark: the benchmark's name, without the
// GOMAXPROCS suffix that go test adds, and the metric's unit.
type Metric struct {
	Benchmark string
	Unit      string
}

// Samples are the values of each metric, one per run of its benchmark, in
// the order that the runs printed them.
type Samples map[Metric][]float64

// lineRE reads a result line of go test -bench: the benchmark's name, with
// its GOMAXPROCS suffix, the iterations, and the metrics, each a value and
// its unit.
var lineRE = regexp.MustCompile(`^(Benchmark\S+?)(?:-\d+)?\s+(\d+)((?:\s+\S+\s+\S+)+)\s*$`)

// Parse reads the output of go test -bench, of any number of runs and
// packages, and returns the samples of each metric. A line that is not a
// benchmark's result, as the goos and pkg lines, PASS, or a test's log, is
// passed over.
func Parse(r io.Reader) (Samples, error) {
	out := Samples{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		m := lineRE.FindStringSubmatch(sc.Text())
		if m == nil {
			continue
		}
		fields := strings.Fields(m[3])
		for i := 0; i+1 < len(fields); i += 2 {
			v, err := strconv.ParseFloat(fields[i], 64)
			if err != nil {
				return nil, fmt.Errorf("benchgate: %s: cannot read %q as a value of %s", m[1], fields[i], fields[i+1])
			}
			key := Metric{Benchmark: m[1], Unit: fields[i+1]}
			out[key] = append(out[key], v)
		}
	}
	return out, sc.Err()
}

// Gated reports whether a metric of the unit gates the build: a time, where
// lower is better.
func Gated(unit string) bool {
	return unit == "ns/op" || strings.HasSuffix(unit, "-ms") || strings.HasSuffix(unit, "-ns")
}

// Status is what the comparison of a metric found.
type Status string

const (
	// Same is a metric that did not regress, or that gates nothing.
	Same Status = "same"
	// Regressed is a metric that regressed, which fails the build.
	Regressed Status = "regressed"
	// New is a metric that only the head has.
	New Status = "new"
	// Gone is a metric that only the base has.
	Gone Status = "gone"
)

// Result is the comparison of a metric of the head with the base's.
type Result struct {
	Metric
	Status Status
	// Base and Head are the medians of the samples of each, or NaN for a
	// side without samples.
	Base, Head float64
	// Change is how much the head's median is above the base's, as a
	// fraction of it, which is negative for an improvement, or NaN when
	// either side has no samples.
	Change float64
	// P is the p-value of the Mann-Whitney U test of the two sets of samples,
	// or NaN when either side has no samples.
	P float64
}

// Compare compares each metric of the head with the base's, and returns the
// results, ordered by benchmark and unit.
func Compare(base, head Samples, opts Options) []Result {
	keys := slices.Collect(maps.Keys(head))
	for k := range base {
		if _, ok := head[k]; !ok {
			keys = append(keys, k)
		}
	}
	slices.SortFunc(keys, func(a, b Metric) int {
		return strings.Compare(a.Benchmark+"\x00"+a.Unit, b.Benchmark+"\x00"+b.Unit)
	})
	out := make([]Result, 0, len(keys))
	for _, k := range keys {
		b, h := base[k], head[k]
		r := Result{Metric: k, Status: Same, Base: median(b), Head: median(h), Change: math.NaN(), P: math.NaN()}
		switch {
		case len(b) == 0:
			r.Status = New
		case len(h) == 0:
			r.Status = Gone
		default:
			r.Change = r.Head/r.Base - 1
			r.P = mannWhitney(b, h)
			if Gated(k.Unit) && r.Change > opts.Threshold && r.P < opts.Alpha {
				r.Status = Regressed
			}
		}
		out = append(out, r)
	}
	return out
}

// Report writes the results as a Markdown table, for the job's summary, with
// a line before it that says what the comparison found.
func Report(w io.Writer, results []Result, opts Options) error {
	regressed := 0
	for _, r := range results {
		if r.Status == Regressed {
			regressed++
		}
	}
	var b strings.Builder
	switch {
	case len(results) == 0:
		b.WriteString("No benchmark ran on either side.\n")
	case regressed > 0:
		fmt.Fprintf(&b, "%d metric(s) regressed by more than %.0f%%, at p < %g.\n", regressed, opts.Threshold*100,
			opts.Alpha)
	default:
		fmt.Fprintf(&b, "No metric regressed by more than %.0f%%, at p < %g.\n", opts.Threshold*100, opts.Alpha)
	}
	if len(results) > 0 {
		b.WriteString("\n| Benchmark | Unit | Base | Head | Change | p | |\n| --- | --- | ---: | ---: | ---: | ---: | --- |\n")
		for _, r := range results {
			mark := ""
			switch {
			case r.Status == Regressed:
				mark = "⚠ regressed"
			case r.Status != Same:
				mark = string(r.Status)
			case !Gated(r.Unit):
				mark = "not gated"
			}
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s | %s |\n", r.Benchmark, r.Unit, number(r.Base),
				number(r.Head), percent(r.Change), pValue(r.P), mark)
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func number(v float64) string {
	if math.IsNaN(v) {
		return "–"
	}
	return strconv.FormatFloat(v, 'g', 4, 64)
}

func percent(v float64) string {
	if math.IsNaN(v) {
		return "–"
	}
	return fmt.Sprintf("%+.1f%%", v*100)
}

func pValue(v float64) string {
	if math.IsNaN(v) {
		return "–"
	}
	return strconv.FormatFloat(v, 'f', 3, 64)
}

// median returns the median of xs, or NaN if there are none.
func median(xs []float64) float64 {
	if len(xs) == 0 {
		return math.NaN()
	}
	s := slices.Clone(xs)
	slices.Sort(s)
	if n := len(s); n%2 == 1 {
		return s[n/2]
	}
	return (s[len(s)/2-1] + s[len(s)/2]) / 2
}

// mannWhitney returns the two-sided p-value of the Mann-Whitney U test of
// the samples a and b, neither empty: exact, from the distribution of U, when
// no value is tied, and otherwise from its normal approximation, with the
// correction for ties and for continuity.
func mannWhitney(a, b []float64) float64 {
	m, n := len(a), len(b)
	type value struct {
		v     float64
		fromA bool
	}
	all := make([]value, 0, m+n)
	for _, v := range a {
		all = append(all, value{v, true})
	}
	for _, v := range b {
		all = append(all, value{v, false})
	}
	slices.SortFunc(all, func(x, y value) int { return cmpFloat(x.v, y.v) })
	// The rank sum of a, with each run of tied values given their mean rank,
	// and the sum of t³ − t over the runs, for the correction.
	rankA, ties := 0.0, 0.0
	for i := 0; i < len(all); {
		j := i
		for j < len(all) && all[j].v == all[i].v {
			j++
		}
		mean := float64(i+j+1) / 2 // the mean of the ranks i+1 … j
		for k := i; k < j; k++ {
			if all[k].fromA {
				rankA += mean
			}
		}
		if t := float64(j - i); t > 1 {
			ties += t*t*t - t
		}
		i = j
	}
	u := rankA - float64(m*(m+1))/2
	if ties == 0 {
		return exactP(m, n, int(math.Round(u)))
	}
	mu := float64(m*n) / 2
	nn := float64(m + n)
	sigma := math.Sqrt(float64(m*n) / 12 * (nn + 1 - ties/(nn*(nn-1))))
	if sigma == 0 {
		return 1
	}
	z := (math.Abs(u-mu) - 0.5) / sigma
	if z < 0 {
		return 1
	}
	return math.Min(1, math.Erfc(z/math.Sqrt2))
}

// exactP returns the two-sided p-value of U = u for samples of m and n
// values without ties: twice the probability of a U as far from its mean,
// on the side of u, as no larger than 1.
func exactP(m, n, u int) float64 {
	// count[k] is the number of orderings of m and n values with U = k:
	// count(m, n, k) = count(m-1, n, k-n) + count(m, n-1, k), built up one
	// value at a time.
	prev := make([][]float64, n+1)
	for j := range prev {
		prev[j] = []float64{1} // m = 0: U is 0
	}
	for i := 1; i <= m; i++ {
		cur := make([][]float64, n+1)
		cur[0] = []float64{1} // n = 0: U is 0
		for j := 1; j <= n; j++ {
			c := make([]float64, i*j+1)
			for k := range c {
				if k-j >= 0 && k-j < len(prev[j]) {
					c[k] += prev[j][k-j]
				}
				if k < len(cur[j-1]) {
					c[k] += cur[j-1][k]
				}
			}
			cur[j] = c
		}
		prev = cur
	}
	counts := prev[n]
	total := 0.0
	for _, c := range counts {
		total += c
	}
	k := min(u, m*n-u) // the distribution is symmetric
	tail := 0.0
	for i := 0; i <= k; i++ {
		tail += counts[i]
	}
	return math.Min(1, 2*tail/total)
}

func cmpFloat(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
