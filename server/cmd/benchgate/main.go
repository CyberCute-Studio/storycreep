// Command benchgate compares the output of the server's benchmarks of a
// change, go test -bench, with its base's, and fails on a regression (PRD
// §9.1, plan note N52): a time metric whose median is more than 20% above the
// base's, where a Mann-Whitney U test finds the two apart at p < 0.05.
//
// Each output is of any number of runs, a go test each, and each run is one
// sample of each metric of its benchmarks (package benchgate).
//
// It writes a Markdown table of every metric to standard output, for the CI
// job's summary, warns on standard error, as a GitHub Actions annotation, of
// each gated metric that it could not compare: one of the base that the
// change's output does not have, as one of a benchmark that the change
// renamed, one of the change that the base's output does not have, as one of
// a benchmark that the change adds, or one that failed in every run of the
// base, and one with too few runs on a side, as where the base failed in
// some of its runs. It exits with status 1 if any metric regressed, or with
// status 2 if an output cannot be read.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/CyberCute-Studio/storycreep/server/internal/benchgate"
)

func main() {
	base := flag.String("base", "", "the output of the base's benchmarks")
	head := flag.String("head", "", "the output of the change's benchmarks")
	threshold := flag.Float64("threshold", benchgate.DefaultThreshold,
		"how much above the base's a median may be, as a fraction of it, before it regresses")
	alpha := flag.Float64("alpha", benchgate.DefaultAlpha, "the p-value under which a difference is not noise")
	flag.Parse()
	if *base == "" || *head == "" {
		fmt.Fprintln(os.Stderr, "benchgate: both -base and -head are needed")
		os.Exit(2)
	}

	samples := make([]benchgate.Samples, 2)
	for i, path := range []string{*base, *head} {
		f, err := os.Open(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, "benchgate:", err)
			os.Exit(2)
		}
		samples[i], err = benchgate.Parse(f)
		f.Close()
		if err != nil {
			fmt.Fprintf(os.Stderr, "benchgate: %s: %v\n", path, err)
			os.Exit(2)
		}
	}
	opts := benchgate.Options{Threshold: *threshold, Alpha: *alpha}
	results := benchgate.Compare(samples[0], samples[1], opts)
	if err := benchgate.Report(os.Stdout, results, opts); err != nil {
		fmt.Fprintln(os.Stderr, "benchgate:", err)
		os.Exit(2)
	}
	regressed := false
	for _, r := range results {
		switch {
		case r.Status == benchgate.Regressed:
			regressed = true
		case r.Status == benchgate.Gone && benchgate.Gated(r.Unit):
			fmt.Fprintf(os.Stderr, "::warning::%s %s (%s) is not compared: the change has no such metric.\n",
				r.Package, r.Benchmark, r.Unit)
		case r.Status == benchgate.New && benchgate.Gated(r.Unit):
			fmt.Fprintf(os.Stderr, "::warning::%s %s (%s) is not compared: the base has no such metric, as of a "+
				"benchmark that the change adds, or one that failed in every run of the base.\n", r.Package,
				r.Benchmark, r.Unit)
		case r.Status == benchgate.TooFew:
			fmt.Fprintf(os.Stderr, "::warning::%s %s (%s) is not compared: the base has %d runs of it, and the "+
				"change %d, too few for the test to find them apart.\n", r.Package, r.Benchmark, r.Unit,
				len(samples[0][r.Metric]), len(samples[1][r.Metric]))
		}
	}
	if regressed {
		os.Exit(1)
	}
}
