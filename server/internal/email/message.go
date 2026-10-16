package email

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net/mail"
	"strings"
	"time"
)

// Message is an email to send.
type Message struct {
	// To is the recipient: their address, and their name, if any, which the
	// To header shows.
	To mail.Address
	// Subject is the subject, on one line.
	Subject string
	// Body is the text, in lines that end with "\n".
	Body string
}

// compose returns the message msg from from, sent at date, as SMTP's DATA
// command sends it: its header and its body, in lines that end with CRLF.
// The header holds no text of msg as it is: the names and the subject are
// encoded (RFC 2047) where they are not ASCII, and a line break in the
// subject is a space, so that no text, such as a display name that a user
// chose, can add a header. The body is quoted-printable text in UTF-8.
func compose(from *mail.Address, msg Message, date time.Time) ([]byte, error) {
	var b bytes.Buffer
	header := func(name, value string) {
		fmt.Fprintf(&b, "%s: %s\r\n", name, value)
	}
	header("From", from.String())
	header("To", msg.To.String())
	header("Subject", mime.QEncoding.Encode("utf-8", oneLine(msg.Subject)))
	header("Date", date.Format(time.RFC1123Z))
	header("Message-ID", messageID(from.Address))
	header("MIME-Version", "1.0")
	header("Content-Type", "text/plain; charset=utf-8")
	header("Content-Transfer-Encoding", "quoted-printable")
	// The email is sent by the instance itself, not by a person, so an
	// out-of-office reply does not answer it (RFC 3834).
	header("Auto-Submitted", "auto-generated")
	b.WriteString("\r\n")
	w := quotedprintable.NewWriter(&b)
	if _, err := w.Write([]byte(strings.ReplaceAll(msg.Body, "\r\n", "\n"))); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// oneLine returns s with every line break, and any other control character,
// replaced by a space.
func oneLine(s string) string {
	return strings.Map(func(r rune) rune {
		if r < ' ' || r == 0x7f {
			return ' '
		}
		return r
	}, s)
}

// messageID returns a new Message-ID, unique on the domain of the address
// that the email comes from (RFC 5322 §3.6.4).
func messageID(from string) string {
	domain := from[strings.LastIndexByte(from, '@')+1:]
	b := make([]byte, 16)
	// crypto/rand.Read never fails; the program crashes if the operating
	// system's source does.
	_, _ = rand.Read(b)
	return "<" + hex.EncodeToString(b) + "@" + domain + ">"
}
