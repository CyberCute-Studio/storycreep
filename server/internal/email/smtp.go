package email

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"github.com/CyberCute-Studio/storycreep/server/internal/config"
)

// deliver sends msg through the SMTP server of settings, and returns once
// the server has accepted it, within the Service's timeout. Its errors say
// which step failed, and never hold the password.
func (s *Service) deliver(ctx context.Context, settings config.SMTP, msg Message) error {
	from, problem := config.ParseSMTPFrom(settings.From)
	if problem != "" {
		return fmt.Errorf("email: the address that email comes from %s", problem)
	}
	data, err := compose(from, msg, time.Now())
	if err != nil {
		return fmt.Errorf("email: compose the email: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	addr := net.JoinHostPort(settings.Host, strconv.Itoa(settings.Port))
	tlsConfig := &tls.Config{ServerName: settings.Host, MinVersion: tls.VersionTLS12, RootCAs: s.rootCAs}
	dialer := &net.Dialer{}
	var conn net.Conn
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
	if err := c.Mail(from.Address); err != nil {
		return fmt.Errorf("email: the SMTP server %s refuses the sender: %w", addr, err)
	}
	if err := c.Rcpt(msg.To.Address); err != nil {
		return fmt.Errorf("email: the SMTP server %s refuses the recipient: %w", addr, err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("email: send the email to the SMTP server %s: %w", addr, err)
	}
	if _, err := w.Write(data); err != nil {
		return fmt.Errorf("email: send the email to the SMTP server %s: %w", addr, err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("email: the SMTP server %s refuses the email: %w", addr, err)
	}
	// The server has accepted the email once DATA ends, so a failure to say
	// goodbye does not fail the sending, which would send it again.
	_ = c.Quit()
	return nil
}

// helloName returns the name that the client gives itself in its EHLO.
func (s *Service) helloName() string {
	if s.hostname == "" {
		return "localhost"
	}
	return s.hostname
}

// authFor returns the mechanism that signs in to the server of c with
// settings: PLAIN, or LOGIN where the server offers only that. Each sends
// the password only over TLS, or to a server on a loopback address, as
// net/smtp's PLAIN does.
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
	if !server.TLS && !loopback(server.Name) {
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

// loopback reports whether host names the machine itself, as net/smtp's
// PLAIN reads it.
func loopback(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}
