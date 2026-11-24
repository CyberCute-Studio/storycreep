// Package snapshot publishes the projects' drafts as snapshots, their
// releases, and builds them (PRD FR-SNAP-01, FR-DEP-04, §7.3, plan item 78,
// note N85).
//
// Publishing (Publish) runs the publish gate's pre-check, the validation of
// the draft as it is (package validation), so that its author has an answer
// at once: an error refuses it (*BlockedError), and so does a warning that is
// neither muted nor among the warnings that the request acknowledges
// (*UnacknowledgedError). It then adds a snapshot that is building, of the
// version and the note that the author chose, which must be greater than
// every version of the project's snapshots that are building or ready
// (*NotNewerError), under a lock of the project's snapshots, and enqueues the
// job that builds it (Build), in one transaction, which records the
// publication in the audit log and the snapshot in the workspace's usage.
//
// The build reads the draft as of one moment, in one transaction that only
// reads, whose project sequence it records as the snapshot's source_seq,
// validates it again there, which alone decides, and serializes it into the
// published layout (package serializer), whose files it validates against
// the schemas of the published format (package schemaversion). An error of
// that validation fails the snapshot with validation_failed; a warning that
// the author did not acknowledge, in a draft that changed since the report
// that they acknowledged, with validation_changed; and a file that does not
// validate, or any other failure of the build itself, with build_failed.
// Otherwise the snapshot becomes ready, with its files, in one transaction,
// so that a failed build leaves nothing of them (FR-SNAP-01 AC5). Either way
// the report that the build computed is stored with the snapshot, which
// describes exactly what it read (FR-DEP-04 AC2, AC4), and the snapshot never
// changes again, which the database holds to as well (migration 00035).
//
// The snapshots are listed newest first (List, FR-SNAP-02, plan item 79,
// note N86), and a ready snapshot's files are exported as a zip of the
// published layout (Export, Export.Zip, FR-SYNC-01), at the version of the
// published format that a client asks for, rendered by package
// schemaversion, as the draft's are (ExportDraft), which its manifest marks
// as the draft's (§7.8).
package snapshot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/audit"
	"github.com/CyberCute-Studio/storycreep/server/internal/authz"
	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/i18n"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/input"
	"github.com/CyberCute-Studio/storycreep/server/internal/jobs"
	"github.com/CyberCute-Studio/storycreep/server/internal/project"
	"github.com/CyberCute-Studio/storycreep/server/internal/published"
	"github.com/CyberCute-Studio/storycreep/server/internal/role"
	"github.com/CyberCute-Studio/storycreep/server/internal/usage"
	"github.com/CyberCute-Studio/storycreep/server/internal/validation"
)

// MaxNoteLength is the most characters that a snapshot's note may have, as
// the database's check of snapshot.note takes (migration 00035): change both
// at once.
const MaxNoteLength = 2000

// Status is where a snapshot is in its lifecycle (§7.3).
type Status string

// The statuses.
const (
	// Building is a snapshot whose build has not ended yet.
	Building Status = "building"
	// Ready is a snapshot that its build made, with its files, which never
	// change.
	Ready Status = "ready"
	// Failed is a snapshot whose build failed, which holds no files, and is
	// kept as the record of the attempt, with its report.
	Failed Status = "failed"
)

// FailureCode is why a snapshot failed.
type FailureCode string

// The failure codes.
const (
	// FailureValidation is a build whose validation found an error.
	FailureValidation FailureCode = "validation_failed"
	// FailureChanged is a build whose validation found a warning that the
	// author did not acknowledge, of a draft that changed since the report
	// that they acknowledged, which they publish again to acknowledge.
	FailureChanged FailureCode = "validation_changed"
	// FailureBuild is a build that failed itself: a file that it wrote did
	// not validate against the schemas, or it failed otherwise, as on every
	// attempt that its job had.
	FailureBuild FailureCode = "build_failed"
)

// Request is a publication of a project's draft as a snapshot (§7.3).
type Request struct {
	// Version is the snapshot's version, which the author chose.
	Version string
	// Note is the snapshot's changelog note, of 1 to MaxNoteLength
	// characters, not all of them blank.
	Note string
	// AcknowledgedSeq is the project's operation sequence of the report that
	// the author validated, and AcknowledgedWarnings the warnings of it that
	// they acknowledged, of which the snapshot keeps those of the draft that
	// the pre-check validates (validation.Stored.Acknowledged).
	AcknowledgedSeq      int64
	AcknowledgedWarnings []validation.Acknowledgement
}

// Snapshot is a snapshot of a project, as the API shows it.
type Snapshot struct {
	ID          string      `json:"id" doc:"The snapshot's ID"`
	ProjectID   string      `json:"project_id" doc:"The ID of the project that it is a snapshot of"`
	Version     string      `json:"version" doc:"Its version, MAJOR.MINOR.PATCH, which its author chose (PRD FR-SNAP-01): unique among the project's snapshots that are building or ready, and greater than every version of them before it"`
	Note        string      `json:"note" doc:"Its changelog note, which its manifest holds too"`
	Status      Status      `json:"status" enum:"building,ready,failed" doc:"Where it is in its lifecycle (PRD §7.3): building until its build ends, then ready, with its files, which never change, or failed, without files, kept as the record of the attempt, whose version is free again"`
	FailureCode FailureCode `json:"failure_code,omitempty" enum:"validation_failed,validation_changed,build_failed" doc:"Why it failed, which a failed snapshot has, and no other: validation_failed, an error of its build's validation; validation_changed, a warning that its author did not acknowledge, of a draft that changed since the report that they acknowledged; or build_failed, a failure of the build itself"`
	PublishedBy string      `json:"published_by,omitempty" doc:"The ID of the user who published it, whom it no longer names once their account is deleted"`
	// PublishedByName is the publisher's display name as it is now, which
	// no published file holds (§6.2).
	PublishedByName string     `json:"published_by_name,omitempty" doc:"The display name of the user who published it, as it is now, which none of its files holds (PRD §6.2): a snapshot whose publisher's account is deleted has none, and a client shows it as a former member's"`
	PublishedAt     time.Time  `json:"published_at" doc:"When it was published, which its manifest names to the second"`
	BuiltAt         *time.Time `json:"built_at,omitempty" doc:"When its build ended, which a snapshot that is ready or failed has"`
	// AcknowledgedSeq and SourceSeq are the sequences of the drafts that
	// its author validated and that its build read.
	AcknowledgedSeq int64         `json:"acknowledged_seq" doc:"The project's operation sequence of the validation report that its author acknowledged when they published it"`
	SourceSeq       *int64        `json:"source_seq,omitempty" doc:"The project's operation sequence as of the draft that its build read, validated, and serialized, of which its stored report and its files are (PRD FR-DEP-04 AC4), which a ready snapshot has, and a failed one whose build read the draft"`
	SchemaVersion   *int32        `json:"schema_version,omitempty" doc:"The version of the published format that its files are of, which a ready snapshot has"`
	ContentHash     string        `json:"content_hash,omitempty" doc:"The hash of its content, which its manifest holds (PRD §7.3), the same for the same content, which a ready snapshot has"`
	Counts          *EntityCounts `json:"counts,omitempty" doc:"How many of each entity it holds, as its manifest counts them, which a ready snapshot has"`
}

// EntityCounts are how many of each entity a snapshot holds, as its manifest
// counts them: the fields of published.Counts, which the build converts to
// EntityCounts, so that a count that the manifest gains does not compile
// until EntityCounts, and so the API, has it too.
type EntityCounts struct {
	Boards      published.Int `json:"boards"`
	Components  published.Int `json:"components"`
	Connections published.Int `json:"connections"`
	Nodes       published.Int `json:"nodes" doc:"The nodes of every type: elements, branches, and jumpers"`
	Variables   published.Int `json:"variables" doc:"The variables, global and of each board"`
}

// Proposal is the version that the server proposes for a project's next
// snapshot (§7.3).
type Proposal struct {
	Version       string `json:"version" doc:"The proposed version: a patch bump of the newest version of the project's snapshots that are building or ready, or 0.1.0 for a project that has none. Its author may publish another that is greater than the newest."`
	LatestVersion string `json:"latest_version,omitempty" doc:"The newest version of the project's snapshots that are building or ready, if it has any, which a new snapshot's version must be greater than"`
}

// ErrNotFound is the error for a snapshot that the project does not have.
var ErrNotFound = errors.New("snapshot: no such snapshot of the project")

// BlockedError is Publish's error for a draft that has validation errors,
// which block publishing (FR-DEP-04 AC1).
type BlockedError struct {
	// Report holds the errors that the pre-check found, of the draft as of
	// its Seq.
	Report validation.Stored
}

func (e *BlockedError) Error() string {
	return fmt.Sprintf("snapshot: the draft has %d validation errors, which block publishing", len(e.Report.Findings))
}

// UnacknowledgedError is Publish's error for a draft that has warnings that
// are neither muted nor acknowledged by the request (FR-DEP-04).
type UnacknowledgedError struct {
	// Report holds those warnings, of the draft as of its Seq.
	Report validation.Stored
}

func (e *UnacknowledgedError) Error() string {
	return fmt.Sprintf("snapshot: the draft has %d validation warnings that the publication does not acknowledge",
		len(e.Report.Findings))
}

// NotNewerError is Publish's error for a version that is not greater than
// the newest of the project's snapshots that are building or ready
// (FR-SNAP-01 AC4).
type NotNewerError struct {
	// Latest is the newest version, and Proposed its patch bump, or "" if it
	// is the greatest version.
	Latest, Proposed string
}

func (e *NotNewerError) Error() string {
	return fmt.Sprintf("snapshot: the version is not greater than %s, the project's newest", e.Latest)
}

// DB runs a transaction of the application login, as *database.DB does, and
// one that only reads, as of one moment (InSnapshot), which the pre-check
// and the build read the draft in.
type DB interface {
	InTx(ctx context.Context, scope database.Scope, fn func(tx pgx.Tx) error) error
	InSnapshot(ctx context.Context, scope database.Scope, fn func(tx pgx.Tx) error) error
}

// Queue enqueues jobs in a transaction, as *jobs.Queue does.
type Queue interface {
	Enqueue(ctx context.Context, tx pgx.Tx, args jobs.Args, opts *jobs.EnqueueOptions) (jobs.Enqueued, error)
}

// Options configure a Service.
type Options struct {
	// Projects reads the projects, and the user's role on them. It is
	// required.
	Projects *project.Service
	// Queue enqueues the jobs that build the snapshots. It is required.
	Queue Queue
	// Log is where a build that fails itself, as on a file that does not
	// validate, logs why, or nil for nowhere.
	Log *slog.Logger
}

// Service publishes and builds the projects' snapshots, and reads them.
type Service struct {
	db       DB
	projects *project.Service
	queue    Queue
	log      *slog.Logger
	// hooks are the tests' (export_test.go).
	hooks hooks
}

// New returns a Service that keeps the snapshots in db, with opts. It panics
// if an option that it requires is nil.
func New(db DB, opts Options) *Service {
	switch {
	case opts.Projects == nil:
		panic("snapshot: a Service needs the projects that its snapshots are of")
	case opts.Queue == nil:
		panic("snapshot: a Service needs the queue of its builds' jobs")
	}
	log := opts.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Service{db: db, projects: opts.Projects, queue: opts.Queue, log: log}
}

// Publish publishes the draft of the project projectID as a snapshot of the
// version and the note of r, which the actor asks for, and returns the
// snapshot, building, whose job builds it (§7.3). It returns the errors of
// project.Service.Get; input.FieldErrors for a version that is not of the
// grammar VersionPattern, a note that is empty, blank, longer than
// MaxNoteLength, with white space around it, or a control character but a
// line feed or a tab, or an acknowledged_seq past the project's operation
// sequence;
// authz.ErrInsufficientRole unless the actor's role allows publishing, as
// the project's publish_min_role says (FR-SNAP-01 AC3); project.ErrNotFound
// for a project in the trash; commands.ErrProjectArchived for an archived
// one, which is read-only; a *BlockedError for a draft with validation
// errors, and an *UnacknowledgedError for one with warnings that are neither
// muted nor in r's acknowledged warnings, as the pre-check finds the draft
// as it is (FR-DEP-04); and a *NotNewerError for a version that is not
// greater than the newest of the project's snapshots that are building or
// ready (FR-SNAP-01 AC4). The token's scopes are the route's to check.
//
// The pre-check reads the draft in a transaction that only reads, as of one
// moment, and the snapshot is added in another, which checks the actor's
// role again, under the lock of the project's snapshots, so that the
// versions of two publications at once are compared with one another; a
// change of the draft that commits in between is the build's to validate.
func (s *Service) Publish(ctx context.Context, actor commands.Actor, projectID ids.UUID, r Request) (Snapshot,
	error) {
	r.Note = input.LineFeeds(r.Note)
	v, err := checkRequest(r)
	if err != nil {
		return Snapshot{}, err
	}
	workspaceID, err := s.projects.ActorWorkspace(ctx, actor, projectID)
	if err != nil {
		return Snapshot{}, err
	}
	scope := database.Scope{WorkspaceID: workspaceID, UserID: actor.UserID}
	var acks []validation.Acknowledgement
	err = s.db.InSnapshot(ctx, scope, func(tx pgx.Tx) error {
		if err := s.authorise(ctx, tx, actor, workspaceID, projectID); err != nil {
			return err
		}
		res, err := validation.Run(ctx, tx, workspaceID, projectID)
		if err != nil {
			return err
		}
		report := res.Stored()
		if errs := report.Errors(); len(errs) > 0 {
			return &BlockedError{Report: validation.Stored{Seq: report.Seq, Findings: errs}}
		}
		if r.AcknowledgedSeq > report.Seq {
			return input.FieldErrors{{Field: "acknowledged_seq",
				Message: fmt.Sprintf("is past the project's operation sequence, %d", report.Seq)}}
		}
		if warnings := report.Unacknowledged(r.AcknowledgedWarnings); len(warnings) > 0 {
			return &UnacknowledgedError{Report: validation.Stored{Seq: report.Seq, Findings: warnings}}
		}
		acks = report.Acknowledged(r.AcknowledgedWarnings)
		return nil
	})
	if err != nil {
		return Snapshot{}, err
	}
	acknowledged, err := json.Marshal(acks)
	if err != nil {
		return Snapshot{}, fmt.Errorf("snapshot: encode the acknowledged warnings: %w", err)
	}
	var out Snapshot
	err = s.db.InTx(ctx, scope, func(tx pgx.Tx) error {
		if err := s.authorise(ctx, tx, actor, workspaceID, projectID); err != nil {
			return err
		}
		q := queries.New(tx)
		w, p := nullUUID(workspaceID), nullUUID(projectID)
		if err := q.LockSnapshots(ctx, queries.LockSnapshotsParams{Class: database.SnapshotLock,
			Object: database.LockObject(workspaceID, projectID[:])}); err != nil {
			return fmt.Errorf("snapshot: lock the project's snapshots: %w", err)
		}
		latest, ok, err := newest(ctx, q, w, p)
		if err != nil {
			return err
		}
		if ok && v.Compare(latest) <= 0 {
			e := &NotNewerError{Latest: latest.String()}
			if next, ok := latest.Next(); ok {
				e.Proposed = next.String()
			}
			return e
		}
		// The new snapshot's ID sorts after every ID of the project's
		// snapshots, as their list orders them newest first, by their IDs.
		// Nothing in the database holds to that order, so code that adds a
		// snapshot otherwise than Publish, as an import would, makes its ID
		// so too, under the same lock (CONTRIBUTING.md).
		last, err := q.NewestSnapshotID(ctx, queries.NewestSnapshotIDParams{WorkspaceID: w, ProjectID: p})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("snapshot: read the project's newest snapshot: %w", err)
		}
		id := ids.NewAfter(last.Bytes)
		if err := q.InsertSnapshot(ctx, queries.InsertSnapshotParams{ID: nullUUID(id), WorkspaceID: w,
			ProjectID: p, Version: v.String(), VersionMajor: v.Major, VersionMinor: v.Minor, VersionPatch: v.Patch,
			Note: r.Note, AcknowledgedSeq: r.AcknowledgedSeq, AcknowledgedWarnings: acknowledged,
			PublishedBy: nullUUID(actor.UserID)}); err != nil {
			return fmt.Errorf("snapshot: add the snapshot: %w", err)
		}
		row, err := get(ctx, tx, workspaceID, projectID, id)
		if err != nil {
			return err
		}
		if err := usage.Record(ctx, tx, workspaceID, usage.Snapshots, 1); err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Entry{Action: audit.ActionSnapshotPublish, WorkspaceID: workspaceID,
			ActorUserID: actor.UserID, ActorTokenID: actor.Token.TokenID(), SubjectID: id, Result: audit.ResultSuccess,
			IP: actor.Client.IP, UserAgent: actor.Client.UserAgent, Metadata: map[string]any{
				"project_id": ids.Encode(ids.Project, projectID), "version": v.String()}}); err != nil {
			return err
		}
		if _, err := s.queue.Enqueue(ctx, tx, buildJob{WorkspaceID: workspaceID.String(), SnapshotID: id.String()},
			&jobs.EnqueueOptions{MaxAttempts: buildAttempts}); err != nil {
			return fmt.Errorf("snapshot: enqueue the build: %w", err)
		}
		out = snapshotOf(row)
		return nil
	})
	return out, err
}

// checkRequest returns r's version, or the input.FieldErrors of what is
// wrong with r.
func checkRequest(r Request) (Version, error) {
	var fields input.FieldErrors
	v, err := ParseVersion(r.Version)
	if err != nil {
		fields = append(fields, input.FieldError{Field: "version", Message: fmt.Sprintf(
			"is not a version MAJOR.MINOR.PATCH, each a number from 0 to %d without a leading zero", MaxVersionNumber)})
	}
	if strings.IndexFunc(r.Note, input.Printed) < 0 {
		// A note of blank characters alone, as U+200B ZERO WIDTH SPACE, is
		// empty, as a mute's reason is (package validation).
		fields = append(fields, input.FieldError{Field: "note", Message: "is empty"})
	} else if msg := input.CheckTrimmedText(r.Note, MaxNoteLength); msg != "" {
		fields = append(fields, input.FieldError{Field: "note", Message: msg})
	}
	if r.AcknowledgedSeq < 0 {
		fields = append(fields, input.FieldError{Field: "acknowledged_seq", Message: "is negative"})
	}
	if len(fields) > 0 {
		return Version{}, fields
	}
	return v, nil
}

// CheckImportedNote says what is wrong with the note of a release of an
// imported archive, or returns "": a note of the length that the database
// holds. A stored note may break the text rules of a new one (checkRequest),
// and is kept as it is.
func CheckImportedNote(note string) string {
	return input.CheckStoredLength(note, 1, MaxNoteLength)
}

// authorise returns nil if the actor may publish a snapshot of the project
// projectID, read in tx, a transaction of its workspace workspaceID, and the
// error that Publish refuses it with otherwise: project.Service.GetIn's,
// which is project.ErrNotFound for a project in the trash, too.
func (s *Service) authorise(ctx context.Context, tx pgx.Tx, actor commands.Actor, workspaceID,
	projectID ids.UUID) error {
	p, err := s.projects.GetIn(ctx, tx, actor, workspaceID, projectID)
	if err != nil {
		return err
	}
	if err := authz.CheckPublish(p.Role, role.Role(p.PublishMinRole)); err != nil {
		return err
	}
	if p.ArchivedAt != nil {
		return commands.ErrProjectArchived
	}
	return nil
}

// newest returns the greatest version of the project's snapshots that are
// building or ready, read with q, and false if it has none.
func newest(ctx context.Context, q *queries.Queries, w, p pgtype.UUID) (Version, bool, error) {
	s, err := q.NewestSnapshotVersion(ctx, queries.NewestSnapshotVersionParams{WorkspaceID: w, ProjectID: p})
	if errors.Is(err, pgx.ErrNoRows) {
		return Version{}, false, nil
	}
	if err != nil {
		return Version{}, false, fmt.Errorf("snapshot: read the project's newest version: %w", err)
	}
	v, err := ParseVersion(s)
	if err != nil {
		return Version{}, false, err
	}
	return v, true, nil
}

// Get returns the snapshot snapshotID of the project projectID, once the
// actor may read the project (project.Service.ReadContent). It returns the
// errors of project.Service.Get, and ErrNotFound if the project has no such
// snapshot. The token's scopes are the route's to check.
func (s *Service) Get(ctx context.Context, actor commands.Actor, projectID, snapshotID ids.UUID) (Snapshot, error) {
	var out Snapshot
	err := s.projects.ReadContent(ctx, s.db, actor, projectID, func(tx pgx.Tx, ws ids.UUID) error {
		row, err := get(ctx, tx, ws, projectID, snapshotID)
		if err != nil {
			return err
		}
		out = snapshotOf(row)
		return nil
	})
	return out, err
}

// Report returns the validation report that the build of the snapshot
// snapshotID of the project projectID stored (FR-DEP-04 AC2), of the draft
// as of its source_seq, with its messages in the locale of ctx's localizer
// (i18n.FromContext), and the languages that they are in, as
// validation.Service.Report does, once the actor may read the project. It
// returns the errors of Get, and ErrNotFound for a snapshot that holds no
// report: one that is building, or that failed before its build read the
// draft. A mute of the report names who muted it only while their account
// exists, as the draft's report does (validation.Mute), although the
// snapshot, which never changes, keeps their ID (forgetDeleted).
func (s *Service) Report(ctx context.Context, actor commands.Actor, projectID, snapshotID ids.UUID) (
	validation.Report, string, error) {
	var stored validation.Stored
	err := s.projects.ReadContent(ctx, s.db, actor, projectID, func(tx pgx.Tx, ws ids.UUID) error {
		q := queries.New(tx)
		data, err := q.SnapshotReport(ctx, queries.SnapshotReportParams{WorkspaceID: nullUUID(ws),
			ProjectID: nullUUID(projectID), ID: nullUUID(snapshotID)})
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return ErrNotFound
		case err != nil:
			return fmt.Errorf("snapshot: read the stored report: %w", err)
		case data == nil:
			return ErrNotFound
		}
		if err := json.Unmarshal(data, &stored); err != nil {
			return fmt.Errorf("snapshot: read the stored report: %w", err)
		}
		return forgetDeleted(ctx, q, stored)
	})
	if err != nil {
		return validation.Report{}, "", err
	}
	report, languages := stored.Report(i18n.FromContext(ctx))
	return report, languages, nil
}

// forgetDeleted takes from each mute of the stored report s who muted it, if
// their account is deleted, read with q, as the draft's report no longer
// names them either, since the deletion's cascade takes the mute's author
// from it.
func forgetDeleted(ctx context.Context, q *queries.Queries, s validation.Stored) error {
	var users []pgtype.UUID
	for _, f := range s.Findings {
		if f.Mute == nil || f.Mute.MutedBy == "" {
			continue
		}
		if id, err := ids.Decode(ids.User, f.Mute.MutedBy); err == nil {
			users = append(users, nullUUID(id))
		}
	}
	if len(users) == 0 {
		return nil
	}
	rows, err := q.UsersWithAccounts(ctx, users)
	if err != nil {
		return fmt.Errorf("snapshot: read who muted the stored report's findings: %w", err)
	}
	exist := make(map[string]bool, len(rows))
	for _, id := range rows {
		exist[ids.Encode(ids.User, id.Bytes)] = true
	}
	for _, f := range s.Findings {
		if f.Mute != nil && !exist[f.Mute.MutedBy] {
			f.Mute.MutedBy = ""
		}
	}
	return nil
}

// NextVersion returns the version that the server proposes for the next
// snapshot of the project projectID, a patch bump of its newest of those
// that are building or ready, or FirstVersion if it has none (§7.3), once
// the actor may read the project. It returns the errors of
// project.Service.Get. The proposal of a project whose newest version is the
// greatest is empty.
func (s *Service) NextVersion(ctx context.Context, actor commands.Actor, projectID ids.UUID) (Proposal, error) {
	var out Proposal
	err := s.projects.ReadContent(ctx, s.db, actor, projectID, func(tx pgx.Tx, ws ids.UUID) error {
		latest, ok, err := newest(ctx, queries.New(tx), nullUUID(ws), nullUUID(projectID))
		if err != nil {
			return err
		}
		if !ok {
			out = Proposal{Version: FirstVersion.String()}
			return nil
		}
		out.LatestVersion = latest.String()
		if next, ok := latest.Next(); ok {
			out.Version = next.String()
		}
		return nil
	})
	return out, err
}

// get returns the snapshot snapshotID of the project projectID of the
// workspace ws, read in tx, or ErrNotFound.
func get(ctx context.Context, tx pgx.Tx, ws, projectID, snapshotID ids.UUID) (queries.SnapshotRow, error) {
	row, err := queries.New(tx).Snapshot(ctx, queries.SnapshotParams{WorkspaceID: nullUUID(ws),
		ProjectID: nullUUID(projectID), ID: nullUUID(snapshotID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return queries.SnapshotRow{}, ErrNotFound
	}
	if err != nil {
		return queries.SnapshotRow{}, fmt.Errorf("snapshot: read the snapshot: %w", err)
	}
	return row, nil
}

// snapshotOf returns the snapshot of row as the API shows it.
func snapshotOf(row queries.SnapshotRow) Snapshot {
	out := Snapshot{ID: ids.Encode(ids.Snapshot, row.ID.Bytes), ProjectID: ids.Encode(ids.Project, row.ProjectID.Bytes),
		Version: row.Version, Note: row.Note, Status: Status(row.Status), FailureCode: FailureCode(row.FailureCode.String),
		PublishedAt: row.PublishedAt.Time.UTC(), AcknowledgedSeq: row.AcknowledgedSeq, ContentHash: row.ContentHash.String}
	if row.PublishedBy.Valid {
		out.PublishedBy, out.PublishedByName = ids.Encode(ids.User, row.PublishedBy.Bytes), row.PublishedByName.String
	}
	if row.BuiltAt.Valid {
		t := row.BuiltAt.Time.UTC()
		out.BuiltAt = &t
	}
	if row.SourceSeq.Valid {
		out.SourceSeq = &row.SourceSeq.Int64
	}
	if row.SchemaVersion.Valid {
		out.SchemaVersion = &row.SchemaVersion.Int32
	}
	if row.Counts != nil {
		var c EntityCounts
		if json.Unmarshal(row.Counts, &c) == nil {
			out.Counts = &c
		}
	}
	return out
}

// nullUUID returns id as a uuid column, null if id is zero.
func nullUUID(id ids.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: id != (ids.UUID{})}
}
