package authoring

import (
	"bytes"
	"cmp"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"path"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/project"
	"github.com/CyberCute-Studio/storycreep/server/internal/richtext"
)

// Section is a part of the archive: one JSON file, of the rows of Tables. The
// registry is the one list of what the format holds. A table of authoring
// data that a pull request adds joins a section here, and plan item 84's
// import reads the same list, so that it is registered in both directions.
type Section struct {
	// Name is the section's name in the manifest, by which an importer that
	// cannot import it names it (FR-DATA-01 AC12).
	Name string
	// Path is the path of its file.
	Path string
	// Tables are the tables whose rows it holds.
	Tables []string
	// DraftOnly says that only an archive of the draft holds it, as a
	// release's files hold the rest of what the section would.
	DraftOnly bool
	// FullOnly says that only a full archive holds it (FR-DATA-01 AC5).
	FullOnly bool
	// ReleasesOnly and AssetsOnly say that only an archive that includes
	// the release history, or the assets, holds it.
	ReleasesOnly, AssetsOnly bool
	// root is the type of its file.
	root reflect.Type
	read func(context.Context, *reading) (any, int, error)
	// store decodes the section's file of an archive into what an import
	// writes (plan item 84), or refuses it. A section without one is a
	// section that this server declares and cannot import, whose presence in
	// an archive refuses it, naming the section (FR-DATA-01 AC12).
	store func(*archived, []byte) error
}

// holds says whether an archive of the export row holds the section.
func (s Section) holds(row queries.AuthoringExport) bool {
	return !(s.DraftOnly && row.SnapshotID.Valid) && !(s.FullOnly && Profile(row.Profile) != ProfileFull) &&
		!(s.ReleasesOnly && !row.IncludeReleases) && !(s.AssetsOnly && !row.IncludeAssets)
}

// section returns a Section of a file of items of T, read by read.
func section[T any](s Section, read func(context.Context, *reading) ([]T, error),
	store func(*archived, []T) error) Section {
	s.root = reflect.TypeFor[Items[T]]()
	s.store = func(c *archived, data []byte) error {
		var f Items[T]
		if err := json.Unmarshal(data, &f); err != nil {
			return &ImportError{Code: FailSchemaInvalid, Section: s.Name, Check: "json", Detail: err.Error()}
		}
		return store(c, f.Items)
	}
	s.read = func(ctx context.Context, r *reading) (any, int, error) {
		items, err := read(ctx, r)
		if err != nil {
			return nil, 0, fmt.Errorf("authoring: read the section %s: %w", s.Name, err)
		}
		if items == nil {
			items = []T{}
		}
		return Items[T]{Items: items}, len(items), nil
	}
	return s
}

// sections is the registry, in the order in which they are read: people is
// last, as it names whom the others name.
var sections = []Section{
	{Name: "project", Path: "draft/project.json", Tables: []string{"project"}, DraftOnly: true,
		root: reflect.TypeFor[Project](), read: readProject, store: storeProject},
	section(Section{Name: "folders", Path: "draft/folders.json", Tables: []string{"folder"}, DraftOnly: true},
		readFolders, func(c *archived, v []Folder) error { c.folders = v; return nil }),
	section(Section{Name: "boards", Path: "draft/boards.json", Tables: []string{"board"}, DraftOnly: true},
		readBoards, func(c *archived, v []Board) error { c.boards = v; return nil }),
	section(Section{Name: "variables", Path: "draft/variables.json", Tables: []string{"variable"}, DraftOnly: true},
		readVariables, func(c *archived, v []Variable) error { c.variables = v; return nil }),
	section(Section{Name: "component_types", Path: "draft/component_types.json",
		Tables: []string{"component_type", "attribute_def"}, DraftOnly: true}, readComponentTypes,
		func(c *archived, v []ComponentType) error { c.types = v; return nil }),
	section(Section{Name: "components", Path: "draft/components.json",
		Tables: []string{"component", "attribute_value"}, DraftOnly: true}, readComponents,
		func(c *archived, v []Component) error { c.components = v; return nil }),
	section(Section{Name: "nodes", Path: "draft/nodes.json", Tables: []string{"node"}, DraftOnly: true}, readNodes,
		func(c *archived, v []Node) error { c.nodes = v; return nil }),
	section(Section{Name: "branch_outputs", Path: "draft/branch_outputs.json", Tables: []string{"branch_output"},
		DraftOnly: true}, readOutputs, func(c *archived, v []BranchOutput) error { c.outputs = v; return nil }),
	section(Section{Name: "connections", Path: "draft/connections.json", Tables: []string{"connection"},
		DraftOnly: true}, readConnections, func(c *archived, v []Connection) error { c.connections = v; return nil }),
	section(Section{Name: "expressions", Path: "draft/expressions.json", Tables: []string{"expression"},
		DraftOnly: true}, readExpressions, func(c *archived, v []Expression) error { c.expressions = v; return nil }),
	section(Section{Name: "validation_mutes", Path: "draft/validation_mutes.json", Tables: []string{"validation_mute"},
		DraftOnly: true}, readMutes, func(c *archived, v []ValidationMute) error { c.mutes = v; return nil }),
	section(Section{Name: "comments", Path: "comments.json", Tables: []string{"comment"}, FullOnly: true}, readComments,
		func(c *archived, v []Comment) error { c.comments = v; return nil }),
	section(Section{Name: "play_states", Path: "play_states.json", Tables: []string{"play_state"}, FullOnly: true},
		readPlayStates, func(c *archived, v []PlayState) error { c.playStates = v; return nil }),
	section(Section{Name: "annotations", Path: "annotations.json", FullOnly: true}, readAnnotations, storeAnnotations),
	section(Section{Name: "releases", Path: "releases/index.json", Tables: []string{"snapshot", "snapshot_file"},
		ReleasesOnly: true}, readReleases, func(c *archived, v []Release) error { c.releases = v; return nil }),
	section(Section{Name: "assets", Path: "assets/index.json", Tables: []string{"asset"}, AssetsOnly: true}, readAssets,
		func(c *archived, v []Asset) error { c.assets = v; return nil }),
	section(Section{Name: "people", Path: "people.json", FullOnly: true}, readPeople,
		func(c *archived, v []Person) error { c.people = v; return nil }),
}

// storeProject decodes the project's section of an archive.
func storeProject(c *archived, data []byte) error {
	var p Project
	if err := json.Unmarshal(data, &p); err != nil {
		return &ImportError{Code: FailSchemaInvalid, Section: "project", Check: "json", Detail: err.Error()}
	}
	c.project = &p
	return nil
}

// storeAnnotations refuses an archive that holds canvas annotations, which
// this server cannot import until FR-EDIT-12 (plan item 127), and never drops
// silently (FR-DATA-01 AC12).
func storeAnnotations(c *archived, v []Annotation) error {
	if len(v) > 0 {
		return &ImportError{Code: FailSectionUnsupported, Section: "annotations",
			Detail: "this server cannot import canvas annotations"}
	}
	c.annotations = v
	return nil
}

// Sections returns the registry.
func Sections() []Section { return slices.Clone(sections) }

// NotExported lists the tables with a project's rows that the format does not
// hold, each with why: what the server derives again from what it does hold,
// and what belongs to this instance or to its accounts rather than to the
// project's authoring data. A new table joins a section or this list, which
// TestEveryProjectTableIsAccounted holds them to.
var NotExported = map[string]string{
	"api_token":            "credentials of this instance's accounts",
	"applied_command":      "the operation log's record of applied commands, which is this instance's history",
	"authoring_export":     "the exports themselves",
	"expression_name":      "an index that compiling the expressions writes again",
	"expression_ref":       "an index that compiling the expressions writes again",
	"idempotency_record":   "the answers to recent batches, which are pruned within a day",
	"notification":         "an account's notifications",
	"operation":            "the operation log, whose undo history is this instance's",
	"project_grant":        "who has access on this instance",
	"schema_migration_log": "the log of this instance's own migrations of stored content",
	"search_entry":         "an index that the search job writes again",
	"search_state":         "the search job's progress",
	"text_statistic":       "counts that the statistics job writes again",
	"text_statistic_state": "the statistics job's progress",
}

// reading is what the sections read: the project of a transaction that reads
// as of one moment, and what they find as they read.
type reading struct {
	q       *queries.Queries
	ws      pgtype.UUID
	project pgtype.UUID
	profile Profile
	// release holds what the release of an archive of one has, or is nil
	// for an archive of the draft.
	release *releaseEntities
	// meta is the project's name and slug, its live IDs the draft's own.
	live    map[string]bool
	people  map[ids.UUID]bool
	leftOut LeftOut
	// assets are the assets to copy, with their files' keys.
	assets []assetFile
	// releases are the releases to copy.
	releases []Release
}

// assetFile is a file of an asset to copy into the archive.
type assetFile struct {
	key, path string
}

// person records that the archive names the user u, if it is full and u is
// set, and returns the user's public ID, or "".
func (r *reading) person(u pgtype.UUID) string {
	if !u.Valid || r.profile != ProfileFull {
		return ""
	}
	r.people[ids.UUID(u.Bytes)] = true
	return ids.Encode(ids.User, u.Bytes)
}

// encode returns the public ID of u, or "" where it is not set.
func encode(k ids.Kind, u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	return ids.Encode(k, u.Bytes)
}

// liveID returns the public ID of u, and records it as one of the draft's.
func (r *reading) liveID(k ids.Kind, u pgtype.UUID) string {
	id := encode(k, u)
	r.live[id] = true
	return id
}

func readProject(ctx context.Context, r *reading) (any, int, error) {
	row, err := r.q.AuthoringProject(ctx, queries.AuthoringProjectParams{WorkspaceID: r.ws, ID: r.project})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, 0, errProjectGone
	}
	if err != nil {
		return nil, 0, fmt.Errorf("authoring: read the project: %w", err)
	}
	settings, err := r.settings(ctx, row.Settings)
	if err != nil {
		return nil, 0, fmt.Errorf("authoring: read the project's entry: %w", err)
	}
	return Project{Name: row.Name, Slug: row.Slug, Description: row.Description, Settings: settings}, 1, nil
}

// settings returns the project's settings as the archive holds them: with its
// entry's board and node left unset where they are in the trash, which the
// settings keep for a restore to mend, and by their canonical IDs, which the
// archive's boards and nodes have, as the stored entry keeps the case of its
// letters as they were sent.
func (r *reading) settings(ctx context.Context, doc []byte) (Value, error) {
	board, node, err := project.EntryOf(doc)
	if err != nil {
		return nil, err
	}
	if board == (ids.UUID{}) && node == (ids.UUID{}) {
		return Value(doc), nil
	}
	live, err := r.q.AuthoringEntry(ctx, queries.AuthoringEntryParams{WorkspaceID: r.ws, ProjectID: r.project,
		BoardID: uuid(board), NodeID: uuid(node)})
	if err != nil {
		return nil, err
	}
	var entry project.ProjectEntry
	if live.BoardLive {
		entry.BoardID = ids.Encode(ids.Board, board)
		if live.NodeLive {
			entry.NodeID = ids.Encode(ids.Node, node)
		}
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(doc, &fields); err != nil {
		return nil, err
	}
	if fields["entry"], err = json.Marshal(entry); err != nil {
		return nil, err
	}
	out, err := json.Marshal(fields)
	if err != nil {
		return nil, err
	}
	return Value(out), nil
}

func readFolders(ctx context.Context, r *reading) ([]Folder, error) {
	rows, err := r.q.AuthoringFolders(ctx, queries.AuthoringFoldersParams{WorkspaceID: r.ws, ProjectID: r.project})
	if err != nil {
		return nil, err
	}
	out := make([]Folder, len(rows))
	for i, f := range rows {
		out[i] = Folder{ID: encode(ids.Folder, f.ID), Kind: f.Kind, ParentID: encode(ids.Folder, f.ParentID),
			Name: f.Name, Order: f.SortKey}
	}
	return out, nil
}

func readBoards(ctx context.Context, r *reading) ([]Board, error) {
	rows, err := r.q.AuthoringBoards(ctx, queries.AuthoringBoardsParams{WorkspaceID: r.ws, ProjectID: r.project})
	if err != nil {
		return nil, err
	}
	out := make([]Board, len(rows))
	for i, b := range rows {
		out[i] = Board{ID: r.liveID(ids.Board, b.ID), FolderID: encode(ids.Folder, b.FolderID), Order: b.SortKey,
			Name: b.Name, Slug: b.Slug, Description: b.Description, Color: b.Color,
			EntryNodeID: encode(ids.Node, b.EntryNodeID)}
	}
	return out, nil
}

func readVariables(ctx context.Context, r *reading) ([]Variable, error) {
	rows, err := r.q.AuthoringVariables(ctx, queries.AuthoringVariablesParams{WorkspaceID: r.ws, ProjectID: r.project})
	if err != nil {
		return nil, err
	}
	out := make([]Variable, len(rows))
	for i, v := range rows {
		out[i] = Variable{ID: r.liveID(ids.Variable, v.ID), Scope: v.Scope, BoardID: encode(ids.Board, v.BoardID),
			FolderID: encode(ids.Folder, v.FolderID), Order: v.SortKey, Key: v.Key, Type: v.Type,
			Default: Value(v.DefaultValue), Description: v.Description, ResetOnEnter: v.ResetOnEnter}
	}
	return out, nil
}

func readComponentTypes(ctx context.Context, r *reading) ([]ComponentType, error) {
	rows, err := r.q.AuthoringComponentTypes(ctx, queries.AuthoringComponentTypesParams{WorkspaceID: r.ws,
		ProjectID: r.project})
	if err != nil {
		return nil, err
	}
	defs, err := r.q.SerializerAttributeDefs(ctx, queries.SerializerAttributeDefsParams{WorkspaceID: r.ws,
		ProjectID: r.project})
	if err != nil {
		return nil, err
	}
	slices.SortStableFunc(defs, func(a, b queries.SerializerAttributeDefsRow) int {
		return cmp.Compare(a.SortKey, b.SortKey)
	})
	byType := map[pgtype.UUID][]AttributeDef{}
	for _, d := range defs {
		byType[d.ComponentTypeID] = append(byType[d.ComponentTypeID], AttributeDef{
			ID: r.liveID(ids.AttributeDef, d.ID), Order: d.SortKey, Key: d.Key, Name: d.Name, Type: d.Type,
			EnumOptions: d.EnumOptions, Default: Value(d.DefaultValue), IsRuntime: d.IsRuntime})
	}
	out := make([]ComponentType, len(rows))
	for i, t := range rows {
		attrs := byType[t.ID]
		if attrs == nil {
			attrs = []AttributeDef{}
		}
		out[i] = ComponentType{ID: encode(ids.ComponentType, t.ID), Key: t.Key, Name: t.Name, Icon: t.Icon,
			Attributes: attrs}
	}
	return out, nil
}

func readComponents(ctx context.Context, r *reading) ([]Component, error) {
	rows, err := r.q.AuthoringComponents(ctx, queries.AuthoringComponentsParams{WorkspaceID: r.ws, ProjectID: r.project})
	if err != nil {
		return nil, err
	}
	values, err := r.q.SerializerAttributeValues(ctx, queries.SerializerAttributeValuesParams{WorkspaceID: r.ws,
		ProjectID: r.project})
	if err != nil {
		return nil, err
	}
	byComponent := map[pgtype.UUID]map[string]Value{}
	for _, v := range values {
		def := encode(ids.AttributeDef, v.AttributeDefID)
		// A value of a definition in the trash is in the trash with it.
		if !r.live[def] {
			continue
		}
		if byComponent[v.ComponentID] == nil {
			byComponent[v.ComponentID] = map[string]Value{}
		}
		byComponent[v.ComponentID][def] = Value(v.Value)
	}
	out := make([]Component, len(rows))
	for i, c := range rows {
		vals := byComponent[c.ID]
		if vals == nil {
			vals = map[string]Value{}
		}
		out[i] = Component{ID: encode(ids.Component, c.ID), ComponentTypeID: encode(ids.ComponentType, c.ComponentTypeID),
			FolderID: encode(ids.Folder, c.FolderID), Order: c.SortKey, Key: c.Key, Name: c.Name,
			Description: Text(c.Description), ImageAssetID: encode(ids.Asset, c.ImageAssetID), Values: vals}
	}
	return out, nil
}

// decimal returns a pointer to the decimal s, or nil for "", as a node without
// a size has.
func decimal(s string) *Decimal {
	if s == "" {
		return nil
	}
	d := Decimal(s)
	return &d
}

func readNodes(ctx context.Context, r *reading) ([]Node, error) {
	rows, err := r.q.AuthoringNodes(ctx, queries.AuthoringNodesParams{WorkspaceID: r.ws, ProjectID: r.project})
	if err != nil {
		return nil, err
	}
	out := make([]Node, len(rows))
	for i, n := range rows {
		tags := n.Tags
		if tags == nil {
			tags = []string{}
		}
		out[i] = Node{ID: r.liveID(ids.Node, n.ID), BoardID: encode(ids.Board, n.BoardID), Type: n.Type, Kind: n.Kind,
			Title: n.Title, Text: Text(n.Text), SpeakerID: encode(ids.Component, n.SpeakerComponentID),
			CoverAssetID: encode(ids.Asset, n.CoverAssetID), Color: n.Color, Tags: tags, X: Decimal(n.X), Y: Decimal(n.Y),
			Width: decimal(n.Width), Height: decimal(n.Height), IsEnding: n.IsEnding,
			TargetBoardID: encode(ids.Board, n.TargetBoardID), TargetNodeID: encode(ids.Node, n.TargetNodeID)}
	}
	return out, nil
}

func readOutputs(ctx context.Context, r *reading) ([]BranchOutput, error) {
	rows, err := r.q.SerializerOutputs(ctx, queries.SerializerOutputsParams{WorkspaceID: r.ws, ProjectID: r.project})
	if err != nil {
		return nil, err
	}
	out := make([]BranchOutput, len(rows))
	for i, o := range rows {
		out[i] = BranchOutput{ID: r.liveID(ids.BranchOutput, o.ID), NodeID: encode(ids.Node, o.NodeID), Kind: o.Kind,
			Order: o.SortKey}
	}
	return out, nil
}

func readConnections(ctx context.Context, r *reading) ([]Connection, error) {
	rows, err := r.q.SerializerConnections(ctx, queries.SerializerConnectionsParams{WorkspaceID: r.ws,
		ProjectID: r.project})
	if err != nil {
		return nil, err
	}
	out := make([]Connection, len(rows))
	for i, c := range rows {
		out[i] = Connection{ID: r.liveID(ids.Connection, c.ID), BoardID: encode(ids.Board, c.BoardID),
			FromNodeID: encode(ids.Node, c.FromNodeID), FromOutputID: encode(ids.BranchOutput, c.FromOutputID),
			ToNodeID: encode(ids.Node, c.ToNodeID), Order: c.SortKey, Label: Text(c.Label)}
	}
	return out, nil
}

func readExpressions(ctx context.Context, r *reading) ([]Expression, error) {
	rows, err := r.q.SerializerExpressions(ctx, queries.SerializerExpressionsParams{WorkspaceID: r.ws,
		ProjectID: r.project})
	if err != nil {
		return nil, err
	}
	out := make([]Expression, 0, len(rows))
	for _, e := range rows {
		x := Expression{Field: e.Field, Source: e.Source}
		switch {
		case e.NodeID.Valid:
			x.OwnerType, x.OwnerID = "node", encode(ids.Node, e.NodeID)
		case e.ConnectionID.Valid:
			x.OwnerType, x.OwnerID = "connection", encode(ids.Connection, e.ConnectionID)
		case e.BranchOutputID.Valid:
			x.OwnerType, x.OwnerID = "branch_output", encode(ids.BranchOutput, e.BranchOutputID)
		default:
			return nil, errors.New("an expression has no owner")
		}
		out = append(out, x)
	}
	slices.SortFunc(out, func(a, b Expression) int {
		return cmp.Or(cmp.Compare(a.OwnerID, b.OwnerID), cmp.Compare(a.Field, b.Field))
	})
	return out, nil
}

func readMutes(ctx context.Context, r *reading) ([]ValidationMute, error) {
	rows, err := r.q.AuthoringMutes(ctx, queries.AuthoringMutesParams{WorkspaceID: r.ws, ProjectID: r.project})
	if err != nil {
		return nil, err
	}
	out := make([]ValidationMute, 0, len(rows))
	for _, m := range rows {
		x := ValidationMute{Code: m.Code, Reason: m.Reason}
		switch {
		case m.NodeID.Valid:
			x.OwnerType, x.OwnerID = "node", encode(ids.Node, m.NodeID)
		case m.ConnectionID.Valid:
			x.OwnerType, x.OwnerID = "connection", encode(ids.Connection, m.ConnectionID)
		case m.BranchOutputID.Valid:
			x.OwnerType, x.OwnerID = "branch_output", encode(ids.BranchOutput, m.BranchOutputID)
		case m.VariableID.Valid:
			x.OwnerType, x.OwnerID = "variable", encode(ids.Variable, m.VariableID)
		case m.BoardID.Valid:
			x.OwnerType, x.OwnerID = "board", encode(ids.Board, m.BoardID)
		default:
			return nil, errors.New("a validation mute has no owner")
		}
		// A mute of an owner in the trash is in the trash with it.
		if !r.live[x.OwnerID] {
			continue
		}
		x.MutedBy = r.person(m.UpdatedBy)
		out = append(out, x)
	}
	slices.SortFunc(out, func(a, b ValidationMute) int {
		return cmp.Or(cmp.Compare(a.OwnerID, b.OwnerID), cmp.Compare(a.Code, b.Code))
	})
	return out, nil
}

// targetKinds are the public kinds of the targets of a comment's thread.
var targetKinds = map[string]ids.Kind{"node": ids.Node, "connection": ids.Connection, "board": ids.Board,
	"variable": ids.Variable}

func readComments(ctx context.Context, r *reading) ([]Comment, error) {
	rows, err := r.q.AuthoringComments(ctx, queries.AuthoringCommentsParams{WorkspaceID: r.ws, ProjectID: r.project})
	if err != nil {
		return nil, err
	}
	out := make([]Comment, 0, len(rows))
	for _, c := range rows {
		kind, ok := targetKinds[c.TargetType]
		if !ok {
			return nil, fmt.Errorf("a comment's target is of the kind %q", c.TargetType)
		}
		target := ids.Encode(kind, c.TargetID.Bytes)
		if r.release != nil && !r.release.has(target) {
			// A root that its author deleted is in the trash, which is not
			// counted, though its row holds its thread's place.
			if !c.DeletedAt.Valid {
				r.leftOut.Comments++
			}
			continue
		}
		x := Comment{ID: encode(ids.Comment, c.ID), ThreadID: encode(ids.Comment, c.ThreadID),
			ParentID: encode(ids.Comment, c.ParentID), CreatedAt: c.CreatedAt.Time.UTC(),
			EditedAt: timeOf(c.EditedAt), ResolvedAt: timeOf(c.ResolvedAt), ResolvedBy: r.person(c.ResolvedBy)}
		if !c.ParentID.Valid {
			x.TargetType, x.TargetID = c.TargetType, target
		}
		if c.DeletedAt.Valid {
			// What its author deleted is in the trash, and only its place
			// in its thread is kept, without naming them.
			x.Deleted = true
		} else {
			x.AuthorID = r.person(c.CreatedBy)
			x.Body = Text(c.Body)
			if err := r.mentioned(c.Body); err != nil {
				return nil, err
			}
		}
		out = append(out, x)
	}
	return out, nil
}

// mentioned records the members whom a comment's body mentions, whom the
// document holds by ID alone, so that the archive names them.
func (r *reading) mentioned(body []byte) error {
	doc, err := richtext.Parse(body)
	if err != nil {
		return fmt.Errorf("read a comment's body: %w", err)
	}
	for _, m := range doc.Members() {
		id, err := ids.Decode(ids.User, m.User)
		if err != nil {
			return fmt.Errorf("read a comment's mention: %w", err)
		}
		r.people[id] = true
	}
	return nil
}

// timeOf returns the time of t, or nil where it is not set.
func timeOf(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	u := t.Time.UTC()
	return &u
}

func readPlayStates(ctx context.Context, r *reading) ([]PlayState, error) {
	rows, err := r.q.AuthoringPlayStates(ctx, queries.AuthoringPlayStatesParams{WorkspaceID: r.ws, ProjectID: r.project})
	if err != nil {
		return nil, err
	}
	out := make([]PlayState, 0, len(rows))
	for _, p := range rows {
		if r.release != nil {
			var state struct {
				Cursor *struct {
					NodeID string `json:"nodeId"`
				} `json:"cursor"`
			}
			if err := json.Unmarshal(p.State, &state); err != nil {
				return nil, fmt.Errorf("read a play state: %w", err)
			}
			if state.Cursor != nil && !r.release.has(state.Cursor.NodeID) {
				r.leftOut.PlayStates++
				continue
			}
		}
		out = append(out, PlayState{ID: encode(ids.PlayState, p.ID), Name: p.Name, State: Value(p.State),
			Names: Value(p.Names), CreatedBy: r.person(p.CreatedBy), CreatedAt: p.CreatedAt.Time.UTC()})
	}
	return out, nil
}

// readAnnotations returns none: canvas annotations do not exist until plan
// item 127 (note N4).
func readAnnotations(context.Context, *reading) ([]Annotation, error) {
	return []Annotation{}, nil
}

func readReleases(ctx context.Context, r *reading) ([]Release, error) {
	rows, err := r.q.AuthoringReleases(ctx, queries.AuthoringReleasesParams{WorkspaceID: r.ws, ProjectID: r.project})
	if err != nil {
		return nil, err
	}
	out := make([]Release, len(rows))
	for i, row := range rows {
		report, err := r.report(ctx, row.ValidationReport)
		if err != nil {
			return nil, err
		}
		id := encode(ids.Snapshot, row.ID)
		out[i] = Release{ID: id, Version: row.Version, Note: row.Note, AcknowledgedSeq: row.AcknowledgedSeq,
			AcknowledgedWarnings: Value(row.AcknowledgedWarnings), SchemaVersion: int(row.SchemaVersion.Int32),
			ContentHash: row.ContentHash.String, SourceSeq: row.SourceSeq.Int64, Counts: Value(row.Counts),
			ValidationReport: report, PublishedBy: r.person(row.PublishedBy), PublishedAt: row.PublishedAt.Time.UTC(),
			BuiltAt: row.BuiltAt.Time.UTC(), Path: "releases/" + id + "/"}
	}
	r.releases = out
	return out, nil
}

// report returns a release's stored validation report as the archive holds
// it: in a full archive it names who muted each finding while their account
// exists, as the API's read of the report does (snapshot.Report), which also
// reads an ID that names no user as no account, and in a content-only one it
// names nobody.
func (r *reading) report(ctx context.Context, raw []byte) (Value, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var doc map[string]any
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("read a release's validation report: %w", err)
	}
	findings, _ := doc["findings"].([]any)
	stripped := false
	var mutes []map[string]any
	var users []pgtype.UUID
	for _, f := range findings {
		finding, _ := f.(map[string]any)
		mute, _ := finding["mute"].(map[string]any)
		by, ok := mute["muted_by"].(string)
		if !ok {
			continue
		}
		if r.profile != ProfileFull {
			delete(mute, "muted_by")
			stripped = true
			continue
		}
		id, err := ids.Decode(ids.User, by)
		if err != nil {
			delete(mute, "muted_by")
			stripped = true
			continue
		}
		mutes = append(mutes, mute)
		users = append(users, pgtype.UUID{Bytes: id, Valid: true})
	}
	if len(users) > 0 {
		rows, err := r.q.UsersWithAccounts(ctx, users)
		if err != nil {
			return nil, fmt.Errorf("read who muted the findings of a release: %w", err)
		}
		exist := make(map[ids.UUID]bool, len(rows))
		for _, u := range rows {
			exist[ids.UUID(u.Bytes)] = true
		}
		for i, mute := range mutes {
			if u := ids.UUID(users[i].Bytes); exist[u] {
				r.people[u] = true
			} else {
				delete(mute, "muted_by")
				stripped = true
			}
		}
	}
	if !stripped {
		return Value(raw), nil
	}
	out, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("write a release's validation report: %w", err)
	}
	return Value(out), nil
}

func readAssets(ctx context.Context, r *reading) ([]Asset, error) {
	rows, err := r.q.AuthoringAssets(ctx, queries.AuthoringAssetsParams{WorkspaceID: r.ws, ProjectID: r.project})
	if err != nil {
		return nil, err
	}
	out := make([]Asset, len(rows))
	r.assets = make([]assetFile, len(rows))
	for i, a := range rows {
		id := encode(ids.Asset, a.ID)
		// assets/<the ID's body>.<its extension>, as a snapshot's assets are
		// laid out (PRD §6.5).
		_, body, _ := strings.Cut(id, "_")
		file := "assets/" + body + path.Ext(a.StorageKey)
		out[i] = Asset{ID: id, Path: file, SHA256: hex.EncodeToString(a.Sha256), Mime: a.Mime, Bytes: a.Bytes,
			Width: int4(a.Width), Height: int4(a.Height)}
		r.assets[i] = assetFile{key: a.StorageKey, path: file}
	}
	return out, nil
}

// int4 returns a pointer to n's value, or nil where it is not set.
func int4(n pgtype.Int4) *int32 {
	if !n.Valid {
		return nil
	}
	return &n.Int32
}

func readPeople(ctx context.Context, r *reading) ([]Person, error) {
	if len(r.people) == 0 {
		return nil, nil
	}
	users := make([]pgtype.UUID, 0, len(r.people))
	for _, u := range slices.SortedFunc(maps.Keys(r.people), func(a, b ids.UUID) int {
		return bytes.Compare(a[:], b[:])
	}) {
		users = append(users, pgtype.UUID{Bytes: u, Valid: true})
	}
	rows, err := r.q.AuthoringPeople(ctx, users)
	if err != nil {
		return nil, err
	}
	out := make([]Person, len(rows))
	for i, p := range rows {
		out[i] = Person{ID: encode(ids.User, p.ID), DisplayName: p.DisplayName}
	}
	return out, nil
}

// releaseEntities are the IDs of the boards, nodes, connections, and
// variables of a release's files.
type releaseEntities struct {
	ids map[string]bool
}

// has says whether the release has the entity of the public ID id.
func (e *releaseEntities) has(id string) bool { return e.ids[id] }

// entitiesOf reads which entities a release's files hold.
func entitiesOf(files map[string][]byte) (*releaseEntities, error) {
	type item struct {
		ID string `json:"id"`
	}
	e := &releaseEntities{ids: map[string]bool{}}
	for name, data := range files {
		switch {
		case name == "project/variables.json":
			var f struct {
				Variables []item `json:"variables"`
			}
			if err := json.Unmarshal(data, &f); err != nil {
				return nil, fmt.Errorf("authoring: read the release's %s: %w", name, err)
			}
			for _, v := range f.Variables {
				e.ids[v.ID] = true
			}
		case strings.HasPrefix(name, "boards/"):
			var f struct {
				Board       item   `json:"board"`
				Nodes       []item `json:"nodes"`
				Connections []item `json:"connections"`
			}
			if err := json.Unmarshal(data, &f); err != nil {
				return nil, fmt.Errorf("authoring: read the release's %s: %w", name, err)
			}
			e.ids[f.Board.ID] = true
			for _, n := range f.Nodes {
				e.ids[n.ID] = true
			}
			for _, c := range f.Connections {
				e.ids[c.ID] = true
			}
		}
	}
	return e, nil
}
