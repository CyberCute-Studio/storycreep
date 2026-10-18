package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/CyberCute-Studio/storycreep/server/internal/secret"
)

// DefaultAssetDir is the directory where the local driver stores the files of
// the instance's assets unless STORYCREEP_ASSET_STORAGE names another (PRD
// FR-ASSET-01): the directory of the container image, where
// deploy/docker-compose.yml mounts the asset volume (plan note N18).
const DefaultAssetDir = "/var/lib/storycreep/assets"

// The limits of an asset's file (PRD FR-ASSET-02, NFR-SEC-07).
const (
	// DefaultAssetMaxBytes is the largest file that an upload takes unless
	// STORYCREEP_ASSET_MAX_BYTES says otherwise: 10 MB.
	DefaultAssetMaxBytes = 10 << 20
	// MaxAssetMaxBytes is the most that STORYCREEP_ASSET_MAX_BYTES may allow:
	// the 50 MB that NFR-SEC-07 allows the body of an upload.
	MaxAssetMaxBytes = 50 << 20
)

// AssetStorage is where the instance stores the files of its assets (PRD
// FR-ASSET-01, ADR-009): the directory of the local driver, or a bucket of an
// S3-compatible object storage.
type AssetStorage struct {
	// Dir is the absolute path of the local driver's directory, or "" for a
	// bucket.
	Dir string
	// S3 is the bucket, or nil for a directory.
	S3 *S3
}

// Driver returns the driver that stores the files.
func (s AssetStorage) Driver() BlobDriver {
	if s.S3 != nil {
		return BlobS3
	}
	return BlobLocal
}

// String returns the storage as STORYCREEP_ASSET_STORAGE writes it, with a
// bucket's secret key redacted, for display.
func (s AssetStorage) String() string {
	if s.S3 != nil {
		return s.S3.URL()
	}
	return s.Dir
}

// ParseAssetStorage parses STORYCREEP_ASSET_STORAGE: the absolute path of a
// directory, for the local driver, or an s3:// URL (ParseS3URL). Its errors
// never quote the value, which may hold a secret key.
func ParseAssetStorage(v string) (AssetStorage, error) {
	if strings.HasPrefix(strings.ToLower(v), "s3:") {
		s, err := ParseS3URL(v)
		if err != nil {
			return AssetStorage{}, err
		}
		return AssetStorage{S3: &s}, nil
	}
	if !filepath.IsAbs(v) {
		return AssetStorage{}, hiddenValueError{errors.New("must be the absolute path of a directory, such as " +
			DefaultAssetDir + ", or an s3:// URL")}
	}
	return AssetStorage{Dir: filepath.Clean(v)}, nil
}

// S3Security is how the connection to an S3-compatible object storage is
// protected.
type S3Security string

const (
	// S3TLS is HTTPS, as every hosted object storage takes.
	S3TLS S3Security = "tls"
	// S3None is plain HTTP, for an object storage on the same host or
	// network, such as a MinIO beside the server, where nothing on the way
	// can read the files. The requests are signed, so the secret key itself
	// is never sent.
	S3None S3Security = "none"
)

// S3Securities are the values of S3Security.
var S3Securities = []S3Security{S3TLS, S3None}

// The limits and defaults of the S3 settings.
const (
	// DefaultS3Region is the region of a URL that names none: us-east-1,
	// which MinIO and most S3-compatible object storages take whatever
	// their location.
	DefaultS3Region = "us-east-1"
	// maxS3CredentialLength bounds the access key ID and the secret key.
	maxS3CredentialLength = 256
	// maxS3RegionLength bounds the region.
	maxS3RegionLength = 64
	// The default ports of the two securities.
	s3TLSPort  = 443
	s3NonePort = 80
)

// s3Bucket matches a bucket's name as S3 allows it: 3 to 63 lowercase letters,
// digits, dots, and hyphens, starting and ending with a letter or a digit.
var s3Bucket = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)

// s3Region matches a region's name: letters, digits, hyphens, and
// underscores, as eu-central-1 or auto.
var s3Region = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// S3 is a bucket of an S3-compatible object storage, which the S3 driver
// stores the files of the instance's assets in (PRD FR-ASSET-01): the
// settings of STORYCREEP_ASSET_STORAGE, or those that first-run setup chose
// (FR-ADMIN-02). The driver addresses the bucket in the path of its URLs, as
// https://host/bucket/key, which every S3-compatible storage takes.
type S3 struct {
	// Host is the storage's host name or IP address, and Port its port.
	Host string
	Port int
	// Security is how the connection is protected.
	Security S3Security
	// Bucket is the bucket's name. It must exist: the server does not create
	// it.
	Bucket string
	// Region is the bucket's region, which the requests' signatures name.
	Region string
	// AccessKeyID and SecretAccessKey sign the requests, and the URLs that
	// the server gives out to read a file (FR-ASSET-01).
	AccessKeyID     string
	SecretAccessKey secret.Value
}

// S3Problem is a setting of a bucket that is not valid.
type S3Problem struct {
	// Field names the setting: host, port, security, bucket, region,
	// access_key_id, or secret_access_key.
	Field string
	// Msg says what is wrong with it, without its value.
	Msg string
}

// Check returns what is wrong with the settings s, each setting once, in the
// order of S3's fields.
func (s S3) Check() []S3Problem {
	var problems []S3Problem
	add := func(field, msg string) {
		problems = append(problems, S3Problem{Field: field, Msg: msg})
	}
	if msg := checkSMTPHost(s.Host); msg != "" {
		add("host", msg)
	}
	if s.Port < 1 || s.Port > 65535 {
		add("port", "must be between 1 and 65535")
	}
	if s.Security != S3TLS && s.Security != S3None {
		add("security", "is not tls or none")
	}
	switch {
	case s.Bucket == "":
		add("bucket", "is empty")
	case !s3Bucket.MatchString(s.Bucket) || strings.Contains(s.Bucket, ".."):
		add("bucket", "is not a bucket's name: 3 to 63 lowercase letters, digits, dots, and hyphens, starting and "+
			"ending with a letter or a digit")
	}
	switch {
	case s.Region == "":
		add("region", "is empty")
	case len(s.Region) > maxS3RegionLength:
		add("region", fmt.Sprintf("is longer than %d characters", maxS3RegionLength))
	case !s3Region.MatchString(s.Region):
		add("region", "is not a region's name: letters, digits, hyphens, and underscores, such as eu-central-1")
	}
	if msg := checkS3Credential(s.AccessKeyID); msg != "" {
		add("access_key_id", msg)
	}
	if msg := checkS3Credential(s.SecretAccessKey.Reveal()); msg != "" {
		add("secret_access_key", msg)
	}
	return problems
}

// checkS3Credential says what is wrong with an access key ID or a secret
// key, or returns "": it is printable ASCII without spaces, as the keys of
// every S3-compatible storage are, which a request's signature holds.
func checkS3Credential(v string) string {
	switch {
	case v == "":
		return "is empty; the server signs its requests and the URLs that it gives out, so it needs a key"
	case len(v) > maxS3CredentialLength:
		return fmt.Sprintf("is longer than %d characters", maxS3CredentialLength)
	}
	for _, r := range v {
		if r <= ' ' || r > '~' {
			return "is not printable ASCII without spaces"
		}
	}
	return ""
}

// Endpoint returns the URL of the storage, without the bucket: https, or
// http for S3None, with the host and the port.
func (s S3) Endpoint() string {
	scheme := "https"
	if s.Security == S3None {
		scheme = "http"
	}
	return (&url.URL{Scheme: scheme, Host: net.JoinHostPort(s.Host, strconv.Itoa(s.Port))}).String()
}

// ParseS3URL parses the URL of a bucket (STORYCREEP_ASSET_STORAGE):
// s3://access_key_id:secret_access_key@host[:port]/bucket, over HTTPS, on port
// 443 unless it names another, with the query parameters region, us-east-1
// unless it names one, and security=none for plain HTTP, on port 80 unless it
// names another. The keys are percent-encoded, as in any URL. Its errors
// never quote the URL, which holds the secret key.
func ParseS3URL(v string) (S3, error) {
	fail := func(msg string) (S3, error) {
		return S3{}, hiddenValueError{errors.New(msg)}
	}
	if !strings.HasPrefix(v, "s3://") {
		return fail("must start with s3://, in lowercase")
	}
	u, err := url.Parse(v)
	if err != nil {
		// The parser's explanation can quote part of the secret key.
		return fail("not a valid URL")
	}
	switch {
	case u.Opaque != "":
		return fail("not a valid URL")
	case u.Fragment != "":
		return fail("must not have a fragment")
	case u.User == nil:
		return fail("has no access key ID and secret key, as s3://access_key_id:secret_access_key@host/bucket")
	}
	s := S3{Security: S3TLS, Region: DefaultS3Region, Host: strings.ToLower(u.Hostname())}
	s.AccessKeyID = u.User.Username()
	key, _ := u.User.Password()
	s.SecretAccessKey = secret.New(key)
	bucket, ok := strings.CutPrefix(u.Path, "/")
	if !ok || bucket == "" || strings.Contains(strings.TrimSuffix(bucket, "/"), "/") {
		return fail("must name the bucket as its path, and nothing else, as s3://key:secret@host/bucket")
	}
	s.Bucket = strings.TrimSuffix(bucket, "/")
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return fail("has a query that is not valid")
	}
	for name, values := range query {
		if len(values) != 1 {
			return fail("has a query parameter more than once")
		}
		switch name {
		case "region":
			s.Region = values[0]
		case "security":
			if values[0] != string(S3TLS) && values[0] != string(S3None) {
				return fail("has a security that is not tls or none")
			}
			s.Security = S3Security(values[0])
		default:
			// The name is not quoted: a secret key with a ? that is not
			// percent-encoded ends the URL's authority there, and the rest of
			// the key becomes the query.
			return fail("has a query parameter other than region and security, which are the only ones it takes")
		}
	}
	s.Port = s3TLSPort
	if s.Security == S3None {
		s.Port = s3NonePort
	}
	if port := u.Port(); port != "" {
		if s.Port, err = strconv.Atoi(port); err != nil {
			return fail("the port must be between 1 and 65535")
		}
	}
	var msgs []string
	for _, p := range s.Check() {
		msgs = append(msgs, "the "+strings.ReplaceAll(p.Field, "_", " ")+" "+p.Msg)
	}
	if len(msgs) > 0 {
		return fail(strings.Join(msgs, "; "))
	}
	return s, nil
}

// URL returns the settings as STORYCREEP_ASSET_STORAGE writes them, with the
// secret key redacted, for display.
func (s S3) URL() string {
	u := url.URL{Scheme: "s3", Host: net.JoinHostPort(s.Host, strconv.Itoa(s.Port)), Path: "/" + s.Bucket,
		User: url.UserPassword(s.AccessKeyID, secret.Redacted)}
	q := url.Values{"region": {s.Region}}
	if s.Security == S3None {
		q.Set("security", string(S3None))
	}
	u.RawQuery = q.Encode()
	// url.URL escapes the brackets of the redacted key; they are shown as
	// they are, as in SMTP.URL.
	return strings.Replace(u.String(), url.QueryEscape(secret.Redacted), secret.Redacted, 1)
}
