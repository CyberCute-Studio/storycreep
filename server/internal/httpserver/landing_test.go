package httpserver

import (
	"bytes"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/ratelimit"
	"github.com/CyberCute-Studio/storycreep/server/internal/webassets"
)

// lastBoardEntry returns an entry of the last-board cookie, as the web app
// writes it: the IDs of an account, a project, and a board, separated by ':'.
func lastBoardEntry(user, project, board string) string {
	return user + ":" + project + ":" + board
}

// TestLastBoardCookie checks how the landing reads the last-board cookie:
// the first entry of the account's, of the first five, whose three IDs are
// an account's, a project's, and a board's, in either case, past entries of
// other accounts and entries that are not three such IDs.
func TestLastBoardCookie(t *testing.T) {
	user, other := ids.New(), ids.New()
	p1, b1, p2, b2 := ids.New(), ids.New(), ids.New(), ids.New()
	u, o := ids.Encode(ids.User, user), ids.Encode(ids.User, other)
	e1 := lastBoardEntry(u, ids.Encode(ids.Project, p1), ids.Encode(ids.Board, b1))
	e2 := lastBoardEntry(u, ids.Encode(ids.Project, p2), ids.Encode(ids.Board, b2))
	of := lastBoardEntry(o, ids.Encode(ids.Project, p2), ids.Encode(ids.Board, b2))
	for _, tt := range []struct {
		name, value string
		want        [2]ids.UUID
		ok          bool
	}{
		{"none", "", [2]ids.UUID{}, false},
		{"the account's", e1, [2]ids.UUID{p1, b1}, true},
		{"after another account's", of + "." + e1, [2]ids.UUID{p1, b1}, true},
		{"the first of the account's", e2 + "." + e1, [2]ids.UUID{p2, b2}, true},
		{"another account's alone", of, [2]ids.UUID{}, false},
		{"in lowercase", strings.ToLower(e1), [2]ids.UUID{p1, b1}, true},
		{"after an entry that is no entry", "x." + e1, [2]ids.UUID{p1, b1}, true},
		{"after one of four parts", e2 + ":x." + e1, [2]ids.UUID{p1, b1}, true},
		{"after one whose project is a board", lastBoardEntry(u, ids.Encode(ids.Board, p2),
			ids.Encode(ids.Board, b2)) + "." + e1, [2]ids.UUID{p1, b1}, true},
		{"after one whose board is a project", lastBoardEntry(u, ids.Encode(ids.Project, p2),
			ids.Encode(ids.Project, b2)) + "." + e1, [2]ids.UUID{p1, b1}, true},
		{"whose account is a project", lastBoardEntry(ids.Encode(ids.Project, user), ids.Encode(ids.Project, p1),
			ids.Encode(ids.Board, b1)), [2]ids.UUID{}, false},
		{"fifth", strings.Repeat(of+".", 4) + e1, [2]ids.UUID{p1, b1}, true},
		{"sixth", strings.Repeat(of+".", 5) + e1, [2]ids.UUID{}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.value != "" {
				r.AddCookie(&http.Cookie{Name: lastBoardCookie, Value: tt.value})
			}
			p, b, ok := lastBoardOf(r, user)
			if ok != tt.ok || p != tt.want[0] || b != tt.want[1] {
				t.Errorf("lastBoardOf = %v, %v, %v; want %v, %v, %v", p, b, ok, tt.want[0], tt.want[1], tt.ok)
			}
		})
	}
}

// TestLanding checks where a request to / or /login goes (PRD FR-PUB-05),
// with the public site on and off: with a session, to the board that the
// last-board cookie names for the session's account, with a 302 that holds
// nothing of the public site's home page (AC1, AC4), while the account may
// read the board, or else to the project list (AC2), as for a board in the
// trash, another account's entry, a board that the account may not read, or
// a cookie that names none; and without one, or with one of a session that
// has ended, whose cookie the answer clears, or while the server starts, as
// before: / with the page of the public site's setting, and /login with the
// sign-in screen (AC3). A request over the read limit goes to the project
// list without reading the board.
//
// Verifies: FR-PUB-05/AC1, FR-PUB-05/AC2, FR-PUB-05/AC3, FR-PUB-05/AC4
func TestLanding(t *testing.T) {
	t.Parallel()
	siteIndex, err := fs.ReadFile(webassets.Site(), "index.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, site := range []string{"off", "on"} {
		t.Run("public site "+site, func(t *testing.T) {
			t.Parallel()
			s, d, owner, ws := inviteServer(t, "open", map[string]string{"STORYCREEP_PUBLIC_SITE": site})
			p := createProject(t, s.main, owner, ws, "Harbour")
			b := createBoard(t, s.main, owner, p.ID, "Pier")
			trashed := createBoard(t, s.main, owner, p.ID, "Gone")
			if res := call(s.main, http.MethodPost, commandsPath(p.ID), commandBatch(t,
				boardCommand("board.delete", map[string]string{"id": trashed})), owner.header(true)); res.status !=
				http.StatusOK {
				t.Fatalf("delete the board: %d %s", res.status, res.body)
			}
			newAccount(t, s.main, "other@example.com")
			other := signIn(t, s.main, "other@example.com")
			ownerID := owner.body.UserID

			// cookies returns the headers of a request made with the session
			// of si, if it is not nil, and the last-board cookie value, if it
			// is not empty, from a client of its own.
			cookies := func(si *signedIn, value string) http.Header {
				var pairs []string
				if si != nil {
					pairs = append(pairs, si.cookie.Name+"="+si.cookie.Value)
				}
				if value != "" {
					pairs = append(pairs, lastBoardCookie+"="+value)
				}
				h := newClient()
				if len(pairs) > 0 {
					h.Set("Cookie", strings.Join(pairs, "; "))
				}
				return h
			}
			// anonymous checks that a request to path is answered as one
			// without a session.
			anonymous := func(t *testing.T, method, path string, h http.Header) response {
				t.Helper()
				res := do(s.main, method, path, h)
				switch path {
				case "/login":
					if res.status != http.StatusFound || res.header.Get("Location") != "/app/login" {
						t.Errorf("%s %s: %d to %q, want 302 to the sign-in screen", method, path, res.status,
							res.header.Get("Location"))
					}
				default:
					if res.status != http.StatusOK || res.header.Get("Location") != "" {
						t.Errorf("%s %s: %d %v, want 200 and the page", method, path, res.status, res.header)
					}
					if site == "on" && method == http.MethodGet && !bytes.Equal(res.body, siteIndex) {
						t.Errorf("%s %s: the body is not the site's home page", method, path)
					}
				}
				return res
			}
			// lands checks that a request to path with h goes to want, with a
			// 302 that holds nothing of the public site's home page.
			lands := func(t *testing.T, path string, h http.Header, want string) {
				t.Helper()
				for _, method := range []string{http.MethodGet, http.MethodHead} {
					res := do(s.main, method, path, h)
					if res.status != http.StatusFound || res.header.Get("Location") != want {
						t.Errorf("%s %s: %d to %q, want 302 to %s", method, path, res.status,
							res.header.Get("Location"), want)
					}
					if bytes.Contains(res.body, siteIndex) || bytes.Contains(res.body, []byte("<script")) {
						t.Errorf("%s %s: the redirect holds a page: %s", method, path, res.body)
					}
				}
			}
			entry := lastBoardEntry(ownerID, p.ID, b)
			board := "/app/projects/" + p.ID + "/boards/" + b

			for name, path := range map[string]string{"root": "/", "login": "/login"} {
				t.Run(name, func(t *testing.T) {
					lands(t, path, cookies(&owner, entry), board)
					// The account's entry after another's, as on a machine
					// that another account used since.
					lands(t, path, cookies(&owner, lastBoardEntry(other.body.UserID, p.ID, b)+"."+entry), board)
					// Spelled in lowercase, the board's page names it as the
					// server writes it.
					lands(t, path, cookies(&owner, strings.ToLower(entry)), board)
					lands(t, path, cookies(&owner, ""), projectList)
					lands(t, path, cookies(&owner, "not-a-board"), projectList)
					lands(t, path, cookies(&owner, lastBoardEntry(ownerID, p.ID, trashed)), projectList)
					lands(t, path, cookies(&owner, lastBoardEntry(ownerID, p.ID, newBoardID())), projectList)
					// Another account's entry is not the owner's.
					lands(t, path, cookies(&owner, lastBoardEntry(other.body.UserID, p.ID, b)), projectList)
					// An entry that names a board of a project that the
					// account may not read sends it to its project list.
					lands(t, path, cookies(&other, lastBoardEntry(other.body.UserID, p.ID, b)), projectList)

					for _, method := range []string{http.MethodGet, http.MethodHead} {
						anonymous(t, method, path, cookies(nil, entry))
						anonymous(t, method, path, cookies(nil, ""))
					}
				})
			}

			t.Run("while the server starts", func(t *testing.T) {
				s.ready.setPhase(phaseStarting)
				defer s.ready.setPhase(phaseReady)
				for _, path := range []string{"/", "/login"} {
					anonymous(t, http.MethodGet, path, cookies(&owner, entry))
				}
			})

			t.Run("over the read limit", func(t *testing.T) {
				// The client's address may make one request that reads a
				// minute, which the landing counts: the second goes to the
				// project list. The two must fall in one window of a minute.
				once := ratelimit.DefaultPolicy()
				read := once.Rates[ratelimit.Read]
				read.Limits[ratelimit.IP] = 1
				once.Rates[ratelimit.Read] = read
				was := s.limits
				s.limits = ratelimit.New(readyDB{s.ready}, once)
				defer func() { s.limits = was }()
				h := cookies(&owner, entry)
				d.AwaitWindow(t, time.Minute, 30*time.Second)
				for _, want := range []string{board, projectList} {
					if res := do(s.main, http.MethodGet, "/", h); res.status != http.StatusFound ||
						res.header.Get("Location") != want {
						t.Errorf("GET /: %d to %q, want 302 to %s", res.status, res.header.Get("Location"), want)
					}
				}
			})

			t.Run("with a session that has ended", func(t *testing.T) {
				ended := signIn(t, s.main, "admin@example.com")
				if res := call(s.main, http.MethodPost, logoutPath, nil, ended.header(true)); res.status !=
					http.StatusNoContent {
					t.Fatalf("sign out: %d %s", res.status, res.body)
				}
				for _, path := range []string{"/", "/login"} {
					res := anonymous(t, http.MethodGet, path, cookies(&ended, entry))
					if !clears(t, res.header) {
						t.Errorf("GET %s with a session that has ended: the answer does not clear its cookie: %v",
							path, res.header)
					}
				}
			})
		})
	}
}
