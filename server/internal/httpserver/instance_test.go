package httpserver

import (
	"encoding/json"
	"net/http"
	"testing"
)

// instanceOf returns what GET /api/v1/instance answers on s.
func instanceOf(t *testing.T, s *server) instanceBody {
	t.Helper()
	res := send(s.main, http.MethodGet, instancePath, nil, "", nil)
	if res.status != http.StatusOK {
		t.Fatalf("GET %s: %d %s", instancePath, res.status, res.body)
	}
	var b instanceBody
	if err := json.Unmarshal(res.body, &b); err != nil {
		t.Fatal(err)
	}
	return b
}

// TestInstanceSaysWhoCanRegister checks what GET /api/v1/instance tells a
// page before anyone signs in: that first-run setup is pending until it
// completes, the signup policy that applies, closed until setup chooses
// another, the one that setup chose, or the one that STORYCREEP_SIGNUP sets,
// which overrides it, and the instance's default locale, the locale with a
// catalog that STORYCREEP_DEFAULT_LOCALE asks for.
func TestInstanceSaysWhoCanRegister(t *testing.T) {
	t.Parallel()
	pending, _ := pendingServer(t, map[string]string{"STORYCREEP_DEFAULT_LOCALE": "en-GB"})
	if got, want := instanceOf(t, pending), (instanceBody{SetupPending: true, SignupPolicy: "closed",
		DefaultLocale: "en"}); got != want {
		t.Errorf("before setup: %+v, want %+v", got, want)
	}
	completeSetup(t, pending, "jo@example.com", map[string]any{"signup_policy": "open"})
	if got, want := instanceOf(t, pending), (instanceBody{SignupPolicy: "open", DefaultLocale: "en"}); got != want {
		t.Errorf("after setup: %+v, want %+v", got, want)
	}

	fixed, _ := pendingServer(t, map[string]string{"STORYCREEP_SIGNUP": "invite"})
	completeSetup(t, fixed, "jo@example.com", map[string]any{"signup_policy": "open"})
	if got, want := instanceOf(t, fixed), (instanceBody{SignupPolicy: "invite", DefaultLocale: "en"}); got != want {
		t.Errorf("with STORYCREEP_SIGNUP: %+v, want %+v", got, want)
	}
}
