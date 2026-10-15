package api

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/danielgtaylor/huma/v2"
	"golang.org/x/text/language"

	"github.com/CyberCute-Studio/storycreep/server/internal/i18n"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/logging"
)

// czech translates some of the server's messages into Czech, for the tests:
// the server has no Czech catalog until plan item 120.
const czech = `{
  "error.invalid_id.wrong_prefix": "Prefix {prefix}_ identifikátoru patří typu {got}, ne {expected}.",
  "error.not_found": "Prostředek neexistuje.",
  "error.validation_failed": "Požadavek je neplatný."
}`

// localeMux returns a Mux with the server's English catalog and the Czech
// one above, the instance default locale instanceDefault, and the routes of
// itemMux and GET /fail, which fails with an identifier of the wrong type.
// Its log is written to the buffer it returns.
func localeMux(t *testing.T, instanceDefault language.Tag) (*Mux, *bytes.Buffer) {
	t.Helper()
	en, err := os.ReadFile(filepath.Join("..", "i18n", "catalog", "en.json"))
	if err != nil {
		t.Fatal(err)
	}
	locales, err := i18n.Load(fstest.MapFS{"en.json": {Data: en}, "cs.json": {Data: []byte(czech)}})
	if err != nil {
		t.Fatal(err)
	}
	var logged bytes.Buffer
	m := NewMux(MuxOptions{
		Log:           logging.New(&logged, slog.LevelDebug),
		Locales:       locales,
		DefaultLocale: instanceDefault,
		Middleware: []func(http.Handler) http.Handler{func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				next.ServeHTTP(w, r.WithContext(logging.WithRequestID(r.Context(), testRequestID)))
			})
		}},
	})
	Register(m, Public, huma.Operation{OperationID: "createItem", Method: http.MethodPost, Path: "/items"},
		func(_ context.Context, in *itemInput) (*itemOutput, error) { return &itemOutput{Body: in.Body}, nil })
	Register(m, Public, huma.Operation{OperationID: "fail", Method: http.MethodGet, Path: "/fail"},
		func(context.Context, *struct{}) (*itemOutput, error) {
			_, err := ids.Decode(ids.Node, ids.Encode(ids.Board, ids.New()))
			return nil, err
		})
	return m, &logged
}

// serveLanguage serves a request with the Accept-Language header lines
// accept, and a JSON body if body is set.
func serveLanguage(m http.Handler, method, path string, accept []string, body string) response {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	for _, a := range accept {
		r.Header.Add("Accept-Language", a)
	}
	w := httptest.NewRecorder()
	m.ServeHTTP(w, r)
	return response{status: w.Code, header: w.Header(), body: w.Body.Bytes()}
}

// TestLocalizedErrors checks that an envelope's message is in the locale
// that the request's Accept-Language and the instance's default negotiate,
// or in English where that locale's catalog lacks it, and that the response
// says which, whichever way the envelope is written.
func TestLocalizedErrors(t *testing.T) {
	tests := []struct {
		name            string
		instanceDefault language.Tag
		method, path    string
		accept          []string
		body            string
		code            Code
		message, locale string
	}{
		{"a route that does not exist", language.English, http.MethodGet, "/nowhere",
			[]string{"cs-CZ, en;q=0.5"}, "", CodeNotFound, "Prostředek neexistuje.", "cs"},
		{"an error that huma reports", language.English, http.MethodPost, "/items",
			[]string{"cs"}, `{"name": 1}`, CodeValidationFailed, "Požadavek je neplatný.", "cs"},
		{"an error with arguments", language.English, http.MethodGet, "/fail",
			[]string{"cs"}, "", CodeInvalidID, "Prefix bd_ identifikátoru patří typu board, ne node.", "cs"},
		{"a message that the Czech catalog lacks", language.English, http.MethodDelete, "/items",
			[]string{"cs"}, "", CodeMethodNotAllowed, "The method is not allowed for this resource.", "en"},
		{"the instance default", language.Czech, http.MethodGet, "/nowhere",
			[]string{"fr, de;q=0.5"}, "", CodeNotFound, "Prostředek neexistuje.", "cs"},
		{"Accept-Language over the instance default", language.Czech, http.MethodGet, "/nowhere",
			[]string{"en-GB"}, "", CodeNotFound, "The resource does not exist.", "en"},
		{"Accept-Language on two lines", language.English, http.MethodGet, "/nowhere",
			[]string{"fr", "cs;q=0.8"}, "", CodeNotFound, "Prostředek neexistuje.", "cs"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, _ := localeMux(t, tt.instanceDefault)
			res := serveLanguage(m, tt.method, tt.path, tt.accept, tt.body)
			if b := res.envelope(t); b.Code != tt.code || b.Message != tt.message {
				t.Errorf("code %s, message %q, want %s, %q", b.Code, b.Message, tt.code, tt.message)
			}
			if got := res.header.Get("Content-Language"); got != tt.locale {
				t.Errorf("Content-Language = %q, want %q", got, tt.locale)
			}
			if got := res.header.Values("Vary"); len(got) != 1 || got[0] != "Accept-Language" {
				t.Errorf("Vary = %q, want Accept-Language", got)
			}
		})
	}
}

// TestUnknownLocaleErrors checks that a request whose Accept-Language names
// only locales that the server has no catalog for, or is not valid, is
// answered in English, with nothing logged.
//
// Verifies: FR-I18N-02/AC2
func TestUnknownLocaleErrors(t *testing.T) {
	for _, accept := range [][]string{
		{"fr"},
		{"xx-YY, fr;q=0.8"},
		{"*"},
		{"en;q=garbage,,;"},
		{strings.Repeat("a-", 2000)},
	} {
		m, logged := localeMux(t, language.French)
		res := serveLanguage(m, http.MethodGet, "/nowhere", accept, "")
		if b := res.envelope(t); b.Message != "The resource does not exist." {
			t.Errorf("Accept-Language %q: message %q", accept, b.Message)
		}
		if got := res.header.Get("Content-Language"); got != "en" {
			t.Errorf("Accept-Language %q: Content-Language %q", accept, got)
		}
		if logged.Len() > 0 {
			t.Errorf("Accept-Language %q is logged:\n%s", accept, logged)
		}
	}
}

// TestNotModifiedLanguage checks that a 304, which has no message, says
// nothing about its language.
func TestNotModifiedLanguage(t *testing.T) {
	m, _ := localeMux(t, language.English)
	Register(m, Public, huma.Operation{OperationID: "cached", Method: http.MethodGet, Path: "/cached",
		Responses: map[string]*huma.Response{"304": NotModifiedResponse()}},
		func(context.Context, *struct{}) (*itemOutput, error) { return nil, NotModified(`"x"`) })
	res := serveLanguage(m, http.MethodGet, "/cached", []string{"cs"}, "")
	if res.status != http.StatusNotModified || res.header.Get("Content-Language") != "" || res.header.Get("Vary") != "" {
		t.Errorf("a 304: status %d, Content-Language %q, Vary %q", res.status, res.header.Get("Content-Language"),
			res.header.Get("Vary"))
	}
}
