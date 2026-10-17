package email

import (
	"bytes"
	"net/mail"
	"strings"
	"testing"
	"time"
)

// TestComposeLines checks that every line of a composed email ends in CRLF,
// as SMTP's DATA needs, and is at most 78 characters long, as RFC 5322
// recommends, so that a long link is wrapped with quoted-printable's soft
// line breaks rather than by a server that would break it.
func TestComposeLines(t *testing.T) {
	from := &mail.Address{Name: "Storycreep", Address: "noreply@example.com"}
	msg := Message{To: mail.Address{Name: "Jo", Address: "jo@example.com"}, Subject: "Hello",
		Body: "First line\r\nhttps://storycreep.example.com/app/verify-email#token=" + strings.Repeat("A", 120) + "\n"}
	data, err := compose(from, msg, time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasSuffix(data, []byte("\r\n")) {
		t.Errorf("the email does not end with CRLF: %q", data)
	}
	for i, line := range strings.SplitAfter(string(data), "\n") {
		if line == "" {
			continue
		}
		if !strings.HasSuffix(line, "\r\n") {
			t.Errorf("line %d does not end with CRLF: %q", i+1, line)
		}
		if len(line) > 80 {
			t.Errorf("line %d is %d bytes long: %q", i+1, len(line), line)
		}
	}
	if !strings.Contains(string(data), "Date: Fri, 16 Oct 2026 12:00:00 +0000\r\n") {
		t.Errorf("the email has no Date header of the time it was composed: %q", data)
	}
}
