package component

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/ident"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/input"
	"github.com/CyberCute-Studio/storycreep/server/internal/slug"
)

// suffixDigits is the most digits of the number that a derived key is
// given to make it unique: more than the entities that a project can hold.
const suffixDigits = 9

// DeriveKey returns the key that an entity named name is given when a command
// gives none (FR-COMP-02): the name's letters and digits in lowercase ASCII,
// without their accents, each run of other characters an underscore, as
// tomas_novak for Tomáš Novák (slug.Base), with an underscore before it if it
// starts with a digit, as _1st_mate for 1st Mate, cut to ident.MaxLength
// characters, or fallback, an identifier, for a name without one, as one in a
// script other than Latin. It may be a reserved word of the expression
// language, which the caller takes for a key that another entity has.
func DeriveKey(name, fallback string) string {
	k := strings.ReplaceAll(slug.Base(name), "-", "_")
	switch {
	case k == "":
		return fallback
	case k[0] >= '0' && k[0] <= '9':
		k = "_" + k
	}
	return strings.TrimRight(k[:min(len(k), ident.MaxLength)], "_")
}

// keysFrom returns the keys of the entities of a scope that start with stem,
// those that a key derived from stem may collide with.
type keysFrom func(ctx context.Context, stem string) ([]string, error)

// deriveKey returns the key that an entity named name is given in a scope of
// keys (DeriveKey), with fallback for a name without letters or digits:
// that key, unless an entity of the scope that is not in the trash has it,
// or it is a reserved word, and otherwise the first of that with _2, _3, and
// so on, cut to leave room for the number, that is neither. It reads only
// the keys that start as each of those does (from), so that a batch that
// creates many entities of different names reads few keys for each.
func deriveKey(ctx context.Context, from keysFrom, name, fallback string) (string, error) {
	base := DeriveKey(name, fallback)
	stem := strings.TrimRight(base[:min(len(base), ident.MaxLength-1-suffixDigits)], "_")
	keys, err := from(ctx, stem)
	if err != nil {
		return "", err
	}
	used := make(map[string]bool, len(keys))
	for _, k := range keys {
		used[k] = true
	}
	for _, w := range ident.ReservedWords() {
		used[w] = true
	}
	// Fewer entities have the keys read than the numbers tried, so one is
	// free.
	for n := 1; ; n++ {
		k := base
		if n > 1 {
			suffix := "_" + strconv.Itoa(n)
			k = strings.TrimRight(base[:min(len(base), ident.MaxLength-len(suffix))], "_") + suffix
		}
		if !used[k] {
			return k, nil
		}
	}
}

// keyOwner returns the UUID of the entity of the scope that is not in the
// trash whose key is key, or pgx.ErrNoRows if none has it.
type keyOwner func(ctx context.Context, key string) (pgtype.UUID, error)

// checkKey refuses the batch, for the payload's field at, unless key is a key
// that the entity self of the scope, or a new entity if self is null, may
// have: an identifier (package ident), which the API answers with
// key_invalid, as it answers a slug that is not one with slug_invalid, and
// which no other entity of the scope that is not in the trash has (keyFree),
// whose kind what names.
func checkKey(ctx context.Context, owner keyOwner, at, key string, self pgtype.UUID, what string) error {
	var invalid *ident.Error
	if errors.As(ident.Check(key), &invalid) {
		return commands.Refuse(at, invalid.Problem(), invalid)
	}
	return keyFree(ctx, owner, at, key, self, fmt.Sprintf("is the key of another %s, which is not in the "+
		"project's trash", what))
}

// keyFree refuses the batch, for the payload's field at, with the message
// msg and ErrKeyTaken, if an entity of the scope other than self, which is
// not in the trash, has the key key.
func keyFree(ctx context.Context, owner keyOwner, at, key string, self pgtype.UUID, msg string) error {
	id, err := owner(ctx, key)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil
	case err != nil:
		return fmt.Errorf("component: read the entity with the key: %w", err)
	case id != self:
		return commands.Refuse(at, msg, ErrKeyTaken)
	}
	return nil
}

// checkName says what is wrong with the name of a type, an attribute
// definition, or a component, or returns "".
func checkName(name string) string {
	return input.CheckTrimmedName(name, MaxNameLength)
}

// changed returns a function that reports the change of the entity id of the
// kind, to the revision that an update returned, with err, or reports
// nothing if the update found nothing to change, which it returns as
// pgx.ErrNoRows.
func changed(tx *commands.Tx, kind ids.Kind, id pgtype.UUID) func(rev int64, err error) error {
	return func(rev int64, err error) error {
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return nil
		case err != nil:
			return fmt.Errorf("component: change the %s: %w", kind, err)
		}
		return tx.Changed(kind, id.Bytes, rev)
	}
}

// by returns the batch's actor, as the column of who changed a row.
func by(tx *commands.Tx) pgtype.UUID {
	return nullUUID(tx.Actor().UserID)
}

// decoded returns the UUID of id, an ID of the kind that a payload's Check
// checked, as a uuid column.
func decoded(kind ids.Kind, id string) pgtype.UUID {
	// Check refused a payload whose ID does not decode.
	u, _ := ids.Decode(kind, id)
	return nullUUID(u)
}

// newID returns the problem of id, the ID of an entity of the kind that a
// command creates, unless it is an ID of the kind of a UUIDv7 (PRD §5.2).
func newID(kind ids.Kind, id string) input.FieldErrors {
	u, err := ids.Decode(kind, id)
	switch {
	case err != nil:
		return field("id", fmt.Sprintf("is not a %s's ID: %v", kind, err))
	case !u.IsV7():
		return field("id", "is not the ID of a UUIDv7, which the ID of every entity is")
	}
	return nil
}

// entityID returns the problem of the payload's field name, unless its value
// id is an ID of the kind.
func entityID(kind ids.Kind, name, id string) input.FieldErrors {
	if _, err := ids.Decode(kind, id); err != nil {
		return field(name, fmt.Sprintf("is not a %s's ID: %v", kind, err))
	}
	return nil
}

// optionalID returns the problem of the payload's field name as entityID
// does, unless its value id is "", for none.
func optionalID(kind ids.Kind, name, id string) input.FieldErrors {
	if id == "" {
		return nil
	}
	return entityID(kind, name, id)
}

// field returns the problem msg of the payload's field name, or none if msg
// is "".
func field(name, msg string) input.FieldErrors {
	if msg == "" {
		return nil
	}
	return input.FieldErrors{{Field: name, Message: msg}}
}

// refuse refuses the batch for the first of errs, the problems of a payload
// that Apply finds with what the project holds, or returns nil if there are
// none.
func refuse(errs input.FieldErrors) error {
	if len(errs) == 0 {
		return nil
	}
	return commands.Refuse(errs[0].Field, errs[0].Message, nil)
}

// uuids returns entities as uuid columns.
func uuids(entities []ids.UUID) []pgtype.UUID {
	out := make([]pgtype.UUID, len(entities))
	for i, id := range entities {
		out[i] = nullUUID(id)
	}
	return out
}
