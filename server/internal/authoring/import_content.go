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
	"strconv"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/richtext"
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
	data, err := a.readJSON(ManifestPath)
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
	held := int64(len(data))
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
		file, err := a.readJSON(sec.Path)
		if err != nil {
			return nil, err
		}
		if held += int64(len(file)); held > maxJSONTotal {
			return nil, invalidArchive("size", "its sections hold more than the %d bytes that an import reads",
				maxJSONTotal)
		}
		sum := sha256.Sum256(file)
		if hex.EncodeToString(sum[:]) != entry.SHA256 {
			return nil, &ImportError{Code: FailSchemaInvalid, Section: entry.Name, Check: "hash",
				Detail: "the file's SHA-256 is not the manifest's"}
		}
		if err := checkSchema(sec.Path, entry.Name, file); err != nil {
			return nil, err
		}
		file = plainIDs(file)
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

// plainIDs returns the JSON document data with each escape of a character that
// an ID is written in, a letter, a digit, or an underscore of ASCII, as that
// character, and anything else as it is. An ID is found by its spelling in the
// file (idToken), and a parser reads an escaped underscore as an underscore, so
// that an ID left escaped would be neither mapped nor rewritten, and would
// still name its row once the document is read.
func plainIDs(data []byte) []byte {
	if !bytes.Contains(data, []byte{'\\', 'u'}) {
		return data
	}
	out := make([]byte, 0, len(data))
	for i := 0; i < len(data); i++ {
		if data[i] != '\\' || i+1 == len(data) {
			out = append(out, data[i])
			continue
		}
		if data[i+1] == 'u' && i+5 < len(data) {
			if r, err := strconv.ParseUint(string(data[i+2:i+6]), 16, 16); err == nil && idByte(r) {
				out = append(out, byte(r))
				i += 5
				continue
			}
		}
		out = append(out, data[i], data[i+1])
		i++
	}
	return out
}

// idByte says whether r is a character that an ID is written in.
func idByte(r uint64) bool {
	return r == '_' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

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

// mentionsIn adds to set the user of each mention of a member that the comment
// body holds. A body that is not a document adds none, and its comment is
// refused when it is written.
func mentionsIn(body []byte, set map[ids.UUID]bool) {
	doc, err := richtext.Parse(body)
	if err != nil {
		return
	}
	for _, m := range doc.Members() {
		if id, err := ids.Decode(ids.User, m.User); err == nil {
			set[id] = true
		}
	}
}

// rewriteMentions returns the comment body with each mention of a member naming
// the account that table maps its user to, so that no ID from an archive can
// name an account of the instance that the workspace cannot see. What the
// body's text spells is its author's, and names nobody.
func rewriteMentions(body []byte, table map[ids.UUID]ids.UUID) ([]byte, error) {
	doc, err := richtext.Parse(body)
	if err != nil {
		return nil, err
	}
	var failure error
	doc.MapMembers(func(user string) string {
		id, err := ids.Decode(ids.User, user)
		if err != nil {
			failure = err
			return user
		}
		fresh, ok := table[id]
		if !ok {
			failure = fmt.Errorf("the mention of %s is of no one whom the archive names", user)
			return user
		}
		return ids.Encode(ids.User, fresh)
	})
	if failure != nil {
		return nil, failure
	}
	return json.Marshal(*doc)
}

// reportMutes returns the validation report raw as a tree, and the mute of each
// of its findings that names who muted it. A report that is not an object has
// none.
func reportMutes(raw []byte) (map[string]any, []map[string]any) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var doc map[string]any
	if err := dec.Decode(&doc); err != nil {
		return nil, nil
	}
	findings, _ := doc["findings"].([]any)
	var mutes []map[string]any
	for _, f := range findings {
		finding, _ := f.(map[string]any)
		mute, _ := finding["mute"].(map[string]any)
		if _, ok := mute["muted_by"].(string); ok {
			mutes = append(mutes, mute)
		}
	}
	return doc, mutes
}

// reportUsersIn adds to set each user who a validation report says muted a
// finding.
func reportUsersIn(raw []byte, set map[ids.UUID]bool) {
	_, mutes := reportMutes(raw)
	for _, mute := range mutes {
		if id, err := ids.Decode(ids.User, mute["muted_by"].(string)); err == nil {
			set[id] = true
		}
	}
}

// rewriteReport returns the validation report raw with each finding's muted_by
// naming the account that table maps it to, and without one that names no one
// of the table, which is how the API reads an ID that is no account's.
func rewriteReport(raw []byte, table map[ids.UUID]ids.UUID) ([]byte, error) {
	doc, mutes := reportMutes(raw)
	if len(mutes) == 0 {
		return raw, nil
	}
	for _, mute := range mutes {
		id, err := ids.Decode(ids.User, mute["muted_by"].(string))
		fresh, ok := table[id]
		if err != nil || !ok {
			delete(mute, "muted_by")
			continue
		}
		mute["muted_by"] = ids.Encode(ids.User, fresh)
	}
	return json.Marshal(doc)
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

// freshIDs returns a table of a fresh ID for each of list, which is sorted and
// holds each once, that keeps their order: a release's files hold their arrays
// sorted by ID (schema/published/README.md), and a project's list of releases
// is in the order of their IDs (package snapshot), which rewriting them in
// place then leaves as they were.
func freshIDs(list []ids.UUID) map[ids.UUID]ids.UUID {
	fresh := make([]ids.UUID, len(list))
	for i := range fresh {
		fresh[i] = ids.New()
	}
	slices.SortFunc(fresh, func(a, b ids.UUID) int { return bytes.Compare(a[:], b[:]) })
	table := make(map[ids.UUID]ids.UUID, len(list))
	for i, id := range list {
		table[id] = fresh[i]
	}
	return table
}

// remapIDs returns list with every ID that the files of the archive's releases
// spell, sorted, each once. A release can hold what the draft no longer does,
// which no row has, and which has to take a fresh ID too, or it would sort
// apart from the fresh IDs around it.
func (c *archived) remapIDs(a *archive, list []ids.UUID) ([]ids.UUID, error) {
	set := make(map[ids.UUID]bool, len(list))
	for _, id := range list {
		set[id] = true
	}
	add := func(files schemaversion.Files) {
		for p, data := range files {
			if !schemaversion.IsJSON(p) {
				continue
			}
			for _, tok := range idToken.FindAll(plainIDs(data), -1) {
				if _, id, err := ids.DecodeAny(string(tok)); err == nil {
					set[id] = true
				}
			}
		}
	}
	add(c.sourceFiles)
	for _, dir := range c.releasePaths {
		files, err := releaseFiles(a, dir)
		if err != nil {
			return nil, err
		}
		add(files)
	}
	return slices.SortedFunc(maps.Keys(set), func(a, b ids.UUID) int { return bytes.Compare(a[:], b[:]) }), nil
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
			out[p] = rewriteIDs(plainIDs(data), table)
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
