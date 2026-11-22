package authoring

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
)

// refusingDB is a database that takes no transaction.
type refusingDB struct{ DB }

func (refusingDB) InTx(context.Context, database.Scope, func(pgx.Tx) error) error {
	return errors.New("the database is down")
}

// TestFailWaitsForTheDatabase checks that a build whose export could not be
// failed runs again rather than end, which would leave the export on its way
// and keep its user from asking for another until it expires.
func TestFailWaitsForTheDatabase(t *testing.T) {
	t.Parallel()
	s := New(refusingDB{}, Options{})
	err := s.fail(t.Context(), ids.New(), ids.New(), errors.New("every attempt failed"))
	if !errors.As(err, new(*river.JobSnoozeError)) {
		t.Errorf("fail returned %v, want a snooze of the job", err)
	}
}
