package benchgate

import (
	"math"
	"reflect"
	"strings"
	"testing"
)

const sampleOutput = `goos: linux
goarch: amd64
pkg: github.com/CyberCute-Studio/storycreep/server/internal/httpserver
cpu: AMD EPYC 7763 64-Core Processor
BenchmarkCommandRoundTrip/node.update_text-4         	     100	  12713840 ns/op	        14.52 p95-ms
BenchmarkCommandRoundTrip/node.move-4                	     100	  13019603 ns/op	        14.71 p95-ms
    bench_test.go:120: loaded the large fixture, 105 batches, in 41s
BenchmarkCommandRoundTrip/node.update_text-4         	     100	  12000000 ns/op	        14.00 p95-ms
--- FAIL: BenchmarkCommandRoundTrip/node.move
BenchmarkSearch	      10	   2000 ns/op	     512 B/op	       4 allocs/op
PASS
ok  	github.com/CyberCute-Studio/storycreep/server/internal/httpserver	147.140s
`

func TestParse(t *testing.T) {
	got, err := Parse(strings.NewReader(sampleOutput))
	if err != nil {
		t.Fatal(err)
	}
	want := Samples{
		{"BenchmarkCommandRoundTrip/node.update_text-4", "ns/op"}:  {12713840, 12000000},
		{"BenchmarkCommandRoundTrip/node.update_text-4", "p95-ms"}: {14.52, 14.00},
		{"BenchmarkCommandRoundTrip/node.move-4", "ns/op"}:         {13019603},
		{"BenchmarkCommandRoundTrip/node.move-4", "p95-ms"}:        {14.71},
		{"BenchmarkSearch", "ns/op"}:                               {2000},
		{"BenchmarkSearch", "B/op"}:                                {512},
		{"BenchmarkSearch", "allocs/op"}:                           {4},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Parse = %v, want %v", got, want)
	}
}

// A benchmark's own -<digits>, where go test added no GOMAXPROCS suffix, as
// it adds none when GOMAXPROCS is 1, is part of its name, so that two
// benchmarks that differ by it are two metrics.
func TestParseNameDigits(t *testing.T) {
	got, err := Parse(strings.NewReader("BenchmarkX/size-100\t10\t5 ns/op\nBenchmarkX/size-200\t10\t9 ns/op\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := Samples{{"BenchmarkX/size-100", "ns/op"}: {5}, {"BenchmarkX/size-200", "ns/op"}: {9}}
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
		want := Samples{{"BenchmarkA-4", "ns/op"}: {5}, {"BenchmarkB-4", "ns/op"}: {7}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("Parse, ending in %q = %v, want %v", end, got, want)
		}
	}
	got, err := Parse(strings.NewReader("BenchmarkA-4\t10\t5 ns/op\n" + long))
	if err != nil {
		t.Fatal(err)
	}
	if want := (Samples{{"BenchmarkA-4", "ns/op"}: {5}}); !reflect.DeepEqual(got, want) {
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
		{"10% slower, every run", "ns/op", base, scaled(base, 1.1), Same},
		{"faster", "ns/op", base, scaled(base, 0.5), Same},
		{"slower within the noise", "ns/op", base, noisy, Same},
		{"more allocations, which gate nothing", "allocs/op", base, scaled(base, 2), Same},
		{"new", "ns/op", nil, base, New},
		{"gone", "ns/op", base, nil, Gone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := Metric{"BenchmarkX", tc.unit}
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

func TestCompareOrder(t *testing.T) {
	b := Samples{{"BenchmarkB", "ns/op"}: {1}, {"BenchmarkA", "p95-ms"}: {1}}
	h := Samples{{"BenchmarkA", "ns/op"}: {1}, {"BenchmarkA", "p95-ms"}: {1}}
	var got []Metric
	for _, r := range Compare(b, h, Options{Threshold: DefaultThreshold, Alpha: DefaultAlpha}) {
		got = append(got, r.Metric)
	}
	want := []Metric{{"BenchmarkA", "ns/op"}, {"BenchmarkA", "p95-ms"}, {"BenchmarkB", "ns/op"}}
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
}

func TestReport(t *testing.T) {
	opts := Options{Threshold: DefaultThreshold, Alpha: DefaultAlpha}
	base := Samples{{"BenchmarkA", "ns/op"}: {97, 98, 99, 101, 102, 103}}
	head := Samples{{"BenchmarkA", "ns/op"}: {147, 148, 149, 151, 152, 153}, {"BenchmarkB", "ns/op"}: {1}}
	var b strings.Builder
	if err := Report(&b, Compare(base, head, opts), opts); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"1 metric(s) regressed by more than 20%, at p < 0.05. 1 metric(s) were not " +
		"compared, since only one side has them: 1 new, of the change alone, and 0 gone, of the base alone.\n",
		"| BenchmarkA | ns/op | 100 | 150 | +50.0% | 0.002 | ⚠ regressed |", "| BenchmarkB | ns/op | – | 1 | – | – | new |"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("the report has no %q:\n%s", want, b.String())
		}
	}
	// A comparison of metrics that only one side has each says so, rather
	// than only that nothing regressed.
	b.Reset()
	if err := Report(&b, Compare(Samples{{"BenchmarkA", "ns/op"}: {1}}, Samples{{"BenchmarkB", "ns/op"}: {1}}, opts),
		opts); err != nil {
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
