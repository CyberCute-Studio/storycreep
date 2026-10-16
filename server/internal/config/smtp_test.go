package config

import (
	"slices"
	"strings"
	"testing"

	"github.com/CyberCute-Studio/storycreep/server/internal/secret"
)

// TestParseSMTPURL checks the forms of STORYCREEP_SMTP_URL: the scheme says
// how the connection is protected and what its default port is, the
// username and the password are percent-decoded, and the URL is shown with
// its password redacted.
func TestParseSMTPURL(t *testing.T) {
	for _, c := range []struct {
		url, shown string
		want       SMTP
	}{
		{"smtp://smtp.example.com", "smtp://smtp.example.com:587",
			SMTP{Host: "smtp.example.com", Port: 587, Security: SMTPStartTLS}},
		{"smtps://smtp.example.com", "smtps://smtp.example.com:465",
			SMTP{Host: "smtp.example.com", Port: 465, Security: SMTPTLS}},
		{"smtp://SMTP.Example.com:2525/", "smtp://smtp.example.com:2525",
			SMTP{Host: "smtp.example.com", Port: 2525, Security: SMTPStartTLS}},
		{"smtp://relay:25?security=none", "smtp://relay:25?security=none",
			SMTP{Host: "relay", Port: 25, Security: SMTPNone}},
		{"smtp://jo%40example.com:p%40ss%3Aw%2Frd@smtp.example.com:587?security=starttls",
			"smtp://jo%40example.com:" + secret.Redacted + "@smtp.example.com:587",
			SMTP{Host: "smtp.example.com", Port: 587, Security: SMTPStartTLS, Username: "jo@example.com",
				Password: secret.New("p@ss:w/rd")}},
		{"smtp://jo:pw@127.0.0.1:25?security=none", "smtp://jo:" + secret.Redacted + "@127.0.0.1:25?security=none",
			SMTP{Host: "127.0.0.1", Port: 25, Security: SMTPNone, Username: "jo", Password: secret.New("pw")}},
		{"smtp://[::1]:25?security=none", "smtp://[::1]:25?security=none",
			SMTP{Host: "::1", Port: 25, Security: SMTPNone}},
	} {
		got, err := ParseSMTPURL(c.url)
		if err != nil {
			t.Errorf("%s: %v", c.url, err)
			continue
		}
		if got.Host != c.want.Host || got.Port != c.want.Port || got.Security != c.want.Security ||
			got.Username != c.want.Username || got.Password.Reveal() != c.want.Password.Reveal() {
			t.Errorf("%s: %+v, want %+v", c.url, got, c.want)
		}
		if shown := got.URL(); shown != c.shown {
			t.Errorf("%s is shown as %s, want %s", c.url, shown, c.shown)
		}
	}
}

// TestParseSMTPURLErrors checks the URLs that STORYCREEP_SMTP_URL refuses,
// and that no error repeats the password.
func TestParseSMTPURLErrors(t *testing.T) {
	for _, c := range []struct{ url, want string }{
		{"http://smtp.example.com", "must start with smtp:// or smtps://"},
		{"SMTP://smtp.example.com", "must start with smtp:// or smtps://"},
		{"smtp://", "the host is empty"},
		{"smtp://smtp.example.com/mail", "must not have a path"},
		{"smtp://smtp.example.com#x", "must not have a fragment"},
		{"smtp://smtp.example.com:0", "the port must be between 1 and 65535"},
		{"smtp://smtp.example.com:70000", "the port must be between 1 and 65535"},
		{"smtp://smtp.example.com?tls=off", `has the query parameter "tls"`},
		{"smtp://smtp.example.com?security=tls", "has a security that is not starttls or none"},
		{"smtps://smtp.example.com?security=none", "takes security only with smtp://"},
		{"smtp://jo@smtp.example.com", "a username needs a password"},
		{"smtp://:s3cr3t-pw@smtp.example.com", "a password needs a username"},
		{"smtp://jo:s3cr3t-pw@smtp.example.com?security=none", "sends a password only to localhost"},
		{"smtp://jo:s3cr3t-pw@smtp.example.com:99999", "the port must be between 1 and 65535"},
		{"smtp://jo:s3cr3t%00pw@smtp.example.com", "contains a NUL character"},
		{"smtp://smtp_ex%ample.com", "not a valid URL"},
	} {
		_, err := ParseSMTPURL(c.url)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want an error that says %q", c.url, err, c.want)
		}
		if err != nil && strings.Contains(err.Error(), "s3cr3t") {
			t.Errorf("%s: the error repeats the password: %v", c.url, err)
		}
		if _, ok := err.(hiddenValueError); err != nil && !ok {
			t.Errorf("%s: the error is not a hiddenValueError, so Load would quote the URL", c.url)
		}
	}
}

// TestParseSMTPFrom checks the addresses that the email may come from.
func TestParseSMTPFrom(t *testing.T) {
	for _, from := range []string{"noreply@example.com", "Storycreep <noreply@example.com>",
		`"Storycreep, the tool" <noreply@example.com>`, "Jiří <jiri@example.com>"} {
		if a, msg := ParseSMTPFrom(from); msg != "" || a == nil {
			t.Errorf("%q: %s", from, msg)
		}
	}
	for _, from := range []string{"", "  ", "noreply", "Storycreep", "noreply@example.com\r\nBcc: x@example.com",
		"a@example.com, b@example.com", strings.Repeat("a", MaxSMTPFromLength) + "@example.com"} {
		if _, msg := ParseSMTPFrom(from); msg == "" {
			t.Errorf("%q is accepted", from)
		}
	}
}

// TestSMTPCheck checks the settings that first-run setup is given, field by
// field.
func TestSMTPCheck(t *testing.T) {
	valid := SMTP{Host: "smtp.example.com", Port: 587, Security: SMTPStartTLS, Username: "jo",
		Password: secret.New("pw"), From: "noreply@example.com"}
	if p := valid.Check(); len(p) != 0 {
		t.Fatalf("valid settings: %v", p)
	}
	for _, c := range []struct {
		name   string
		mutate func(*SMTP)
		fields []string
	}{
		{"no host", func(s *SMTP) { s.Host = "" }, []string{"host"}},
		{"a host with a space", func(s *SMTP) { s.Host = "smtp example.com" }, []string{"host"}},
		{"port 0", func(s *SMTP) { s.Port = 0 }, []string{"port"}},
		{"another security", func(s *SMTP) { s.Security = "ssl" }, []string{"security"}},
		{"no TLS to another host", func(s *SMTP) { s.Security = SMTPNone }, []string{"security"}},
		{"a username alone", func(s *SMTP) { s.Password = secret.Value{} }, []string{"password"}},
		{"a password alone", func(s *SMTP) { s.Username = "" }, []string{"username"}},
		{"no address", func(s *SMTP) { s.From = "" }, []string{"from"}},
		{"everything", func(s *SMTP) { *s = SMTP{Security: "x", Password: secret.New("pw")} },
			[]string{"host", "port", "security", "username", "from"}},
	} {
		s := valid
		c.mutate(&s)
		var fields []string
		for _, p := range s.Check() {
			fields = append(fields, p.Field)
		}
		if !slices.Equal(fields, c.fields) {
			t.Errorf("%s: problems with %q, want %q", c.name, fields, c.fields)
		}
	}
	local := valid
	local.Host, local.Security = "localhost", SMTPNone
	if p := local.Check(); len(p) != 0 {
		t.Errorf("signing in without TLS on localhost: %v", p)
	}
}

// TestSMTPKeys checks STORYCREEP_SMTP_URL and STORYCREEP_SMTP_FROM: each
// needs the other, they are shown with the password redacted, and hosted
// mode, which always sends email, requires them.
func TestSMTPKeys(t *testing.T) {
	c := mustLoad(t, withRequired(map[string]string{"STORYCREEP_SMTP_URL": "smtp://jo:s3cr3t@smtp.example.com",
		"STORYCREEP_SMTP_FROM": "Storycreep <noreply@example.com>"}))
	if c.SMTP == nil || c.SMTP.Host != "smtp.example.com" || c.SMTP.Password.Reveal() != "s3cr3t" ||
		c.SMTP.From != "Storycreep <noreply@example.com>" {
		t.Errorf("SMTP = %+v", c.SMTP)
	}
	if e := entry(t, c, "STORYCREEP_SMTP_URL"); e.Value != "smtp://jo:"+secret.Redacted+"@smtp.example.com:587" {
		t.Errorf("STORYCREEP_SMTP_URL is shown as %q", e.Value)
	}
	if c := mustLoad(t, withRequired(nil)); c.SMTP != nil {
		t.Errorf("without the keys, SMTP = %+v", c.SMTP)
	}

	for _, tt := range []struct {
		name    string
		env     map[string]string
		missing []string
	}{
		{"a URL alone", map[string]string{"STORYCREEP_SMTP_URL": "smtp://smtp.example.com"},
			[]string{"STORYCREEP_SMTP_FROM"}},
		{"an address alone", map[string]string{"STORYCREEP_SMTP_FROM": "noreply@example.com"},
			[]string{"STORYCREEP_SMTP_URL"}},
		{"hosted mode", map[string]string{"STORYCREEP_MODE": "hosted", "STORYCREEP_SMTP_URL": "",
			"STORYCREEP_SMTP_FROM": ""}, []string{"STORYCREEP_SMTP_URL", "STORYCREEP_SMTP_FROM"}},
		{"a setup URL alone", map[string]string{"STORYCREEP_SETUP_OWNER_EMAIL": "jo@example.com",
			"STORYCREEP_SETUP_OWNER_NAME": "Jo", "STORYCREEP_SETUP_OWNER_PASSWORD": "correct horse battery",
			"STORYCREEP_SETUP_WORKSPACE_NAME": "Harbour", "STORYCREEP_SETUP_SMTP_URL": "smtp://smtp.example.com"},
			[]string{"STORYCREEP_SETUP_SMTP_FROM"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			env := withRequired(tt.env)
			for k, v := range env {
				if v == "" {
					delete(env, k)
				}
			}
			var keys []string
			for _, p := range loadProblems(t, Options{Env: MapEnvironment(env)}) {
				keys = append(keys, p.Key)
			}
			if !slices.Equal(keys, tt.missing) {
				t.Errorf("problems with %q, want %q", keys, tt.missing)
			}
		})
	}
}

// TestUnattendedSMTP checks the SMTP server of unattended setup, which the
// other STORYCREEP_SETUP_ keys must come with.
func TestUnattendedSMTP(t *testing.T) {
	c := mustLoad(t, withRequired(map[string]string{"STORYCREEP_SETUP_OWNER_EMAIL": "jo@example.com",
		"STORYCREEP_SETUP_OWNER_NAME": "Jo", "STORYCREEP_SETUP_OWNER_PASSWORD": "correct horse battery",
		"STORYCREEP_SETUP_WORKSPACE_NAME": "Harbour", "STORYCREEP_SETUP_SMTP_URL": "smtps://jo:s3cr3t@smtp.example.com",
		"STORYCREEP_SETUP_SMTP_FROM": "noreply@example.com"}))
	if s := c.Setup.SMTP; s == nil || s.Security != SMTPTLS || s.Port != 465 || s.From != "noreply@example.com" {
		t.Errorf("Setup.SMTP = %+v", s)
	}
	if c.SMTP != nil {
		t.Errorf("the setup's SMTP server is the configuration's too: %+v", c.SMTP)
	}
	if e := entry(t, c, "STORYCREEP_SETUP_SMTP_URL"); strings.Contains(e.Value, "s3cr3t") {
		t.Errorf("STORYCREEP_SETUP_SMTP_URL is shown as %q", e.Value)
	}
	var keys []string
	for _, p := range loadProblems(t, Options{Env: MapEnvironment(withRequired(map[string]string{
		"STORYCREEP_SETUP_SMTP_URL": "smtp://smtp.example.com", "STORYCREEP_SETUP_SMTP_FROM": "noreply@example.com",
	}))}) {
		keys = append(keys, p.Key)
	}
	want := []string{"STORYCREEP_SETUP_OWNER_EMAIL", "STORYCREEP_SETUP_OWNER_NAME", "STORYCREEP_SETUP_OWNER_PASSWORD",
		"STORYCREEP_SETUP_WORKSPACE_NAME"}
	if !slices.Equal(keys, want) {
		t.Errorf("the SMTP keys of setup alone: problems with %q, want %q", keys, want)
	}
}
