package account

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/text/language"

	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/i18n"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/input"
)

// The account's own settings: what its user sees of it, and the locale that
// the user prefers (PRD FR-I18N-02), which the web app shows its pages in,
// before the browser's languages and the instance's default, and which the
// account's email is written in.

// Profile is what an account's own user sees of it.
type Profile struct {
	ID          ids.UUID
	Email       string
	DisplayName string
	// Locale is the locale that the user prefers, one that the server has a
	// catalog for, or language.Und if the user has none, when each request's
	// own languages choose.
	Locale        language.Tag
	EmailVerified bool
}

// Profile returns the profile of the active account userID, or ErrNoSession
// if the account is no longer active, as when it was suspended since the
// session of the request was read.
func (s *Service) Profile(ctx context.Context, userID ids.UUID) (Profile, error) {
	var row queries.AccountProfileRow
	err := s.db.InTx(ctx, database.Scope{UserID: userID}, func(tx pgx.Tx) error {
		var err error
		row, err = queries.New(tx).AccountProfile(ctx, nullUUID(userID))
		return err
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return Profile{}, ErrNoSession
	case err != nil:
		return Profile{}, fmt.Errorf("account: read the account: %w", err)
	}
	return newProfile(row), nil
}

// SetLocale sets the locale that the user of the active account userID
// prefers to the locale with a catalog that the BCP 47 tag locale asks for,
// as a workspace's default locale is set (i18n.Bundle.MatchString), or, if
// locale is "", to none, and returns the account's profile. It returns
// input.FieldErrors, and changes nothing, if locale is neither, and
// ErrNoSession if the account is no longer active.
func (s *Service) SetLocale(ctx context.Context, userID ids.UUID, locale string) (Profile, error) {
	var tag pgtype.Text
	if locale != "" {
		m, ok := i18n.Default().MatchString(locale)
		if !ok {
			return Profile{}, input.FieldErrors{{Field: "locale", Message: i18n.Default().UnmatchedProblem()}}
		}
		tag = pgtype.Text{String: m.String(), Valid: true}
	}
	var row queries.SetUserLocaleRow
	err := s.db.InTx(ctx, database.Scope{UserID: userID}, func(tx pgx.Tx) error {
		var err error
		row, err = queries.New(tx).SetUserLocale(ctx, queries.SetUserLocaleParams{Locale: tag, ID: nullUUID(userID)})
		return err
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return Profile{}, ErrNoSession
	case err != nil:
		return Profile{}, fmt.Errorf("account: set the preferred locale: %w", err)
	}
	return newProfile(queries.AccountProfileRow(row)), nil
}

func newProfile(row queries.AccountProfileRow) Profile {
	p := Profile{ID: row.ID.Bytes, Email: row.Email, DisplayName: row.DisplayName, Locale: language.Und,
		EmailVerified: row.EmailVerified}
	if row.Locale.Valid {
		// A stored locale is one that a catalog matched; one that no catalog
		// matches any longer, as of a catalog that a release removed, is none.
		if m, ok := i18n.Default().MatchString(row.Locale.String); ok {
			p.Locale = m
		}
	}
	return p
}
