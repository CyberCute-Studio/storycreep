package config

import (
	"errors"
	"fmt"
	"net"
	"net/mail"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/CyberCute-Studio/storycreep/server/internal/secret"
)

// SMTPSecurity is how the connection to an SMTP server is protected.
type SMTPSecurity string

const (
	// SMTPTLS is TLS from the start of the connection, as on port 465
	// (RFC 8314).
	SMTPTLS SMTPSecurity = "tls"
	// SMTPStartTLS is a connection that STARTTLS protects before anything
	// is sent, as on port 587: a server that does not offer it is refused.
	SMTPStartTLS SMTPSecurity = "starttls"
	// SMTPNone is a connection without TLS, which sends the email as it is,
	// for a relay on the same host or network. It cannot sign in to the
	// server, except on a loopback address, so that it never sends a
	// password where it can be read.
	SMTPNone SMTPSecurity = "none"
)

// SMTPSecurities are the values of SMTPSecurity.
var SMTPSecurities = []SMTPSecurity{SMTPTLS, SMTPStartTLS, SMTPNone}

// The limits of the SMTP settings.
const (
	// maxSMTPHostLength is the longest host name of DNS (RFC 1035).
	maxSMTPHostLength = 253
	// maxSMTPCredentialLength bounds the username and the password.
	maxSMTPCredentialLength = 256
	// MaxSMTPFromLength bounds the address that email is sent from, with its
	// name.
	MaxSMTPFromLength = 320
)

// SMTP is an SMTP server that the instance sends its email through (PRD
// FR-AUTH-03), and the address that the email comes from: the settings of
// STORYCREEP_SMTP_URL and STORYCREEP_SMTP_FROM, or those that first-run setup
// chose (FR-ADMIN-02).
type SMTP struct {
	// Host is the server's host name or IP address, and Port its port.
	Host string
	Port int
	// Security is how the connection is protected.
	Security SMTPSecurity
	// Username and Password sign in to the server, or are empty for a server
	// that needs no sign-in.
	Username string
	Password secret.Value
	// From is the address that the email comes from, alone or with a name,
	// as in Storycreep <noreply@example.com>.
	From string
}

// SMTPProblem is a setting of an SMTP server that is not valid.
type SMTPProblem struct {
	// Field names the setting: host, port, security, username, password,
	// or from.
	Field string
	// Msg says what is wrong with it, without its value.
	Msg string
}

// Check returns what is wrong with the settings s, each setting once, in the
// order of SMTP's fields.
func (s SMTP) Check() []SMTPProblem {
	var problems []SMTPProblem
	add := func(field, msg string) {
		problems = append(problems, SMTPProblem{Field: field, Msg: msg})
	}
	if msg := checkSMTPHost(s.Host); msg != "" {
		add("host", msg)
	}
	if s.Port < 1 || s.Port > 65535 {
		add("port", "must be between 1 and 65535")
	}
	switch s.Security {
	case SMTPTLS, SMTPStartTLS:
	case SMTPNone:
		// The mailer sends a password only over TLS, or to the machine
		// itself, so these settings could never sign in.
		if s.Username != "" && s.Host != "localhost" && s.Host != "127.0.0.1" && s.Host != "::1" {
			add("security", "is none, which sends a password only to localhost, 127.0.0.1, or ::1; use "+
				"starttls or tls to sign in to another host")
		}
	default:
		add("security", "is not tls, starttls, or none")
	}
	password := s.Password.Reveal()
	switch {
	case s.Username == "" && password != "":
		add("username", "is empty, but the password is set; a password needs a username")
	case s.Username != "" && password == "":
		add("password", "is empty, but the username is set; a username needs a password")
	}
	if msg := checkSMTPCredential(s.Username); msg != "" {
		add("username", msg)
	}
	if msg := checkSMTPCredential(password); msg != "" {
		add("password", msg)
	}
	if _, msg := ParseSMTPFrom(s.From); msg != "" {
		add("from", msg)
	}
	return problems
}

// checkSMTPHost says what is wrong with an SMTP server's host, or returns "".
func checkSMTPHost(host string) string {
	switch {
	case host == "":
		return "is empty"
	case len(host) > maxSMTPHostLength:
		return fmt.Sprintf("is longer than %d characters", maxSMTPHostLength)
	}
	if _, msg := canonicalHost(host); msg != "" {
		return "is not valid: " + msg
	}
	return ""
}

// checkSMTPCredential says what is wrong with a username or a password, or
// returns "". SMTP's PLAIN mechanism separates them with NUL, so neither may
// hold one, and they are text.
func checkSMTPCredential(v string) string {
	switch {
	case len(v) > maxSMTPCredentialLength:
		return fmt.Sprintf("is longer than %d bytes", maxSMTPCredentialLength)
	case !utf8.ValidString(v):
		return "is not UTF-8 text"
	case strings.ContainsRune(v, 0):
		return "contains a NUL character"
	}
	return ""
}

// ParseSMTPFrom returns the address that the email comes from, as from
// writes it, alone or with a name, or says what is wrong with it. It takes
// an address that SMTP can deliver to, of at most MaxSMTPFromLength bytes
// with its name, and no control character, which could start another header
// of the email.
func ParseSMTPFrom(from string) (*mail.Address, string) {
	switch {
	case strings.TrimSpace(from) == "":
		return nil, "is empty"
	case len(from) > MaxSMTPFromLength:
		return nil, fmt.Sprintf("is longer than %d bytes", MaxSMTPFromLength)
	case strings.IndexFunc(from, unicode.IsControl) >= 0:
		return nil, "contains a control character"
	}
	a, err := mail.ParseAddress(from)
	if err != nil || strings.IndexFunc(a.Address, unicode.IsSpace) >= 0 {
		return nil, "is not an email address, alone or with a name, as noreply@example.com or " +
			"Storycreep <noreply@example.com>"
	}
	return a, ""
}

// Default ports of the SMTP URL's schemes.
const (
	smtpSubmissionPort = 587
	smtpTLSPort        = 465
)

// ParseSMTPURL parses the URL of an SMTP server (STORYCREEP_SMTP_URL):
// smtp://[username:password@]host[:port] for a connection that STARTTLS
// protects, on port 587 unless it names another, or the same with
// ?security=none for one without TLS; and smtps:// for TLS from the start,
// on port 465 unless it names another. The username and the password are
// percent-encoded, as in any URL. The returned settings have no From. Its
// errors never quote the URL, which may hold a password.
func ParseSMTPURL(v string) (SMTP, error) {
	fail := func(msg string) (SMTP, error) {
		return SMTP{}, hiddenValueError{errors.New(msg)}
	}
	var s SMTP
	switch {
	case strings.HasPrefix(v, "smtp://"):
		s.Security, s.Port = SMTPStartTLS, smtpSubmissionPort
	case strings.HasPrefix(v, "smtps://"):
		s.Security, s.Port = SMTPTLS, smtpTLSPort
	default:
		return fail("must start with smtp:// or smtps://, in lowercase")
	}
	u, err := url.Parse(v)
	if err != nil {
		// The parser's explanation can quote part of a password.
		return fail("not a valid URL")
	}
	switch {
	case u.Opaque != "":
		return fail("not a valid URL")
	case u.Path != "" && u.Path != "/":
		return fail("must not have a path")
	case u.Fragment != "":
		return fail("must not have a fragment")
	}
	if u.User != nil {
		s.Username = u.User.Username()
		password, _ := u.User.Password()
		s.Password = secret.New(password)
	}
	s.Host = strings.ToLower(u.Hostname())
	if port := u.Port(); port != "" {
		if s.Port, err = strconv.Atoi(port); err != nil {
			return fail("the port must be between 1 and 65535")
		}
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return fail("has a query that is not valid")
	}
	for name, values := range query {
		if name != "security" {
			return fail(fmt.Sprintf("has the query parameter %q, but takes only security", name))
		}
		switch {
		case s.Security == SMTPTLS:
			return fail("takes security only with smtp://; smtps:// is always TLS")
		case len(values) != 1 || values[0] != string(SMTPStartTLS) && values[0] != string(SMTPNone):
			return fail("has a security that is not starttls or none")
		}
		s.Security = SMTPSecurity(values[0])
	}
	var msgs []string
	for _, p := range s.Check() {
		if p.Field != "from" {
			msgs = append(msgs, "the "+p.Field+" "+p.Msg)
		}
	}
	if len(msgs) > 0 {
		return fail(strings.Join(msgs, "; "))
	}
	return s, nil
}

// URL returns the settings as STORYCREEP_SMTP_URL writes them, with the
// password redacted, for display.
func (s SMTP) URL() string {
	u := url.URL{Scheme: "smtp", Host: net.JoinHostPort(s.Host, strconv.Itoa(s.Port))}
	switch s.Security {
	case SMTPTLS:
		u.Scheme = "smtps"
	case SMTPNone:
		u.RawQuery = "security=none"
	}
	if s.Username != "" {
		u.User = url.UserPassword(s.Username, secret.Redacted)
	}
	// url.URL escapes the brackets of the redacted password; they are shown
	// as they are, as in a DatabaseURL.
	return strings.Replace(u.String(), url.QueryEscape(secret.Redacted), secret.Redacted, 1)
}
