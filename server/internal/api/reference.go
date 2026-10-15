package api

import (
	"fmt"
	"strings"
)

// Reference returns the error code reference, docs/error-codes.md, which is
// generated from the registry so that the documented codes and statuses
// cannot drift from the real ones (PRD FR-API-04).
func Reference() string {
	var b strings.Builder
	b.WriteString(referenceIntro)
	b.WriteString("| Code | Status | Message | When |\n| --- | --- | --- | --- |\n")
	for _, s := range codes {
		fmt.Fprintf(&b, "| `%s` | %d | %s | %s |\n", s.code, s.status, s.message, s.doc)
	}
	return b.String()
}

const referenceIntro = `<!-- Generated from server/internal/api. Do not edit; run
     "go test ./internal/api -run TestReference -update" in server/ instead. -->

# Error codes

Every response with a 4xx or 5xx status has the same body, the error envelope
(PRD FR-API-04):

` + "```json" + `
{
  "error": {
    "code": "validation_failed",
    "message": "The request is invalid.",
    "details": {
      "errors": [{"location": "body.colour", "message": "unexpected property"}]
    },
    "request_id": "0199e2c4-5b7a-7d43-9f0e-3c1d2a8b6e51"
  }
}
` + "```" + `

- ` + "`code`" + ` is one of the codes below. A code keeps its meaning and its
  status once it is released, so clients branch on it.
- ` + "`message`" + ` says what went wrong, for people. It may change, and is
  not for programs to read.
- ` + "`details`" + ` is an object whose keys the code decides, or is absent.
- ` + "`request_id`" + ` is the request's ID, which is also in the
  ` + "`X-Request-ID`" + ` response header and in every line the server logs
  about the request.

## Codes

`
