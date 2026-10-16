// Package email sends the instance's email through an SMTP server (PRD
// FR-AUTH-03): the links that verify an email address and reset a password.
//
// The SMTP server is the one that STORYCREEP_SMTP_URL and STORYCREEP_SMTP_FROM
// set, for as long as they are set, or else the one that first-run setup
// chose and the instance's row holds (FR-ADMIN-02), or none. An instance
// without one sends no email: it says so (Configured), and its links are
// given as text to the people who may hold them instead (package account).
// Hosted mode always has one (the EmailRequired capability), which its
// configuration requires.
//
// Email is plain text in UTF-8, with quoted-printable lines. An address
// outside ASCII, which registration takes, goes as it is to a server that
// offers SMTPUTF8, and to any other with its domain in its ASCII form, the
// xn-- labels of IDNA; a local part outside ASCII has no such form, so only a
// server that offers SMTPUTF8 delivers to it. The connection is protected as
// the settings say: with TLS from the start, with STARTTLS, which a server
// that does not offer it is refused for, or not at all, for a relay on the
// same host or network, which then cannot sign in unless its host is
// localhost, 127.0.0.1, or ::1 (config.SMTPLoopback). A server's certificate
// must be valid for its host name. Signing in uses PLAIN, or LOGIN where the
// server offers only that.
//
// Sending takes time and can fail, so it is the work of a job (package jobs),
// which is attempted again when it fails, and never of a request, whose
// answer would otherwise take as long as the server does to accept the
// email, and tell whether an email was sent.
package email

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/CyberCute-Studio/storycreep/server/internal/config"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/secret"
)

// ErrNotConfigured is returned by Send when the instance has no SMTP server.
var ErrNotConfigured = errors.New("email: the instance has no SMTP server, so it sends no email")

// DB runs a transaction of the application login, as *database.DB does.
type DB interface {
	InTx(ctx context.Context, scope database.Scope, fn func(tx pgx.Tx) error) error
}

// Options configure a Service.
type Options struct {
	// SMTP is the SMTP server that the configuration sets, which overrides
	// the one that first-run setup chose, or nil if it sets none.
	SMTP *config.SMTP
	// Hostname is the name that the server gives itself to the SMTP server,
	// in its EHLO: the host of STORYCREEP_PUBLIC_URL, for which an address
	// literal stands where it is an IP address, or not a domain that SMTP
	// takes (helloName).
	Hostname string
	// RootCAs, if set, are the certificates that an SMTP server's must chain
	// to, instead of the system's. Tests set it.
	RootCAs *x509.CertPool
	// Timeout bounds the time that sending one email takes, from connecting
	// to the SMTP server to its acceptance of the email. Zero means 45
	// seconds, less than the minute that a job has before it is cancelled,
	// so that a server that does not answer fails the attempt with an error
	// that names the step it hung at.
	Timeout time.Duration
}

// Service sends the instance's email.
type Service struct {
	db       DB
	override *config.SMTP
	hostname string
	rootCAs  *x509.CertPool
	timeout  time.Duration
}

// defaultTimeout bounds the time that sending one email takes, unless
// Options says otherwise: less than a job's minute, which reading the
// settings and preparing the link take from too.
const defaultTimeout = 45 * time.Second

// New returns a Service that reads the SMTP settings of the instance in db,
// with opts.
func New(db DB, opts Options) *Service {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	return &Service{db: db, override: opts.SMTP, hostname: opts.Hostname, rootCAs: opts.RootCAs, timeout: timeout}
}

// WithDB returns a Service like s that reads the SMTP settings that
// first-run setup chose in db, as a job reads them on the workers' pool that
// it carries (package jobs).
func (s *Service) WithDB(db DB) *Service {
	c := *s
	c.db = db
	return &c
}

// Fixed reports whether the configuration sets the SMTP server, which then
// overrides the one that first-run setup chose.
func (s *Service) Fixed() bool {
	return s.override != nil
}

// Settings returns the SMTP server that the instance sends its email
// through, and false if it has none: the configuration's, or else the one
// that first-run setup chose. It reads the latter from the database each
// time, so that a choice that setup makes while the server runs applies at
// once.
func (s *Service) Settings(ctx context.Context) (config.SMTP, bool, error) {
	if s.override != nil {
		return *s.override, true, nil
	}
	var row queries.InstanceSMTPRow
	err := s.db.InTx(ctx, database.Scope{}, func(tx pgx.Tx) error {
		var err error
		row, err = queries.New(tx).InstanceSMTP(ctx)
		return err
	})
	if err != nil {
		return config.SMTP{}, false, fmt.Errorf("email: read the SMTP settings: %w", err)
	}
	if !row.SmtpHost.Valid {
		return config.SMTP{}, false, nil
	}
	return config.SMTP{
		Host:     row.SmtpHost.String,
		Port:     int(row.SmtpPort.Int32),
		Security: config.SMTPSecurity(row.SmtpSecurity.String),
		Username: row.SmtpUsername.String,
		Password: secret.New(row.SmtpPassword.String),
		From:     row.SmtpFrom.String,
	}, true, nil
}

// Configured reports whether the instance has an SMTP server, and so sends
// email.
func (s *Service) Configured(ctx context.Context) (bool, error) {
	_, ok, err := s.Settings(ctx)
	return ok, err
}

// Send sends msg through the instance's SMTP server, and returns once the
// server has accepted it, or ErrNotConfigured if the instance has none.
func (s *Service) Send(ctx context.Context, msg Message) error {
	settings, ok, err := s.Settings(ctx)
	switch {
	case err != nil:
		return err
	case !ok:
		return ErrNotConfigured
	}
	return s.Deliver(ctx, settings, msg)
}
