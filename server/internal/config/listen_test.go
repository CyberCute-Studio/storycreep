package config

import (
	"strings"
	"testing"

	"github.com/CyberCute-Studio/storycreep/server/internal/secret"
)

func TestListenAddresses(t *testing.T) {
	for _, v := range []string{":8080", "0.0.0.0:80", "127.0.0.1:9090", "[::1]:8080", "[::]:443", "localhost:8080",
		"app.internal:65535", "[fe80::1%eth0]:8080"} {
		c := mustLoad(t, withRequired(map[string]string{"STORYCREEP_HTTP_ADDR": v, "STORYCREEP_ADMIN_ADDR": "off"}))
		if c.HTTPAddr != v || c.AdminAddr != "" {
			t.Errorf("STORYCREEP_HTTP_ADDR=%s gives %q, and STORYCREEP_ADMIN_ADDR=off gives %q", v, c.HTTPAddr, c.AdminAddr)
		}
		if e := entry(t, c, "STORYCREEP_ADMIN_ADDR"); e.Value != "off" {
			t.Errorf("the admin address is shown as %q, want off", e.Value)
		}
	}
	// The default admin address is the loopback interface's.
	c := mustLoad(t, withRequired(map[string]string{"STORYCREEP_ADMIN_ADDR": ":9091"}))
	if c.AdminAddr != ":9091" {
		t.Errorf("AdminAddr = %q", c.AdminAddr)
	}
}

// TestMetricsToken checks the token's validation, which never repeats the
// token, and that it is shown redacted.
//
// Verifies: NFR-SEC-14
func TestMetricsToken(t *testing.T) {
	c := mustLoad(t, withRequired(map[string]string{"STORYCREEP_METRICS_TOKEN": testMetricsToken}))
	if c.MetricsToken.Reveal() != testMetricsToken || c.Capabilities.MetricsEndpoint != MetricsProtected {
		t.Errorf("the token is not stored, or the metrics are not protected")
	}
	if e := entry(t, c, "STORYCREEP_METRICS_TOKEN"); e.Value != secret.Redacted {
		t.Errorf("the token is shown as %q", e.Value)
	}

	const tail = "s3cr3t-tail"
	for _, tt := range []struct{ value, want string }{
		{"short-" + tail[:4], "at least 16 characters"},
		{"has space " + tail + "0123456789", "may contain only letters, digits"},
		{"quote\"" + tail + "0123456789", "may contain only letters, digits"},
		{"padding=in-the-" + tail + "-middle", "may contain only letters, digits"},
		{"================", "must start with a letter"},
	} {
		problems := loadProblems(t, Options{Env: MapEnvironment(withRequired(map[string]string{
			"STORYCREEP_METRICS_TOKEN": tt.value}))})
		if len(problems) != 1 || problems[0].Key != "STORYCREEP_METRICS_TOKEN" || !strings.Contains(problems[0].Msg, tt.want) {
			t.Errorf("%q: problems = %v, want one saying %q", tt.value, problems, tt.want)
			continue
		}
		if msg := problems[0].String(); strings.Contains(msg, tail[:4]) || strings.Contains(msg, tt.value) {
			t.Errorf("%q: the problem repeats the token: %s", tt.value, msg)
		}
	}
}
