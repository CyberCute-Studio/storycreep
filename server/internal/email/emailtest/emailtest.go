// Package emailtest is an SMTP server for tests, which keeps the email that
// it is sent (package email). It speaks as much SMTP as package email does:
// EHLO, STARTTLS, AUTH PLAIN and LOGIN, MAIL, RCPT, DATA, RSET, NOOP, and
// QUIT, over plain TCP or TLS, with a certificate for 127.0.0.1 and
// localhost that it makes itself.
package emailtest

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"fmt"
	"io"
	"math/big"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/textproto"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/CyberCute-Studio/storycreep/server/internal/config"
	"github.com/CyberCute-Studio/storycreep/server/internal/secret"
)

// Options configure a Server.
type Options struct {
	// TLS makes the server speak TLS from the start of each connection.
	TLS bool
	// StartTLS makes the server offer STARTTLS on a connection without TLS.
	StartTLS bool
	// Mechanisms are the sign-in mechanisms that the server offers, of
	// PLAIN and LOGIN, and none if it is empty. A server that offers one
	// refuses to send email until the client signs in with Username and
	// Password.
	Mechanisms         []string
	Username, Password string
	// ASCII makes the server offer no SMTPUTF8, so that it refuses an
	// address outside ASCII in MAIL or RCPT, as such a server does.
	ASCII bool
	// RefuseRecipients, if it is not zero, is the code of the reply with
	// which the server refuses every recipient: 550 for an address that does
	// not exist, or 552 for a full mailbox. The reply quotes the address, as
	// many servers' do.
	RefuseRecipients int
}

// Message is an email that the server was sent.
type Message struct {
	// From is the envelope's sender, and To its recipients.
	From string
	To   []string
	// Data is the email, as DATA sent it, without the dots that it doubled.
	Data []byte
	// TLS says whether the email came over TLS, and Username who signed in
	// to send it, if anyone did.
	TLS      bool
	Username string
	// Hello is the name that the client gave itself in its last EHLO.
	Hello string
}

// Header returns the email's header.
func (m Message) Header(t testing.TB) mail.Header {
	t.Helper()
	msg, err := mail.ReadMessage(bytes.NewReader(m.Data))
	if err != nil {
		t.Fatalf("read the email: %v", err)
	}
	return msg.Header
}

// Body returns the email's body, decoded from quoted-printable.
func (m Message) Body(t testing.TB) string {
	t.Helper()
	msg, err := mail.ReadMessage(bytes.NewReader(m.Data))
	if err != nil {
		t.Fatalf("read the email: %v", err)
	}
	b, err := io.ReadAll(quotedprintable.NewReader(msg.Body))
	if err != nil {
		t.Fatalf("decode the email's body: %v", err)
	}
	return string(b)
}

// linkToken finds the token in the fragment of a link.
var linkToken = regexp.MustCompile(`/app/[a-z-]+#token=([A-Za-z0-9_-]+)`)

// Token returns the token of the link that the email's body holds.
func (m Message) Token(t testing.TB) string {
	t.Helper()
	match := linkToken.FindStringSubmatch(m.Body(t))
	if match == nil {
		t.Fatalf("the email holds no link: %s", m.Body(t))
	}
	return match[1]
}

// Server is an SMTP server for a test.
type Server struct {
	opts  Options
	ln    net.Listener
	certs *x509.CertPool
	tls   *tls.Config

	mu       sync.Mutex
	messages []Message
	// arrived is closed and replaced when a message arrives.
	arrived chan struct{}
}

// Start starts a server with opts on 127.0.0.1, which stops when the test
// ends.
func Start(t testing.TB, opts Options) *Server {
	t.Helper()
	cert, pool := certificate(t)
	s := &Server{opts: opts, certs: pool, arrived: make(chan struct{}),
		tls: &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if opts.TLS {
		ln = tls.NewListener(ln, s.tls)
	}
	s.ln = ln
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(conn)
		}
	}()
	return s
}

// Port returns the port that the server listens on.
func (s *Server) Port() int {
	return s.ln.Addr().(*net.TCPAddr).Port
}

// CertPool returns the pool of the server's certificate, which a client
// must trust.
func (s *Server) CertPool() *x509.CertPool {
	return s.certs
}

// Settings returns the settings of the server, on 127.0.0.1, with security,
// and the Options' username and password, from from.
func (s *Server) Settings(security config.SMTPSecurity, from string) config.SMTP {
	return config.SMTP{Host: "127.0.0.1", Port: s.Port(), Security: security, Username: s.opts.Username,
		Password: secret.New(s.opts.Password), From: from}
}

// URL returns the server as STORYCREEP_SMTP_URL writes it, without TLS, as
// its Options must allow, with the username and the password
// percent-encoded.
func (s *Server) URL() string {
	u := url.URL{Scheme: "smtp", Host: "127.0.0.1:" + strconv.Itoa(s.Port()), RawQuery: "security=none"}
	if s.opts.Username != "" {
		u.User = url.UserPassword(s.opts.Username, s.opts.Password)
	}
	return u.String()
}

// Messages returns the email that the server was sent, in order.
func (s *Server) Messages() []Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Message(nil), s.messages...)
}

// Wait waits up to timeout for the server to have been sent n emails, and
// returns them, or fails the test.
func (s *Server) Wait(t testing.TB, n int, timeout time.Duration) []Message {
	t.Helper()
	deadline := time.After(timeout)
	for {
		s.mu.Lock()
		got, arrived := append([]Message(nil), s.messages...), s.arrived
		s.mu.Unlock()
		if len(got) >= n {
			return got
		}
		select {
		case <-arrived:
		case <-deadline:
			t.Fatalf("the SMTP server was sent %d emails in %v, want %d", len(got), timeout, n)
		}
	}
}

func (s *Server) receive(m Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = append(s.messages, m)
	close(s.arrived)
	s.arrived = make(chan struct{})
}

// serve speaks SMTP on conn until the client quits or goes away.
func (s *Server) serve(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(time.Minute))
	tp := textproto.NewConn(conn)
	reply := func(code int, msg string) { _ = tp.PrintfLine("%d %s", code, msg) }
	_, overTLS := conn.(*tls.Conn)
	var current Message
	var hello string
	reply(220, "emailtest ESMTP")
	for {
		line, err := tp.ReadLine()
		if err != nil {
			return
		}
		verb, arg, _ := strings.Cut(line, " ")
		switch strings.ToUpper(verb) {
		case "EHLO", "HELO":
			hello = arg
			lines := []string{"emailtest"}
			if s.opts.StartTLS && !overTLS {
				lines = append(lines, "STARTTLS")
			}
			if len(s.opts.Mechanisms) > 0 {
				lines = append(lines, "AUTH "+strings.Join(s.opts.Mechanisms, " "))
			}
			lines = append(lines, "8BITMIME")
			if !s.opts.ASCII {
				lines = append(lines, "SMTPUTF8")
			}
			for i, l := range lines {
				sep := "-"
				if i == len(lines)-1 {
					sep = " "
				}
				_ = tp.PrintfLine("250%s%s", sep, l)
			}
		case "STARTTLS":
			if !s.opts.StartTLS || overTLS {
				reply(502, "no STARTTLS")
				continue
			}
			reply(220, "go ahead")
			tc := tls.Server(conn, s.tls)
			if err := tc.Handshake(); err != nil {
				return
			}
			conn, overTLS, current = tc, true, Message{}
			tp = textproto.NewConn(conn)
		case "AUTH":
			user, ok := s.auth(tp, arg)
			if !ok {
				reply(535, "authentication failed")
				continue
			}
			current.Username = user
			reply(235, "authenticated")
		case "MAIL":
			switch {
			case len(s.opts.Mechanisms) > 0 && current.Username == "":
				reply(530, "authentication required")
			case s.opts.ASCII && !ascii(arg):
				reply(553, "an address outside ASCII needs SMTPUTF8")
			default:
				current.From = address(arg)
				reply(250, "ok")
			}
		case "RCPT":
			if s.opts.ASCII && !ascii(arg) {
				reply(553, "an address outside ASCII needs SMTPUTF8")
				continue
			}
			if code := s.opts.RefuseRecipients; code != 0 {
				text := "5.1.1 <" + address(arg) + ">: Recipient address rejected: User unknown"
				if code == 552 {
					text = "5.2.2 <" + address(arg) + ">: Mailbox full"
				}
				reply(code, text)
				continue
			}
			current.To = append(current.To, address(arg))
			reply(250, "ok")
		case "DATA":
			reply(354, "go ahead")
			data, err := tp.ReadDotBytes()
			if err != nil {
				return
			}
			current.Data, current.TLS, current.Hello = data, overTLS, hello
			s.receive(current)
			current = Message{Username: current.Username}
			reply(250, "queued")
		case "RSET":
			current = Message{Username: current.Username}
			reply(250, "ok")
		case "NOOP":
			reply(250, "ok")
		case "QUIT":
			reply(221, "bye")
			return
		default:
			reply(502, "not implemented")
		}
	}
}

// auth checks the sign-in of an AUTH command whose argument is arg, and
// returns the username that signed in.
func (s *Server) auth(tp *textproto.Conn, arg string) (string, bool) {
	mechanism, initial, _ := strings.Cut(arg, " ")
	mechanism = strings.ToUpper(mechanism)
	offered := false
	for _, m := range s.opts.Mechanisms {
		offered = offered || m == mechanism
	}
	if !offered {
		return "", false
	}
	ask := func(prompt string) (string, bool) {
		_ = tp.PrintfLine("334 %s", base64.StdEncoding.EncodeToString([]byte(prompt)))
		line, err := tp.ReadLine()
		if err != nil {
			return "", false
		}
		b, err := base64.StdEncoding.DecodeString(line)
		return string(b), err == nil
	}
	var user, password string
	switch mechanism {
	case "PLAIN":
		var b []byte
		var err error
		if initial == "" {
			s, ok := ask("")
			if !ok {
				return "", false
			}
			b = []byte(s)
		} else if b, err = base64.StdEncoding.DecodeString(initial); err != nil {
			return "", false
		}
		parts := strings.Split(string(b), "\x00")
		if len(parts) != 3 {
			return "", false
		}
		user, password = parts[1], parts[2]
	case "LOGIN":
		var ok bool
		if user, ok = ask("Username:"); !ok {
			return "", false
		}
		if password, ok = ask("Password:"); !ok {
			return "", false
		}
	}
	return user, user == s.opts.Username && password == s.opts.Password
}

// ascii reports whether s is ASCII.
func ascii(s string) bool {
	return strings.IndexFunc(s, func(r rune) bool { return r >= utf8.RuneSelf }) < 0
}

// address returns the address of a MAIL FROM: or RCPT TO: argument.
func address(arg string) string {
	_, rest, _ := strings.Cut(arg, ":")
	rest = strings.TrimSpace(rest)
	if i := strings.IndexByte(rest, '>'); strings.HasPrefix(rest, "<") && i > 0 {
		return rest[1:i]
	}
	return rest
}

// certificate returns a new self-signed certificate for 127.0.0.1 and
// localhost, and a pool that trusts it.
func certificate(t testing.TB) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "emailtest"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IsCA:                  true,
		BasicConstraintsValid: true,
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1)},
		DNSNames:              []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}, pool
}

// String describes the server, for a test's messages.
func (s *Server) String() string {
	return fmt.Sprintf("the SMTP server on %s", s.ln.Addr())
}
