package fixture

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"strconv"
	"time"
)

// Sender sends a fixture's batches to the command endpoint of a running
// instance, as a client does, which is how a fixture is loaded for manual
// testing (cmd/fixturegen), and how the benchmarks load one through the API.
type Sender struct {
	// Client sends the requests, or http.DefaultClient if it is nil.
	Client *http.Client
	// Endpoint is the URL of the project's command endpoint,
	// …/api/v1/projects/{project_id}/commands.
	Endpoint string
	// Header is added to each request, as an API token's Authorization.
	Header http.Header
	// Progress, if not nil, is told after each batch how many of the
	// fixture's batches have applied.
	Progress func(done, total int)

	// wait waits for d to pass, or for ctx to end; tests replace it.
	wait func(ctx context.Context, d time.Duration) error
}

// The retries of a batch: one that failed in transit, or that the server
// answered with 5xx, is sent again up to maxFailures times, after 1, 2, and 4
// seconds, and one answered with 429 rate_limited up to maxLimited times,
// each once its Retry-After has passed.
const (
	maxFailures = 3
	maxLimited  = 30
)

// maxShown is the most of an answer's body that an error holds.
const maxShown = 2048

// Send sends the fixture's batches, one a request, in order, and stops at the
// first that does not apply. Sending a batch again is safe: its commands'
// IDs make it idempotent (FR-API-03), so a batch that applied is answered as
// already applied, and Send, run again after it stopped, resumes where it
// stopped.
func (s *Sender) Send(ctx context.Context, f *Fixture) error {
	for i, b := range f.Batches {
		if err := s.send(ctx, Body(b)); err != nil {
			return fmt.Errorf("fixture: batch %d of %d: %w", i+1, len(f.Batches), err)
		}
		if s.Progress != nil {
			s.Progress(i+1, len(f.Batches))
		}
	}
	return nil
}

// send sends a batch's body until it is answered, and returns why it did not
// apply, if it did not.
func (s *Sender) send(ctx context.Context, body []byte) error {
	failures, limited := 0, 0
	for {
		status, answer, header, err := s.post(ctx, body)
		var delay time.Duration
		switch {
		case err != nil && ctx.Err() != nil:
			return err
		case err != nil || status >= http.StatusInternalServerError:
			if failures == maxFailures {
				if err != nil {
					return err
				}
				return fmt.Errorf("answered with %d: %s", status, shown(answer))
			}
			delay = time.Second << failures
			failures++
		case status == http.StatusTooManyRequests:
			if limited == maxLimited {
				return fmt.Errorf("answered with 429 %d times: %s", limited+1, shown(answer))
			}
			delay = time.Second
			if n, err := strconv.Atoi(header.Get("Retry-After")); err == nil && n > 0 {
				delay = time.Duration(n) * time.Second
			}
			limited++
		case status != http.StatusOK:
			return fmt.Errorf("answered with %d: %s", status, shown(answer))
		default:
			var result struct {
				Status string `json:"status"`
			}
			if err := json.Unmarshal(answer, &result); err != nil ||
				result.Status != "applied" && result.Status != "already_applied" {
				return fmt.Errorf("answered with 200 but no applied batch: %s", shown(answer))
			}
			return nil
		}
		wait := s.wait
		if wait == nil {
			wait = sleep
		}
		if err := wait(ctx, delay); err != nil {
			return err
		}
	}
}

// post sends one request of the body, and returns the answer's status, body,
// and header.
func (s *Sender) post(ctx context.Context, body []byte) (int, []byte, http.Header, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.Endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, nil, nil, err
	}
	maps.Copy(req.Header, s.Header)
	req.Header.Set("Content-Type", "application/json")
	client := s.Client
	if client == nil {
		client = http.DefaultClient
	}
	res, err := client.Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	defer res.Body.Close()
	answer, err := io.ReadAll(res.Body)
	if err != nil {
		return 0, nil, nil, err
	}
	return res.StatusCode, answer, res.Header, nil
}

// shown returns the body of an answer as an error shows it: at most maxShown
// bytes of it.
func shown(answer []byte) string {
	if len(answer) > maxShown {
		return string(answer[:maxShown]) + "…"
	}
	return string(answer)
}

// sleep waits for d to pass, or for ctx to end.
func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
