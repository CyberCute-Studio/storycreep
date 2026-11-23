package authoring

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/schemaversion"
)

// archived is what an archive holds, decoded, once its files have passed
// their hashes and their schema.
type archived struct {
	manifest    Manifest
	project     *Project
	folders     []Folder
	boards      []Board
	variables   []Variable
	types       []ComponentType
	components  []Component
	nodes       []Node
	outputs     []BranchOutput
	connections []Connection
	expressions []Expression
	mutes       []ValidationMute
	comments    []Comment
	playStates  []PlayState
	annotations []Annotation
	releases    []Release
	assets      []Asset
	people      []Person
	// raw holds each section's file as the archive holds it, by the section's
	// name, which a remap rewrites and decodes again.
	raw map[string][]byte
	// sourceFiles are the files of the release that an archive of a release
	// holds as its content, at the version that the draft is built from.
	sourceFiles schemaversion.Files
	// releasePaths are the directories of the releases' files in the archive,
	// by the releases' places in the index, which a remap leaves as they are.
	releasePaths []string
}

// fileSchemas are the compiled schemas of the format's JSON files, by their
// paths in an archive.
var fileSchemas = sync.OnceValues(func() (map[string]*jsonschema.Schema, error) {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(Schema()))
	if err != nil {
		return nil, fmt.Errorf("authoring: read the format's schema: %w", err)
	}
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	if err := c.AddResource(SchemaID, doc); err != nil {
		return nil, fmt.Errorf("authoring: load the format's schema: %w", err)
	}
	out := map[string]*jsonschema.Schema{}
	paths := []string{ManifestPath}
	for _, s := range sections {
		paths = append(paths, s.Path)
	}
	for _, p := range paths {
		sch, err := c.Compile(SchemaID + "#/properties/" + strings.ReplaceAll(p, "/", "~1"))
		if err != nil {
			return nil, fmt.Errorf("authoring: compile the schema of %s: %w", p, err)
		}
		out[p] = sch
	}
	return out, nil
})

// checkSchema validates a JSON file of the archive against its schema
// (NFR-SEC-13), before anything of it is read.
func checkSchema(path, section string, data []byte) error {
	schemas, err := fileSchemas()
	if err != nil {
		return err
	}
	sch, ok := schemas[path]
	if !ok {
		return &ImportError{Code: FailSectionUnsupported, Section: section}
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return &ImportError{Code: FailSchemaInvalid, Section: section, Check: "json", Detail: err.Error()}
	}
	if err := sch.Validate(inst); err != nil {
		return &ImportError{Code: FailSchemaInvalid, Section: section, Check: "schema", Detail: firstLine(err.Error())}
	}
	return nil
}

// firstLine returns the first lines of a validator's message, which can list
// every problem of a large file.
func firstLine(s string) string {
	lines := strings.SplitN(s, "\n", 4)
	if len(lines) > 3 {
		lines = lines[:3]
	}
	out := strings.Join(lines, " ")
	if len(out) > 500 {
		out = strings.ToValidUTF8(out[:500], "")
	}
	return out
}

// readArchived reads the manifest and every section that it lists, refusing an
// archive of a format that this server does not read, a section that it cannot
// import, a file whose hash is not the manifest's or that its schema refuses,
// and a member that nothing accounts for, all before any row is written.
func readArchived(a *archive) (*archived, error) {
	data, err := a.read(ManifestPath, maxJSONMember)
	if err != nil {
		return nil, err
	}
	var head struct {
		Format        string `json:"format"`
		FormatVersion int    `json:"formatVersion"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return nil, &ImportError{Code: FailSchemaInvalid, Section: "manifest", Check: "json", Detail: err.Error()}
	}
	if head.Format != Format {
		return nil, &ImportError{Code: FailFormatUnsupported, Check: "format",
			Detail: fmt.Sprintf("the archive is of the format %q", head.Format)}
	}
	if head.FormatVersion < 1 || head.FormatVersion > Version {
		return nil, &ImportError{Code: FailFormatUnsupported, Check: "format_version",
			Detail: fmt.Sprintf("the archive is of version %d of the format, and this server reads up to version %d",
				head.FormatVersion, Version)}
	}
	if err := checkSchema(ManifestPath, "manifest", data); err != nil {
		return nil, err
	}
	c := &archived{raw: map[string][]byte{}}
	if err := json.Unmarshal(data, &c.manifest); err != nil {
		return nil, &ImportError{Code: FailSchemaInvalid, Section: "manifest", Check: "json", Detail: err.Error()}
	}
	known := map[string]bool{ManifestPath: true, ReadmePath: true}
	seen := map[string]bool{}
	for _, entry := range c.manifest.Sections {
		if seen[entry.Name] {
			return nil, &ImportError{Code: FailSchemaInvalid, Section: entry.Name, Check: "duplicate"}
		}
		seen[entry.Name] = true
		sec, ok := sectionNamed(entry.Name)
		if !ok || sec.store == nil {
			return nil, &ImportError{Code: FailSectionUnsupported, Section: entry.Name,
				Detail: "this server cannot import the section"}
		}
		if entry.Path != sec.Path {
			return nil, &ImportError{Code: FailSchemaInvalid, Section: entry.Name, Check: "path",
				Detail: fmt.Sprintf("the section is at %q, and belongs at %q", entry.Path, sec.Path)}
		}
		if sec.DraftOnly && c.manifest.Source.Kind != "draft" {
			return nil, &ImportError{Code: FailSchemaInvalid, Section: entry.Name, Check: "source",
				Detail: "only an archive of a draft holds the section"}
		}
		file, err := a.read(sec.Path, maxJSONMember)
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(file)
		if hex.EncodeToString(sum[:]) != entry.SHA256 {
			return nil, &ImportError{Code: FailSchemaInvalid, Section: entry.Name, Check: "hash",
				Detail: "the file's SHA-256 is not the manifest's"}
		}
		if err := checkSchema(sec.Path, entry.Name, file); err != nil {
			return nil, err
		}
		if err := sec.store(c, file); err != nil {
			return nil, err
		}
		c.raw[entry.Name] = file
		known[sec.Path] = true
	}
	switch c.manifest.Source.Kind {
	case "draft":
		if c.project == nil {
			return nil, &ImportError{Code: FailSchemaInvalid, Section: "project", Check: "missing",
				Detail: "an archive of a draft holds the project section"}
		}
	case "release":
		for _, name := range a.under("release/") {
			known[name] = true
		}
	}
	c.releasePaths = make([]string, len(c.releases))
	for i, rel := range c.releases {
		if !strings.HasPrefix(rel.Path, "releases/") || !strings.HasSuffix(rel.Path, "/") ||
			strings.Count(rel.Path, "/") != 2 {
			return nil, &ImportError{Code: FailSchemaInvalid, Section: "releases", Check: "path",
				Detail: fmt.Sprintf("release %s has the path %q", rel.Version, rel.Path)}
		}
		c.releasePaths[i] = rel.Path
		for _, name := range a.under(rel.Path) {
			known[name] = true
		}
	}
	for _, as := range c.assets {
		if !strings.HasPrefix(as.Path, "assets/") {
			return nil, &ImportError{Code: FailAssetInvalid, Section: "assets", Check: "path", Detail: as.Path}
		}
		known[as.Path] = true
	}
	for _, name := range slices.Sorted(maps.Keys(a.files)) {
		if !known[name] {
			return nil, invalidArchive("unexpected_member", "the member %q is in no section that the manifest lists",
				name)
		}
	}
	return c, nil
}

// declaredSections are sections that a test build declares and no importer
// handles, whose presence in an archive refuses it, naming the section
// (FR-DATA-01 AC12, in the same way as the synthetic schema version). No
// release build has any.
var declaredSections []Section

// sectionNamed returns the section of the registry of the name, or one that a
// test build declares.
func sectionNamed(name string) (Section, bool) {
	for _, s := range slices.Concat(sections, declaredSections) {
		if s.Name == name {
			return s, true
		}
	}
	return Section{}, false
}

// idToken finds what may be a public ID of what an archive holds, in any of
// the spellings that a stored reference may have, as the IDs of a rich text's
// or an expression's references, which keep the case that they were written
// in. A person's ID is not one, as people are mapped apart (mapPeople).
var idToken = regexp.MustCompile(`(?i)\b(?:prj|fld|bd|nd|bo|cn|var|ct|atr|cmp|cmt|snp|ast|pst)_[0-9a-z]{26}\b`)

// userToken finds a user's public ID.
var userToken = regexp.MustCompile(`(?i)\busr_[0-9a-z]{26}\b`)

// rewriteIDs returns data with each ID that table has written as its new ID,
// in its canonical spelling, and anything else as it is.
func rewriteIDs(data []byte, table map[ids.UUID]ids.UUID) []byte {
	return idToken.ReplaceAllFunc(data, func(tok []byte) []byte {
		kind, id, err := ids.DecodeAny(string(tok))
		if err != nil {
			return tok
		}
		fresh, ok := table[id]
		if !ok {
			return tok
		}
		return []byte(ids.Encode(kind, fresh))
	})
}

// rewriteUsers returns data with each user's ID written as table says, or as
// the ID that fallback gives for one that the table does not have, so that no
// ID from an archive can name an account of the instance that the workspace
// cannot see.
func rewriteUsers(data []byte, table map[ids.UUID]ids.UUID) []byte {
	return userToken.ReplaceAllFunc(data, func(tok []byte) []byte {
		id, err := ids.Decode(ids.User, string(tok))
		if err != nil {
			return tok
		}
		fresh, ok := table[id]
		if !ok {
			return tok
		}
		return []byte(ids.Encode(ids.User, fresh))
	})
}

// usersIn adds to set each user's ID that data holds.
func usersIn(data []byte, set map[ids.UUID]bool) {
	for _, tok := range userToken.FindAll(data, -1) {
		if id, err := ids.Decode(ids.User, string(tok)); err == nil {
			set[id] = true
		}
	}
}

// entityIDs returns the ID of every row that the archive would add, each once,
// which the collision check asks about (§5.2), and refuses an ID of the wrong
// kind, which no row could hold.
func (c *archived) entityIDs() ([]ids.UUID, error) {
	set := map[ids.UUID]bool{}
	var failure error
	add := func(section string, k ids.Kind, id string) {
		if failure != nil {
			return
		}
		u, err := ids.Decode(k, id)
		if err != nil {
			failure = &ImportError{Code: FailContentInvalid, Section: section, Check: "id",
				Detail: fmt.Sprintf("%q is no %s ID", id, k)}
			return
		}
		if set[u] {
			failure = &ImportError{Code: FailContentInvalid, Section: section, Check: "duplicate_id",
				Detail: fmt.Sprintf("the ID %s is in the archive twice", id)}
			return
		}
		set[u] = true
	}
	add("manifest", ids.Project, c.manifest.Project.ID)
	for _, x := range c.folders {
		add("folders", ids.Folder, x.ID)
	}
	for _, x := range c.boards {
		add("boards", ids.Board, x.ID)
	}
	for _, x := range c.variables {
		add("variables", ids.Variable, x.ID)
	}
	for _, x := range c.types {
		add("component_types", ids.ComponentType, x.ID)
		for _, d := range x.Attributes {
			add("component_types", ids.AttributeDef, d.ID)
		}
	}
	for _, x := range c.components {
		add("components", ids.Component, x.ID)
	}
	for _, x := range c.nodes {
		add("nodes", ids.Node, x.ID)
	}
	for _, x := range c.outputs {
		add("branch_outputs", ids.BranchOutput, x.ID)
	}
	for _, x := range c.connections {
		add("connections", ids.Connection, x.ID)
	}
	for _, x := range c.comments {
		add("comments", ids.Comment, x.ID)
	}
	for _, x := range c.playStates {
		add("play_states", ids.PlayState, x.ID)
	}
	for _, x := range c.releases {
		add("releases", ids.Snapshot, x.ID)
	}
	for _, x := range c.assets {
		add("assets", ids.Asset, x.ID)
	}
	if failure != nil {
		return nil, failure
	}
	out := slices.SortedFunc(maps.Keys(set), func(a, b ids.UUID) int { return bytes.Compare(a[:], b[:]) })
	return out, nil
}

// freshIDs returns a table of a fresh ID for each of list. The releases' IDs
// are made in the order of their IDs, each after the last, as a project's list
// of releases is in the order of their IDs (package snapshot).
func (c *archived) freshIDs(list []ids.UUID) (map[ids.UUID]ids.UUID, error) {
	table := make(map[ids.UUID]ids.UUID, len(list))
	for _, id := range list {
		table[id] = ids.New()
	}
	order := make([]ids.UUID, 0, len(c.releases))
	for _, rel := range c.releases {
		id, err := ids.Decode(ids.Snapshot, rel.ID)
		if err != nil {
			return nil, &ImportError{Code: FailContentInvalid, Section: "releases", Check: "id", Detail: rel.ID}
		}
		order = append(order, id)
	}
	slices.SortFunc(order, func(a, b ids.UUID) int { return bytes.Compare(a[:], b[:]) })
	var last ids.UUID
	for _, id := range order {
		last = ids.NewAfter(last)
		table[id] = last
	}
	return table, nil
}

// remap rewrites every ID that table holds through the archive's sections, and
// decodes them again, so that each reference, those inside a rich text, an
// expression's source, a play state, or a stored report among them, names the
// row's new ID. The releases' files are rewritten as they are read.
func (c *archived) remap(table map[ids.UUID]ids.UUID) error {
	project, err := ids.Decode(ids.Project, c.manifest.Project.ID)
	if err != nil {
		return &ImportError{Code: FailContentInvalid, Section: "manifest", Check: "id", Detail: c.manifest.Project.ID}
	}
	c.manifest.Project.ID = ids.Encode(ids.Project, table[project])
	raw := c.raw
	fresh := &archived{manifest: c.manifest, raw: map[string][]byte{}, releasePaths: c.releasePaths}
	for name, data := range raw {
		sec, ok := sectionNamed(name)
		if !ok || sec.store == nil {
			return &ImportError{Code: FailSectionUnsupported, Section: name}
		}
		rewritten := rewriteIDs(data, table)
		if err := sec.store(fresh, rewritten); err != nil {
			return err
		}
		fresh.raw[name] = rewritten
	}
	if c.sourceFiles != nil {
		files, err := rewriteRelease(c.sourceFiles, table)
		if err != nil {
			return err
		}
		fresh.sourceFiles = files
	}
	*c = *fresh
	return nil
}

// rewriteRelease returns the files of a release with its IDs written anew, and
// its hashes computed again, so that it is sealed as a build leaves one.
func rewriteRelease(files schemaversion.Files, table map[ids.UUID]ids.UUID) (schemaversion.Files, error) {
	out := make(schemaversion.Files, len(files))
	for p, data := range files {
		if schemaversion.IsJSON(p) {
			out[p] = rewriteIDs(data, table)
		} else {
			out[p] = data
		}
	}
	sealed, err := schemaversion.Seal(out)
	if err != nil {
		return nil, &ImportError{Code: FailReleaseInvalid, Check: "seal", Detail: err.Error()}
	}
	return sealed, nil
}
