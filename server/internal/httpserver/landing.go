package httpserver

import (
	"errors"
	"net/http"
	"strings"

	"github.com/CyberCute-Studio/storycreep/server/internal/account"
	"github.com/CyberCute-Studio/storycreep/server/internal/board"
	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/project"
	"github.com/CyberCute-Studio/storycreep/server/internal/ratelimit"
)

// The authenticated landing (PRD FR-PUB-05, FR-PROJ-04, plan note N67). A
// request to / or /login made with a session goes to the board that the
// browser last showed the session's account, while the account may still
// read it, or else to the project list, which is the screen that creates a
// workspace for an account that has none, as a 302 that the server answers
// before any page is sent, so that a signed-in user never sees the public
// site's home page or the sign-in screen first (AC4). A request without a
// session is answered as it was before (AC3): / with the public site's home
// page or the tool's entry page, and /login with a redirect to the tool's
// sign-in screen.
//
// The web app keeps what the device last showed each account, with the
// viewport and the selection, in the browser's storage, which the server
// cannot read, and the board alone, for the server, in a cookie that it
// writes itself (web/app/src/project/lastOpened.ts), so that the state is the
// device's, as the browser's, and no table of the server holds it. The
// cookie is a hint, never more: the server reads the board as the account
// before it sends the account there, so a board that is gone, in the trash,
// or that the account may no longer read, as of a workspace that it left,
// sends it to the project list instead, and so does a cookie that a script or
// another account wrote, which can name no board that the account may not
// read. The cookie holds no secret, so a script may write it, as the app does,
// and it is not __Host- prefixed, as the session's is on an https URL: a
// cookie that another host of the domain planted could send the account to
// a board that it may read, and nowhere else.

// lastBoardCookie is the name of the cookie in which the web app keeps the
// board that the browser last showed each of the accounts that last used it.
// Its value is at most maxLastBoards entries, the most recent first,
// separated by '.', each the IDs of an account, a project, and a board of
// it, as the server writes them, separated by ':', as
// usr_….:prj_…:brd_… (web/app/src/project/lastOpened.ts writes it so).
const lastBoardCookie = "sc_last_board"

// maxLastBoards is how many accounts the cookie keeps a board of, the most
// recent, so that a browser that many accounts use, as a shared machine,
// does not send an ever longer cookie with every request.
const maxLastBoards = 5

// projectList is where the landing sends an account without a board to go
// back to: the project list, which opens the workspace that the device last
// showed the account, or its first, or, for an account that has none, offers
// to create one (plan note N62).
const projectList = appPrefix + "projects"

// lastBoardOf returns the project and the board that the last-board cookie of
// r names for the account userID, and false if it names none: the first
// entry of the account's, of the first maxLastBoards. An entry that is not
// three IDs of an account, a project, and a board, as one that a script
// wrote, names nothing, and the entries after it are read all the same.
func lastBoardOf(r *http.Request, userID ids.UUID) (projectID, boardID ids.UUID, ok bool) {
	ck, err := r.Cookie(lastBoardCookie)
	if err != nil {
		return ids.UUID{}, ids.UUID{}, false
	}
	entries := strings.SplitN(ck.Value, ".", maxLastBoards+1)
	for _, entry := range entries[:min(len(entries), maxLastBoards)] {
		parts := strings.Split(entry, ":")
		if len(parts) != 3 {
			continue
		}
		user, err := ids.Decode(ids.User, parts[0])
		if err != nil || user != userID {
			continue
		}
		p, perr := ids.Decode(ids.Project, parts[1])
		b, berr := ids.Decode(ids.Board, parts[2])
		if perr == nil && berr == nil {
			return p, b, true
		}
	}
	return ids.UUID{}, ids.UUID{}, false
}

// boardPage returns the path of the tool's page of the board boardID of the
// project projectID (web/app/src/routes/projects.$projectId.boards.$boardId.tsx).
func boardPage(projectID, boardID ids.UUID) string {
	return appPrefix + "projects/" + ids.Encode(ids.Project, projectID) + "/boards/" + ids.Encode(ids.Board, boardID)
}

// lander returns where an authenticated request to / or /login goes, and
// false for a request without a session, which the page answers as it does
// any other. It may add headers to w, as one that clears the cookie of a
// session that has ended.
type lander func(w http.ResponseWriter, r *http.Request) (target string, ok bool)

// landing returns the server's lander, for the session cookie cookie. It
// answers false, so that the page is served as without a session, for a
// request without the cookie, which it reads nothing of the database for,
// as of a visitor to the public site, before the server is ready, and when
// the session cannot be read, which it logs; and false, clearing the cookie,
// for one of a session that has ended, as the API does, so that the browser
// stops sending it. A session's request counts in the read bucket of the
// rate limits, for the client's IP address and the account, as the API's
// reads do, before the landing reads the board: one over the limit, which
// the landing does not read the board for, goes to the project list, whose
// requests the API refuses then too.
func (s *server) landing(cookie sessionCookie) lander {
	return func(w http.ResponseWriter, r *http.Request) (string, bool) {
		value, ok := cookie.read(r)
		if !ok {
			return "", false
		}
		ctx := r.Context()
		if s.ready.notReady(ctx, false) != nil {
			return "", false
		}
		session, err := s.accounts.Session(ctx, value)
		switch {
		case errors.Is(err, account.ErrNoSession):
			w.Header().Add("Set-Cookie", cookie.clear())
			return "", false
		case err != nil:
			s.log.WarnContext(ctx, "the session of a request to "+r.URL.Path+" could not be read, so it is "+
				"answered as without one", "error", err)
			return "", false
		}
		client := clientFrom(ctx)
		if _, err := s.limits.Take(ctx, ratelimit.Read, ratelimit.ClientIP(client.IP),
			ratelimit.UserID(session.UserID)); err != nil {
			var limited *ratelimit.LimitError
			if !errors.As(err, &limited) {
				s.log.WarnContext(ctx, "the rate limit of a request to "+r.URL.Path+" could not be counted, so "+
					"it goes to the project list", "error", err)
			}
			return projectList, true
		}
		projectID, boardID, ok := lastBoardOf(r, session.UserID)
		if !ok {
			return projectList, true
		}
		_, err = s.boards.Get(ctx, commands.Actor{UserID: session.UserID, Client: client}, projectID, boardID)
		switch {
		case errors.Is(err, board.ErrNotFound), errors.Is(err, project.ErrNotFound):
			return projectList, true
		case err != nil:
			s.log.WarnContext(ctx, "the last board of a request to "+r.URL.Path+" could not be read, so it "+
				"goes to the project list", "error", err)
			return projectList, true
		}
		return boardPage(projectID, boardID), true
	}
}
