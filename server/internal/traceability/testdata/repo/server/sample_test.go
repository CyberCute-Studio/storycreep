package sample

import "testing"

// TestSample is the tagged sample test.
//
// Verifies: FR-A-01/AC1
func TestSample(t *testing.T) {}

func TestUntagged(t *testing.T) {}

func TestTable(t *testing.T) {
	// Verifies: FR-B-01/AC1
	t.Run("polished case", func(t *testing.T) {})
}

// Verifies: NFR-PERF-01
func BenchmarkSample(b *testing.B) {}
