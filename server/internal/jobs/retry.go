package jobs

import (
	"math"
	"math/rand/v2"
	"time"

	"github.com/riverqueue/river/rivertype"
)

// The retry policy's waits (PRD FR-JOBS-01): the first retry comes about a
// second after the first failure, and each failure doubles the wait, up to an
// hour. A job that fails every one of its 25 attempts is discarded about 13
// hours after the first.
const (
	firstRetry = time.Second
	maxRetry   = time.Hour
)

// backoff is the queue's retry policy: exponential backoff. It is River's
// ClientRetryPolicy.
type backoff struct {
	// first is the wait after the first failure, and max the longest wait.
	first, max time.Duration
}

// NextRetry returns when the job, whose attempt has just failed, is attempted
// again. As River's own policy does, it counts the failures by the job's
// errors, to which River adds this failure's once NextRetry returns.
func (b backoff) NextRetry(job *rivertype.JobRow) time.Time {
	return time.Now().Add(b.wait(len(job.Errors) + 1))
}

// wait returns the wait after the nth failure, n ≥ 1: first × 2ⁿ⁻¹, but at
// most max, with up to 10% more or less at random, so that jobs that failed
// together, as when the database restarted, are not attempted again together.
func (b backoff) wait(n int) time.Duration {
	d := min(float64(b.first)*math.Exp2(float64(n-1)), float64(b.max))
	return time.Duration(d * (0.9 + 0.2*rand.Float64()))
}
