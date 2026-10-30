// Package benchgate compares the server's benchmarks of a change with those
// of its base, and finds the regressions that fail the build (PRD §9.1, plan
// note N52).
//
// CI's benchmark job runs the benchmarks of a pull request's base and of its
// head on one runner, in turn, four times each, so that both are measured on
// the same hardware, whatever hardware GitHub gives the job: the base, the
// head, the head again, and the base again, twice, so that a steady drift of
// the machine during the job falls on both alike. Each run is a go test of
// its own, with a database server and a fixture of its own, and is one
// sample of each metric of its benchmarks: the median of the run's values of
// it, where the run ran a benchmark more than once (-count), since what makes
// a run fast or slow, as the state that its fixture's load left the database
// in, is the same for each of its counts, which are no samples apart. A
// metric whose unit is a time, where lower is better, ns/op or a unit that
// ends in -ms or -ns, as p95-ms, regresses when the median of the head's
// samples is more than Threshold above the base's, and a Mann-Whitney U test
// finds the two sets of samples apart, at a p-value under Alpha, so that the
// noise of a shared runner seldom fails the build. Other metrics are
// reported, and gate nothing. A benchmark that only the head has is new, and
// one that only the base has is gone, and a time metric of so few runs on a
// side that the test could find no two sets of them apart, as of a base that
// failed in some of its runs, has too few; none of them is compared, which
// the report says before anything else.
package benchgate

import (
	"bufio"
	"cmp"
	"errors"
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

// Metric names a metric of a benchmark: the benchmark's package, as the pkg
// line of go test's output names it, so that benchmarks of one name in two
// packages are two metrics, the benchmark's name, as go test prints it, and
// the metric's unit. The name keeps the GOMAXPROCS suffix that go test adds,
// as -4, which the base and the head, run on one machine, have alike: go test
// adds none when GOMAXPROCS is 1, so a name's own -<digits>, as of a
// benchmark size-100, would read as one otherwise.
type Metric struct {
	Package   string
	Benchmark string
	Unit      string
}

// Samples are the values of each metric, one per run of its package's
// benchmarks, in the order of the runs.
type Samples map[Metric][]float64

// lineRE reads a result line of go test -bench: the benchmark's name, with
// its GOMAXPROCS suffix, the iterations, and the metrics, each a value and
// its unit.
var lineRE = regexp.MustCompile(`^(Benchmark\S+)\s+(\d+)((?:\s+\S+\s+\S+)+)\s*$`)

// maxLine is the longest line that Parse reads. A result line is far
// shorter, so a longer one, as a test's log of a large body, is passed over.
const maxLine = 64 << 10

// Parse reads the output of go test -bench, of any number of runs and
// packages, and returns the samples of each metric: one of each run, the
// median of the run's values of it. A package's run begins at its pkg line,
// which go test prints once before the package's first result; the results
// before any pkg line are of one run, of no package. A line that is neither a
// pkg line nor a benchmark's result, as the goos line, PASS, or a test's log,
// of any length, is passed over.
func Parse(r io.Reader) (Samples, error) {
	out := Samples{}
	pkg, run := "", map[Metric][]float64{}
	// end adds the run's sample of each of its metrics to out.
	end := func() {
		for k, vs := range run {
			out[k] = append(out[k], median(vs))
		}
		clear(run)
	}
	br := bufio.NewReaderSize(r, maxLine)
	for {
		line, err := br.ReadSlice('\n')
		if errors.Is(err, bufio.ErrBufferFull) {
			for errors.Is(err, bufio.ErrBufferFull) {
				_, err = br.ReadSlice('\n')
			}
			line = nil
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
		text := strings.TrimRight(string(line), "\r\n")
		if name, ok := strings.CutPrefix(text, "pkg: "); ok {
			end()
			pkg = strings.TrimSpace(name)
		} else if m := lineRE.FindStringSubmatch(text); m != nil {
			fields := strings.Fields(m[3])
			for i := 0; i+1 < len(fields); i += 2 {
				v, err := strconv.ParseFloat(fields[i], 64)
				if err != nil {
					return nil, fmt.Errorf("benchgate: %s: cannot read %q as a value of %s", m[1], fields[i],
						fields[i+1])
				}
				key := Metric{Package: pkg, Benchmark: m[1], Unit: fields[i+1]}
				run[key] = append(run[key], v)
			}
		}
		if err != nil {
			end()
			return out, nil
		}
	}
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
	// TooFew is a time metric of so few samples on a side, as of a base that
	// failed in some of its runs, that the test could find no two sets of
	// them apart at a p-value under Alpha, whatever their values.
	TooFew Status = "too few runs"
)

// Result is the comparison of a metric of the head with the base's.
type Result struct {
	Metric
	Status Status
	// Base and Head are the medians of the samples of each, or NaN for a
	// side without samples.
	Base, Head float64
	// Change is how much the head's median is above the base's, as a
	// fraction of it, which is negative for an improvement, 0 where both
	// medians are 0, and +Inf where the base's alone is 0, or NaN when either
	// side has no samples.
	Change float64
	// P is the p-value of the Mann-Whitney U test of the two sets of samples,
	// or NaN when either side has no samples.
	P float64
}

// Compare compares each metric of the head with the base's, and returns the
// results, ordered by package, benchmark, and unit.
func Compare(base, head Samples, opts Options) []Result {
	keys := slices.Collect(maps.Keys(head))
	for k := range base {
		if _, ok := head[k]; !ok {
			keys = append(keys, k)
		}
	}
	slices.SortFunc(keys, func(a, b Metric) int {
		return cmp.Or(strings.Compare(a.Package, b.Package), strings.Compare(a.Benchmark, b.Benchmark),
			strings.Compare(a.Unit, b.Unit))
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
			// Two medians of 0 are no change, and the division makes the
			// change of a base's median alone of 0 +Inf.
			r.Change = 0
			if r.Base != 0 || r.Head != 0 {
				r.Change = r.Head/r.Base - 1
			}
			r.P = mannWhitney(b, h)
			switch {
			case !Gated(k.Unit):
			case exactP(len(b), len(h), 0) >= opts.Alpha:
				// Even every sample of one side above every sample of the
				// other would be no difference at p < Alpha.
				r.Status = TooFew
			case r.Change > opts.Threshold && r.P < opts.Alpha:
				r.Status = Regressed
			}
		}
		out = append(out, r)
	}
	return out
}

// Report writes the results as a Markdown table, for the job's summary, with
// a line before it that says what the comparison found, how many metrics
// only one side has, and how many time metrics have too few runs, which it
// could not compare.
func Report(w io.Writer, results []Result, opts Options) error {
	count := map[Status]int{}
	for _, r := range results {
		count[r.Status]++
	}
	var b strings.Builder
	switch {
	case len(results) == 0:
		b.WriteString("No benchmark ran on either side.\n")
	case count[Regressed] > 0:
		fmt.Fprintf(&b, "%d metric(s) regressed by more than %.0f%%, at p < %g.", count[Regressed],
			opts.Threshold*100, opts.Alpha)
	default:
		fmt.Fprintf(&b, "No metric regressed by more than %.0f%%, at p < %g.", opts.Threshold*100, opts.Alpha)
	}
	if len(results) > 0 {
		if n := count[New] + count[Gone]; n > 0 {
			fmt.Fprintf(&b, " %d metric(s) were not compared, since only one side has them: %d new, of the "+
				"change alone, and %d gone, of the base alone.", n, count[New], count[Gone])
		}
		if n := count[TooFew]; n > 0 {
			fmt.Fprintf(&b, " %d time metric(s) were not compared, since a side has too few runs of them for "+
				"the test to find a difference at p < %g.", n, opts.Alpha)
		}
		b.WriteString("\n\n| Package | Benchmark | Unit | Base | Head | Change | p | |\n" +
			"| --- | --- | --- | ---: | ---: | ---: | ---: | --- |\n")
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
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s | %s | %s |\n", r.Package, r.Benchmark, r.Unit,
				number(r.Base), number(r.Head), percent(r.Change), pValue(r.P), mark)
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
	slices.SortFunc(all, func(x, y value) int { return cmp.Compare(x.v, y.v) })
	// The rank sum of a, with each run of tied values given their mean rank,
	// and the sum of t³ − t over the runs, for the correction.
	rankA, ties := 0.0, 0.0
	for i := 0; i < len(all); {
		// A run is at least its first value, even a NaN, which equals none.
		j := i + 1
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
