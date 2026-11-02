// Command e2ehelper does for the web app's end-to-end tests (web/app/e2e)
// what Node cannot do without a dependency of its own: it makes and drops
// the PostgreSQL databases that the tests' instances run on, each on a
// database of its own, whose first-run setup is pending, and it runs a proxy
// that terminates TLS in front of an instance, with a certificate that it
// makes, as a reverse proxy in front of a self-hosted instance does (PRD
// FR-AUTH-02 AC6). It reads its flags alone, never the environment.
//
//	e2ehelper createdb -admin-url postgres://postgres:…@localhost:5432/storycreep -name storycreep_e2e_8788
//	e2ehelper dropdb -admin-url postgres://postgres:…@localhost:5432/storycreep -name storycreep_e2e_8788
//	e2ehelper tlsproxy -listen 127.0.0.1:8443 -upstream http://127.0.0.1:8790 -host localhost
//
// createdb makes an empty database, in place of one of the name that an
// earlier run left; dropdb drops it, and sessions still connected to it with
// it. The admin URL is a superuser's, of any database of the server.
//
// tlsproxy listens for HTTPS on -listen, with a certificate for -host, a DNS
// name or an IP address, signed by its own key, which no browser trusts, and
// passes each request on to -upstream over plain HTTP, WebSockets too, with
// the X-Forwarded-For, X-Forwarded-Host, and X-Forwarded-Proto headers, and
// the request's Host. It writes one line, "ready", with its URL, once it
// listens, and runs until it is interrupted, or until its standard input
// ends, as when the process that started it ends.
//
// It exits with status 1 if what it does fails, and with status 2 if a flag
// is wrong.
package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"regexp"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
)

func main() {
	if len(os.Args) < 2 {
		usage(errors.New("name a command: createdb, dropdb, or tlsproxy"))
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var err error
	switch cmd, args := os.Args[1], os.Args[2:]; cmd {
	case "createdb", "dropdb":
		err = database(ctx, cmd, args)
	case "tlsproxy":
		err = tlsProxy(ctx, args)
	default:
		usage(fmt.Errorf("unknown command %q: createdb, dropdb, or tlsproxy", cmd))
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "e2ehelper:", err)
		os.Exit(1)
	}
}

func usage(err error) {
	fmt.Fprintln(os.Stderr, "e2ehelper:", err)
	os.Exit(2)
}

// databaseName is the form of a database's name that createdb and dropdb
// take, which needs no quoting anywhere it is written.
var databaseName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

// database runs createdb or dropdb.
func database(ctx context.Context, cmd string, args []string) error {
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	adminURL := fs.String("admin-url", "", "a superuser's postgres:// URL, of any database of the server")
	name := fs.String("name", "", "the database's name: lowercase letters, digits, and _")
	_ = fs.Parse(args)
	if *adminURL == "" || !databaseName.MatchString(*name) {
		usage(errors.New("-admin-url and -name, of lowercase letters, digits, and _, are needed"))
	}
	conn, err := pgx.Connect(ctx, *adminURL)
	if err != nil {
		return err
	}
	defer conn.Close(context.Background())
	ident := pgx.Identifier{*name}.Sanitize()
	if _, err := conn.Exec(ctx, "DROP DATABASE IF EXISTS "+ident+" WITH (FORCE)"); err != nil {
		return err
	}
	if cmd == "createdb" {
		_, err = conn.Exec(ctx, "CREATE DATABASE "+ident)
	}
	return err
}

// tlsProxy runs tlsproxy.
func tlsProxy(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("tlsproxy", flag.ExitOnError)
	listen := fs.String("listen", "127.0.0.1:0", "the address to listen for HTTPS on")
	upstream := fs.String("upstream", "", "the http:// URL of the server to pass requests on to")
	host := fs.String("host", "localhost", "the DNS name or IP address of the certificate")
	_ = fs.Parse(args)
	to, err := url.Parse(*upstream)
	if err != nil || to.Scheme != "http" || to.Host == "" {
		usage(fmt.Errorf("-upstream %q is not an http:// URL", *upstream))
	}
	cert, err := certificate(*host)
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		return err
	}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(to)
			r.SetXForwarded()
			// As a proxy configured to pass the Host on does, so that the
			// server sees the host of its public URL.
			r.Out.Host = r.In.Host
		},
		ErrorLog: log.New(os.Stderr, "e2ehelper tlsproxy: ", 0),
	}
	srv := &http.Server{Handler: proxy, ReadHeaderTimeout: 10 * time.Second,
		TLSConfig: &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}}
	served := make(chan error, 1)
	go func() { served <- srv.ServeTLS(ln, "", "") }()
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	fmt.Printf("ready https://%s\n", net.JoinHostPort(*host, port))

	// The process that started the proxy holds its standard input, which
	// ends when that process does, even when it is killed.
	stdin := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.Discard, os.Stdin)
		close(stdin)
	}()
	select {
	case err := <-served:
		return err
	case <-ctx.Done():
	case <-stdin:
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return srv.Shutdown(shutdown)
}

// certificate returns a certificate for host, a DNS name or an IP address,
// valid for a day, signed by a key of its own.
func certificate(host string) (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: host},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if ip := net.ParseIP(host); ip != nil {
		tmpl.IPAddresses = []net.IP{ip}
	} else {
		tmpl.DNSNames = []string{host}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, nil
}
