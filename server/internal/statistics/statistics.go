// Package statistics keeps the writing statistics of a project's content,
// and answers them (PRD FR-TEXT-06, plan note N76): the words and the
// characters of its texts, an element's text and a connection's label, and
// how many nodes, choices, and conditions it has, of each node, of each
// board, and of the project, of what is not in its trash.
//
// A text's words and characters are those that package richtext counts
// (WordCount, CharacterCount), with the names of what the text mentions and
// shows (Names), as the text export counts them (FR-TEXT-05 AC3), so that the
// statistics and the export count with one implementation, as
// schema/rich-text/README.md asks. Each node and each connection has its row
// of them (migration 00030), whether it is in the trash or not, which the
// derivation that Register registers with the pipeline writes once every
// command of a batch has applied, in the batch's transaction: of each text
// that the batch may have changed, and of each text that names what it
// renamed, a variable's or an attribute's key or a component's key or name,
// as the index of what the texts name finds them (package dependency). So a
// batch counts the texts that it changes, and no other, and the statistics
// that a read gives once it commits hold them (AC1), always what counting
// every text from scratch gives (statisticstest.Drift). The job that
// RegisterJobs registers writes the rows of the texts that have none of the
// current Version, or of a newer one: those of the content that is there
// already when migration 00030 applies, and every row again once a version
// of the server changes how a text is counted; and the trash's purge counts
// again the texts that name what it deletes (Recount), whose names they
// lose. A read adds up the rows of the nodes and the connections that are
// not in the trash, and counts the nodes, the choices, and the conditions of
// the content itself, which needs no text read, and says whether every text
// that it adds up was counted of the current version (Counts.Complete).
package statistics

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/project"
)

// Version is the version of how a text is counted, which each row of the
// statistics records it was written of. A change of what a text's words or
// characters are (count), or of which entities have a row, increments it, so
// that the job of the statistics writes every row again, as it writes the
// rows that are missing (CountProjects). While a rolling
// upgrade replaces a version's replicas with the next one's, a replica of
// the older version writes the rows of what its batches change of its own
// version, and deletes the project's state that the newer one recorded
// (queries.UpsertTextStatistics), so that the newer job writes them again,
// while the older job leaves alone the rows and the states of a newer version
// (queries.ProjectsToCount, queries.UncountedTexts,
// queries.SetTextStatisticState). A read says that the statistics of a text
// of an older version are not complete.
const Version = 1

// ErrNotFound is returned for a node that the project does not have, or that
// is in its trash, itself or with its board.
var ErrNotFound = errors.New("statistics: no such node")

// Counts are the writing statistics of a part of a project's content: a
// node, a board, or the project, of what of it is not in the trash.
type Counts struct {
	Words      int64 `json:"words" minimum:"0" doc:"How many words its texts have: an element's text and the labels of the connections that leave a node. A word is a run of characters between white space with at least one letter or number among them, a mention counting as its component's name and a value reference as its name in braces, as PRD FR-TEXT-05 and FR-TEXT-06 define them."`
	Characters int64 `json:"characters" minimum:"0" doc:"How many characters its texts have, white space among them, as code points: of what their authors wrote, a mention counting as its component's name and a value reference as its name in braces, but not the line breaks between paragraphs, list items, and lines, nor the bullets, numbers, and indentation of list items."`
	Choices    int64 `json:"choices" minimum:"0" doc:"How many choices it has: the connections that leave an element that is not an ending (PRD §5.8.5)."`
	Conditions int64 `json:"conditions" minimum:"0" doc:"How many conditions it has: of the connections that leave its nodes, and of its branches' outputs."`
	Complete   bool  `json:"complete" doc:"Whether every text that it adds up has been counted. A text that predates the statistics is counted by a job that runs as the server starts and then every hour, and until it has, its words and characters are not among these."`
}

// add adds o to c, and takes c to be complete only if both are.
func (c *Counts) add(o Counts) {
	c.Words += o.Words
	c.Characters += o.Characters
	c.Choices += o.Choices
	c.Conditions += o.Conditions
	c.Complete = c.Complete && o.Complete
}

// BoardStatistics is the writing statistics of a board.
type BoardStatistics struct {
	ID    string `json:"id" doc:"The board's ID"`
	Name  string `json:"name" doc:"The board's name"`
	Nodes int64  `json:"nodes" minimum:"0" doc:"How many nodes it has, of every type"`
	Counts
}

// ProjectStatistics is the writing statistics of a project, and of each of
// its boards.
type ProjectStatistics struct {
	Seq   int64 `json:"seq" doc:"The project's operation sequence as of the draft that the statistics were read of, the seq of the last batch that they hold"`
	Nodes int64 `json:"nodes" minimum:"0" doc:"How many nodes it has, of every type"`
	Counts
	Boards []BoardStatistics `json:"boards" nullable:"false" doc:"The project's boards that are not in its trash, each with its statistics, which add up to the project's, in the order of their IDs"`
}

// NodeStatistics is the writing statistics of a node: of its text and of the
// connections that leave it.
type NodeStatistics struct {
	Seq int64  `json:"seq" doc:"The project's operation sequence as of the draft that the statistics were read of, the seq of the last batch that they hold"`
	ID  string `json:"id" doc:"The node's ID"`
	Counts
}

// DB is the database that a Service reads the statistics in.
type DB interface {
	project.SnapshotDB
}

// Options configure a Service.
type Options struct {
	// Projects reads the projects, which authorises each read of their
	// statistics. It is required.
	Projects *project.Service
}

// Service answers the reads of the projects' writing statistics.
type Service struct {
	db       DB
	projects *project.Service
}

// New returns a Service that reads the statistics in db, with opts. It
// panics if opts.Projects is nil.
func New(db DB, opts Options) *Service {
	if opts.Projects == nil {
		panic("statistics: a Service needs the projects' service that authorises its reads")
	}
	return &Service{db: db, projects: opts.Projects}
}

// Project returns the writing statistics of the project projectID and of
// each of its boards that is not in its trash, once the actor may read the
// project's content (project.Service.ReadSnapshot), as of one moment, so
// that a batch that commits meanwhile is in them whole or not at all. The
// project's are its boards' added up. It returns the errors of
// project.Service.Get, as project.ErrNotFound.
func (s *Service) Project(ctx context.Context, actor commands.Actor, projectID ids.UUID) (*ProjectStatistics, error) {
	var out *ProjectStatistics
	err := s.projects.ReadSnapshot(ctx, s.db, actor, projectID, func(tx pgx.Tx, workspaceID ids.UUID) error {
		q, ws, p := queries.New(tx), nullUUID(workspaceID), nullUUID(projectID)
		seq, err := q.Project(ctx, queries.ProjectParams{WorkspaceID: ws, ID: p})
		if err != nil {
			return fmt.Errorf("statistics: read the project's seq: %w", err)
		}
		rows, err := q.BoardStatistics(ctx, queries.BoardStatisticsParams{WorkspaceID: ws, ProjectID: p,
			Version: Version})
		if err != nil {
			return fmt.Errorf("statistics: read the boards' statistics: %w", err)
		}
		out = &ProjectStatistics{Seq: seq.Project.Seq, Counts: Counts{Complete: true}, Boards: make([]BoardStatistics, 0, len(rows))}
		for _, r := range rows {
			b := BoardStatistics{ID: ids.Encode(ids.Board, r.ID.Bytes), Name: r.Name, Nodes: r.Nodes, Counts: Counts{
				Words: r.Words, Characters: r.Characters, Choices: r.Choices, Conditions: r.Conditions,
				Complete: r.Uncounted == 0}}
			out.Nodes += b.Nodes
			out.add(b.Counts)
			out.Boards = append(out.Boards, b)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Node returns the writing statistics of the node nodeID of the project
// projectID, of its text and of the connections that leave it, once the
// actor may read the project's content, as Project does. It returns
// ErrNotFound for a node that the project does not have, or that is in its
// trash, and the errors of project.Service.Get.
func (s *Service) Node(ctx context.Context, actor commands.Actor, projectID, nodeID ids.UUID) (*NodeStatistics, error) {
	var out *NodeStatistics
	err := s.projects.ReadSnapshot(ctx, s.db, actor, projectID, func(tx pgx.Tx, workspaceID ids.UUID) error {
		q, ws, p := queries.New(tx), nullUUID(workspaceID), nullUUID(projectID)
		seq, err := q.Project(ctx, queries.ProjectParams{WorkspaceID: ws, ID: p})
		if err != nil {
			return fmt.Errorf("statistics: read the project's seq: %w", err)
		}
		r, err := q.NodeStatistics(ctx, queries.NodeStatisticsParams{WorkspaceID: ws, ProjectID: p,
			ID: nullUUID(nodeID), Version: Version})
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return ErrNotFound
		case err != nil:
			return fmt.Errorf("statistics: read the node's statistics: %w", err)
		}
		out = &NodeStatistics{Seq: seq.Project.Seq, ID: ids.Encode(ids.Node, r.ID.Bytes), Counts: Counts{Words: r.Words,
			Characters: r.Characters, Choices: r.Choices, Conditions: r.Conditions, Complete: r.Uncounted == 0}}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// nullUUID returns id as a uuid column, null for zero.
func nullUUID(id ids.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: id != (ids.UUID{})}
}

// list returns l, or an empty list for nil, which a query reads as an array
// of no element rather than as null.
func list(l []pgtype.UUID) []pgtype.UUID {
	if l == nil {
		return []pgtype.UUID{}
	}
	return l
}
