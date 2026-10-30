package fixture

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// answer is what the test server answers a request with.
type answer struct {
	status     int
	retryAfter string
	body       string
}

// sendTo returns a Sender of the fixture's first batches to a server that
// answers each request with the next of answers, which records the requests'
// bodies and the waits.
func sendTo(t *testing.T, answers []answer) (*Sender, *[][]byte, *[]time.Duration) {
	t.Helper()
	var mu sync.Mutex
	var bodies [][]byte
	var waits []time.Duration
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/projects/prj_1/commands" ||
			r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Authorization") != "Bearer t0k3n" {
			t.Errorf("%s %s with %v", r.Method, r.URL.Path, r.Header)
		}
		body, _ := io.ReadAll(r.Body)
		bodies = append(bodies, body)
		if len(bodies) > len(answers) {
			t.Errorf("request %d, of %d answers", len(bodies), len(answers))
			w.WriteHeader(http.StatusTeapot)
			return
		}
		a := answers[len(bodies)-1]
		if a.retryAfter != "" {
			w.Header().Set("Retry-After", a.retryAfter)
		}
		w.WriteHeader(a.status)
		io.WriteString(w, a.body)
	}))
	t.Cleanup(srv.Close)
	s := &Sender{Endpoint: srv.URL + "/api/v1/projects/prj_1/commands",
		Header: http.Header{"Authorization": {"Bearer t0k3n"}},
		wait: func(_ context.Context, d time.Duration) error {
			waits = append(waits, d)
			return nil
		}}
	return s, &bodies, &waits
}

// firstBatches returns the small fixture, seed 1, with its first n batches
// alone.
func firstBatches(t *testing.T, n int) *Fixture {
	t.Helper()
	f, err := Generate(Small, 1)
	if err != nil {
		t.Fatal(err)
	}
	f.Batches = f.Batches[:n]
	return f
}

const applied = `{"status":"applied","seq":1}`

func TestSendRetries(t *testing.T) {
	f := firstBatches(t, 2)
	s, bodies, waits := sendTo(t, []answer{
		{status: http.StatusServiceUnavailable},
		{status: http.StatusTooManyRequests, retryAfter: "7"},
		{status: http.StatusOK, body: applied},
		{status: http.StatusOK, body: `{"status":"already_applied","seq":1}`},
	})
	var progress []int
	s.Progress = func(done, total int) {
		if total != 2 {
			t.Errorf("progress of %d batches, want 2", total)
		}
		progress = append(progress, done)
	}
	if err := s.Send(context.Background(), f); err != nil {
		t.Fatal(err)
	}
	if want := []time.Duration{time.Second, 7 * time.Second}; !reflect.DeepEqual(*waits, want) {
		t.Errorf("waited %v, want %v", *waits, want)
	}
	if !reflect.DeepEqual(progress, []int{1, 2}) {
		t.Errorf("progress %v, want [1 2]", progress)
	}
	want := [][]byte{Body(f.Batches[0]), Body(f.Batches[0]), Body(f.Batches[0]), Body(f.Batches[1])}
	if !reflect.DeepEqual(*bodies, want) {
		t.Errorf("sent %d bodies, not the batches, each again until it applied", len(*bodies))
	}
}

func TestSendRefused(t *testing.T) {
	f := firstBatches(t, 2)
	refusal := `{"code":"validation_failed","message":"` + strings.Repeat("x", 3000) + `"}`
	s, bodies, waits := sendTo(t, []answer{{status: http.StatusOK, body: applied},
		{status: http.StatusUnprocessableEntity, body: refusal}})
	err := s.Send(context.Background(), f)
	if err == nil || !strings.Contains(err.Error(), "batch 2 of 2: answered with 422: "+refusal[:maxShown]+"…") {
		t.Errorf("Send = %v, want the refusal of batch 2, cut short", err)
	}
	if len(*bodies) != 2 || len(*waits) != 0 {
		t.Errorf("sent %d requests and waited %v, want 2 and no wait", len(*bodies), *waits)
	}
}

func TestSendGivesUp(t *testing.T) {
	f := firstBatches(t, 1)
	failed := answer{status: http.StatusInternalServerError, body: "down"}
	s, bodies, waits := sendTo(t, []answer{failed, failed, failed, failed})
	if err := s.Send(context.Background(), f); err == nil || !strings.Contains(err.Error(), "answered with 500: down") {
		t.Errorf("Send = %v, want the last failure", err)
	}
	if want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}; len(*bodies) != 4 ||
		!reflect.DeepEqual(*waits, want) {
		t.Errorf("sent %d requests and waited %v, want 4 and %v", len(*bodies), *waits, want)
	}
}

func TestSendNotApplied(t *testing.T) {
	f := firstBatches(t, 1)
	s, _, _ := sendTo(t, []answer{{status: http.StatusOK, body: `{"items":[]}`}})
	if err := s.Send(context.Background(), f); err == nil || !strings.Contains(err.Error(), "no applied batch") {
		t.Errorf("Send = %v, want an answer of no applied batch refused", err)
	}
}

func TestBody(t *testing.T) {
	f := firstBatches(t, 1)
	c := f.Batches[0][0]
	want := `{"commands":[{"command_id":"` + c.ID.String() + `","type":"` + string(c.Type) + `","payload":` +
		string(c.Payload) + `}`
	if got := Body(f.Batches[0][:1]); !bytes.HasPrefix(got, []byte(want)) || !bytes.HasSuffix(got, []byte("]}")) {
		t.Errorf("Body = %s, want %s]}", got, want)
	}
}
