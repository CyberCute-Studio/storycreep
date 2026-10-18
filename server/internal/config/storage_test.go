package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/CyberCute-Studio/storycreep/server/internal/secret"
)

// testAssetDir is an absolute directory, on every OS, for
// STORYCREEP_ASSET_STORAGE.
var testAssetDir = filepath.Join(os.TempDir(), "storycreep-assets")

// TestParseS3URL checks the URLs of a bucket that STORYCREEP_ASSET_STORAGE
// takes, their defaults, and that an error never quotes the secret key.
func TestParseS3URL(t *testing.T) {
	s, err := ParseS3URL("s3://AKIA%2FKEY:se%2Fcr%3Aet@S3.Example.com/assets")
	if err != nil {
		t.Fatal(err)
	}
	want := S3{Host: "s3.example.com", Port: 443, Security: S3TLS, Bucket: "assets", Region: DefaultS3Region,
		AccessKeyID: "AKIA/KEY"}
	if got := s; got.SecretAccessKey.Reveal() != "se/cr:et" || func() bool {
		got.SecretAccessKey = secret.Value{}
		return got != want
	}() {
		t.Errorf("ParseS3URL = %+v, want %+v with the secret key se/cr:et", s, want)
	}
	if got := s.Endpoint(); got != "https://s3.example.com:443" {
		t.Errorf("Endpoint() = %q", got)
	}
	if got := s.URL(); got != "s3://AKIA%2FKEY:"+secret.Redacted+"@s3.example.com:443/assets?region=us-east-1" {
		t.Errorf("URL() = %q", got)
	}

	local, err := ParseS3URL("s3://minio:minio-secret@minio:9000/storycreep?region=eu-central-1&security=none")
	if err != nil {
		t.Fatal(err)
	}
	if local.Security != S3None || local.Port != 9000 || local.Region != "eu-central-1" ||
		local.Endpoint() != "http://minio:9000" {
		t.Errorf("a MinIO beside the server: %+v, %s", local, local.Endpoint())
	}
	if plain, err := ParseS3URL("s3://k:s@minio/bucket?security=none"); err != nil || plain.Port != 80 {
		t.Errorf("plain HTTP without a port: %+v, %v; want port 80", plain, err)
	}

	for _, tt := range []struct{ name, url, want string }{
		{"another scheme", "https://k:s3cr3t@host/bucket", "must start with s3://"},
		{"no keys", "s3://host/bucket", "has no access key ID"},
		{"no secret key", "s3://k@host/bucket", "the secret access key is empty"},
		{"no bucket", "s3://k:s3cr3t@host", "must name the bucket"},
		{"a key in the bucket", "s3://k:s3cr3t@host/bucket/prefix", "must name the bucket"},
		{"a bucket in capitals", "s3://k:s3cr3t@host/Bucket", "is not a bucket's name"},
		{"a short bucket", "s3://k:s3cr3t@host/ab", "is not a bucket's name"},
		{"no host", "s3://k:s3cr3t@/bucket", "the host is empty"},
		{"a bad port", "s3://k:s3cr3t@host:99999/bucket", "the port must be between 1 and 65535"},
		{"another security", "s3://k:s3cr3t@host/bucket?security=starttls", "not tls or none"},
		{"a region with a space", "s3://k:s3cr3t@host/bucket?region=eu+west", "is not a region's name"},
		{"another parameter", "s3://k:s3cr3t@host/bucket?path_style=true", "other than region and security"},
		{"a parameter twice", "s3://k:s3cr3t@host/bucket?region=a&region=b", "more than once"},
		{"a fragment", "s3://k:s3cr3t@host/bucket#x", "must not have a fragment"},
		{"a key with a space", "s3://k%20k:s3cr3t@host/bucket", "is not printable ASCII"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseS3URL(tt.url)
			if err == nil {
				t.Fatalf("ParseS3URL accepts %s", tt.url)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %q, want one that says %q", err, tt.want)
			}
			if strings.Contains(err.Error(), "s3cr3t") {
				t.Errorf("the error %q quotes the secret key", err)
			}
		})
	}
}

// TestAssetStorageKeys checks STORYCREEP_ASSET_STORAGE, which takes a
// directory or a bucket, sets the BlobDriver capability, is shown with the
// secret key redacted, and is required in hosted mode, as a bucket;
// STORYCREEP_ASSET_MAX_BYTES and its bounds; and
// STORYCREEP_SETUP_ASSET_STORAGE, which takes a bucket alone.
func TestAssetStorageKeys(t *testing.T) {
	c := mustLoad(t, withRequired(nil))
	if c.AssetStorage != nil || c.Capabilities.BlobDriver != BlobSetup || c.AssetMaxBytes != DefaultAssetMaxBytes {
		t.Errorf("without the keys: storage %+v, BlobDriver %s, max %d", c.AssetStorage, c.Capabilities.BlobDriver,
			c.AssetMaxBytes)
	}

	c = mustLoad(t, withRequired(map[string]string{"STORYCREEP_ASSET_STORAGE": testAssetDir + string(filepath.Separator)}))
	if c.AssetStorage == nil || c.AssetStorage.Dir != testAssetDir || c.AssetStorage.S3 != nil {
		t.Errorf("a directory: %+v, want %s", c.AssetStorage, testAssetDir)
	}

	c = mustLoad(t, withRequired(map[string]string{"STORYCREEP_ASSET_STORAGE": testS3URL,
		"STORYCREEP_ASSET_MAX_BYTES": "52428800"}))
	if c.AssetStorage == nil || c.AssetStorage.S3 == nil || c.AssetStorage.S3.SecretAccessKey.Reveal() != "s3-s3cr3t" {
		t.Errorf("a bucket: %+v", c.AssetStorage)
	}
	if e := entry(t, c, "STORYCREEP_ASSET_STORAGE"); strings.Contains(e.Value, "s3-s3cr3t") ||
		!strings.Contains(e.Value, secret.Redacted) {
		t.Errorf("STORYCREEP_ASSET_STORAGE is shown as %q", e.Value)
	}
	if c.AssetMaxBytes != MaxAssetMaxBytes {
		t.Errorf("AssetMaxBytes = %d", c.AssetMaxBytes)
	}

	c = mustLoad(t, withRequired(map[string]string{"STORYCREEP_MODE": "hosted"}))
	if c.Capabilities.BlobDriver != BlobS3 || c.AssetStorage.S3 == nil {
		t.Errorf("hosted mode: BlobDriver %s, storage %+v", c.Capabilities.BlobDriver, c.AssetStorage)
	}

	for _, tt := range []struct {
		name string
		env  map[string]string
		key  string
	}{
		{"a relative directory", map[string]string{"STORYCREEP_ASSET_STORAGE": "assets"}, "STORYCREEP_ASSET_STORAGE"},
		{"a bucket without keys", map[string]string{"STORYCREEP_ASSET_STORAGE": "s3://host/bucket"},
			"STORYCREEP_ASSET_STORAGE"},
		{"hosted mode with a directory", map[string]string{"STORYCREEP_MODE": "hosted",
			"STORYCREEP_ASSET_STORAGE": testAssetDir}, "STORYCREEP_ASSET_STORAGE"},
		{"hosted mode without storage", map[string]string{"STORYCREEP_MODE": "hosted", "STORYCREEP_ASSET_STORAGE": ""},
			"STORYCREEP_ASSET_STORAGE"},
		{"no bytes", map[string]string{"STORYCREEP_ASSET_MAX_BYTES": "0"}, "STORYCREEP_ASSET_MAX_BYTES"},
		{"more than an upload's body", map[string]string{"STORYCREEP_ASSET_MAX_BYTES": "52428801"},
			"STORYCREEP_ASSET_MAX_BYTES"},
		{"a unit", map[string]string{"STORYCREEP_ASSET_MAX_BYTES": "10MB"}, "STORYCREEP_ASSET_MAX_BYTES"},
		{"a setup directory", map[string]string{"STORYCREEP_SETUP_OWNER_EMAIL": "jo@example.com",
			"STORYCREEP_SETUP_OWNER_NAME": "Jo", "STORYCREEP_SETUP_OWNER_PASSWORD": "correct horse battery",
			"STORYCREEP_SETUP_WORKSPACE_NAME": "Harbour", "STORYCREEP_SETUP_ASSET_STORAGE": testAssetDir},
			"STORYCREEP_SETUP_ASSET_STORAGE"},
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
				if strings.Contains(p.Msg, "s3cr3t") {
					t.Errorf("the problem %q quotes the secret key", p)
				}
			}
			if !slices.Equal(keys, []string{tt.key}) {
				t.Errorf("problems with %q, want %s", keys, tt.key)
			}
		})
	}

	c = mustLoad(t, withRequired(map[string]string{"STORYCREEP_SETUP_OWNER_EMAIL": "jo@example.com",
		"STORYCREEP_SETUP_OWNER_NAME": "Jo", "STORYCREEP_SETUP_OWNER_PASSWORD": "correct horse battery",
		"STORYCREEP_SETUP_WORKSPACE_NAME": "Harbour", "STORYCREEP_SETUP_ASSET_STORAGE": testS3URL}))
	if c.Setup == nil || c.Setup.S3 == nil || c.Setup.S3.Bucket != "storycreep" {
		t.Errorf("unattended setup's bucket: %+v", c.Setup)
	}
	if e := entry(t, c, "STORYCREEP_SETUP_ASSET_STORAGE"); strings.Contains(e.Value, "s3-s3cr3t") {
		t.Errorf("STORYCREEP_SETUP_ASSET_STORAGE is shown as %q", e.Value)
	}
}
