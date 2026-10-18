package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/dbtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/logging"
	"github.com/CyberCute-Studio/storycreep/server/internal/setup"
)

// setupCode returns the setup code of the database of s.
func setupCode(t *testing.T, s *server) string {
	t.Helper()
	code, err := s.setups.Code(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return code
}

// setupRequest returns the JSON body of a setup with the code, whose owner
// has the address email and testPassword, with the fields of extra besides,
// which replace the others.
func setupRequest(t *testing.T, code, email string, extra map[string]any) []byte {
	t.Helper()
	body := map[string]any{
		"setup_code": code,
		"owner":      map[string]any{"email": email, "display_name": "Jo Novak", "password": testPassword},
		"workspace":  map[string]any{"name": "Harbour Studio"},
	}
	maps.Copy(body, extra)
	return jsonBody(t, body)
}

// completedSetupFrom returns the body of an answer of 201 to a setup.
func completedSetupFrom(t *testing.T, res response) completedSetupBody {
	t.Helper()
	var b completedSetupBody
	if res.status != http.StatusCreated || json.Unmarshal(res.body, &b) != nil {
		t.Fatalf("setup: %d %s, want 201", res.status, res.body)
	}
	return b
}

// completeSetup completes the setup of s with the owner email and
// testPassword, and the fields of extra.
func completeSetup(t *testing.T, s *server, email string, extra map[string]any) completedSetupBody {
	t.Helper()
	return completedSetupFrom(t, send(s.main, http.MethodPost, setupPath, setupRequest(t, setupCode(t, s), email,
		extra), "", nil))
}

// TestSetupRouteGone checks that once setup has completed, the setup route
// answers 404 for everyone: for a client without credentials, for the
// owner, signed in, and for a setup with the right code, or one that is not
// valid. Before, it answers 200.
//
// Verifies: FR-ADMIN-02/AC1
func TestSetupRouteGone(t *testing.T) {
	t.Parallel()
	s, _ := pendingServer(t, map[string]string{"STORYCREEP_ASSET_STORAGE": ""})
	res := send(s.main, http.MethodGet, setupPath, nil, "", nil)
	if res.status != http.StatusOK || strings.TrimSpace(string(res.body)) !=
		`{"signup_policy_fixed":false,"signup_policy":"closed","smtp_fixed":false,"storage_fixed":false}` {
		t.Errorf("GET %s before setup: %d %s", setupPath, res.status, res.body)
	}
	code := setupCode(t, s)
	completeSetup(t, s, "jo@example.com", nil)
	owner := signIn(t, s.main, "jo@example.com")
	for _, c := range []struct {
		name, method string
		body         []byte
		header       http.Header
	}{
		{"read without credentials", http.MethodGet, nil, nil},
		{"read by the owner", http.MethodGet, nil, owner.header(true)},
		{"a setup with the code", http.MethodPost, setupRequest(t, code, "mallory@example.com", nil), nil},
		{"a setup by the owner", http.MethodPost, setupRequest(t, code, "mallory@example.com", nil),
			owner.header(true)},
		{"a setup that is not valid", http.MethodPost, []byte(`{"owner": 1}`), nil},
		{"a setup with a wrong code", http.MethodPost, setupRequest(t, "0000-0000-0000-0000", "m@example.com", nil),
			nil},
	} {
		res := send(s.main, c.method, setupPath, c.body, "", c.header)
		if res.status != http.StatusNotFound || res.code() != api.CodeNotFound {
			t.Errorf("%s: %d %s, want 404 not_found", c.name, res.status, res.body)
		}
	}
}

// TestSetupOwner checks what a setup gives its owner: an account that signs
// in, and exactly one workspace, the one that setup created, which the owner
// owns, and which is their personal workspace, so that signing in gives them
// no other.
func TestSetupOwner(t *testing.T) {
	t.Parallel()
	s, d := pendingServer(t, nil)
	done := completeSetup(t, s, "jo@example.com", map[string]any{
		"workspace": map[string]any{"name": "Harbour Studio", "slug": "harbour"},
	})
	if done.Workspace.Name != "Harbour Studio" || done.Workspace.Slug != "harbour" ||
		done.Workspace.DefaultLocale != "en" {
		t.Errorf("the workspace is %+v", done.Workspace)
	}
	owner := signIn(t, s.main, "jo@example.com")
	if owner.body.UserID != done.OwnerID {
		t.Errorf("the owner signs in as %s, and setup says %s", owner.body.UserID, done.OwnerID)
	}
	got := personalWorkspace(t, s.main, owner)
	if got.ID != done.Workspace.ID || got.Role != "owner" {
		t.Errorf("the owner's workspace is %+v, want %s, owned", got, done.Workspace.ID)
	}
	if n := countRows(t, d, `SELECT count(*) FROM "user" WHERE is_instance_admin`); n != 1 {
		t.Errorf("%d instance admins, want the owner", n)
	}
}

// TestRegisterBeforeSetup checks that until setup has completed, registering
// is refused with setup_required whatever the signup policy and whatever the
// registration holds, creates no account, and leaves the setup route open.
//
// Verifies: FR-ADMIN-02/AC4
func TestRegisterBeforeSetup(t *testing.T) {
	t.Parallel()
	for _, env := range []map[string]string{
		{"STORYCREEP_SIGNUP": "open"},
		{"STORYCREEP_SIGNUP": "invite"},
		{"STORYCREEP_SIGNUP": "closed"},
		nil,
		{"STORYCREEP_MODE": "hosted", "STORYCREEP_PUBLIC_URL": "https://storycreep.example.com"},
	} {
		s, d := pendingServer(t, env)
		for _, body := range [][]byte{
			registration(t, "jo@example.com", "Jo", testPassword),
			registration(t, "", "", "short"),
		} {
			res := send(s.main, http.MethodPost, registerPath, body, "", nil)
			if res.status != http.StatusForbidden || res.code() != api.CodeSetupRequired {
				t.Errorf("%v: register: %d %s, want 403 setup_required", env, res.status, res.body)
			}
		}
		if n := countAccounts(t, d); n != 0 {
			t.Errorf("%v: %d accounts, want none", env, n)
		}
		if res := send(s.main, http.MethodGet, setupPath, nil, "", nil); res.status != http.StatusOK {
			t.Errorf("%v: GET %s: %d %s", env, setupPath, res.status, res.body)
		}
	}
}

// TestSetupRefuses checks the answers to a setup with a wrong code, or that
// is not valid, which create nothing and leave setup pending.
func TestSetupRefuses(t *testing.T) {
	t.Parallel()
	s, d := pendingServer(t, nil)
	code := setupCode(t, s)
	for _, c := range []struct {
		name      string
		body      []byte
		status    int
		code      api.Code
		locations []string
	}{
		{"a wrong code", setupRequest(t, "7K2M-Q9XD-4RTB-H8VW", "jo@example.com", nil), http.StatusForbidden,
			api.CodeInvalidSetupCode, nil},
		{"no code", setupRequest(t, "", "jo@example.com", nil), http.StatusForbidden, api.CodeInvalidSetupCode, nil},
		{"fields", setupRequest(t, code, "jo@example.com", map[string]any{
			"owner":     map[string]any{"email": "jo@example.com", "display_name": " ", "password": testPassword},
			"workspace": map[string]any{"name": " "},
		}), http.StatusUnprocessableEntity, api.CodeValidationFailed,
			[]string{"body.owner.display_name", "body.workspace.name"}},
		{"an address", setupRequest(t, code, "jo", nil), http.StatusUnprocessableEntity, api.CodeValidationFailed,
			[]string{"body.owner.email"}},
		{"a weak password", setupRequest(t, code, "jo@example.com", map[string]any{
			"owner": map[string]any{"email": "jo@example.com", "display_name": "Jo", "password": "Unbelievable"},
		}), http.StatusUnprocessableEntity, api.CodeWeakPassword, []string{"body.owner.password"}},
		{"a reserved slug", setupRequest(t, code, "jo@example.com", map[string]any{
			"workspace": map[string]any{"name": "Harbour Studio", "slug": "setup"},
		}), http.StatusUnprocessableEntity, api.CodeSlugInvalid, []string{"body.workspace.slug"}},
		{"an unknown policy", setupRequest(t, code, "jo@example.com", map[string]any{"signup_policy": "anyone"}),
			http.StatusUnprocessableEntity, api.CodeValidationFailed, []string{"body.signup_policy"}},
		{"an SMTP server", setupRequest(t, code, "jo@example.com", map[string]any{"smtp": map[string]any{
			"host": "smtp example.com", "port": 25, "security": "none", "username": "jo", "from": "noreply"}}),
			http.StatusUnprocessableEntity, api.CodeValidationFailed,
			[]string{"body.smtp.host", "body.smtp.security", "body.smtp.password", "body.smtp.from"}},
		{"a bucket", setupRequest(t, code, "jo@example.com", map[string]any{"storage": map[string]any{
			"host": "s3 example.com", "bucket": "Harbour", "region": "eu west", "access_key_id": "key",
			"secret_access_key": "has a space"}}),
			http.StatusUnprocessableEntity, api.CodeValidationFailed, []string{"body.storage.host",
				"body.storage.bucket", "body.storage.region", "body.storage.secret_access_key"}},
		{"a bucket's security", setupRequest(t, code, "jo@example.com", map[string]any{"storage": map[string]any{
			"host": "s3.example.com", "security": "starttls", "bucket": "harbour", "access_key_id": "key",
			"secret_access_key": "secret"}}),
			http.StatusUnprocessableEntity, api.CodeValidationFailed, []string{"body.storage.security"}},
		{"an SMTP server's port", setupRequest(t, code, "jo@example.com", map[string]any{"smtp": map[string]any{
			"host": "smtp.example.com", "port": 0, "security": "tls", "from": "noreply@example.com"}}),
			http.StatusUnprocessableEntity, api.CodeValidationFailed, []string{"body.smtp.port"}},
		{"no owner", jsonBody(t, map[string]any{"setup_code": code, "workspace": map[string]any{"name": "W"}}),
			http.StatusUnprocessableEntity, api.CodeValidationFailed, []string{"body"}},
	} {
		res := send(s.main, http.MethodPost, setupPath, c.body, "", nil)
		if res.status != c.status || res.code() != c.code {
			t.Errorf("%s: %d %s, want %d %s", c.name, res.status, res.body, c.status, c.code)
			continue
		}
		if got := problemLocations(t, res); !slices.Equal(got, c.locations) {
			t.Errorf("%s: the problems are at %q, want %q", c.name, got, c.locations)
		}
	}
	if n := countAccounts(t, d) + countRows(t, d, "SELECT count(*) FROM workspace"); n != 0 {
		t.Errorf("%d accounts and workspaces, want none", n)
	}
	if res := send(s.main, http.MethodGet, setupPath, nil, "", nil); res.status != http.StatusOK {
		t.Errorf("GET %s: %d %s, want 200", setupPath, res.status, res.body)
	}
}

// problemLocations returns the locations of an envelope's details.errors.
func problemLocations(t *testing.T, res response) []string {
	t.Helper()
	var env struct {
		Error struct {
			Details struct {
				Errors []api.Problem `json:"errors"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(res.body, &env); err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, p := range env.Error.Details.Errors {
		out = append(out, p.Location)
	}
	return out
}

// TestSetupSignupPolicy checks that registering, once setup has completed,
// follows the signup policy that setup chose, closed if it chose none,
// unless STORYCREEP_SIGNUP sets one, which overrides it, and which the setup
// route reports.
func TestSetupSignupPolicy(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		env    map[string]string
		chosen string
		get    string
		want   api.Code
	}{
		{nil, "open", `{"signup_policy_fixed":false,"signup_policy":"closed","smtp_fixed":false,"storage_fixed":true}`, ""},
		{nil, "invite", `{"signup_policy_fixed":false,"signup_policy":"closed","smtp_fixed":false,"storage_fixed":true}`, api.CodeInviteRequired},
		{nil, "", `{"signup_policy_fixed":false,"signup_policy":"closed","smtp_fixed":false,"storage_fixed":true}`, api.CodeSignupClosed},
		{map[string]string{"STORYCREEP_SIGNUP": "closed"}, "open",
			`{"signup_policy_fixed":true,"signup_policy":"closed","smtp_fixed":false,"storage_fixed":true}`, api.CodeSignupClosed},
		{map[string]string{"STORYCREEP_SIGNUP": "open"}, "closed",
			`{"signup_policy_fixed":true,"signup_policy":"open","smtp_fixed":false,"storage_fixed":true}`, ""},
		{map[string]string{"STORYCREEP_MODE": "hosted", "STORYCREEP_PUBLIC_URL": "https://storycreep.example.com"},
			"closed", `{"signup_policy_fixed":true,"signup_policy":"open","smtp_fixed":true,"storage_fixed":true}`, ""},
	} {
		s, _ := pendingServer(t, c.env)
		if res := send(s.main, http.MethodGet, setupPath, nil, "", nil); strings.TrimSpace(string(res.body)) != c.get {
			t.Errorf("%v: GET %s: %d %s, want %s", c.env, setupPath, res.status, res.body, c.get)
		}
		var extra map[string]any
		if c.chosen != "" {
			extra = map[string]any{"signup_policy": c.chosen}
		}
		completeSetup(t, s, "owner@example.com", extra)
		res := send(s.main, http.MethodPost, registerPath, registration(t, "jo@example.com", "Jo", testPassword), "",
			nil)
		switch {
		case c.want == "" && res.status != http.StatusAccepted:
			t.Errorf("%v, %q chosen: register: %d %s, want 202", c.env, c.chosen, res.status, res.body)
		case c.want != "" && res.code() != c.want:
			t.Errorf("%v, %q chosen: register: %d %s, want %s", c.env, c.chosen, res.status, res.body, c.want)
		}
	}
}

// unattendedEnv is the configuration of unattended setup that
// TestUnattendedSetupMatchesTheAPI completes setup with, and that
// unattendedBody sends to the API.
var unattendedEnv = map[string]string{
	"STORYCREEP_SETUP_OWNER_EMAIL":    "Jo@Example.com",
	"STORYCREEP_SETUP_OWNER_NAME":     "Jo Novak",
	"STORYCREEP_SETUP_OWNER_PASSWORD": testPassword,
	"STORYCREEP_SETUP_WORKSPACE_NAME": "Harbour Studio",
	"STORYCREEP_SETUP_WORKSPACE_SLUG": "harbour",
	"STORYCREEP_SETUP_SIGNUP":         "invite",
	"STORYCREEP_SETUP_SMTP_URL":       "smtps://jo%40example.com:p%40ss@SMTP.Example.com",
	"STORYCREEP_SETUP_SMTP_FROM":      "Harbour Studio <noreply@example.com>",
	"STORYCREEP_SETUP_ASSET_STORAGE":  "s3://AKIA%2FKEY:se%2Fcret@S3.Example.com:9000/harbour?region=eu-west-1&security=none",
}

// instanceState describes what setup leaves in the database d, without what
// differs whatever setup does: IDs, times, the password's hash, which is
// salted, and the client's address and user agent, which unattended setup
// has none of.
func instanceState(t *testing.T, d *dbtest.Database) string {
	t.Helper()
	rows, err := d.Admin(t).Query(context.Background(), `
		SELECT format('user %s %s %s %s locale=%s instance_admin=%s personal=%s verified=%s password=%s',
			email, display_name, status, locale IS NULL, locale, is_instance_admin,
			personal_workspace_created_at IS NOT NULL, email_verified_at IS NOT NULL, password_hash IS NOT NULL)
		FROM "user"
		UNION ALL SELECT format('workspace %s %s %s %s %s deleted=%s', name, slug, plan_key, settings, status,
			deleted_at IS NOT NULL)
		FROM workspace
		UNION ALL SELECT format('membership %s of %s by %s', m.role, w.slug, u.email)
		FROM membership m JOIN workspace w ON w.id = m.workspace_id JOIN "user" u ON u.id = m.user_id
		UNION ALL SELECT format('instance completed=%s signup=%s smtp=%s %s %s %s %s %s s3=%s %s %s %s %s %s %s',
			setup_completed_at IS NOT NULL, signup_policy, smtp_host, smtp_port, smtp_security, smtp_username,
			smtp_password, smtp_from, s3_host, s3_port, s3_security, s3_bucket, s3_region, s3_access_key_id,
			s3_secret_access_key)
		FROM instance
		UNION ALL SELECT format('audit %s %s %s %s in=%s actor=%s token=%s subject=%s', a.action, a.subject_kind,
			a.result, a.metadata, w.slug, u.email, a.actor_token_id, a.subject_id = w.id)
		FROM audit_entry a LEFT JOIN workspace w ON w.id = a.workspace_id LEFT JOIN "user" u ON u.id = a.actor_user_id
		ORDER BY 1`)
	var lines []string
	if err == nil {
		for rows.Next() {
			var line string
			if err = rows.Scan(&line); err != nil {
				break
			}
			lines = append(lines, line)
		}
		rows.Close()
		if err == nil {
			err = rows.Err()
		}
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(lines, "\n")
}

// TestUnattendedSetupMatchesTheAPI completes setup on one instance through
// the API, and on another unattended, from the STORYCREEP_SETUP_ keys, when
// the server starts, with the same values, and checks that the two
// databases hold the same, and that the owner signs in to both with the
// same password.
//
// Verifies: FR-ADMIN-02/AC2
func TestUnattendedSetupMatchesTheAPI(t *testing.T) {
	t.Parallel()
	viaAPI, apiDB := pendingServer(t, nil)
	completeSetup(t, viaAPI, "Jo@Example.com", map[string]any{
		"owner":         map[string]any{"email": "Jo@Example.com", "display_name": "Jo Novak", "password": testPassword},
		"workspace":     map[string]any{"name": "Harbour Studio", "slug": "harbour"},
		"signup_policy": "invite",
		"smtp": map[string]any{"host": "smtp.example.com", "port": 465, "security": "tls",
			"username": "jo@example.com", "password": "p@ss", "from": "Harbour Studio <noreply@example.com>"},
		"storage": map[string]any{"host": "S3.Example.com", "port": 9000, "security": "none", "bucket": "harbour",
			"region": "eu-west-1", "access_key_id": "AKIA/KEY", "secret_access_key": "se/cret"},
	})

	d := dbtest.New(t)
	env := map[string]string{
		"STORYCREEP_DATABASE_URL":       d.AppURL.Reveal(),
		"STORYCREEP_DATABASE_ADMIN_URL": d.AdminURL.Reveal(),
	}
	maps.Copy(env, unattendedEnv)
	inst := startInstance(t, loadConfig(t, env), Options{AutoMigrate: true})
	inst.waitFor(t, "/readyz", http.StatusOK)
	if len(inst.log.records(t, setupUnattendedMessage)) != 1 || len(inst.log.records(t, setupPendingMessage)) != 0 {
		t.Errorf("the log does not say that the server completed setup:\n%s", inst.log)
	}

	if a, u := instanceState(t, apiDB), instanceState(t, d); a != u {
		t.Errorf("the API's setup left\n%s\nand unattended setup\n%s", a, u)
	} else if !strings.Contains(a, "instance completed=t signup=invite smtp=smtp.example.com 465 tls jo@example.com "+
		"p@ss Harbour Studio <noreply@example.com> s3=s3.example.com 9000 none harbour eu-west-1 AKIA/KEY se/cret") {
		t.Errorf("setup left\n%s", a)
	}
	signIn(t, viaAPI.main, "jo@example.com")
	res := inst.send(t, http.MethodPost, loginPath, credentials(t, "jo@example.com", testPassword), nil)
	if res.status != http.StatusOK {
		t.Errorf("the owner of unattended setup signs in: %d %s", res.status, res.body)
	}
}

// TestSetupStep checks what the setup step of startup does: without the
// STORYCREEP_SETUP_ keys, it logs the setup code while setup is pending,
// which then completes setup, and not once it has completed; with them,
// once setup has completed, it warns that it ignores them; and a value of
// them that setup refuses stops the server, naming its key.
func TestSetupStep(t *testing.T) {
	t.Parallel()
	d := dbtest.New(t)
	cfg := dbConfig(t, d)
	inst := startInstance(t, cfg, Options{AutoMigrate: true})
	inst.waitFor(t, "/readyz", http.StatusOK)
	rs := inst.log.records(t, setupPendingMessage)
	if len(rs) != 1 || rs[0]["level"] != "WARN" {
		t.Fatalf("the log does not give the setup code:\n%s", inst.log)
	}
	code, _ := rs[0]["setup_code"].(string)
	res := inst.send(t, http.MethodPost, setupPath, setupRequest(t, code, "jo@example.com", nil), nil)
	if res.status != http.StatusCreated {
		t.Fatalf("setup with the logged code: %d %s", res.status, res.body)
	}
	inst.stop()
	if err := inst.wait(t); err != nil {
		t.Fatal(err)
	}

	again := startInstance(t, cfg, Options{AutoMigrate: true})
	again.waitFor(t, "/readyz", http.StatusOK)
	if rs := again.log.records(t, setupPendingMessage); len(rs) != 0 {
		t.Errorf("the log gives the setup code once setup has completed:\n%s", again.log)
	}

	env := map[string]string{
		"STORYCREEP_DATABASE_URL":       d.AppURL.Reveal(),
		"STORYCREEP_DATABASE_ADMIN_URL": d.AdminURL.Reveal(),
	}
	maps.Copy(env, unattendedEnv)
	ignored := startInstance(t, loadConfig(t, env), Options{AutoMigrate: true})
	ignored.waitFor(t, "/readyz", http.StatusOK)
	if rs := ignored.log.records(t, setupIgnoredMessage); len(rs) != 1 || rs[0]["level"] != "WARN" {
		t.Errorf("the log does not warn that the server ignores the keys:\n%s", ignored.log)
	}

	fresh := dbtest.New(t)
	env["STORYCREEP_DATABASE_URL"], env["STORYCREEP_DATABASE_ADMIN_URL"] = fresh.AppURL.Reveal(),
		fresh.AdminURL.Reveal()
	env["STORYCREEP_SETUP_OWNER_PASSWORD"] = "short"
	refused := startInstance(t, loadConfig(t, env), Options{AutoMigrate: true})
	err := refused.wait(t)
	var cerr *setup.ConfigError
	if !errors.As(err, &cerr) || !strings.Contains(err.Error(), "STORYCREEP_SETUP_OWNER_PASSWORD: has fewer than") {
		t.Errorf("Run = %v, want a *setup.ConfigError that names STORYCREEP_SETUP_OWNER_PASSWORD", err)
	}
	if strings.Contains(refused.log.String(), `"short"`) {
		t.Errorf("the log shows the password:\n%s", refused.log)
	}
}

// TestSetupCodeWhateverTheLogLevel checks that the setup step gives the setup
// code even when STORYCREEP_LOG_LEVEL drops warnings, because without it
// setup cannot complete through the API, while the level still drops the
// other warnings.
func TestSetupCodeWhateverTheLogLevel(t *testing.T) {
	t.Parallel()
	d := dbtest.New(t)
	var logged logBuffer
	s := newServer(loadConfig(t, map[string]string{"STORYCREEP_LOG_LEVEL": "error"}),
		logging.New(&logged, slog.LevelError))
	s.ready.db.Store(d.Open(t))
	if err := s.setupStep(context.Background()); err != nil {
		t.Fatal(err)
	}
	rs := logged.records(t, setupPendingMessage)
	if len(rs) != 1 || rs[0]["level"] != "WARN" || rs[0]["setup_code"] != setupCode(t, s) {
		t.Errorf("the log does not give the setup code:\n%s", logged.String())
	}
	s.log.WarnContext(context.Background(), "another warning")
	if rs := logged.records(t, "another warning"); len(rs) != 0 {
		t.Errorf("the level does not drop other warnings:\n%s", logged.String())
	}
}

// TestSetupStepOnceMigrated checks that a server without auto-migration
// takes the setup step once the pending migrations have been applied, before
// it is ready.
func TestSetupStepOnceMigrated(t *testing.T) {
	t.Parallel()
	d := dbtest.Empty(t)
	env := map[string]string{
		"STORYCREEP_DATABASE_URL":       d.AppURL.Reveal(),
		"STORYCREEP_DATABASE_ADMIN_URL": d.AdminURL.Reveal(),
	}
	maps.Copy(env, unattendedEnv)
	cfg := loadConfig(t, env)
	inst := startInstance(t, cfg, Options{PollInterval: 20 * time.Millisecond})
	for deadline := time.Now().Add(time.Minute); ; time.Sleep(20 * time.Millisecond) {
		if _, body, _ := get(t, inst.url+"/readyz"); reason(body) == "migrations_pending" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the server does not report the pending migrations")
		}
	}
	if _, err := database.Migrate(context.Background(), cfg.DatabaseAdminURL, cfg.DatabaseURL,
		database.MigrateOptions{}); err != nil {
		t.Fatal(err)
	}
	inst.waitFor(t, "/readyz", http.StatusOK)
	if len(inst.log.records(t, setupUnattendedMessage)) != 1 {
		t.Errorf("the log does not say that the server completed setup:\n%s", inst.log)
	}
	if n := countRows(t, d, "SELECT count(*) FROM instance WHERE setup_completed_at IS NOT NULL"); n != 1 {
		t.Error("setup is pending")
	}
}
