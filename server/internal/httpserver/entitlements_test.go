package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/dbtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/entitlements"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
)

// hostedEntitlements is the configuration of a hosted server, which needs an
// https URL.
var hostedEntitlements = map[string]string{
	"STORYCREEP_MODE":       "hosted",
	"STORYCREEP_PUBLIC_URL": "https://storycreep.example.com",
}

// fixedMeter is an entitlements.Meter whose usage of each limit is fixed.
type fixedMeter map[entitlements.LimitKey]int64

func (m fixedMeter) Usage(_ context.Context, _ pgx.Tx, _ ids.UUID, key entitlements.LimitKey) (int64, error) {
	return m[key], nil
}

// addPlanWorkspace adds a workspace with the plan to the database, as the
// superuser, and returns its ID.
func addPlanWorkspace(t *testing.T, d *dbtest.Database, plan string) ids.UUID {
	t.Helper()
	id := ids.New()
	if _, err := d.Admin(t).Exec(context.Background(),
		"INSERT INTO workspace (id, name, slug, plan_key) VALUES ($1, 'Harbour', $2, $3)",
		id, fmt.Sprintf("harbour-%x", id[10:]), plan); err != nil {
		t.Fatal(err)
	}
	return id
}

// planOf returns the plan that the row of the workspace names.
func planOf(t *testing.T, d *dbtest.Database, ws ids.UUID) string {
	t.Helper()
	var plan string
	if err := d.Admin(t).QueryRow(context.Background(), "SELECT plan_key FROM workspace WHERE id = $1",
		ws).Scan(&plan); err != nil {
		t.Fatal(err)
	}
	return plan
}

// workspaceTx runs fn in a transaction of the workspace, on d.
func workspaceTx(t *testing.T, d *dbtest.Database, ws ids.UUID, fn func(tx pgx.Tx)) {
	t.Helper()
	if err := d.Open(t).InTx(context.Background(), database.Scope{WorkspaceID: ws}, func(tx pgx.Tx) error {
		fn(tx)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// TestSelfHostedEntitlements checks that a self-hosted server gives every
// workspace the unlimited plan, the workspaces that it creates and one whose
// row names another plan, as one created while the instance was hosted: no
// limit, and every feature, unless STORYCREEP_WORKSPACE_LIMITS caps a limit,
// which then applies to every workspace. It never offers an upgrade, even
// with the public site on.
//
// Verifies: FR-ENT-01/AC2
func TestSelfHostedEntitlements(t *testing.T) {
	s, d := readyServer(t, openSignup)
	ctx := context.Background()
	si := newNamedAccount(t, s.main, "jo@example.com", "Jo")
	personal := decode(t, ids.Workspace, personalWorkspace(t, s.main, si).ID)
	if plan := planOf(t, d, personal); plan != entitlements.Unlimited {
		t.Errorf("the personal workspace has the plan %s, want unlimited", plan)
	}
	for _, ws := range []ids.UUID{personal, addPlanWorkspace(t, d, entitlements.Free)} {
		workspaceTx(t, d, ws, func(tx pgx.Tx) {
			for _, key := range entitlements.LimitKeys() {
				if q, err := s.entitlements.Limit(ctx, tx, ws, key); err != nil || q != entitlements.NoLimit {
					t.Errorf("Limit(%s) = %v, %v; want no limit", key, q, err)
				}
			}
			for _, f := range entitlements.Default().Features() {
				if ok, err := s.entitlements.Enabled(ctx, tx, ws, f); err != nil || !ok {
					t.Errorf("Enabled(%s) = %v, %v; want true", f, ok, err)
				}
			}
		})
	}

	env := map[string]string{"STORYCREEP_WORKSPACE_LIMITS": "members=3, storage_bytes=1048576",
		"STORYCREEP_PUBLIC_SITE": "on"}
	s, d = readyServer(t, env)
	ws := addPlanWorkspace(t, d, entitlements.Unlimited)
	want := map[entitlements.LimitKey]entitlements.Quota{
		entitlements.Members:      entitlements.AtMost(3),
		entitlements.Projects:     entitlements.NoLimit,
		entitlements.StorageBytes: entitlements.AtMost(1048576),
	}
	workspaceTx(t, d, ws, func(tx pgx.Tx) {
		for _, key := range entitlements.LimitKeys() {
			if q, err := s.entitlements.Limit(ctx, tx, ws, key); err != nil || q != want[key] {
				t.Errorf("with caps, Limit(%s) = %v, %v; want %v", key, q, err, want[key])
			}
		}
		for _, f := range entitlements.Default().Features() {
			if ok, err := s.entitlements.Enabled(ctx, tx, ws, f); err != nil || !ok {
				t.Errorf("with caps, Enabled(%s) = %v, %v; want true", f, ok, err)
			}
		}
	})
	if opts := entitlementOptions(loadConfig(t, env)); opts.UpgradeURL != "" || opts.Enforce {
		t.Errorf("self-hosted entitlements enforce plans (%v) or offer an upgrade at %q", opts.Enforce,
			opts.UpgradeURL)
	}
}

// TestHostedEntitlements checks that a hosted server gives a new workspace
// the free plan, and each workspace the limits and features that the plan
// file gives its plan, and offers an upgrade on its pricing page, unless it
// serves no public site.
//
// Verifies: FR-ENT-01/AC3
func TestHostedEntitlements(t *testing.T) {
	s, d := readyServer(t, hostedEntitlements)
	ctx := context.Background()
	if plan := s.entitlements.NewWorkspacePlan(); plan != entitlements.Free {
		t.Errorf("a new workspace has the plan %s, want free", plan)
	}
	for _, p := range entitlements.Default().Plans() {
		ws := addPlanWorkspace(t, d, p.Key)
		workspaceTx(t, d, ws, func(tx pgx.Tx) {
			for _, key := range entitlements.LimitKeys() {
				if q, err := s.entitlements.Limit(ctx, tx, ws, key); err != nil || q != p.Quota(key) {
					t.Errorf("%s: Limit(%s) = %v, %v; want the plan file's %v", p.Key, key, q, err, p.Quota(key))
				}
			}
			for _, f := range entitlements.Default().Features() {
				if ok, err := s.entitlements.Enabled(ctx, tx, ws, f); err != nil || ok != p.Has(f) {
					t.Errorf("%s: Enabled(%s) = %v, %v; want the plan file's %v", p.Key, f, ok, err, p.Has(f))
				}
			}
		})
	}
	if opts := entitlementOptions(loadConfig(t, hostedEntitlements)); !opts.Enforce ||
		opts.UpgradeURL != "https://storycreep.example.com/pricing" {
		t.Errorf("hosted entitlements: enforce %v, upgrade at %q", opts.Enforce, opts.UpgradeURL)
	}
	noSite := map[string]string{"STORYCREEP_PUBLIC_SITE": "off"}
	for k, v := range hostedEntitlements {
		noSite[k] = v
	}
	if opts := entitlementOptions(loadConfig(t, noSite)); opts.UpgradeURL != "" {
		t.Errorf("a hosted server without a public site offers an upgrade at %q", opts.UpgradeURL)
	}
}

// limitAnswer returns the answer to a request that err answers, as the
// API's routes answer limitExceededError(err).
func limitAnswer(t *testing.T, err error) response {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces", nil)
	w := httptest.NewRecorder()
	api.WriteError(w, r, limitExceededError(err))
	return response{status: w.Code, header: w.Header(), body: w.Body.Bytes()}
}

// TestLimitExceeded checks that a change past a limit is answered with 409
// limit_exceeded, whose details name the limit and give the workspace's
// usage and limit, and whose message names the limit, and that the answer
// links to the upgrade page only on a hosted server, where the plan sets the
// limit, and never on a self-hosted one, whose cap does.
//
// Verifies: FR-ENT-03/AC1
func TestLimitExceeded(t *testing.T) {
	ctx := context.Background()
	free, _ := entitlements.Default().Plan(entitlements.Free)
	members, _ := free.Quota(entitlements.Members).Max()
	tests := []struct {
		name    string
		env     map[string]string
		plan    string
		usage   fixedMeter
		key     entitlements.LimitKey
		limit   int64
		upgrade string
		message string
	}{
		{
			name: "hosted", env: hostedEntitlements, plan: entitlements.Free,
			usage: fixedMeter{entitlements.Members: members}, key: entitlements.Members, limit: members,
			upgrade: "https://storycreep.example.com/pricing",
			message: fmt.Sprintf("This would take the workspace past its limit of %d member", members),
		},
		{
			name: "self-hosted", env: map[string]string{"STORYCREEP_WORKSPACE_LIMITS": "projects=2"},
			plan: entitlements.Unlimited, usage: fixedMeter{entitlements.Projects: 2}, key: entitlements.Projects,
			limit: 2, message: "This would take the workspace past its limit of 2 projects.",
		},
		{
			name: "self-hosted storage", env: map[string]string{"STORYCREEP_WORKSPACE_LIMITS": "storage_bytes=1048576",
				"STORYCREEP_PUBLIC_SITE": "on"},
			plan: entitlements.Unlimited, usage: fixedMeter{entitlements.StorageBytes: 1000000},
			key: entitlements.StorageBytes, limit: 1048576,
			message: "This would take the workspace past its storage limit of 1,048,576 bytes.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := dbtest.New(t)
			opts := entitlementOptions(loadConfig(t, tt.env))
			opts.Meter = tt.usage
			s := entitlements.New(opts)
			ws := addPlanWorkspace(t, d, tt.plan)
			var err error
			workspaceTx(t, d, ws, func(tx pgx.Tx) {
				err = s.Check(ctx, tx, ws, tt.key, 100000)
			})
			res := limitAnswer(t, err)
			if res.status != http.StatusConflict || res.code() != api.CodeLimitExceeded {
				t.Fatalf("%d %s, want 409 limit_exceeded", res.status, res.body)
			}
			var env struct {
				Error struct {
					Message string `json:"message"`
					Details struct {
						Key        string  `json:"key"`
						Usage      *int64  `json:"usage"`
						Limit      *int64  `json:"limit"`
						UpgradeURL *string `json:"upgrade_url"`
					} `json:"details"`
				} `json:"error"`
			}
			dec := json.NewDecoder(strings.NewReader(string(res.body)))
			if err := dec.Decode(&env); err != nil {
				t.Fatal(err)
			}
			got := env.Error.Details
			if got.Key != string(tt.key) || got.Usage == nil || *got.Usage != tt.usage[tt.key] ||
				got.Limit == nil || *got.Limit != tt.limit {
				t.Errorf("details %s, want the key %s, usage %d, and limit %d", res.body, tt.key, tt.usage[tt.key],
					tt.limit)
			}
			switch {
			case tt.upgrade == "" && got.UpgradeURL != nil:
				t.Errorf("a self-hosted answer offers an upgrade: %s", res.body)
			case tt.upgrade != "" && (got.UpgradeURL == nil || *got.UpgradeURL != tt.upgrade):
				t.Errorf("details %s, want the upgrade link %s", res.body, tt.upgrade)
			}
			if !strings.HasPrefix(env.Error.Message, tt.message) {
				t.Errorf("message %q, want one that starts %q", env.Error.Message, tt.message)
			}
		})
	}

	// Any other error is left to the route's own mapping.
	other := errors.New("the database is gone")
	if err := limitExceededError(other); err != other {
		t.Errorf("limitExceededError(%v) = %v, want it unchanged", other, err)
	}
}

// TestLimitExceededHasOneSource checks that limitExceededError is the only
// code that answers with limit_exceeded, so that every such answer carries
// the limit's key, usage, and limit: outside the registry of error codes,
// no other code of the server names the code.
//
// Verifies: FR-ENT-03/AC1
func TestLimitExceededHasOneSource(t *testing.T) {
	root := filepath.Join("..")
	allowed := []string{filepath.Join("api", "errors.go"), filepath.Join("httpserver", "entitlements.go")}
	fset := token.NewFileSet()
	var found []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == "testdata" {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.Ident:
				if n.Name == "CodeLimitExceeded" && !slices.Contains(allowed, rel) {
					found = append(found, fset.Position(n.Pos()).String())
				}
			case *ast.BasicLit:
				if n.Kind == token.STRING && strings.Contains(n.Value, `"limit_exceeded"`) &&
					!slices.Contains(allowed, rel) {
					found = append(found, fset.Position(n.Pos()).String())
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) > 0 {
		t.Errorf("code outside limitExceededError answers with limit_exceeded: %v", found)
	}
}
