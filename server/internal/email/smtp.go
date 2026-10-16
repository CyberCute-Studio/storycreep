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
// a job keeps its errors (package jobs).
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
	if err := c.Hello(s.helloName()); err != nil {
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
		return fmt.Errorf("email: the SMTP server %s offers no SMTPUTF8, and the recipient's address %w", addr, err)
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
		return fmt.Errorf("email: the SMTP server %s refuses the recipient: %w", addr, replyError(err))
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

// helloName returns the name that the client gives itself in its EHLO: the
// host name, or, for an IP address, its address literal, as [192.0.2.1] or
// [IPv6:2001:db8::1], which RFC 5321 §4.1.3 requires in place of a bare
// address.
func (s *Service) helloName() string {
	if s.hostname == "" {
		return "localhost"
	}
	if ip, err := netip.ParseAddr(s.hostname); err == nil {
		if ip.Is6() {
			return "[IPv6:" + ip.String() + "]"
		}
		return "[" + ip.String() + "]"
	}
	return s.hostname
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
	domain, err := idna.Lookup.ToASCII(address[at+1:])
	if err != nil {
		return "", errors.New("has a domain outside ASCII that has no ASCII form")
	}
	return address[:at+1] + domain, nil
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
