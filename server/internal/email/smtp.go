package email

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/mail"
	"net/netip"
	"net/smtp"
	"net/textproto"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/net/idna"

	"github.com/CyberCute-Studio/storycreep/server/internal/config"
)

// Deliver sends msg through the SMTP server of settings, as Settings returns
// them, and returns once the server has accepted it, within the Service's
// timeout. Its errors say which step failed, and hold neither the password
// nor an email address that the server's replies quote (replyError), because
// a job keeps its errors (package jobs), and Permanent tells those that
// sending the email again would meet again.
func (s *Service) Deliver(ctx context.Context, settings config.SMTP, msg Message) error {
	from, problem := config.ParseSMTPFrom(settings.From)
	if problem != "" {
		return fmt.Errorf("email: the address that email comes from %s", problem)
	}
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	addr := net.JoinHostPort(settings.Host, strconv.Itoa(settings.Port))
	tlsConfig := &tls.Config{ServerName: settings.Host, MinVersion: tls.VersionTLS12, RootCAs: s.rootCAs}
	dialer := &net.Dialer{}
	var conn net.Conn
	var err error
	if settings.Security == config.SMTPTLS {
		conn, err = (&tls.Dialer{NetDialer: dialer, Config: tlsConfig}).DialContext(ctx, "tcp", addr)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("email: connect to the SMTP server %s: %w", addr, err)
	}
	defer conn.Close()
	// net/smtp takes no context, so the context's end ends the connection's
	// reads and writes, which the client then fails with.
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Unix(1, 0)) })
	defer stop()

	c, err := smtp.NewClient(conn, settings.Host)
	if err != nil {
		return fmt.Errorf("email: greet the SMTP server %s: %w", addr, err)
	}
	defer c.Close()
	if err := c.Hello(s.helloName(conn.LocalAddr())); err != nil {
		return fmt.Errorf("email: greet the SMTP server %s: %w", addr, err)
	}
	if settings.Security == config.SMTPStartTLS {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return fmt.Errorf("email: the SMTP server %s does not offer STARTTLS, which the settings require; use "+
				"its TLS port with smtps://, or, for a relay on the same host or network, security=none", addr)
		}
		if err := c.StartTLS(tlsConfig); err != nil {
			return fmt.Errorf("email: start TLS with the SMTP server %s: %w", addr, err)
		}
	}
	if settings.Username != "" {
		auth, err := authFor(c, settings)
		if err != nil {
			return fmt.Errorf("email: sign in to the SMTP server %s: %w", addr, err)
		}
		if err := c.Auth(auth); err != nil {
			return fmt.Errorf("email: sign in to the SMTP server %s as %s: %w", addr, settings.Username, err)
		}
	}
	// An address outside ASCII goes as it is only to a server that offers
	// SMTPUTF8, which net/smtp then asks for in MAIL, and the header names
	// the addresses as the envelope does.
	smtpUTF8, _ := c.Extension("SMTPUTF8")
	sender, err := envelopeAddress(from.Address, smtpUTF8)
	if err != nil {
		return fmt.Errorf("email: the SMTP server %s offers no SMTPUTF8, and the address that email comes from %w",
			addr, err)
	}
	recipient, err := envelopeAddress(msg.To.Address, smtpUTF8)
	if err != nil {
		return permanentError{fmt.Errorf("email: the SMTP server %s offers no SMTPUTF8, and the recipient's "+
			"address %w", addr, err)}
	}
	msg.To.Address = recipient
	data, err := compose(&mail.Address{Name: from.Name, Address: sender}, msg, time.Now())
	if err != nil {
		return fmt.Errorf("email: compose the email: %w", err)
	}
	if err := c.Mail(sender); err != nil {
		return fmt.Errorf("email: the SMTP server %s refuses the sender: %w", addr, replyError(err))
	}
	if err := c.Rcpt(recipient); err != nil {
		return recipientRefused(fmt.Errorf("email: the SMTP server %s refuses the recipient: %w", addr,
			replyError(err)))
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("email: send the email to the SMTP server %s: %w", addr, replyError(err))
	}
	if _, err := w.Write(data); err != nil {
		return fmt.Errorf("email: send the email to the SMTP server %s: %w", addr, err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("email: the SMTP server %s refuses the email: %w", addr, replyError(err))
	}
	// The server has accepted the email once DATA ends, so a failure to say
	// goodbye does not fail the sending, which would send it again.
	_ = c.Quit()
	return nil
}

// Permanent reports whether err, an error of Deliver, says that the email
// cannot be delivered, however often it is sent again: the server refuses the
// recipient because its mailbox does not exist or takes no email
// (mailboxRefused), or the server offers no SMTPUTF8, and the recipient's
// address has a local part outside ASCII, or a domain outside ASCII that has
// no ASCII form (envelopeAddress). A full mailbox is not one, since it may
// empty. Nor is a refusal of the sender, of the sign-in, of a relay, or of the
// email itself, which the settings cause, and which the operator can mend
// while a job still has attempts left, even where the server gives it only in
// its reply to RCPT, as Postfix does by default, or gives it there in a reply
// that does not say which it is, as Exim and qmail do.
func Permanent(err error) bool {
	var p permanentError
	return errors.As(err, &p)
}

// permanentError is an error of Deliver that sending the email again would
// meet again (Permanent).
type permanentError struct{ error }

func (e permanentError) Unwrap() error { return e.error }

// recipientRefused returns err, the error of a refused RCPT, as a
// permanentError if the server's reply says that the recipient's mailbox
// takes no email (mailboxRefused).
func recipientRefused(err error) error {
	var reply *textproto.Error
	if errors.As(err, &reply) && mailboxRefused(reply) {
		return permanentError{err}
	}
	return err
}

// mailboxRefused reports whether reply, the server's refusal of RCPT, says
// that the recipient's mailbox does not exist or takes no email, for good,
// which only a reply of 5xx that starts with an enhanced status code (RFC
// 3463) can say: with 5.1.1 to 5.1.4, 5.1.6, or 5.1.10, a destination address
// that does not exist, is not valid, is ambiguous, has moved, or has a domain
// that takes no email, and with 5.2.1, a mailbox that is disabled. Its other
// codes are about something else, such as 5.2.2, a full mailbox, or 5.7.1, a
// refusal by policy, as of a relay or of the sender, which a server may give
// only in its reply to RCPT, although it took MAIL. A reply without one does
// not say which it is: a server that writes no enhanced status codes, as Exim
// and qmail by default, refuses a relay or a sender at RCPT with the 550 or
// 553 that it refuses a mailbox that does not exist with, as in 550 relay not
// permitted, so it is not permanent, and the email is sent again, as after
// any other failure.
func mailboxRefused(reply *textproto.Error) bool {
	class, subject, detail, ok := enhancedStatus(reply.Msg)
	switch {
	case !ok || reply.Code < 500 || reply.Code > 599 || class != 5:
		return false
	case subject == 1:
		return detail >= 1 && detail <= 4 || detail == 6 || detail == 10
	}
	return subject == 2 && detail == 1
}

// enhancedStatus returns the enhanced status code (RFC 3463) that text, the
// text of a server's reply, starts with, as 5.1.1, and false if it starts
// with none.
func enhancedStatus(text string) (class, subject, detail int, ok bool) {
	words := strings.Fields(text)
	if len(words) == 0 {
		return 0, 0, 0, false
	}
	parts := strings.Split(words[0], ".")
	if len(parts) != 3 {
		return 0, 0, 0, false
	}
	var n [3]int
	for i, p := range parts {
		if p == "" || len(p) > 3 || strings.IndexFunc(p, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
			return 0, 0, 0, false
		}
		n[i], _ = strconv.Atoi(p)
	}
	return n[0], n[1], n[2], true
}

// replyError returns err, an error that net/smtp returned for a command
// about the email, but, if err is the server's reply, with every word of the
// reply that holds an @ replaced by [address]. A server often quotes the
// address that it refuses, as in 550 5.1.1 <jo@example.com>: Recipient
// address rejected, and the reply's code and the rest of its text say why
// without it.
func replyError(err error) error {
	var reply *textproto.Error
	if !errors.As(err, &reply) {
		return err
	}
	words := strings.Fields(reply.Msg)
	for i, w := range words {
		if strings.ContainsRune(w, '@') {
			words[i] = "[address]"
		}
	}
	return &textproto.Error{Code: reply.Code, Msg: strings.Join(words, " ")}
}

// helloName returns the name that the client gives itself in its EHLO, on a
// connection whose own address is local: the host name, without the trailing
// dot that a public URL may end with, or, for an IP address, its address
// literal, as [192.0.2.1] or [IPv6:2001:db8::1], which RFC 5321 §4.1.3
// requires in place of a bare address. A host name that is not a domain as
// RFC 5321 writes one, as one with an underscore, which a public URL may
// have, or no host name at all, gives way to the address literal of local,
// which §4.1.4 has a client send when it has no domain, so that a server that
// checks the name does not refuse the greeting.
func (s *Service) helloName(local net.Addr) string {
	name := strings.TrimSuffix(s.hostname, ".")
	if ip, err := netip.ParseAddr(name); err == nil {
		return addressLiteral(ip)
	}
	if isDomain(name) {
		return name
	}
	if a, ok := local.(*net.TCPAddr); ok {
		if ip, ok := netip.AddrFromSlice(a.IP); ok {
			return addressLiteral(ip.Unmap())
		}
	}
	return "localhost"
}

// addressLiteral returns ip as RFC 5321 §4.1.3 writes it in place of a
// domain, without a zone.
func addressLiteral(ip netip.Addr) string {
	if ip.Is6() {
		return "[IPv6:" + ip.WithZone("").String() + "]"
	}
	return "[" + ip.String() + "]"
}

// isDomain reports whether name is a domain as RFC 5321 §4.1.2 writes one:
// labels of ASCII letters, digits, and hyphens, each starting and ending with
// a letter or a digit, joined by dots.
func isDomain(name string) bool {
	if name == "" {
		return false
	}
	for label := range strings.SplitSeq(name, ".") {
		if label == "" || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
				return false
			}
		}
	}
	return true
}

// envelopeAddress returns address as an SMTP server takes it: as it is, if it
// is ASCII or the server offers SMTPUTF8 (RFC 6531), and else with its domain
// in its ASCII form, the xn-- labels of IDNA, which every server takes, as
// registration takes domains outside ASCII (package account). A local part
// outside ASCII has no such form, so it needs SMTPUTF8. The error does not
// quote the address, which a job keeps its errors with.
func envelopeAddress(address string, smtpUTF8 bool) (string, error) {
	if smtpUTF8 || isASCII(address) {
		return address, nil
	}
	at := strings.LastIndexByte(address, '@')
	if at < 0 || !isASCII(address[:at]) {
		return "", errors.New("has a local part outside ASCII, which only SMTPUTF8 carries")
	}
	domain, err := ASCIIDomain(address[at+1:])
	if err != nil {
		return "", errors.New("has a domain outside ASCII that has no ASCII form")
	}
	return address[:at+1] + domain, nil
}

// ASCIIDomain returns domain, the domain of an email address, in the ASCII
// form that the instance sends email to where the SMTP server takes no other
// (envelopeAddress): IDNA's lookup form (UTS #46), which maps the other forms
// of a letter, such as a full-width one, to the letter, in lower case, and
// the ideographic and full-width full stops to dots, and writes a label
// outside ASCII as its xn-- form. For a domain that has no such form, as one
// with a label that IDNA refuses, it returns an error, with the domain mapped
// as far as IDNA maps it.
func ASCIIDomain(domain string) (string, error) {
	return idna.Lookup.ToASCII(domain)
}

// isASCII reports whether s is ASCII.
func isASCII(s string) bool {
	return strings.IndexFunc(s, func(r rune) bool { return r >= utf8.RuneSelf }) < 0
}

// authFor returns the mechanism that signs in to the server of c with
// settings: PLAIN, or LOGIN where the server offers only that. Each sends
// the password only over TLS, or to the machine itself
// (config.SMTPLoopback), as net/smtp's PLAIN does.
func authFor(c *smtp.Client, settings config.SMTP) (smtp.Auth, error) {
	ok, mechanisms := c.Extension("AUTH")
	if !ok {
		return nil, errors.New("the server offers no sign-in (AUTH), so remove the username and password from " +
			"the settings, or sign in to it over TLS, which some servers require before they offer it")
	}
	offered := strings.Fields(strings.ToUpper(mechanisms))
	password := settings.Password.Reveal()
	for _, m := range offered {
		if m == "PLAIN" {
			return smtp.PlainAuth("", settings.Username, password, settings.Host), nil
		}
	}
	for _, m := range offered {
		if m == "LOGIN" {
			return &loginAuth{username: settings.Username, password: password, host: settings.Host}, nil
		}
	}
	return nil, fmt.Errorf("the server offers only the sign-in mechanisms %s, but not PLAIN or LOGIN",
		strings.Join(offered, ", "))
}

// loginAuth is the LOGIN mechanism, which some servers offer instead of
// PLAIN: the server asks for the username, then for the password.
type loginAuth struct {
	username, password, host string
}

func (a *loginAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	// As PlainAuth does, the password goes only over TLS, or to the
	// machine itself.
	if !server.TLS && !config.SMTPLoopback(server.Name) {
		return "", nil, errors.New("unencrypted connection")
	}
	if server.Name != a.host {
		return "", nil, errors.New("wrong host name")
	}
	return "LOGIN", nil, nil
}

func (a *loginAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	switch prompt := strings.ToLower(strings.TrimSpace(string(fromServer))); {
	case strings.HasPrefix(prompt, "user"):
		return []byte(a.username), nil
	case strings.HasPrefix(prompt, "pass"):
		return []byte(a.password), nil
	}
	return nil, errors.New("the server asked for something other than the username and the password")
}
