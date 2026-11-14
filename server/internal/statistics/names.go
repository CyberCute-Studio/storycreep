package statistics

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/richtext"
)

// Names are the names that a text's counts read of what it names
// (richtext.Names), as the project has them, in the trash or not: a value's
// name is a variable's key, as met_elara, or a component's key and its
// attribute's key joined by a dot, as elara.trust, and a mention's is its
// component's name. A name of something that the project does not have, as
// what the trash's purge deleted, is "", so that a value reference of it is
// {}, two characters and no word, and a mention of it nothing, which is what
// the validation reports a dangling reference for (FR-TEXT-02 AC2).
type Names struct {
	// variables are the variables' keys, components the components' names
	// and keys, and attributes the definitions' keys, each by its public ID
	// in its canonical spelling, as a document holds it.
	variables  map[string]string
	components map[string]componentNames
	attributes map[string]string
}

// componentNames are what a component is named by.
type componentNames struct {
	name, key string
}

// Value returns the name of the value that a reference reads (richtext.Names).
func (n Names) Value(ref, attribute string) string {
	if attribute == "" {
		return n.variables[ref]
	}
	c, ok := n.components[ref]
	a, found := n.attributes[attribute]
	if !ok || !found {
		return ""
	}
	return c.key + "." + a
}

// Component returns the name of a component, as a mention shows it
// (richtext.Names).
func (n Names) Component(id string) string {
	return n.components[id].name
}

// ReadNames returns the names of what the documents docs name, of which
// some may be nil, as the project projectID of the workspace workspaceID
// has them, read in the transaction q with one query, or none for documents
// that name nothing.
func ReadNames(ctx context.Context, q *queries.Queries, workspaceID, projectID ids.UUID,
	docs []*richtext.Doc) (Names, error) {
	n := Names{variables: map[string]string{}, components: map[string]componentNames{},
		attributes: map[string]string{}}
	var variables, components, attributes []pgtype.UUID
	// seen holds each public ID that a document names, read once whatever
	// the number of documents and references that name it.
	seen := map[string]bool{}
	add := func(to *[]pgtype.UUID, k ids.Kind, id string) error {
		if seen[id] {
			return nil
		}
		seen[id] = true
		u, err := ids.Decode(k, id)
		if err != nil {
			return fmt.Errorf("statistics: a text names %q: %w", id, err)
		}
		*to = append(*to, nullUUID(u))
		return nil
	}
	for _, d := range docs {
		if d == nil {
			continue
		}
		for in := range d.Inlines() {
			var err error
			switch in := in.(type) {
			case *richtext.Mention:
				err = add(&components, ids.Component, in.Component)
			case *richtext.Value:
				if in.Attribute == "" {
					err = add(&variables, ids.Variable, in.Ref)
					break
				}
				if err = add(&components, ids.Component, in.Ref); err == nil {
					err = add(&attributes, ids.AttributeDef, in.Attribute)
				}
			}
			if err != nil {
				return Names{}, err
			}
		}
	}
	if len(variables) == 0 && len(components) == 0 && len(attributes) == 0 {
		return n, nil
	}
	rows, err := q.TextNames(ctx, queries.TextNamesParams{WorkspaceID: nullUUID(workspaceID),
		ProjectID: nullUUID(projectID), VariableIds: list(variables), ComponentIds: list(components),
		AttributeIds: list(attributes)})
	if err != nil {
		return Names{}, fmt.Errorf("statistics: read the names that the texts name: %w", err)
	}
	for _, r := range rows {
		switch r.Kind {
		case "variable":
			n.variables[ids.Encode(ids.Variable, r.ID.Bytes)] = r.Name
		case "component":
			n.components[ids.Encode(ids.Component, r.ID.Bytes)] = componentNames{name: r.Name, key: r.Key}
		case "attribute":
			n.attributes[ids.Encode(ids.AttributeDef, r.ID.Bytes)] = r.Name
		}
	}
	return n, nil
}
