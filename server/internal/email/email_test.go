package email_test

import (
	"context"
	"mime"
	"net/mail"
	"strings"
	"testing"
	"time"

	"github.com/CyberCute-Studio/storycreep/server/internal/config"
	"github.com/CyberCute-Studio/storycreep/server/internal/email"
	"github.com/CyberCute-Studio/storycreep/server/internal/email/emailtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/secret"
)

const testFrom = "Storycreep <noreply@example.com>"

// message is an email whose subject, recipient's name, and body are not
// ASCII, and whose body has a line longer than quoted-printable's 76
// characters, a line that starts with a dot, which SMTP doubles, and lines
// that end in CRLF and in LF.
var message = email.Message{
	To:      mail.Address{Name: "Jiří Novák", Address: "jiri@example.com"},
	Subject: "Obnovte si heslo",
	Body: "Dobrý den,\r\n.\nhttps://storycreep.example.com/app/reset-password#token=" +
		strings.Repeat("A", 80) + "\n",
}

// send sends message through server with settings, trusting the server's
// certificate.
func send(t *testing.T, server *emailtest.Server, settings config.SMTP) error {
	t.Helper()
	s := email.New(nil, email.Options{Hostname: "storycreep.example.com", RootCAs: server.CertPool(),
		Timeout: 10 * time.Second})
	return s.Deliver(context.Background(), settings, message)
}

// checkDelivered checks that server was sent message, once, as package email
// writes it.
func checkDelivered(t *testing.T, server *emailtest.Server, tls bool, username string) {
	t.Helper()
	got := server.Messages()
	if len(got) != 1 {
		t.Fatalf("%v was sent %d emails, want 1", server, len(got))
	}
	m := got[0]
	if m.From != "noreply@example.com" || len(m.To) != 1 || m.To[0] != "jiri@example.com" {
		t.Errorf("the envelope is from %q to %q", m.From, m.To)
	}
	if m.TLS != tls || m.Username != username {
		t.Errorf("TLS %v, signed in as %q; want %v and %q", m.TLS, m.Username, tls, username)
	}
	h := m.Header(t)
	dec := new(mime.WordDecoder)
	subject, err := dec.DecodeHeader(h.Get("Subject"))
	if err != nil || subject != message.Subject {
		t.Errorf("Subject %q (%v), want %q", h.Get("Subject"), err, message.Subject)
	}
	to, err := h.AddressList("To")
	if err != nil || len(to) != 1 || *to[0] != message.To {
		t.Errorf("To %q (%v), want %v", h.Get("To"), err, message.To)
	}
	from, err := h.AddressList("From")
	if err != nil || len(from) != 1 || from[0].Address != "noreply@example.com" || from[0].Name != "Storycreep" {
		t.Errorf("From %q (%v)", h.Get("From"), err)
	}
	for name, want := range map[string]string{
		"Content-Type":              "text/plain; charset=utf-8",
		"Content-Transfer-Encoding": "quoted-printable",
		"MIME-Version":              "1.0",
		"Auto-Submitted":            "auto-generated",
	} {
		if got := h.Get(name); got != want {
			t.Errorf("%s: %q, want %q", name, got, want)
		}
	}
	if id := h.Get("Message-Id"); !strings.HasSuffix(id, "@example.com>") {
		t.Errorf("Message-ID %q is not on the sender's domain", id)
	}
	if _, err := h.Date(); err != nil {
		t.Errorf("Date: %v", err)
	}
	// The server reads the lines of DATA, which end in CRLF, as lines that
	// end in LF, so TestComposeLines checks the line ends.
	want := strings.ReplaceAll(message.Body, "\r\n", "\n")
	if body := m.Body(t); body != want {
		t.Errorf("the body is %q, want %q", body, want)
	}
}

// TestSendStartTLS checks an email sent through a server that STARTTLS
// protects, which the client signs in to with PLAIN once TLS has started.
func TestSendStartTLS(t *testing.T) {
	server := emailtest.Start(t, emailtest.Options{StartTLS: true, Mechanisms: []string{"PLAIN", "LOGIN"},
		Username: "storycreep", Password: "s3cret"})
	if err := send(t, server, server.Settings(config.SMTPStartTLS, testFrom)); err != nil {
		t.Fatal(err)
	}
	checkDelivered(t, server, true, "storycreep")
}

// TestSendTLS checks an email sent through a server that speaks TLS from
// the start, which the client signs in to with LOGIN, the only mechanism
// that it offers.
func TestSendTLS(t *testing.T) {
	server := emailtest.Start(t, emailtest.Options{TLS: true, Mechanisms: []string{"LOGIN"},
		Username: "storycreep", Password: "s3cret"})
	if err := send(t, server, server.Settings(config.SMTPTLS, testFrom)); err != nil {
		t.Fatal(err)
	}
	checkDelivered(t, server, true, "storycreep")
}

// TestSendWithoutTLS checks an email sent without TLS, to a relay on a
// loopback address, which the client may sign in to there.
func TestSendWithoutTLS(t *testing.T) {
	server := emailtest.Start(t, emailtest.Options{Mechanisms: []string{"PLAIN"}, Username: "storycreep",
		Password: "s3cret"})
	if err := send(t, server, server.Settings(config.SMTPNone, testFrom)); err != nil {
		t.Fatal(err)
	}
	checkDelivered(t, server, false, "storycreep")
}

// TestSendWithURLCredentials checks that a username and a password that
// STORYCREEP_SMTP_URL percent-encodes, such as ones with @, :, /, and #,
// sign in to the server as they are.
func TestSendWithURLCredentials(t *testing.T) {
	server := emailtest.Start(t, emailtest.Options{Mechanisms: []string{"PLAIN"}, Username: "jo@example.com",
		Password: "p@ss:w/rd#1"})
	settings, err := config.ParseSMTPURL(server.URL())
	if err != nil {
		t.Fatal(err)
	}
	settings.From = testFrom
	if err := send(t, server, settings); err != nil {
		t.Fatal(err)
	}
	checkDelivered(t, server, false, "jo@example.com")
}

// TestSendRefusals checks that the client refuses a server that does not
// protect the connection as the settings say, or whose certificate is not
// valid for its host, and one whose password is wrong, that it fails when the
// server refuses the sender or the recipient, with an error that holds
// neither the password nor the address that the server's reply quotes, which
// a job keeps, and that it sends nothing to any of them. Only a recipient
// whose mailbox does not exist or takes no email is a permanent failure,
// which no attempt would mend: not a full mailbox, nor a refusal of a relay
// or of the sender that the server gives in its reply to RCPT, which the
// settings cause.
func TestSendRefusals(t *testing.T) {
	for _, c := range []struct {
		name      string
		opts      emailtest.Options
		security  config.SMTPSecurity
		mutate    func(*config.SMTP)
		want      string
		permanent bool
	}{
		{"no STARTTLS", emailtest.Options{}, config.SMTPStartTLS, nil, "does not offer STARTTLS", false},
		{"no AUTH", emailtest.Options{StartTLS: true}, config.SMTPStartTLS,
			func(s *config.SMTP) { s.Username, s.Password = "storycreep", secret.New("s3cret") }, "offers no sign-in",
			false},
		{"wrong password", emailtest.Options{StartTLS: true, Mechanisms: []string{"PLAIN"}, Username: "storycreep",
			Password: "s3cret"}, config.SMTPStartTLS, func(s *config.SMTP) { s.Password = secret.New("wrong") },
			"sign in", false},
		{"a refused sender", emailtest.Options{Mechanisms: []string{"PLAIN"}}, config.SMTPNone, nil,
			"refuses the sender: 530", false},
		{"a refused recipient", emailtest.Options{RefuseRecipients: emailtest.RefuseUnknown}, config.SMTPNone, nil,
			`refuses the recipient: 550 "5.1.1 [address] Recipient address rejected`, true},
		{"a disabled mailbox", emailtest.Options{RefuseRecipients: "550 5.2.1 %s: Mailbox disabled"},
			config.SMTPNone, nil, `refuses the recipient: 550 "5.2.1 [address] Mailbox disabled`, true},
		{"a refusal without an enhanced code", emailtest.Options{RefuseRecipients: "550 %s: User unknown"},
			config.SMTPNone, nil, `refuses the recipient: 550 "[address] User unknown`, true},
		{"a full mailbox", emailtest.Options{RefuseRecipients: "552 5.2.2 %s: Mailbox full"}, config.SMTPNone, nil,
			`refuses the recipient: 552 "5.2.2 [address] Mailbox full`, false},
		{"a full mailbox with 550", emailtest.Options{RefuseRecipients: "550 5.2.2 %s: Mailbox full"},
			config.SMTPNone, nil, `refuses the recipient: 550 "5.2.2 [address] Mailbox full`, false},
		{"a relay refused at RCPT", emailtest.Options{RefuseRecipients: "554 5.7.1 %s: Relay access denied"},
			config.SMTPNone, nil, `refuses the recipient: 554 "5.7.1 [address] Relay access denied`, false},
		{"a sender refused at RCPT", emailtest.Options{
			RefuseRecipients: "553 5.7.1 <noreply@example.com>: Sender address rejected: not owned by user"},
			config.SMTPNone, nil, `refuses the recipient: 553 "5.7.1 [address] Sender address rejected`, false},
		{"a refusal of 554 without an enhanced code", emailtest.Options{
			RefuseRecipients: "554 %s: Relay access denied"}, config.SMTPNone, nil,
			`refuses the recipient: 554 "[address] Relay access denied`, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			server := emailtest.Start(t, c.opts)
			settings := server.Settings(c.security, testFrom)
			if c.mutate != nil {
				c.mutate(&settings)
			}
			err := send(t, server, settings)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %v, want one that says %q", err, c.want)
			}
			if email.Permanent(err) != c.permanent {
				t.Errorf("Permanent(%v) is %v, want %v", err, !c.permanent, c.permanent)
			}
			if err != nil && strings.Contains(err.Error(), "s3cret") {
				t.Errorf("the error holds the password: %v", err)
			}
			if err != nil && strings.Contains(err.Error(), message.To.Address) {
				t.Errorf("the error holds the recipient's address: %v", err)
			}
			if n := len(server.Messages()); n != 0 {
				t.Errorf("%v was sent %d emails", server, n)
			}
		})
	}
}

// TestHeaderInjection checks that no text of a message, such as a display
// name that a user chose, or a subject with a line break, adds a header to
// the email.
func TestHeaderInjection(t *testing.T) {
	server := emailtest.Start(t, emailtest.Options{})
	settings := server.Settings(config.SMTPNone, testFrom)
	msg := email.Message{To: mail.Address{Name: "Jo\r\nBcc: mallory@example.com", Address: "jo@example.com"},
		Subject: "Hello\r\nBcc: mallory@example.com", Body: "Hello\n"}
	if err := email.New(nil, email.Options{}).Deliver(context.Background(), settings, msg); err != nil {
		t.Fatal(err)
	}
	m := server.Messages()[0]
	h := m.Header(t)
	if bcc := h.Get("Bcc"); bcc != "" || len(m.To) != 1 {
		t.Errorf("the email has a Bcc header %q, or recipients %q", bcc, m.To)
	}
}

// TestInternationalAddresses checks that an address outside ASCII, which
// registration takes, goes as it is to a server that offers SMTPUTF8, and
// with its domain in its xn-- form to one that does not, in the envelope and
// in the header alike, and that one whose local part is outside ASCII is
// refused before it is sent to such a server, with an error that names
// SMTPUTF8, which is permanent, since no attempt would send it.
func TestInternationalAddresses(t *testing.T) {
	for _, c := range []struct {
		name      string
		ascii     bool
		address   string
		want, err string
	}{
		{"an IDN domain with SMTPUTF8", false, "jo@bücher.example", "jo@bücher.example", ""},
		{"a local part with SMTPUTF8", false, "žofie@bücher.example", "žofie@bücher.example", ""},
		{"an IDN domain without SMTPUTF8", true, "jo@bücher.example", "jo@xn--bcher-kva.example", ""},
		{"a local part without SMTPUTF8", true, "žofie@example.com", "", "offers no SMTPUTF8"},
	} {
		t.Run(c.name, func(t *testing.T) {
			server := emailtest.Start(t, emailtest.Options{ASCII: c.ascii})
			settings := server.Settings(config.SMTPNone, testFrom)
			s := email.New(nil, email.Options{Timeout: 10 * time.Second})
			err := s.Deliver(context.Background(), settings, email.Message{To: mail.Address{Name: "Jo",
				Address: c.address}, Subject: "Hello", Body: "Hello\n"})
			if c.err != "" {
				if err == nil || !strings.Contains(err.Error(), c.err) || strings.Contains(err.Error(), c.address) {
					t.Errorf("error %v, want one that says %q without the address", err, c.err)
				}
				if !email.Permanent(err) {
					t.Errorf("Permanent(%v) is false, want true", err)
				}
				if n := len(server.Messages()); n != 0 {
					t.Errorf("%v was sent %d emails", server, n)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			m := server.Messages()[0]
			to, err := m.Header(t).AddressList("To")
			if len(m.To) != 1 || m.To[0] != c.want || err != nil || len(to) != 1 || to[0].Address != c.want {
				t.Errorf("sent to %q, with the To header %q (%v), want %q", m.To, m.Header(t).Get("To"), err, c.want)
			}
		})
	}
}

// TestHelloName checks the name that the client gives itself in its EHLO:
// the public URL's host name, without the trailing dot that the URL may have,
// or an IP address's address literal, which RFC 5321 requires in place of a
// bare address, and the address literal of the connection's own address for
// a host name that is not a domain that RFC 5321 takes, as one with an
// underscore, which the URL may have too, or for none.
func TestHelloName(t *testing.T) {
	for hostname, want := range map[string]string{
		"storycreep.example.com":  "storycreep.example.com",
		"storycreep.example.com.": "storycreep.example.com",
		"192.168.1.20":            "[192.168.1.20]",
		"2001:db8::1":             "[IPv6:2001:db8::1]",
		"story_creep.example.com": "[127.0.0.1]",
		"-storycreep.example.com": "[127.0.0.1]",
		"":                        "[127.0.0.1]",
	} {
		server := emailtest.Start(t, emailtest.Options{})
		settings := server.Settings(config.SMTPNone, testFrom)
		s := email.New(nil, email.Options{Hostname: hostname, Timeout: 10 * time.Second})
		if err := s.Deliver(context.Background(), settings, message); err != nil {
			t.Fatal(err)
		}
		if got := server.Messages()[0].Hello; got != want {
			t.Errorf("the host name %q greets as %q, want %q", hostname, got, want)
		}
	}
}

// TestUntrustedCertificate checks that the client refuses a server whose
// certificate it does not trust, and sends it nothing.
func TestUntrustedCertificate(t *testing.T) {
	server := emailtest.Start(t, emailtest.Options{TLS: true})
	settings := server.Settings(config.SMTPTLS, testFrom)
	s := email.New(nil, email.Options{Timeout: 10 * time.Second})
	if err := s.Deliver(context.Background(), settings, message); err == nil ||
		!strings.Contains(err.Error(), "certificate") {
		t.Errorf("Deliver() = %v, want an error about the certificate", err)
	}
	if n := len(server.Messages()); n != 0 {
		t.Errorf("%v was sent %d emails", server, n)
	}
}

// TestFixed checks that a Service reports whether the configuration sets
// its SMTP server.
func TestFixed(t *testing.T) {
	settings := config.SMTP{Host: "smtp.example.com", Port: 587, Security: config.SMTPStartTLS, From: testFrom}
	if !email.New(nil, email.Options{SMTP: &settings}).Fixed() {
		t.Error("a Service with the configuration's settings does not report them fixed")
	}
	if email.New(nil, email.Options{}).Fixed() {
		t.Error("a Service without the configuration's settings reports them fixed")
	}
}
