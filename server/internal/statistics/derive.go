package statistics

import (
	"context"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/dependency"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/richtext"
)

// Name is the name of the derivation that counts the texts that a batch
// changed (commands.Derivation).
const Name = "text_statistics"

// Register registers in r the derivation that writes, once every command of
// a batch has applied, the rows of the statistics of the texts whose counts
// the batch may have changed: of each node and connection whose text a
// command of the batch may have changed (dependency.ChangedTexts), and of
// each node and connection whose text names a variable, a component, or an
// attribute's definition that a command of the batch changed, since a text
// counts the names of what it mentions and shows, by the names that the
// types of the commands that changed it change: none for those of
// nameNeutral, and of a component, its name, which a mention shows, or its
// key, which a value reference of one of its attributes shows, as
// componentNaming has them. It changes no entity's revision, as the
// statistics are no part of what the API shows of one. Register it after
// dependency.Register, whose index of what the texts name it reads the texts
// that name what the batch changed by.
func Register(r *commands.Registry) {
	commands.RegisterDerivation(r, commands.Derivation{Name: Name, Derive: derive})
}

// nameNeutral are the types of command that change a variable, a component,
// or an attribute's definition, but none of the names that a text's counts
// read of it (Names): a variable's key, a component's name and key, and a
// definition's key, which a rename, a change of a component's key, and a
// change of a definition's key change. A move, a deletion to the trash or a
// restoration, which keeps them, is neutral, and so are the mutes of the
// validation's findings, which change their owners' revisions alone, and the
// derivation that compiles expressions, whose owners are none of these.
// derive passes over a change that these alone made, so that deleting an
// attribute of a type of 100 components to the trash, which changes each of
// them, counts no text. A type that is not here is taken to change names, so
// that a new type of command counts too much, never too little, until it is
// added here: a creation among them, as a paste's of a variable that a text
// named while the project had none of its ID, which then shows its name.
// TestNameNeutral checks that each is a registered type, or the derivation's
// name.
var nameNeutral = []commands.Type{
	"expression",
	"variable.describe", "variable.set_default", "variable.retype", "variable.set_scope",
	"variable.set_reset_on_enter", "variable.move", "variable.delete", "variable.restore",
	"component.describe", "component.set_image", "component.set_attribute", "component.move",
	"component.delete", "component.restore",
	"attribute.rename", "attribute.set_default", "attribute.set_runtime", "attribute.set_options",
	"attribute.retype", "attribute.move", "attribute.delete", "attribute.restore",
	"validation.mute", "validation.unmute",
}

// neutralNames holds the types of nameNeutral, which derive looks the type
// of each command of a change up in.
var neutralNames = func() map[commands.Type]bool {
	m := make(map[commands.Type]bool, len(nameNeutral))
	for _, t := range nameNeutral {
		m[t] = true
	}
	return m
}()

// naming is which of a component's names a type of command changes: its
// name, which a mention shows, and its key, which a value reference of one
// of its attributes shows.
type naming struct {
	name, key bool
}

// componentNaming are the types of command beyond those of nameNeutral,
// which change no name of a component, that change a component but not all
// of its names, each with those that it changes: a rename its name, a change
// of its key its key, and the creation of a definition neither, which gives
// each component of its type the definition's default (FR-COMP-01 AC1), so
// that adding an attribute to a type of 100 components, which changes each
// of them, counts the texts that name the new definition alone, as a
// paste's that a text named while the project had none of its ID, rather
// than every text that names one of the components. derive counts the texts
// that name a component by the names that the types of the commands that
// changed it change, a type that is in neither list being taken to change
// both, as nameNeutral says. TestNameNeutral checks that each is a
// registered type.
var componentNaming = map[commands.Type]naming{
	"component.rename":  {name: true},
	"component.set_key": {key: true},
	"attribute.create":  {},
}

// namesChanged returns which of a component's names the commands of the
// types by may have changed (componentNaming).
func namesChanged(by []commands.Type) naming {
	var n naming
	for _, t := range by {
		if neutralNames[t] {
			continue
		}
		c, ok := componentNaming[t]
		if !ok {
			return naming{name: true, key: true}
		}
		n.name, n.key = n.name || c.name, n.key || c.key
	}
	return n
}

// derive writes the rows of the statistics of the texts whose counts the
// batch's commands may have changed, as Register says.
func derive(ctx context.Context, tx *commands.Tx, changed []commands.Change) error {
	var sel selection
	for c := range dependency.ChangedTexts(slices.Values(changed)) {
		sel.add(c.Kind, nullUUID(c.ID))
	}
	var variables, named, keyed, attributes []pgtype.UUID
	for _, c := range changed {
		if c.Kind == ids.Component {
			n := namesChanged(c.By)
			if n.name {
				named = append(named, nullUUID(c.ID))
			}
			if n.key {
				keyed = append(keyed, nullUUID(c.ID))
			}
			continue
		}
		if !slices.ContainsFunc(c.By, func(t commands.Type) bool { return !neutralNames[t] }) {
			continue
		}
		switch c.Kind {
		case ids.Variable:
			variables = append(variables, nullUUID(c.ID))
		case ids.AttributeDef:
			attributes = append(attributes, nullUUID(c.ID))
		}
	}
	q := queries.New(tx)
	ws, project := nullUUID(tx.WorkspaceID()), nullUUID(tx.ProjectID())
	// The texts that name a definition that the batch changed are read apart
	// from the others, by the index of the definitions, and only for a batch
	// that changed one, so that a batch that renames a variable, or creates
	// one, reads the rows of the index of the targets that name it alone.
	if len(variables) > 0 || len(named) > 0 || len(keyed) > 0 {
		texts, err := q.TextsNaming(ctx, queries.TextsNamingParams{WorkspaceID: ws, ProjectID: project,
			VariableIds: list(variables), NamedIds: list(named), KeyedIds: list(keyed)})
		if err != nil {
			return fmt.Errorf("statistics: find the texts that name what the batch changed: %w", err)
		}
		for _, t := range texts {
			sel.addText(t.NodeID, t.ConnectionID)
		}
	}
	if len(attributes) > 0 {
		texts, err := q.TextsNamingDefinitions(ctx, queries.TextsNamingDefinitionsParams{WorkspaceID: ws,
			ProjectID: project, AttributeIds: attributes})
		if err != nil {
			return fmt.Errorf("statistics: find the texts that name the definitions that the batch changed: %w", err)
		}
		for _, t := range texts {
			sel.addText(t.NodeID, t.ConnectionID)
		}
	}
	if sel.empty() {
		return nil
	}
	_, err := count(ctx, q, tx.WorkspaceID(), tx.ProjectID(), sel)
	return err
}

// Recount counts again, in the transaction q of the workspace workspaceID,
// which holds the lock of the project projectID, as a batch of its commands
// does, the texts of the project that name a variable, a component, or an
// attribute's definition that it no longer has, as once the trash's purge
// has deleted it, whose counts the names that they lost change (Names), and
// returns how many texts it counted. The purge calls it once it has deleted
// what texts may name, so that the statistics stay what counting every text
// from scratch gives.
func Recount(ctx context.Context, q *queries.Queries, workspaceID, projectID ids.UUID) (int, error) {
	texts, err := q.TextsNamingMissing(ctx, queries.TextsNamingMissingParams{WorkspaceID: nullUUID(workspaceID),
		ProjectID: nullUUID(projectID)})
	if err != nil {
		return 0, fmt.Errorf("statistics: find the texts that name what the project no longer has: %w", err)
	}
	var sel selection
	for _, t := range texts {
		sel.addText(t.NodeID, t.ConnectionID)
	}
	if sel.empty() {
		return 0, nil
	}
	return count(ctx, q, workspaceID, projectID, sel)
}

// selection is the texts whose rows count writes: of the nodes and of the
// connections.
type selection struct {
	nodes, connections []pgtype.UUID
}

// add adds the entity id of the kind k to s, if it is a node or a
// connection, which have texts.
func (s *selection) add(k ids.Kind, id pgtype.UUID) {
	switch k {
	case ids.Node:
		s.nodes = append(s.nodes, id)
	case ids.Connection:
		s.connections = append(s.connections, id)
	}
}

// addText adds to s the text of the node node, or of the connection
// connection, whichever of them is not null, as the index of what the texts
// name gives a text's owner.
func (s *selection) addText(node, connection pgtype.UUID) {
	switch {
	case node.Valid:
		s.nodes = append(s.nodes, node)
	case connection.Valid:
		s.connections = append(s.connections, connection)
	}
}

// empty reports whether s holds no text.
func (s *selection) empty() bool {
	return len(s.nodes) == 0 && len(s.connections) == 0
}

// source is a text to count: its owner's kind and UUID, the rich text as it
// is stored, or nil for a node that has none, a branch or a jumper, and the
// row that the statistics hold of it, if any.
type source struct {
	kind       Kind
	id         pgtype.UUID
	doc        []byte
	words      pgtype.Int4
	characters pgtype.Int4
	version    pgtype.Int4
}

// Kind is the kind of an entity that has a row of the statistics.
type Kind string

// The kinds of entity that have a row of the statistics, as the rows hold
// them.
const (
	KindNode       Kind = "node"
	KindConnection Kind = "connection"
)

// count writes the row of the statistics of each text of sel of the project
// projectID of the workspace workspaceID, in the trash or not, of the
// current Version, as the project's rows hold them now, with the names that
// the project gives of what they name, but for those that the statistics
// hold already, and returns how many texts it found. It reads each kind that
// sel holds with a query of its own, each text with its row, so that a batch
// that changes no count, as an edit of a node's title, writes nothing.
func count(ctx context.Context, q *queries.Queries, workspaceID, projectID ids.UUID, sel selection) (int, error) {
	ws, project := nullUUID(workspaceID), nullUUID(projectID)
	var sources []source
	if len(sel.nodes) > 0 {
		rows, err := q.NodeTextSources(ctx, queries.NodeTextSourcesParams{WorkspaceID: ws, ProjectID: project,
			Ids: sel.nodes})
		if err != nil {
			return 0, fmt.Errorf("statistics: read the texts of the nodes: %w", err)
		}
		for _, r := range rows {
			sources = append(sources, source{kind: KindNode, id: r.ID, doc: r.Doc, words: r.EntryWords,
				characters: r.EntryCharacters, version: r.EntryVersion})
		}
	}
	if len(sel.connections) > 0 {
		rows, err := q.ConnectionTextSources(ctx, queries.ConnectionTextSourcesParams{WorkspaceID: ws,
			ProjectID: project, Ids: sel.connections})
		if err != nil {
			return 0, fmt.Errorf("statistics: read the labels of the connections: %w", err)
		}
		for _, r := range rows {
			sources = append(sources, source{kind: KindConnection, id: r.ID, doc: r.Doc, words: r.EntryWords,
				characters: r.EntryCharacters, version: r.EntryVersion})
		}
	}
	docs := make([]*richtext.Doc, len(sources))
	for i, s := range sources {
		if s.doc == nil {
			continue
		}
		d, err := richtext.Parse(s.doc)
		if err != nil {
			return 0, fmt.Errorf("statistics: the text of the %s %s: %w", s.kind, ids.UUID(s.id.Bytes), err)
		}
		docs[i] = d
	}
	names, err := ReadNames(ctx, q, workspaceID, projectID, docs)
	if err != nil {
		return 0, err
	}
	p := queries.UpsertTextStatisticsParams{WorkspaceID: ws, ProjectID: project, Version: Version}
	for i, s := range sources {
		words, characters := Count(docs[i], names)
		if s.version.Valid && s.version.Int32 == Version && s.words.Int32 == int32(words) &&
			s.characters.Int32 == int32(characters) {
			continue
		}
		p.Kinds = append(p.Kinds, string(s.kind))
		p.EntityIds = append(p.EntityIds, s.id)
		p.Words = append(p.Words, int32(words))
		p.Characters = append(p.Characters, int32(characters))
	}
	if len(p.Kinds) == 0 {
		return len(sources), nil
	}
	if err := q.UpsertTextStatistics(ctx, p); err != nil {
		return 0, fmt.Errorf("statistics: write the rows: %w", err)
	}
	return len(sources), nil
}

// Count returns the words and the characters of the rich text doc, or 0 and
// 0 for nil, which a branch and a jumper have, with the names of what it
// names, as the text export counts them (richtext.WordCount,
// richtext.CharacterCount).
func Count(doc *richtext.Doc, names richtext.Names) (words, characters int) {
	if doc == nil {
		return 0, 0
	}
	segs := doc.Segments(names)
	return richtext.WordCount(segs), richtext.CharacterCount(segs)
}
