package httpserver

import (
	"context"
	"encoding/json"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/CyberCute-Studio/storycreep/server/internal/component"
	"github.com/CyberCute-Studio/storycreep/server/internal/connection"
	"github.com/CyberCute-Studio/storycreep/server/internal/fixture"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/node"
	"github.com/CyberCute-Studio/storycreep/server/internal/ratelimit"
	"github.com/CyberCute-Studio/storycreep/server/internal/variable"
)

// The server's benchmarks (PRD §9.1, plan note N52) run against a project of
// the large fixture, which an owner, signed in, edits through the API, as the
// web app does. CI's benchmark job runs them on a pull request's base and on
// its head, on one machine, and fails on a regression of more than 20%
// (cmd/benchgate); a benchmark checks its own target of §9.1.

// commandRoundTripTarget is NFR-PERF-07's target: the p95 of a command's
// round trip, server-side.
const commandRoundTripTarget = 150 * time.Millisecond

// roundTrips are the batches of BenchmarkCommandRoundTrip: what an author
// does most as they edit, each a batch of its own, as the web app sends it.
// Each batch is given n, a number that no other batch of the benchmarks has,
// which its value holds, so that every batch changes what it names: a batch
// that changed nothing, as one that sets a text that the node has already,
// would be timed as the no-op that the server makes of it, with no revision,
// no compilation, and no derivation.
var roundTrips = []struct {
	name  string
	batch func(m *benchModel, r *rand.Rand, n int) []wireCommand
}{
	{"node.update_text", func(m *benchModel, r *rand.Rand, n int) []wireCommand {
		return []wireCommand{{Type: string(node.Update), Payload: obj("id", pickOf(r, m.elements).id,
			"text", paragraph("The tide turns at "+strconv.Itoa(n)+"."))}}
	}},
	{"node.update_script", func(m *benchModel, r *rand.Rand, n int) []wireCommand {
		v := pickOf(r, m.ints)
		return []wireCommand{{Type: string(node.Update), Payload: obj("id", pickOf(r, m.elements).id,
			"on_enter", v.key+" += "+strconv.Itoa(n))}}
	}},
	{"node.move", func(m *benchModel, r *rand.Rand, n int) []wireCommand {
		// A fixture's positions, and a created node's, are whole numbers, and
		// no two batches have the same n, so that x is never the node's.
		return []wireCommand{{Type: string(node.Move), Payload: obj("nodes", []any{obj("id",
			pickOf(r, m.elements).id, "position", obj("x", float64(n)+0.5, "y", float64(r.IntN(20000))))})}}
	}},
	{"node.create_connected", func(m *benchModel, r *rand.Rand, _ int) []wireCommand {
		from := pickOf(r, m.elements)
		id := ids.Encode(ids.Node, ids.New())
		return []wireCommand{
			{Type: string(node.Create), Payload: obj("id", id, "board_id", from.board, "type", "element",
				"text", paragraph("A new line."), "position", obj("x", float64(r.IntN(20000)), "y", 0.0))},
			{Type: string(connection.Create), Payload: obj("id", ids.Encode(ids.Connection, ids.New()),
				"from_node_id", from.id, "to_node_id", id)},
		}
	}},
	{"connection.update_condition", func(m *benchModel, r *rand.Rand, n int) []wireCommand {
		v := pickOf(r, m.ints)
		return []wireCommand{{Type: string(connection.Update), Payload: obj("id", pickOf(r, m.connections),
			"condition", v.key+" >= "+strconv.Itoa(n))}}
	}},
	{"variable.set_default", func(m *benchModel, r *rand.Rand, n int) []wireCommand {
		return []wireCommand{{Type: string(variable.SetDefault), Payload: obj("id", pickOf(r, m.ints).id,
			"default", n)}}
	}},
}

// The targets of the usage queries: the p95 of a variable's (FR-DEP-01 AC2)
// and of a component's (FR-COMP-04 AC2), server-side, for a project of
// 5,000 nodes, which the large fixture's 10,000 exceed.
const (
	variableUsagesTarget  = 200 * time.Millisecond
	componentUsagesTarget = 300 * time.Millisecond
)

// usageQueries are the requests of BenchmarkUsages: a page of the largest
// size of each list of usages, of a variable or a component of the fixture
// that each request picks anew, as the panel of a variable's or a
// component's usages reads it.
var usageQueries = []struct {
	name   string
	target time.Duration
	path   func(m *benchModel, r *rand.Rand, project string) string
}{
	{"variable_reads", variableUsagesTarget, func(m *benchModel, r *rand.Rand, p string) string {
		return variableUsagesPath(p, pickOf(r, m.variables), "read")
	}},
	{"variable_writes", variableUsagesTarget, func(m *benchModel, r *rand.Rand, p string) string {
		return variableUsagesPath(p, pickOf(r, m.variables), "write")
	}},
	{"component_speakers", componentUsagesTarget, func(m *benchModel, r *rand.Rand, p string) string {
		return componentUsagesPath(p, pickOf(r, m.components), "speaker")
	}},
	{"component_mentions", componentUsagesTarget, func(m *benchModel, r *rand.Rand, p string) string {
		return componentUsagesPath(p, pickOf(r, m.components), "mention")
	}},
	{"component_reads", componentUsagesTarget, func(m *benchModel, r *rand.Rand, p string) string {
		return componentUsagesPath(p, pickOf(r, m.components), "read")
	}},
	{"component_writes", componentUsagesTarget, func(m *benchModel, r *rand.Rand, p string) string {
		return componentUsagesPath(p, pickOf(r, m.components), "write")
	}},
}

// firstBatch is the n of the benchmarks' first batch, beyond every number
// that a fixture's texts, scripts, conditions, and defaults hold, so that no
// batch's value is one that the fixture gave.
const firstBatch = 1_000

// Verifies: NFR-PERF-07
func BenchmarkCommandRoundTrip(b *testing.B) {
	bs := newBenchServer(b, fixture.Large)
	n := firstBatch
	for _, rt := range roundTrips {
		// go test runs a sub-benchmark's function again for each of its runs
		// (-count), so the source is the sub-benchmark's own, and a later run
		// edits other entities than the first did.
		r := rand.New(rand.NewPCG(1, 0x45))
		b.Run(rt.name, func(b *testing.B) {
			var took []time.Duration
			for b.Loop() {
				// The batch is made with the timer stopped, so that ns/op,
				// which the build is gated on too, times the round trip
				// alone, as p95-ms does.
				b.StopTimer()
				n++
				body := commandBatch(b, rt.batch(bs.model, r, n)...)
				b.StartTimer()
				// The owner edits from one address, as a person does, and
				// as the fixture was sent from, so that the rate limiter
				// counts each batch in the rows that it counted the
				// others in.
				start := time.Now()
				res := send(bs.s.main, http.MethodPost, bs.path, body, "", bs.header)
				took = append(took, time.Since(start))
				if res.status != http.StatusOK {
					b.Fatalf("%s: %d %s", rt.name, res.status, res.body)
				}
			}
			p95 := percentile(took, 95)
			b.ReportMetric(float64(p95)/float64(time.Millisecond), "p95-ms")
			if p95 >= commandRoundTripTarget {
				b.Errorf("NFR-PERF-07: the p95 of %s is %v, want under %v", rt.name, p95, commandRoundTripTarget)
			}
		})
	}
}

// BenchmarkUsages times the usage queries of a variable and of a component,
// each a page of 200 usages, the largest, as a client reads the first page
// of each list, and checks the p95 of each against its target.
//
// Verifies: FR-DEP-01/AC2, FR-COMP-04/AC2
func BenchmarkUsages(b *testing.B) {
	bs := newBenchServer(b, fixture.Large)
	for _, q := range usageQueries {
		r := rand.New(rand.NewPCG(1, 0x46))
		b.Run(q.name, func(b *testing.B) {
			var took []time.Duration
			for b.Loop() {
				b.StopTimer()
				path := q.path(bs.model, r, bs.project) + "&limit=200"
				b.StartTimer()
				start := time.Now()
				res := send(bs.s.main, http.MethodGet, path, nil, "", bs.header)
				took = append(took, time.Since(start))
				if res.status != http.StatusOK {
					b.Fatalf("%s: %d %s", path, res.status, res.body)
				}
			}
			p95 := percentile(took, 95)
			b.ReportMetric(float64(p95)/float64(time.Millisecond), "p95-ms")
			if p95 >= q.target {
				b.Errorf("the p95 of %s is %v, want under %v (FR-DEP-01 AC2, FR-COMP-04 AC2)", q.name, p95, q.target)
			}
		})
	}
}

// validationTarget is NFR-PERF-10's target, the validation of the large
// fixture's project, server-side, which is FR-DEP-03 AC2's of a project of
// 5,000 nodes, the large fixture's half.
const validationTarget = 3 * time.Second

// BenchmarkValidation times the validation of the large fixture's project,
// as a client reads its report, and checks the p95 against its target.
//
// Verifies: NFR-PERF-10, FR-DEP-03/AC2
func BenchmarkValidation(b *testing.B) {
	bs := newBenchServer(b, fixture.Large)
	path := validationPath(bs.project)
	var took []time.Duration
	var last response
	for b.Loop() {
		start := time.Now()
		last = send(bs.s.main, http.MethodGet, path, nil, "", bs.header)
		took = append(took, time.Since(start))
		if last.status != http.StatusOK {
			b.Fatalf("%s: %d %s", path, last.status, last.body)
		}
	}
	var report struct {
		Counts   map[string]int `json:"counts"`
		Findings []struct {
			Code string `json:"code"`
		} `json:"findings"`
	}
	if err := json.Unmarshal(last.body, &report); err != nil {
		b.Fatal(err)
	}
	codes := map[string]int{}
	for _, f := range report.Findings {
		codes[f.Code]++
	}
	b.Logf("the report is %d bytes, of %v, by code %v", len(last.body), report.Counts, codes)
	p95 := percentile(took, 95)
	b.ReportMetric(float64(p95)/float64(time.Millisecond), "p95-ms")
	if p95 >= validationTarget {
		b.Errorf("the p95 of the validation is %v, want under %v (NFR-PERF-10, FR-DEP-03 AC2)", p95, validationTarget)
	}
}

// benchServer is a server with a project of a fixture, which its owner,
// signed in, edits.
type benchServer struct {
	s       *server
	project string
	path    string
	header  http.Header
	model   *benchModel
}

// newBenchServer returns a server with a new project of the fixture of the
// size, seed 1, which the owner sent to the command endpoint as
// cmd/fixturegen does, and what its commands may name.
func newBenchServer(b *testing.B, size fixture.Size) *benchServer {
	b.Helper()
	if testing.Short() {
		b.Skip("skipped with -short: needs PostgreSQL, which the benchmarks run in Docker")
	}
	s, d, owner, ws := inviteServer(b, "open", nil)
	// The benchmarks send many more batches, and requests that read, in a
	// minute than a person does.
	policy := ratelimit.DefaultPolicy()
	for _, bucket := range []ratelimit.Bucket{ratelimit.Write, ratelimit.Read} {
		policy.Rates[bucket].Limits[ratelimit.User] = 1_000_000
		policy.Rates[bucket].Limits[ratelimit.IP] = 1_000_000
	}
	s.limits = ratelimit.New(readyDB{s.ready}, policy)
	f, err := fixture.Generate(size, 1)
	if err != nil {
		b.Fatal(err)
	}
	p := createProject(b, s.main, owner, ws, f.Name).ID
	bs := &benchServer{s: s, project: p, path: commandsPath(p), header: owner.header(true),
		model: newBenchModel(b, f)}
	start := time.Now()
	sender := fixture.Sender{Client: &http.Client{Transport: handlerTransport{s.main}},
		Endpoint: "http://localhost:8080" + bs.path, Header: bs.header}
	if err := sender.Send(context.Background(), f); err != nil {
		b.Fatal(err)
	}
	b.Logf("loaded the %s fixture, %d batches, in %v", size, len(f.Batches), time.Since(start).Round(time.Millisecond))
	// The fixture's tests load every size but large, which only the
	// benchmarks do, so they check here that its expressions compile, as
	// the tests do of the others'.
	ctx, admin := context.Background(), d.Admin(b)
	var invalid int
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM expression WHERE project_id = $1 AND NOT is_valid`,
		decode(b, ids.Project, p)).Scan(&invalid); err != nil {
		b.Fatal(err)
	}
	if invalid > 0 {
		b.Fatalf("%d expressions of the %s fixture do not compile", invalid, size)
	}
	// The load leaves the autovacuum and the autoanalyze of what it wrote,
	// and a checkpoint of it, to come, which would land in the timed batches
	// of some runs and not of others, so the benchmarks start from a database
	// that is vacuumed, analyzed, and checkpointed, as a project that has
	// been edited a while is.
	for _, stmt := range []string{"VACUUM (ANALYZE)", "CHECKPOINT"} {
		if _, err := admin.Exec(ctx, stmt); err != nil {
			b.Fatalf("%s: %v", stmt, err)
		}
	}
	return bs
}

// handlerTransport is an http.RoundTripper that serves each request with a
// handler, as a server of its own would: with the request's Content-Length,
// which a client's request of a body of its length declares, and closing its
// body once served, as a RoundTripper must.
type handlerTransport struct{ h http.Handler }

func (t handlerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Body != nil {
		defer r.Body.Close()
	}
	in := httptest.NewRequestWithContext(r.Context(), r.Method, r.URL.String(), r.Body)
	in.Header = r.Header.Clone()
	in.ContentLength = r.ContentLength
	w := httptest.NewRecorder()
	t.h.ServeHTTP(w, in)
	return w.Result(), nil
}

// benchModel is what the benchmarks' commands and queries name of a
// fixture: its elements, the connections that leave them, its global int
// variables, and the IDs of all its variables and of its components.
type benchModel struct {
	elements    []benchNode
	connections []string
	ints        []benchVariable
	variables   []string
	components  []string
}

type benchNode struct{ id, board string }

type benchVariable struct{ id, key string }

// benchPayload holds the fields of the fixture's payloads that newBenchModel
// reads.
type benchPayload struct {
	ID       string `json:"id"`
	BoardID  string `json:"board_id"`
	Type     string `json:"type"`
	Key      string `json:"key"`
	Scope    string `json:"scope"`
	FromPort string `json:"from_port"`
	IsEnding bool   `json:"is_ending"`
}

// newBenchModel reads the model of the fixture from its commands.
func newBenchModel(b *testing.B, f *fixture.Fixture) *benchModel {
	b.Helper()
	m := &benchModel{}
	for _, batch := range f.Batches {
		for _, c := range batch {
			var p benchPayload
			if err := json.Unmarshal(c.Payload, &p); err != nil {
				b.Fatal(err)
			}
			switch {
			case c.Type == node.Create && p.Type == "element" && !p.IsEnding:
				m.elements = append(m.elements, benchNode{id: p.ID, board: p.BoardID})
			case c.Type == connection.Create && p.FromPort == "":
				m.connections = append(m.connections, p.ID)
			case c.Type == component.Create:
				m.components = append(m.components, p.ID)
			}
			if c.Type == variable.Create {
				m.variables = append(m.variables, p.ID)
				if p.Scope == "global" && p.Type == "int" {
					m.ints = append(m.ints, benchVariable{id: p.ID, key: p.Key})
				}
			}
		}
	}
	if len(m.elements) == 0 || len(m.connections) == 0 || len(m.ints) == 0 || len(m.components) == 0 {
		b.Fatalf("the %s fixture has %d elements, %d connections that leave them, %d global ints, and %d components",
			f.Size, len(m.elements), len(m.connections), len(m.ints), len(m.components))
	}
	return m
}

// pickOf returns a random element of xs, which is not empty.
func pickOf[T any](r *rand.Rand, xs []T) T { return xs[r.IntN(len(xs))] }

// percentile returns the pth percentile of the durations, the smallest that
// p in 100 of them are at most.
func percentile(ds []time.Duration, p int) time.Duration {
	if len(ds) == 0 {
		return 0
	}
	sorted := slices.Clone(ds)
	slices.Sort(sorted)
	return sorted[max((len(sorted)*p+99)/100-1, 0)]
}
