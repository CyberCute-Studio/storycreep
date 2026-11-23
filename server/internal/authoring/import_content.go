package authoring

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/jsonscan"
	"github.com/CyberCute-Studio/storycreep/server/internal/richtext"
	"github.com/CyberCute-Studio/storycreep/server/internal/schemacheck"
	"github.com/CyberCute-Studio/storycreep/server/internal/schemaversion"
	"github.com/CyberCute-Studio/storycreep/server/internal/snapshot"
	"github.com/CyberCute-Studio/storycreep/server/internal/validation"
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
	// held is the bytes of the JSON files that stay in memory for the whole
	// import, the manifest, the sections, and sourceFiles, which the files of a
	// release being read count on top of, within maxJSONTotal.
	held int64
}

// archiveSchemas are the compiled schemas of the format's JSON files, by their
// paths in an archive.
type archiveSchemas struct {
	// whole are the schemas of the files that are not lists of items, the
	// manifest and the project, which are validated whole.
	whole map[string]*jsonschema.Schema
	// items are the schemas of one item of each section that is a list, as
	// its file is validated an item at a time (checkItems).
	items map[string]*jsonschema.Schema
}

// fileSchemas compiles the schemas of the format's JSON files once.
var fileSchemas = sync.OnceValues(func() (*archiveSchemas, error) {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(Schema()))
	if err != nil {
		return nil, fmt.Errorf("authoring: read the format's schema: %w", err)
	}
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	if err := c.AddResource(SchemaID, doc); err != nil {
		return nil, fmt.Errorf("authoring: load the format's schema: %w", err)
	}
	compile := func(p string) (*jsonschema.Schema, error) {
		sch, err := c.Compile(SchemaID + "#/properties/" + strings.ReplaceAll(p, "/", "~1"))
		if err != nil {
			return nil, fmt.Errorf("authoring: compile the schema of %s: %w", p, err)
		}
		return sch, nil
	}
	out := &archiveSchemas{whole: map[string]*jsonschema.Schema{}, items: map[string]*jsonschema.Schema{}}
	if out.whole[ManifestPath], err = compile(ManifestPath); err != nil {
		return nil, err
	}
	for _, s := range sections {
		sch, err := compile(s.Path)
		if err != nil {
			return nil, err
		}
		if !s.list {
			out.whole[s.Path] = sch
			continue
		}
		// The file's schema is a reference to its Items[T], whose one property
		// is the array of the items.
		if sch.Ref == nil || sch.Ref.Properties["items"] == nil || sch.Ref.Properties["items"].Items2020 == nil {
			return nil, fmt.Errorf("authoring: the schema of %s is not of a list of items", s.Path)
		}
		out.items[s.Path] = sch.Ref.Properties["items"].Items2020
	}
	schemacheck.FailFast("", slices.Concat(slices.Collect(maps.Values(out.whole)),
		slices.Collect(maps.Values(out.items)))...)
	return out, nil
})

// checkSchema validates a JSON file of the archive against its schema
// (NFR-SEC-13), before anything of it is read: a section that is a list an
// item at a time (checkItems), and any other file whole, which its read has
// held to maxPlainJSON and maxItemValues.
func checkSchema(path, section string, data []byte) error {
	schemas, err := fileSchemas()
	if err != nil {
		return err
	}
	if item, ok := schemas.items[path]; ok {
		return checkItems(item, section, data)
	}
	sch, ok := schemas.whole[path]
	if !ok {
		return &ImportError{Code: FailSectionUnsupported, Section: section}
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return &ImportError{Code: FailSchemaInvalid, Section: section, Check: "json", Detail: err.Error()}
	}
	if err := schemacheck.CheckDepth(data); err != nil {
		return &ImportError{Code: FailArchiveInvalid, Section: section, Check: "json_depth", Detail: err.Error()}
	}
	if err := schemacheck.CheckKeys(data, inst); err != nil {
		return &ImportError{Code: FailSchemaInvalid, Section: section, Check: "duplicate_key", Detail: err.Error()}
	}
	if err := sch.Validate(inst); err != nil {
		return &ImportError{Code: FailSchemaInvalid, Section: section, Check: "schema", Detail: schemaDetail(err)}
	}
	return nil
}

// checkItems validates the file of a section that is a list, which is an
// object of the one key "items", an array, against item, the schema of one of
// them, so that no tree of the whole file is built. Each item's tree lasts as
// long as its check, is held to maxItemBytes and maxItemValues before it is
// built, and the first item that fails ends the check.
func checkItems(item *jsonschema.Schema, section string, data []byte) error {
	fail := func(check, format string, args ...any) error {
		return &ImportError{Code: FailSchemaInvalid, Section: section, Check: check,
			Detail: fmt.Sprintf(format, args...)}
	}
	// What follows walks the document by its punctuation, which is sound only
	// of valid JSON.
	if !json.Valid(data) {
		return fail("json", "%v", json.Unmarshal(data, &struct{}{}))
	}
	if err := schemacheck.CheckDepth(data); err != nil {
		return &ImportError{Code: FailArchiveInvalid, Section: section, Check: "json_depth", Detail: err.Error()}
	}
	if data[jsonscan.SkipSpace(data, 0)] != '{' {
		return fail("schema", "the file is not an object")
	}
	found := false
	err := jsonscan.Members(data, func(key, value []byte) error {
		// Spelled exactly, as a decoder would also take another case of the
		// key, or an escape of it, and the last of two.
		if string(key) != `"items"` || found {
			return fail("schema", `the file's one key is "items"`)
		}
		found = true
		if value[0] != '[' {
			return fail("schema", `"items" is not an array`)
		}
		return jsonscan.Elements(value, func(n int, one []byte) error {
			if len(one) > maxItemBytes {
				return &ImportError{Code: FailArchiveInvalid, Section: section, Check: "item_size",
					Detail: fmt.Sprintf("item %d has %d bytes, more than the %d that an item may", n, len(one),
						maxItemBytes)}
			}
			if values := jsonscan.Values(one); values > maxItemValues {
				return &ImportError{Code: FailArchiveInvalid, Section: section, Check: "item_values",
					Detail: fmt.Sprintf("item %d holds %d JSON values, more than the %d that an item may", n, values,
						maxItemValues)}
			}
			inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(one))
			if err != nil {
				return fail("json", "item %d: %v", n, err)
			}
			if err := schemacheck.CheckKeys(one, inst); err != nil {
				return fail("duplicate_key", "item %d: %v", n, err)
			}
			if err := item.Validate(inst); err != nil {
				return fail("schema", "item %d: %s", n, schemaDetail(err))
			}
			return nil
		})
	})
	if err != nil {
		return err
	}
	if !found {
		return fail("schema", `the file has no "items"`)
	}
	return nil
}

// schemaDetail says where the first failure of a validation is, without the
// message of the whole error, which lists every value that failed.
func schemaDetail(err error) string {
	return firstLine(schemacheck.Failure(err))
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
	data, err := a.readJSON(ManifestPath, maxPlainJSON, maxItemValues)
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
	c := &archived{raw: map[string][]byte{}, held: int64(len(data))}
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
		if sec.AssetsOnly && !c.manifest.IncludesAssets {
			return nil, &ImportError{Code: FailSchemaInvalid, Section: entry.Name, Check: "includes_assets",
				Detail: "the manifest says that the archive holds no assets, and lists a section of them"}
		}
		if sec.DraftOnly && c.manifest.Source.Kind != "draft" {
			return nil, &ImportError{Code: FailSchemaInvalid, Section: entry.Name, Check: "source",
				Detail: "only an archive of a draft holds the section"}
		}
		if err := a.reserve(&c.held, sec.Path, "its sections"); err != nil {
			return nil, err
		}
		// A list is validated an item at a time, and any other file whole.
		limit, values := int64(maxJSONMember), maxJSONValues
		if !sec.list {
			limit, values = maxPlainJSON, maxItemValues
		}
		file, err := a.readJSON(sec.Path, limit, values)
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
	// Each release and each asset is listed once, before any is read, so that
	// an index of the same entry many times over cannot have one member read
	// and verified as often, which the guards count once.
	c.releasePaths = make([]string, len(c.releases))
	releaseIDs := make(map[ids.UUID]bool, len(c.releases))
	releasePaths := make(map[string]bool, len(c.releases))
	for i, rel := range c.releases {
		if !strings.HasPrefix(rel.Path, "releases/") || !strings.HasSuffix(rel.Path, "/") ||
			strings.Count(rel.Path, "/") != 2 {
			return nil, &ImportError{Code: FailSchemaInvalid, Section: "releases", Check: "path",
				Detail: fmt.Sprintf("release %s has the path %q", rel.Version, rel.Path)}
		}
		id, err := ids.Decode(ids.Snapshot, rel.ID)
		if err != nil {
			return nil, &ImportError{Code: FailContentInvalid, Section: "releases", Check: "id",
				Detail: fmt.Sprintf("%q is no %s ID", rel.ID, ids.Snapshot)}
		}
		if releaseIDs[id] || releasePaths[rel.Path] {
			return nil, &ImportError{Code: FailContentInvalid, Section: "releases", Check: "duplicate_release",
				Version: rel.Version, Detail: fmt.Sprintf("the index lists %s or %q twice", rel.ID, rel.Path)}
		}
		releaseIDs[id], releasePaths[rel.Path] = true, true
		if _, err := storedReport(rel.ValidationReport); err != nil {
			return nil, &ImportError{Code: FailReleaseInvalid, Version: rel.Version, Check: "validation_report",
				Detail: firstLine(err.Error())}
		}
		if _, err := storedWarnings(rel.AcknowledgedWarnings); err != nil {
			return nil, &ImportError{Code: FailReleaseInvalid, Version: rel.Version, Check: "acknowledged_warnings",
				Detail: firstLine(err.Error())}
		}
		if _, err := storedCounts(rel.Counts); err != nil {
			return nil, &ImportError{Code: FailReleaseInvalid, Version: rel.Version, Check: "counts",
				Detail: firstLine(err.Error())}
		}
		c.releasePaths[i] = rel.Path
		for _, name := range a.under(rel.Path) {
			known[name] = true
		}
	}
	if err := c.checkTimes(); err != nil {
		return nil, err
	}
	if len(c.assets) > maxImportAssets {
		return nil, &ImportError{Code: FailAssetInvalid, Section: "assets", Check: "count",
			Detail: fmt.Sprintf("it holds %d assets, more than the %d that an import takes", len(c.assets),
				maxImportAssets)}
	}
	assetPaths := make(map[string]bool, len(c.assets))
	for _, as := range c.assets {
		if !strings.HasPrefix(as.Path, "assets/") {
			return nil, &ImportError{Code: FailAssetInvalid, Section: "assets", Check: "path", Detail: as.Path}
		}
		if assetPaths[as.Path] {
			return nil, &ImportError{Code: FailAssetInvalid, Section: "assets", Check: "duplicate_path", Detail: as.Path}
		}
		assetPaths[as.Path] = true
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
// character, each hexadecimal escape of a backslash as its two-character
// escape, which is the one spelling that rewriteEscapedIDs reads, and anything
// else as it is. An ID is found by its spelling in the file (idToken), and a
// parser reads an escaped underscore as an underscore, so that an ID left
// escaped would be neither mapped nor rewritten, and would still name its row
// once the document is read.
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
			r, err := strconv.ParseUint(string(data[i+2:i+6]), 16, 16)
			switch {
			case err != nil:
			case idByte(r):
				out = append(out, byte(r))
				i += 5
				continue
			case r == '\\':
				out = append(out, '\\', '\\')
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

// escapedToken finds a run of what an ID is written in, of which at least one
// character is an escape of an expression's string literal, a backslash, u, and
// four digits, which a JSON file holds with its backslash escaped, after the
// byte before the run. That byte is no backslash, as the run would otherwise
// begin inside an escaped backslash, which is no escape of a character.
var escapedToken = regexp.MustCompile(`[^\x5c](?:[0-9A-Za-z_]|\x5c\x5cu00[0-9A-Fa-f]{2})*` +
	`\x5c\x5cu00[0-9A-Fa-f]{2}(?:[0-9A-Za-z_]|\x5c\x5cu00[0-9A-Fa-f]{2})*`)

// rewriteEscapedIDs returns data with each ID of table that an expression's
// string literal spells with escapes, as the lexer reads it, written as its new
// ID in its canonical spelling, and anything else as it is. plainIDs cannot
// undo these, as the same bytes are plain text in any other string.
func rewriteEscapedIDs(data []byte, table map[ids.UUID]ids.UUID) []byte {
	if !bytes.Contains(data, []byte{'\\', '\\', 'u', '0', '0'}) {
		return data
	}
	return escapedToken.ReplaceAllFunc(data, func(match []byte) []byte {
		// The character before the run, which may take more than a byte. One
		// that an ID is written in makes the run the end of a longer word,
		// which is no ID, as idToken's boundary has it.
		first, before := utf8.DecodeRune(match)
		if first < utf8.RuneSelf && idByte(uint64(first)) {
			return match
		}
		// As long as the longest that idToken finds, so that a run of any
		// length takes no memory to read.
		var plain [30]byte
		n := 0
		for i := before; i < len(match); i++ {
			if n == len(plain) {
				return match
			}
			c := match[i]
			if c == '\\' {
				r, err := strconv.ParseUint(string(match[i+3:i+7]), 16, 16)
				if err != nil || !idByte(r) {
					return match
				}
				c = byte(r)
				i += 6
			}
			plain[n] = c
			n++
		}
		kind, id, err := ids.DecodeAny(string(plain[:n]))
		if err != nil {
			return match
		}
		// Of the kinds that a remap rewrites, and not a person's ID that spells
		// the UUID of a row.
		fresh, ok := table[id]
		if !ok || !idToken.Match(plain[:n]) {
			return match
		}
		return append(slices.Clone(match[:before]), ids.Encode(kind, fresh)...)
	})
}

// rewriteIDs returns data with each ID that table has written as its new ID,
// in its canonical spelling, and anything else as it is.
func rewriteIDs(data []byte, table map[ids.UUID]ids.UUID) []byte {
	return idToken.ReplaceAllFunc(rewriteEscapedIDs(data, table), func(tok []byte) []byte {
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

// storedReport reads a release's validation report as the API reads it
// (snapshot.Service.Report), so that a report that it could not read, as one
// with a number past a float64, is the archive's failure, and what names a
// person here is what names one there, whatever the case of its keys. Its
// numbers stay as they are written.
func storedReport(raw []byte) (validation.Stored, error) {
	var s validation.Stored
	if text := bytes.TrimSpace(raw); len(text) == 0 || text[0] != '{' {
		return s, errors.New("the validation report is no JSON object")
	}
	if err := json.Unmarshal(raw, &validation.Stored{}); err != nil {
		return s, fmt.Errorf("the validation report: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&s); err != nil {
		return s, fmt.Errorf("the validation report: %w", err)
	}
	if s.Findings == nil {
		s.Findings = []validation.StoredFinding{}
	}
	return s, nil
}

// storedWarnings returns a release's acknowledged warnings as the type that
// its build reads them with writes them, so that the row holds what was
// checked, whichever of two spellings of a key a later read would take.
func storedWarnings(raw []byte) ([]byte, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return []byte("[]"), nil
	}
	acks := []validation.Acknowledgement{}
	if err := json.Unmarshal(raw, &acks); err != nil {
		return nil, fmt.Errorf("the acknowledged warnings: %w", err)
	}
	return json.Marshal(acks)
}

// storedCounts returns a release's entity counts as the type that the API
// reads them with writes them.
func storedCounts(raw []byte) ([]byte, error) {
	if text := bytes.TrimSpace(raw); len(text) == 0 || text[0] != '{' {
		return nil, errors.New("the counts are no JSON object")
	}
	var counts snapshot.EntityCounts
	if err := json.Unmarshal(raw, &counts); err != nil {
		return nil, fmt.Errorf("the counts: %w", err)
	}
	return json.Marshal(counts)
}

// checkTimes refuses a time of a row that an import writes as it is, which the
// API could not write back: JSON's times end at the year 9999, and an offset
// can put one that the archive spells within them outside them in UTC, which
// every read of the row answers in.
func (c *archived) checkTimes() error {
	bad := func(t time.Time) bool {
		_, err := t.UTC().MarshalJSON()
		return err != nil
	}
	badOf := func(t *time.Time) bool { return t != nil && bad(*t) }
	for _, x := range c.releases {
		check := ""
		switch {
		case bad(x.PublishedAt):
			check = "published_at"
		case bad(x.BuiltAt):
			check = "built_at"
		}
		if check != "" {
			return &ImportError{Code: FailReleaseInvalid, Version: x.Version, Check: check,
				Detail: "the time is outside the years 0 to 9999"}
		}
	}
	for _, x := range c.comments {
		if bad(x.CreatedAt) || badOf(x.EditedAt) || badOf(x.ResolvedAt) {
			return &ImportError{Code: FailContentInvalid, Section: "comments", Check: "time",
				Detail: x.ID + ": a time is outside the years 0 to 9999"}
		}
	}
	for _, x := range c.playStates {
		if bad(x.CreatedAt) {
			return &ImportError{Code: FailContentInvalid, Section: "play_states", Check: "time",
				Detail: x.ID + ": its time is outside the years 0 to 9999"}
		}
	}
	return nil
}

// reportUsersIn adds to set each user who a validation report says muted a
// finding. A report that cannot be read has been refused (readArchived).
func reportUsersIn(raw []byte, set map[ids.UUID]bool) {
	s, err := storedReport(raw)
	if err != nil {
		return
	}
	for _, f := range s.Findings {
		if f.Mute == nil {
			continue
		}
		if id, err := ids.Decode(ids.User, f.Mute.MutedBy); err == nil {
			set[id] = true
		}
	}
}

// rewriteReport returns the validation report raw with each finding's muted_by
// naming the account that table maps it to, and without one that names no one
// of the table, which is how the API reads an ID that is no account's. The
// report is always written anew, with its keys as the API reads them: jsonb
// sorts a document's keys, so that of two spellings of a key, the one that a
// read of the stored row takes need not be the one that was mapped here.
func rewriteReport(raw []byte, table map[ids.UUID]ids.UUID) ([]byte, error) {
	s, err := storedReport(raw)
	if err != nil {
		return nil, err
	}
	for _, f := range s.Findings {
		if f.Mute == nil {
			continue
		}
		id, err := ids.Decode(ids.User, f.Mute.MutedBy)
		fresh, ok := table[id]
		if err != nil || !ok {
			f.Mute.MutedBy = ""
			continue
		}
		f.Mute.MutedBy = ids.Encode(ids.User, fresh)
	}
	return json.Marshal(s)
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

// allIDs returns list with the IDs that the files of the archive's releases
// spell, sorted, each once. A release can hold what the draft no longer does,
// which no row has, and which the collision check has to see, and a remap to
// give a fresh ID too, or it would sort apart from the fresh IDs around it.
func allIDs(list []ids.UUID, spelled map[ids.UUID]bool) ([]ids.UUID, error) {
	set := maps.Clone(spelled)
	if set == nil {
		set = make(map[ids.UUID]bool, len(list))
	}
	for _, id := range list {
		set[id] = true
	}
	if len(set) > maxRemapIDs {
		return nil, tooManyIDs(maxRemapIDs)
	}
	return slices.SortedFunc(maps.Keys(set), func(a, b ids.UUID) int { return bytes.Compare(a[:], b[:]) }), nil
}

// addFileIDs adds to set every ID that the JSON files spell, and refuses files
// that take it past limit, or one that spells more than limit, so that neither
// the list of what a file spells nor the table grows with its size alone.
func addFileIDs(files schemaversion.Files, set map[ids.UUID]bool, limit int) error {
	for p, data := range files {
		if !schemaversion.IsJSON(p) {
			continue
		}
		tokens := idToken.FindAll(plainIDs(data), limit+1)
		if len(tokens) > limit {
			return tooManyIDs(limit)
		}
		for _, tok := range tokens {
			if _, id, err := ids.DecodeAny(string(tok)); err == nil {
				set[id] = true
			}
		}
		if len(set) > limit {
			return tooManyIDs(limit)
		}
	}
	return nil
}

// tooManyIDs is the refusal of an archive that holds more IDs than a remap
// takes.
func tooManyIDs(limit int) error {
	return invalidArchive("ids", "it holds more than the %d IDs that an import takes", limit)
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
	raw, assets, source := c.raw, c.assets, c.sourceFiles
	fresh := &archived{manifest: c.manifest, raw: make(map[string][]byte, len(raw)), releasePaths: c.releasePaths,
		held: c.held}
	// The rows of the old IDs are not read again, and each file of them goes
	// once it is rewritten, so that a remap holds about one copy of an
	// archive's JSON rather than two. A failure below ends the import.
	*c = archived{manifest: c.manifest, releasePaths: c.releasePaths, held: c.held}
	for name, data := range raw {
		sec, ok := sectionNamed(name)
		if !ok || sec.store == nil {
			return &ImportError{Code: FailSectionUnsupported, Section: name}
		}
		rewritten := rewriteIDs(data, table)
		delete(raw, name)
		if err := sec.store(fresh, rewritten); err != nil {
			return err
		}
		fresh.raw[name] = rewritten
	}
	// An asset's path is where the archive holds its file, which a remap does
	// not move, whatever the path spells.
	for i := range fresh.assets {
		fresh.assets[i].Path = assets[i].Path
	}
	if source != nil {
		files, err := rewriteRelease(source, table)
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
