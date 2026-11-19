// Package schemaversion is the server's registry of the versions of the
// published game format, its schemaVersion (PRD §6.4, FR-DATA-03, plan item
// 77, note N84): the versions that the server reads and validates
// (Validate), migrates a snapshot's files forward to, one version at a time
// (Migrate), and renders them at, down to the oldest, one version at a time
// (Render), and the range of them that it declares at /version (Supported),
// which a pull negotiates with the addon's (Negotiate, FR-SYNC-07).
//
// A migration (Version.Up) is a pure function of a snapshot's files at one
// version to its files at the next, which reports each value of their content
// that the next version could not hold as it was (Entry), so that it never
// loses one silently (FR-DATA-03 AC2), and Record stores what it reported in
// the project's migration log. A renderer (Version.Down) is a pure function
// of a snapshot's files at one version to its files at the version before,
// which fails with an *UnrepresentableError, naming each construct, where
// that version cannot express what they hold, and never writes what that
// version cannot express (AC3). Migrate and Render take only a snapshot that
// validates against its own version, since a migration or a renderer seals
// what it writes again (Seal), which would hide hashes that were not its
// files', and what they return validates against its version, as a consumer
// of it validates it (FR-SYNC-01 AC2).
//
// A release build reads version 1 alone, which package published's types are
// of. The test-only synthetic version, one above the newest real one (note
// N2), is compiled in only with the synthetic_schema build tag, by package
// schemaversion/synthetic, which registers it (Register); the repository's
// testdata/synthetic-schema holds its schemas and its fixture, which the
// TypeScript tests read too.
package schemaversion

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/CyberCute-Studio/storycreep/server/internal/published"
)

// Files are a snapshot's files, by their paths from the directory that holds
// them (§6.3), the manifest's among them, as package serializer writes them.
// No function of the package changes the Files that it is given.
type Files map[string][]byte

// Range is the versions from Min to Max, both included.
type Range struct {
	Min, Max int
}

// Contains reports whether v is one of the range's versions.
func (r Range) Contains(v int) bool {
	return r.Min <= v && v <= r.Max
}

// String returns the range as 1–2, or 1 for one version.
func (r Range) String() string {
	if r.Min == r.Max {
		return fmt.Sprint(r.Min)
	}
	return fmt.Sprintf("%d–%d", r.Min, r.Max)
}

// Version is a version of the published format that the server reads.
type Version struct {
	// Number is the version's schemaVersion.
	Number int
	// Validate checks a snapshot's files of the version as a consumer of it
	// does: each file against its JSON Schema, and what
	// schema/published/README.md says a consumer checks beyond the schemas,
	// its content hash among them.
	Validate func(Files) error
	// Up migrates a snapshot's files of the version before to this one, and
	// returns each value of their content that this version could not hold as
	// it was, in the order of their files' paths and, within a file, of the
	// file. It is nil for the oldest version that the server reads, which has
	// no version before it that it reads.
	Up func(Files) (Files, []Entry, error)
	// Down renders a snapshot's files of this version as the version before,
	// or fails with an *UnrepresentableError where that version cannot express
	// what they hold, as a construct that it does not have. It is nil for the
	// oldest version that the server reads, as Up is, and set for every
	// other, so that the server renders a snapshot at every version that it
	// reads, as it declares (Supported).
	Down func(Files) (Files, error)
}

// Entry is a value of a snapshot's content that a migration did not carry
// forward as it was, since the version that it migrated to cannot hold it:
// the path of the file that held it, the ID of the entity that held it, or ""
// for the file's own, its field, and its value before the migration, and
// after it, which is nil where the migration dropped the value. It is a
// member of the migration log's JSON array (Record) as its tags write it.
type Entry struct {
	File   string          `json:"file"`
	Entity string          `json:"entity,omitempty"`
	Field  string          `json:"field"`
	Before json.RawMessage `json:"before"`
	After  json.RawMessage `json:"after,omitempty"`
}

// Step is the migration of a snapshot's files from the version From to the
// next, To, and the entries that it reported, which may be none.
type Step struct {
	From, To int
	Entries  []Entry
}

// versions are the versions that the server reads, the oldest first, each
// the one after the one before it. A release build has version 1 alone; the
// synthetic version is registered after it (register).
var versions = []Version{v1}

// register adds v to the versions that the server reads, as the newest. It
// panics unless v is the version after the newest, with a validator, a
// migration from the version before it, and a renderer down to that version,
// since a registry that skipped a version would migrate no snapshot across
// the gap, and one without a renderer would declare a version that it
// renders no snapshot at.
func register(v Version) {
	newest := versions[len(versions)-1].Number
	if v.Number != newest+1 || v.Validate == nil || v.Up == nil || v.Down == nil {
		panic(fmt.Sprintf("schemaversion: version %d is registered after %d, or without a validator, a migration, "+
			"or a renderer", v.Number, newest))
	}
	versions = append(versions, v)
}

// Supported returns the versions that the server reads, the oldest to the
// newest, which it declares (/version), validates, migrates a snapshot to,
// and renders a snapshot at.
func Supported() Range {
	return Range{Min: versions[0].Number, Max: versions[len(versions)-1].Number}
}

// version returns the version n, or an *UnsupportedError where the server
// does not read it.
func version(n int) (*Version, error) {
	r := Supported()
	if !r.Contains(n) {
		return nil, &UnsupportedError{Version: n, Supported: r}
	}
	return &versions[n-r.Min], nil
}

// ErrUnsupported is what the errors of a version that the server does not
// read wrap: an *UnsupportedError and a *NoCommonVersionError, which the API
// answers as schema_unsupported, with the server's range (FR-SYNC-01 AC3).
var ErrUnsupported = errors.New("schema_unsupported")

// UnsupportedError is the error of a version, of a snapshot's files or asked
// for, that the server does not read.
type UnsupportedError struct {
	Version   int
	Supported Range
}

func (e *UnsupportedError) Error() string {
	return fmt.Sprintf("schemaversion: the server reads versions %s of the published format, and not %d",
		e.Supported, e.Version)
}

// Unwrap returns ErrUnsupported.
func (e *UnsupportedError) Unwrap() error { return ErrUnsupported }

// NoCommonVersionError is Negotiate's error for a client whose range has no
// version in common with the server's.
type NoCommonVersionError struct {
	Server, Client Range
}

// The sides of a pull, one of which is to be upgraded where they have no
// version in common (NoCommonVersionError.Upgrade).
const (
	UpgradeServer = "server"
	UpgradeClient = "client"
)

// Upgrade returns the side that is to be upgraded for the two to have a
// version in common: UpgradeServer where the client's oldest version is newer
// than the server's newest, and otherwise UpgradeClient, whose newest is
// older than the server's oldest (PRD §6.4, FR-SYNC-07 AC1, AC2).
func (e *NoCommonVersionError) Upgrade() string {
	if e.Client.Min > e.Server.Max {
		return UpgradeServer
	}
	return UpgradeClient
}

func (e *NoCommonVersionError) Error() string {
	return fmt.Sprintf("schemaversion: the server reads versions %s of the published format and the client %s, "+
		"so the %s is to be upgraded", e.Server, e.Client, e.Upgrade())
}

// Unwrap returns ErrUnsupported.
func (e *NoCommonVersionError) Unwrap() error { return ErrUnsupported }

// UnrepresentableError is Render's error for a snapshot's files that the
// version that it renders them down to cannot express: what of them, in the
// order of their files' paths and, within a file, of the file.
type UnrepresentableError struct {
	From, To   int
	Constructs []Construct
}

// Construct is a value of a snapshot's content that a version cannot
// express: the path of its file, the ID of its entity, or "" for the file's
// own, its field, and why, as a title of 250 characters, of which the version
// holds at most 200.
type Construct struct {
	File, Entity, Field string
	Why                 string
}

func (e *UnrepresentableError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "schemaversion: version %d of the published format cannot express what the snapshot holds at "+
		"version %d:", e.To, e.From)
	for i, c := range e.Constructs {
		if i > 0 {
			b.WriteString(";")
		}
		fmt.Fprintf(&b, " %s", c.File)
		if c.Entity != "" {
			fmt.Fprintf(&b, " %s", c.Entity)
		}
		fmt.Fprintf(&b, " %s: %s", c.Field, c.Why)
	}
	return b.String()
}

// Negotiate returns the newest version that the server, of the range that
// Supported returns, and a client of the range client both read, which a
// pull fetches a snapshot at (PRD §6.4, §7.5), as Range.Negotiate does.
func Negotiate(client Range) (int, error) {
	return Supported().Negotiate(client)
}

// Negotiate returns the newest version that a server of the range server and
// a client of the range client both read, min(server.Max, client.Max), or a
// *NoCommonVersionError, which says which side is to be upgraded, where that
// is older than either's oldest, so that they read none in common (PRD §6.4).
// It fails on a client's range that has no version.
func (server Range) Negotiate(client Range) (int, error) {
	if client.Min < 1 || client.Min > client.Max {
		return 0, fmt.Errorf("schemaversion: %d–%d is no range of versions", client.Min, client.Max)
	}
	v := min(server.Max, client.Max)
	if v < server.Min || v < client.Min {
		return 0, &NoCommonVersionError{Server: server, Client: client}
	}
	return v, nil
}

// VersionOf returns the schemaVersion of the manifest of f, as the reference
// runtime reads it (published.SchemaVersionOf): an integer however its JSON
// writes it, as 1 or 1.0, of at most published.MaxInt in magnitude. One that
// the server does not read, as 0, is a version all the same, which Validate,
// Migrate, and Render refuse with an *UnsupportedError, as a runtime refuses
// it as schema_unsupported (flow.ErrSchemaUnsupported).
func VersionOf(f Files) (int, error) {
	data, ok := f[published.ManifestPath]
	if !ok {
		return 0, fmt.Errorf("schemaversion: the snapshot has no %s", published.ManifestPath)
	}
	n, err := published.SchemaVersionOf(data)
	if err != nil {
		return 0, fmt.Errorf("schemaversion: %w", err)
	}
	return int(n), nil
}

// Validate checks f as a consumer of the version of its manifest does
// (Version.Validate), or fails with an *UnsupportedError where the server
// does not read that version.
func Validate(f Files) error {
	n, err := VersionOf(f)
	if err != nil {
		return err
	}
	v, err := version(n)
	if err != nil {
		return err
	}
	return v.Validate(f)
}

// Migrate migrates f, a snapshot's files of a version that the server reads,
// which validate against it, forward to the version to, which is no older,
// one version at a time, and returns the files, which validate against the
// version to, and each step that it took, with what the step reported, which
// Record stores in the project's migration log (FR-DATA-03 AC2). It returns f
// itself, and no step, where f is of the version to. It fails with an
// *UnsupportedError where the server does not read f's version or to, and
// with another error where to is older than f's, or where f does not
// validate against its version, whose hashes, among the rest, a migration
// would otherwise seal again as though they were its files' (validAt).
func Migrate(f Files, to int) (Files, []Step, error) {
	from, err := validAt(f, to)
	if err != nil {
		return nil, nil, err
	}
	if to < from {
		return nil, nil, fmt.Errorf("schemaversion: a migration goes forward, and not from version %d to %d", from,
			to)
	}
	if to == from {
		return f, nil, nil
	}
	target, _ := version(to)
	var steps []Step
	for n := from + 1; n <= to; n++ {
		v, _ := version(n)
		next, entries, err := v.Up(f)
		if err != nil {
			return nil, nil, fmt.Errorf("schemaversion: the migration from version %d to %d: %w", n-1, n, err)
		}
		if got, err := VersionOf(next); err != nil || got != n {
			return nil, nil, fmt.Errorf("schemaversion: the migration from version %d to %d wrote no snapshot of "+
				"version %d", n-1, n, n)
		}
		f = next
		steps = append(steps, Step{From: n - 1, To: n, Entries: entries})
	}
	if err := target.Validate(f); err != nil {
		return nil, nil, fmt.Errorf("schemaversion: the migration to version %d wrote a snapshot that does not "+
			"validate: %w", to, err)
	}
	return f, steps, nil
}

// Render returns f, a snapshot's files of a version that the server reads,
// which validate against it, rendered at the version to: f itself at its own
// version, as Migrate returns it; migrated forward to a newer one, as Migrate
// migrates it, with its steps; and rendered down to an older one, one version
// at a time, by each version's renderer (Version.Down), so that what it
// returns validates against the version to (FR-SYNC-01 AC2). It fails with an
// *UnrepresentableError where a version that it renders down to cannot
// express what f holds, writing nothing that that version cannot express
// (FR-DATA-03 AC3), with an *UnsupportedError where the server does not read
// f's version or to, and with another error where f does not validate
// against its version, as Migrate does.
func Render(f Files, to int) (Files, []Step, error) {
	if from, err := VersionOf(f); err == nil && to >= from {
		return Migrate(f, to)
	}
	from, err := validAt(f, to)
	if err != nil {
		return nil, nil, err
	}
	target, _ := version(to)
	for n := from; n > to; n-- {
		// Every version above the oldest has a renderer (register).
		v, _ := version(n)
		prev, err := v.Down(f)
		var unrepresentable *UnrepresentableError
		if errors.As(err, &unrepresentable) {
			// It names the versions, and each construct.
			return nil, nil, err
		}
		if err != nil {
			return nil, nil, fmt.Errorf("schemaversion: the rendering of version %d at %d: %w", n, n-1, err)
		}
		if got, err := VersionOf(prev); err != nil || got != n-1 {
			return nil, nil, fmt.Errorf("schemaversion: the renderer of version %d wrote no snapshot of version %d",
				n, n-1)
		}
		f = prev
	}
	if err := target.Validate(f); err != nil {
		return nil, nil, fmt.Errorf("schemaversion: the rendering at version %d wrote a snapshot that does not "+
			"validate: %w", to, err)
	}
	return f, nil, nil
}

// validAt returns the version of f, which validates against it, of the
// versions that the server reads, as to is, or fails with an
// *UnsupportedError where the server does not read f's version or to, and
// with another error where f does not validate against its version.
func validAt(f Files, to int) (int, error) {
	from, err := VersionOf(f)
	if err != nil {
		return 0, err
	}
	source, err := version(from)
	if err != nil {
		return 0, err
	}
	if _, err := version(to); err != nil {
		return 0, err
	}
	if err := source.Validate(f); err != nil {
		return 0, fmt.Errorf("schemaversion: the snapshot does not validate at its version %d: %w", from, err)
	}
	return from, nil
}
