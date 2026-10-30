package benchgate

import (
	"math"
	"reflect"
	"strings"
	"testing"
)

const (
	httpserverPkg = "github.com/CyberCute-Studio/storycreep/server/internal/httpserver"
	searchPkg     = "github.com/CyberCute-Studio/storycreep/server/internal/search"
)

// sampleOutput is two runs of go test -bench, the first of two packages, in
// which httpserver ran a benchmark three times (-count), and the second of
// httpserver alone.
const sampleOutput = `goos: linux
goarch: amd64
pkg: ` + httpserverPkg + `
cpu: AMD EPYC 7763 64-Core Processor
BenchmarkCommandRoundTrip/node.update_text-4         	     100	  12000000 ns/op	        14.00 p95-ms
BenchmarkCommandRoundTrip/node.move-4                	     100	  13019603 ns/op	        14.71 p95-ms
    bench_test.go:120: loaded the large fixture, 105 batches, in 41s
BenchmarkCommandRoundTrip/node.update_text-4         	     100	  13000000 ns/op	        15.00 p95-ms
BenchmarkCommandRoundTrip/node.update_text-4         	     100	  12400000 ns/op	        14.40 p95-ms
--- FAIL: BenchmarkCommandRoundTrip/node.move
BenchmarkSearch	      10	   2000 ns/op	     512 B/op	       4 allocs/op
PASS
ok  	` + httpserverPkg + `	147.140s
goos: linux
goarch: amd64
pkg: ` + searchPkg + `
cpu: AMD EPYC 7763 64-Core Processor
BenchmarkSearch	      10	   3000 ns/op	     256 B/op	       2 allocs/op
PASS
ok  	` + searchPkg + `	3.140s
goos: linux
goarch: amd64
pkg: ` + httpserverPkg + `
cpu: AMD EPYC 7763 64-Core Processor
BenchmarkCommandRoundTrip/node.update_text-4         	     100	  11000000 ns/op	        13.00 p95-ms
PASS
ok  	` + httpserverPkg + `	140.010s
`

// Each run of a package is one sample of each of its metrics, the median of
// the run's values, and benchmarks of one name in two packages are two
// metrics.
func TestParse(t *testing.T) {
	got, err := Parse(strings.NewReader(sampleOutput))
	if err != nil {
		t.Fatal(err)
	}
	want := Samples{
		{httpserverPkg, "BenchmarkCommandRoundTrip/node.update_text-4", "ns/op"}:  {12400000, 11000000},
		{httpserverPkg, "BenchmarkCommandRoundTrip/node.update_text-4", "p95-ms"}: {14.40, 13.00},
		{httpserverPkg, "BenchmarkCommandRoundTrip/node.move-4", "ns/op"}:         {13019603},
		{httpserverPkg, "BenchmarkCommandRoundTrip/node.move-4", "p95-ms"}:        {14.71},
		{httpserverPkg, "BenchmarkSearch", "ns/op"}:                               {2000},
		{httpserverPkg, "BenchmarkSearch", "B/op"}:                                {512},
		{httpserverPkg, "BenchmarkSearch", "allocs/op"}:                           {4},
		{searchPkg, "BenchmarkSearch", "ns/op"}:                                   {3000},
		{searchPkg, "BenchmarkSearch", "B/op"}:                                    {256},
		{searchPkg, "BenchmarkSearch", "allocs/op"}:                               {2},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Parse = %v, want %v", got, want)
	}
}

// metric returns the metric of the benchmark of no package, as of results
// before any pkg line.
func metric(benchmark, unit string) Metric { return Metric{Benchmark: benchmark, Unit: unit} }

// A benchmark's own -<digits>, where go test added no GOMAXPROCS suffix, as
// it adds none when GOMAXPROCS is 1, is part of its name, so that two
// benchmarks that differ by it are two metrics.
func TestParseNameDigits(t *testing.T) {
	got, err := Parse(strings.NewReader("BenchmarkX/size-100\t10\t5 ns/op\nBenchmarkX/size-200\t10\t9 ns/op\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := Samples{metric("BenchmarkX/size-100", "ns/op"): {5}, metric("BenchmarkX/size-200", "ns/op"): {9}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Parse = %v, want %v", got, want)
	}
}

// A line longer than any result, as a failure's log of a large body, is
// passed over, and the results around it read, whatever ends the last line.
func TestParseLongLine(t *testing.T) {
	long := "    bench_test.go:102: " + strings.Repeat("x", 3*maxLine)
	for _, end := range []string{"\n", "\r\n", ""} {
		in := "BenchmarkA-4\t10\t5 ns/op\n" + long + "\nBenchmarkB-4\t10\t7 ns/op" + end
		got, err := Parse(strings.NewReader(in))
		if err != nil {
			t.Fatal(err)
		}
		want := Samples{metric("BenchmarkA-4", "ns/op"): {5}, metric("BenchmarkB-4", "ns/op"): {7}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("Parse, ending in %q = %v, want %v", end, got, want)
		}
	}
	got, err := Parse(strings.NewReader("BenchmarkA-4\t10\t5 ns/op\n" + long))
	if err != nil {
		t.Fatal(err)
	}
	if want := (Samples{metric("BenchmarkA-4", "ns/op"): {5}}); !reflect.DeepEqual(got, want) {
		t.Errorf("Parse, ending in the long line = %v, want %v", got, want)
	}
}

func TestParseBadValue(t *testing.T) {
	if _, err := Parse(strings.NewReader("BenchmarkX-4\t10\tmany ns/op\n")); err == nil {
		t.Error("Parse read a value that is not a number")
	}
}

func TestGated(t *testing.T) {
	for unit, want := range map[string]bool{"ns/op": true, "p95-ms": true, "p99-ns": true, "B/op": false,
		"allocs/op": false, "MB/s": false} {
		if got := Gated(unit); got != want {
			t.Errorf("Gated(%q) = %v, want %v", unit, got, want)
		}
	}
}

// scaled returns xs, each times f.
func scaled(xs []float64, f float64) []float64 {
	out := make([]float64, len(xs))
	for i, x := range xs {
		out[i] = x * f
	}
	return out
}

func TestCompare(t *testing.T) {
	base := []float64{100, 102, 98, 101, 99, 103}
	noisy := []float64{60, 140, 90, 170, 100, 190}
	opts := Options{Threshold: DefaultThreshold, Alpha: DefaultAlpha}
	for _, tc := range []struct {
		name       string
		unit       string
		base, head []float64
		want       Status
	}{
		{"30% slower, every run", "ns/op", base, scaled(base, 1.3), Regressed},
		{"30% slower at the p95", "p95-ms", base, scaled(base, 1.3), Regressed},
		// Of the 70 orderings of 4 and 4 values, one puts every base's below
		// every head's: p = 2/70 < 0.05.
		{"30% slower, every run of four each", "ns/op", base[:4], scaled(base[:4], 1.3), Regressed},
		{"10% slower, every run", "ns/op", base, scaled(base, 1.1), Same},
		{"faster", "ns/op", base, scaled(base, 0.5), Same},
		{"slower within the noise", "ns/op", base, noisy, Same},
		{"more allocations, which gate nothing", "allocs/op", base, scaled(base, 2), Same},
		// Of the 35 orderings of 3 and 4 values, one puts every base's below
		// every head's: p = 2/35 > 0.05, however far apart they are.
		{"too few runs of the base", "ns/op", base[:3], scaled(base[:4], 2), TooFew},
		{"too few runs of the head", "p95-ms", base[:4], scaled(base[:3], 2), TooFew},
		{"too few runs of a metric that gates nothing", "allocs/op", base[:3], scaled(base[:4], 2), Same},
		{"new", "ns/op", nil, base, New},
		{"gone", "ns/op", base, nil, Gone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := metric("BenchmarkX", tc.unit)
			b, h := Samples{}, Samples{}
			if tc.base != nil {
				b[m] = tc.base
			}
			if tc.head != nil {
				h[m] = tc.head
			}
			got := Compare(b, h, opts)
			if len(got) != 1 || got[0].Status != tc.want {
				t.Errorf("Compare = %+v, want one result, %s", got, tc.want)
			}
		})
	}
}

// A median of 0 on both sides is no change, and on the base's side alone an
// infinite one.
func TestCompareZero(t *testing.T) {
	opts := Options{Threshold: DefaultThreshold, Alpha: DefaultAlpha}
	m := metric("BenchmarkX", "p95-ms")
	zeros, ones := []float64{0, 0, 0, 0}, []float64{1, 1, 1, 1}
	if got := Compare(Samples{m: zeros}, Samples{m: zeros}, opts); got[0].Change != 0 || got[0].Status != Same {
		t.Errorf("0 on both sides = %+v, want no change", got[0])
	}
	if got := Compare(Samples{m: zeros}, Samples{m: ones}, opts); !math.IsInf(got[0].Change, 1) ||
		got[0].Status != Regressed {
		t.Errorf("0 on the base's side = %+v, want an infinite change, regressed", got[0])
	}
}

func TestCompareOrder(t *testing.T) {
	b := Samples{{"b", "BenchmarkA", "ns/op"}: {1}, {"a", "BenchmarkB", "ns/op"}: {1}, {"a", "BenchmarkA", "p95-ms"}: {1}}
	h := Samples{{"a", "BenchmarkA", "ns/op"}: {1}, {"a", "BenchmarkA", "p95-ms"}: {1}}
	var got []Metric
	for _, r := range Compare(b, h, Options{Threshold: DefaultThreshold, Alpha: DefaultAlpha}) {
		got = append(got, r.Metric)
	}
	want := []Metric{{"a", "BenchmarkA", "ns/op"}, {"a", "BenchmarkA", "p95-ms"}, {"a", "BenchmarkB", "ns/op"},
		{"b", "BenchmarkA", "ns/op"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Compare's order = %v, want %v", got, want)
	}
}

func TestMannWhitney(t *testing.T) {
	for _, tc := range []struct {
		name string
		a, b []float64
		want float64
	}{
		// Of the 924 orderings of 6 and 6 values, one puts every a below
		// every b, and one every a above: p = 2/924.
		{"apart", []float64{1, 2, 3, 4, 5, 6}, []float64{7, 8, 9, 10, 11, 12}, 2.0 / 924},
		{"apart, the other way", []float64{7, 8, 9, 10, 11, 12}, []float64{1, 2, 3, 4, 5, 6}, 2.0 / 924},
		// Of the 6 orderings of 2 and 2 values, U = 1 or less in two, and 3
		// or more in two: p = 4/6.
		{"two each", []float64{1, 3}, []float64{2, 4}, 4.0 / 6},
		{"interleaved", []float64{1, 3, 5, 7}, []float64{2, 4, 6, 8}, 0.686},
		{"the same", []float64{5, 5, 5}, []float64{5, 5, 5}, 1},
		// With ties, the normal approximation: U = 0, μ = 8, and σ² = 16/12 ·
		// (9 − 24/56), for four runs of two tied values, each of which adds
		// 2³ − 2 = 6.
		{"tied, apart", []float64{1, 1, 2, 2}, []float64{3, 3, 4, 4}, math.Erfc(7.5 / math.Sqrt(16.0/12*(9-24.0/56)) /
			math.Sqrt2)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := mannWhitney(tc.a, tc.b); math.Abs(got-tc.want) > 0.001 {
				t.Errorf("mannWhitney(%v, %v) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
	// A NaN, which equals no value, not even itself, is ranked all the same.
	if got := mannWhitney([]float64{math.NaN(), 1}, []float64{2, 3}); math.IsNaN(got) {
		t.Errorf("mannWhitney of a NaN = %v, want a p-value", got)
	}
}

func TestReport(t *testing.T) {
	opts := Options{Threshold: DefaultThreshold, Alpha: DefaultAlpha}
	base := Samples{{"pkg/a", "BenchmarkA", "ns/op"}: {97, 98, 99, 101, 102, 103},
		{"pkg/a", "BenchmarkC", "ns/op"}: {1, 2, 3}}
	head := Samples{{"pkg/a", "BenchmarkA", "ns/op"}: {147, 148, 149, 151, 152, 153},
		{"pkg/a", "BenchmarkB", "ns/op"}: {1}, {"pkg/a", "BenchmarkC", "ns/op"}: {7, 8, 9, 10}}
	var b strings.Builder
	if err := Report(&b, Compare(base, head, opts), opts); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"1 metric(s) regressed by more than 20%, at p < 0.05. 1 metric(s) were not " +
		"compared, since only one side has them: 1 new, of the change alone, and 0 gone, of the base alone. 1 " +
		"time metric(s) were not compared, since a side has too few runs of them for the test to find a " +
		"difference at p < 0.05.\n",
		"| pkg/a | BenchmarkA | ns/op | 100 | 150 | +50.0% | 0.002 | ⚠ regressed |",
		"| pkg/a | BenchmarkB | ns/op | – | 1 | – | – | new |",
		"| pkg/a | BenchmarkC | ns/op | 2 | 8.5 | +325.0% | 0.057 | too few runs |"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("the report has no %q:\n%s", want, b.String())
		}
	}
	// A comparison of metrics that only one side has each says so, rather
	// than only that nothing regressed.
	b.Reset()
	if err := Report(&b, Compare(Samples{metric("BenchmarkA", "ns/op"): {1}},
		Samples{metric("BenchmarkB", "ns/op"): {1}}, opts), opts); err != nil {
		t.Fatal(err)
	}
	if want := "No metric regressed by more than 20%, at p < 0.05. 2 metric(s) were not compared, since only one " +
		"side has them: 1 new, of the change alone, and 1 gone, of the base alone.\n\n"; !strings.HasPrefix(b.String(),
		want) {
		t.Errorf("the report of metrics that one side has each begins %q, want %q", b.String(), want)
	}
	b.Reset()
	if err := Report(&b, nil, opts); err != nil {
		t.Fatal(err)
	}
	if got := b.String(); got != "No benchmark ran on either side.\n" {
		t.Errorf("the report of nothing is %q", got)
	}
}
