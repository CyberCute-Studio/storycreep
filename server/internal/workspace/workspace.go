// Package workspace creates workspaces, changes their settings, lists the
// workspaces that a user belongs to, and resolves the role that a user acts
// with in one (PRD FR-TEN-01, §5.5).
//
// A workspace is the tenant boundary. Its creator becomes its owner, in the
// transaction that creates it (FR-TEN-01 AC2), and every account gets a
// personal workspace at its first sign-in, once (EnsurePersonal). A user
// switches between their workspaces by naming one by its ID, as the API's
// routes do: the server keeps no current workspace.
//
// A workspace's slug is unique on the instance, and has the form of a
// board's slug (FR-PROJ-02 AC4): groups of lowercase ASCII letters and
// digits, joined by single hyphens, at most 64 characters, and not one of
// the reserved words (FR-TEN-01 AC3), which CheckSlug lists.
//
// Where the instance sends email for certain (the EmailRequired capability,
// as in hosted mode), a user must have verified their email address before
// they can create a workspace, the personal one included (PRD FR-AUTH-03).
//
// A workspace's members are listed, and removed, in members.go, and a user
// joins one by accepting an invitation (package invite, Join).
//
// Every operation on a workspace resolves the user's role in it with
// role.Policy.Workspace, from whether the user is an instance admin and
// their membership. A user whose role is none, as one who is not a member,
// or a guest, is told that the workspace does not exist (ErrNotFound), so
// that nothing tells them that it does (FR-TEN-04 AC6). Package authz
// decides what the role allows (§5.6): changing the settings needs owner or
// admin.
package workspace

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/text/language"
	"golang.org/x/text/unicode/norm"

	"github.com/CyberCute-Studio/storycreep/server/internal/audit"
	"github.com/CyberCute-Studio/storycreep/server/internal/authz"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/entitlements"
	"github.com/CyberCute-Studio/storycreep/server/internal/i18n"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/input"
	"github.com/CyberCute-Studio/storycreep/server/internal/role"
	"github.com/CyberCute-Studio/storycreep/server/internal/scope"
)

// Limits of a workspace's fields.
const (
	// MaxNameLength is the most characters that a workspace's name may have.
	MaxNameLength = 100
	// MaxSlugLength is the most characters that a slug may have.
	MaxSlugLength = 64
)

var (
	// ErrNotFound is returned for a workspace that does not exist, or in
	// which the user's role is none, as it is for a user who is not a
	// member, or is a guest (§5.5).
	ErrNotFound = errors.New("workspace: no such workspace, or the user has no role in it")
	// ErrInsufficientRole is returned when the user's role in the workspace
	// does not allow the operation (§5.6), as package authz decides.
	ErrInsufficientRole = authz.ErrInsufficientRole
	// ErrSlugTaken is returned for a slug that another workspace has.
	ErrSlugTaken = errors.New("workspace: another workspace has the slug")
	// ErrLastOwner is returned for a change that would leave a workspace
	// without an owner whose account is active (FR-TEN-02 AC2).
	ErrLastOwner = errors.New("workspace: the change would leave the workspace without an owner")
	// ErrEmailUnverified is returned by Create for a user who has not
	// verified their email address, where the Service requires it.
	ErrEmailUnverified = errors.New("workspace: the user has not verified their email address")
)

// SlugReason says why a slug is not valid.
type SlugReason string

const (
	// SlugFormat is a slug that is not groups of lowercase ASCII letters and
	// digits joined by single hyphens, of at most MaxSlugLength characters.
	SlugFormat SlugReason = "format"
	// SlugReserved is a slug that is one of the reserved words.
	SlugReserved SlugReason = "reserved"
)

// SlugError is a slug that is not valid.
type SlugError struct {
	Reason SlugReason
}

func (e *SlugError) Error() string {
	if e.Reason == SlugReserved {
		return "workspace: the slug is a reserved word"
	}
	return fmt.Sprintf("workspace: a slug is groups of lowercase letters and digits joined by single hyphens, "+
		"of at most %d characters", MaxSlugLength)
}

// reserved are the words that no workspace's slug may be: the paths and
// names that the product uses or may use for itself, so that a workspace
// never looks like one of its pages, and the names that Windows reserves for
// devices, so that a slug is a file name on every platform, as an export's
// may be (FR-PROJ-02 AC4).
var reserved = []string{
	"about", "account", "accounts", "admin", "administrator", "api", "app", "apps", "assets", "auth", "billing",
	"blog", "changelog", "contact", "dashboard", "demo", "docs", "documentation", "download", "downloads",
	"export", "help", "healthz", "home", "import", "invite", "invites", "legal", "login", "logout", "me",
	"metrics", "new", "oauth", "openapi", "pricing", "privacy", "project", "projects", "readyz", "register",
	"release", "releases", "reset", "root", "security", "settings", "setup", "signin", "signout", "signup",
	"site", "static", "status", "storycreep", "support", "system", "terms", "user", "users", "verify",
	"version", "workspaces", "www",
	"aux", "con", "nul", "prn",
	"com1", "com2", "com3", "com4", "com5", "com6", "com7", "com8", "com9",
	"lpt1", "lpt2", "lpt3", "lpt4", "lpt5", "lpt6", "lpt7", "lpt8", "lpt9",
}

// CheckSlug returns a *SlugError if slug is not one that a workspace may
// have, and nil if it is.
func CheckSlug(slug string) error {
	if !wellFormed(slug) {
		return &SlugError{Reason: SlugFormat}
	}
	if slices.Contains(reserved, slug) {
		return &SlugError{Reason: SlugReserved}
	}
	return nil
}

// wellFormed reports whether slug has the form that the database checks:
// groups of lowercase ASCII letters and digits joined by single hyphens, of
// at most MaxSlugLength characters.
func wellFormed(slug string) bool {
	if slug == "" || len(slug) > MaxSlugLength || slug[0] == '-' || slug[len(slug)-1] == '-' ||
		strings.Contains(slug, "--") {
		return false
	}
	for _, c := range []byte(slug) {
		if !slugChar(c) && c != '-' {
			return false
		}
	}
	return true
}

func slugChar(c byte) bool {
	return 'a' <= c && c <= 'z' || '0' <= c && c <= '9'
}

// DB runs a transaction of the application login, as *database.DB does.
type DB interface {
	InTx(ctx context.Context, scope database.Scope, fn func(tx pgx.Tx) error) error
}

// Options configure a Service.
type Options struct {
	// Policy resolves the roles.
	Policy role.Policy
	// Entitlements check the members limit of the workspaces' plans
	// (FR-ENT-01), which every new membership adds to. It is required,
	// because a Service without it would let every workspace past its plan.
	Entitlements *entitlements.Service
	// PlanKey is the plan of a new workspace (FR-ENT-01).
	PlanKey string
	// DefaultLocale is the instance's default locale, which a workspace
	// whose settings name no locale that the server has a catalog for has.
	DefaultLocale language.Tag
	// RequireVerifiedEmail makes a user verify their email address before
	// they create a workspace, the personal one included (FR-AUTH-03), as
	// the EmailRequired capability does.
	RequireVerifiedEmail bool
}

// Service keeps the workspaces.
type Service struct {
	db     DB
	policy role.Policy
	ent    *entitlements.Service
	plan   string
	locale language.Tag
	// verified is Options.RequireVerifiedEmail.
	verified bool
}

// New returns a Service that keeps the workspaces in db, with opts. It
// panics if opts.Entitlements is nil.
func New(db DB, opts Options) *Service {
	if opts.Entitlements == nil {
		panic("workspace: a Service needs the entitlements that check its workspaces' members limit")
	}
	return &Service{db: db, policy: opts.Policy, ent: opts.Entitlements, plan: opts.PlanKey,
		locale: opts.DefaultLocale, verified: opts.RequireVerifiedEmail}
}

// Workspace is a workspace.
type Workspace struct {
	ID   ids.UUID
	Name string
	Slug string
	// DefaultLocale is the workspace's default locale, one that the server
	// has a catalog for.
	DefaultLocale        language.Tag
	CreatedAt, UpdatedAt time.Time
}

// Membership is a user's membership of a workspace.
type Membership struct {
	ID ids.UUID
	// Role is the membership's role, which may be guest.
	Role      role.Role
	Workspace Workspace
}

// settings is a workspace's settings, as the database holds them.
type settings struct {
	// DefaultLocale is the workspace's default locale, as a BCP 47 tag.
	DefaultLocale string `json:"default_locale"`
	// Retention is reserved for the workspace's retention settings, which a
	// later release adds with the jobs that apply them (FR-GDPR-03). It is
	// stored empty.
	Retention struct{} `json:"retention"`
}

// Settings are what a workspace is created with.
type Settings struct {
	Name string
	// Slug is the workspace's slug, or "" to derive one from Name.
	Slug string
	// DefaultLocale is the workspace's default locale, as a BCP 47 tag of
	// a locale that the server has a catalog for, or "" for the locale of
	// the request (i18n.FromContext).
	DefaultLocale string
}

// Patch is a change of a workspace's settings. A nil field stays as it is.
type Patch struct {
	Name, Slug, DefaultLocale *string
}

// Create creates a workspace with in, whose owner is the user userID, which
// client asks for, and records that in the audit log, in one transaction
// (FR-TEN-01 AC2). It returns ErrEmailUnverified if the Service requires a
// verified email address and the user has none, whatever in holds, then
// input.FieldErrors if the name or the default locale is not valid, then a
// *SlugError if the slug is not, and ErrSlugTaken if another workspace has
// it, and creates nothing then. A slug derived from the name is the first of
// the name's slug, then that with -2, -3, and so on, that no workspace has
// and that is not reserved. The owner is the workspace's first member, whom
// the members limit counts (FR-ENT-01), and every plan and every cap of the
// instance allows one, so Create returns an *entitlements.LimitError only if
// that ever stops being so.
func (s *Service) Create(ctx context.Context, userID ids.UUID, in Settings, client audit.Client) (Workspace,
	error) {
	if err := s.CheckVerified(ctx, userID); err != nil {
		return Workspace{}, err
	}
	d, err := s.Check(ctx, in)
	if err != nil {
		return Workspace{}, err
	}
	var w Workspace
	err = s.db.InTx(ctx, database.Scope{WorkspaceID: d.ID(), UserID: userID}, func(tx pgx.Tx) error {
		var err error
		w, err = s.Insert(ctx, tx, d, userID, false, client)
		return err
	})
	if err != nil {
		return Workspace{}, err
	}
	return w, nil
}

// CheckVerified returns ErrEmailUnverified if the Service requires a
// verified email address and the user userID has not verified theirs, or
// is not active, and nil otherwise, as Create does first, so that a route
// can refuse the user before it reads the request's body. A verified address
// stays verified, so the check need not hold in the transaction that creates
// the workspace.
func (s *Service) CheckVerified(ctx context.Context, userID ids.UUID) error {
	if !s.verified {
		return nil
	}
	var verified bool
	err := s.db.InTx(ctx, database.Scope{UserID: userID}, func(tx pgx.Tx) error {
		row, err := queries.New(tx).UserEmail(ctx, nullUUID(userID))
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		verified = row.Verified
		return err
	})
	switch {
	case err != nil:
		return fmt.Errorf("workspace: read whether the email address is verified: %w", err)
	case !verified:
		return ErrEmailUnverified
	}
	return nil
}

// Draft is a workspace to create, whose settings Check has checked.
type Draft struct {
	w     Workspace
	slugs iter.Seq[string]
}

// ID returns the ID of the workspace to create, which the transaction that
// creates it must be scoped to (Insert).
func (d Draft) ID() ids.UUID {
	return d.w.ID
}

// Check checks the settings in as Create does, and returns the workspace to
// create, with a new ID. It returns input.FieldErrors if the name or the
// default locale is not valid, and then a *SlugError if the slug is not.
func (s *Service) Check(ctx context.Context, in Settings) (Draft, error) {
	var errs input.FieldErrors
	name := strings.TrimSpace(in.Name)
	if msg := input.CheckName(name, MaxNameLength); msg != "" {
		errs = append(errs, input.FieldError{Field: "name", Message: msg})
	}
	locale, ok := i18n.FromContext(ctx).Locale(), true
	if in.DefaultLocale != "" {
		locale, ok = catalogLocale(in.DefaultLocale)
	}
	if !ok {
		errs = append(errs, input.FieldError{Field: "default_locale", Message: localeProblem})
	}
	if len(errs) > 0 {
		return Draft{}, errs
	}
	slugs := candidates(name)
	if in.Slug != "" {
		if err := CheckSlug(in.Slug); err != nil {
			return Draft{}, err
		}
		slugs = slices.Values([]string{in.Slug})
	}
	return Draft{w: Workspace{ID: ids.New(), Name: name, DefaultLocale: locale}, slugs: slugs}, nil
}

// Insert creates the workspace d in tx, a transaction of d's workspace, with
// the user userID as its owner, which client asks for, and records that in
// the audit log, personal saying whether it is the user's personal
// workspace, as the one that first-run setup creates is (FR-ADMIN-02). It
// returns ErrSlugTaken if another workspace has the slug, or every slug
// derived from the name.
func (s *Service) Insert(ctx context.Context, tx pgx.Tx, d Draft, userID ids.UUID, personal bool,
	client audit.Client) (Workspace, error) {
	return s.insert(ctx, tx, d.w, userID, d.slugs, personal, client)
}

// EnsurePersonal gives the account userID its personal workspace, at the
// first of its sign-ins that calls it, which client makes (FR-TEN-01): a
// workspace named after the account's display name, with a slug derived from
// it, and the request's locale (i18n.FromContext), which the account owns.
// It does nothing for an account that has had one, whatever it belongs to
// and whatever became of that workspace, or that is not active, or, where
// the Service requires a verified email address, that has not verified its
// own, which the first call after the verification then gives it. Two
// sign-ins at once give it one workspace.
func (s *Service) EnsurePersonal(ctx context.Context, userID ids.UUID, client audit.Client) error {
	w := Workspace{ID: ids.New(), DefaultLocale: i18n.FromContext(ctx).Locale()}
	return s.db.InTx(ctx, database.Scope{WorkspaceID: w.ID, UserID: userID}, func(tx pgx.Tx) error {
		name, err := queries.New(tx).ClaimPersonalWorkspace(ctx, queries.ClaimPersonalWorkspaceParams{
			ID: nullUUID(userID), RequireVerified: s.verified})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("workspace: mark the personal workspace: %w", err)
		}
		// A display name is checked as a workspace's name is, with the same
		// limit of characters.
		w.Name = name
		_, err = s.insert(ctx, tx, w, userID, candidates(name), true, client)
		return err
	})
}

// EmailVerified gives the account userID, whose email address client has
// just verified, the personal workspace that its first sign-in could not,
// where the Service requires a verified address (EnsurePersonal). Elsewhere
// it does nothing, because the first sign-in gives the workspace, whether
// or not the address is verified (FR-TEN-01).
func (s *Service) EmailVerified(ctx context.Context, userID ids.UUID, client audit.Client) error {
	if !s.verified {
		return nil
	}
	return s.EnsurePersonal(ctx, userID, client)
}

// insert adds the workspace w in tx, a transaction of w, with the first slug
// of slugs that no workspace has, and its owner, the user userID, and records
// that, personal saying whether it is the user's personal workspace. It
// returns ErrSlugTaken if every slug is taken.
func (s *Service) insert(ctx context.Context, tx pgx.Tx, w Workspace, userID ids.UUID, slugs iter.Seq[string],
	personal bool, client audit.Client) (Workspace, error) {
	doc, err := json.Marshal(settings{DefaultLocale: w.DefaultLocale.String()})
	if err != nil {
		return Workspace{}, fmt.Errorf("workspace: encode the settings: %w", err)
	}
	q := queries.New(tx)
	for slug := range slugs {
		n, err := q.InsertWorkspace(ctx, queries.InsertWorkspaceParams{ID: nullUUID(w.ID), Name: w.Name, Slug: slug,
			PlanKey: s.plan, Settings: doc})
		if err != nil {
			return Workspace{}, fmt.Errorf("workspace: add the workspace: %w", err)
		}
		if n == 1 {
			w.Slug = slug
			break
		}
	}
	if w.Slug == "" {
		return Workspace{}, ErrSlugTaken
	}
	if _, err := s.addMembership(ctx, tx, w.ID, userID, role.Owner, ids.UUID{}); err != nil {
		return Workspace{}, fmt.Errorf("workspace: add the owner's membership: %w", err)
	}
	row, err := q.Workspace(ctx, nullUUID(w.ID))
	if err != nil {
		return Workspace{}, fmt.Errorf("workspace: read the workspace: %w", err)
	}
	if err := audit.Record(ctx, tx, audit.Entry{Action: audit.ActionWorkspaceCreate, WorkspaceID: w.ID,
		ActorUserID: userID, SubjectID: w.ID, Result: audit.ResultSuccess, IP: client.IP,
		UserAgent: client.UserAgent, Metadata: map[string]any{"personal": personal}}); err != nil {
		return Workspace{}, err
	}
	return s.workspace(row.ID, row.Name, row.Slug, row.Settings, row.CreatedAt, row.UpdatedAt), nil
}

// randomSlugs is how many slugs with a random suffix candidates gives after
// the numbered ones.
const randomSlugs = 5

// numberedSlugs is how many numbered slugs candidates gives, the name's own
// slug first.
const numberedSlugs = 10

// candidates returns the slugs to try for a workspace named name, in order:
// the name's slug (baseSlug), unless it is reserved, then the same with -2,
// -3, and so on, cut to fit, and then a few with a random suffix, which some
// workspace is unlikely to have whatever the name.
func candidates(name string) iter.Seq[string] {
	base := baseSlug(name)
	return func(yield func(string) bool) {
		if CheckSlug(base) == nil && !yield(base) {
			return
		}
		for n := 2; n <= numberedSlugs; n++ {
			if !yield(withSuffix(base, fmt.Sprintf("-%d", n))) {
				return
			}
		}
		for range randomSlugs {
			if !yield(withSuffix(base, "-"+randomSuffix())) {
				return
			}
		}
	}
}

// withSuffix returns base with suffix, which starts with a hyphen, cut so
// that the slug has at most MaxSlugLength characters.
func withSuffix(base, suffix string) string {
	return strings.TrimRight(base[:min(len(base), MaxSlugLength-len(suffix))], "-") + suffix
}

// randomSuffix returns 8 random lowercase letters and digits: 40 bits.
func randomSuffix() string {
	const alphabet = "0123456789abcdefghjkmnpqrstvwxyz"
	b := make([]byte, 8)
	// crypto/rand.Read never fails; the program crashes if the operating
	// system's source does.
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = alphabet[b[i]%byte(len(alphabet))]
	}
	return string(b)
}

// folded are the lowercase letters that baseSlug spells in ASCII although
// Unicode does not decompose them into an ASCII letter and marks.
var folded = map[rune]string{
	'ß': "ss", 'æ': "ae", 'œ': "oe", 'ø': "o", 'ł': "l", 'đ': "d", 'ð': "d", 'þ': "th", 'ı': "i", 'ħ': "h",
}

// baseSlug returns the slug of a workspace named name: its letters and
// digits in lowercase ASCII, without their accents, as tomas-novak for Tomáš
// Novák, each run of other characters a hyphen, cut to MaxSlugLength
// characters at most, or "workspace" if that leaves nothing, as for a name
// in a script other than Latin.
func baseSlug(name string) string {
	var b strings.Builder
	gap := false
	add := func(s string) {
		if gap && b.Len() > 0 {
			b.WriteByte('-')
		}
		gap = false
		b.WriteString(s)
	}
	for _, r := range norm.NFKD.String(name) {
		r = unicode.ToLower(r)
		switch {
		case unicode.Is(unicode.Mn, r):
			// An accent of the letter before it.
		case r < unicode.MaxASCII && slugChar(byte(r)):
			add(string(r))
		case folded[r] != "":
			add(folded[r])
		default:
			gap = true
		}
	}
	slug := strings.TrimRight(b.String()[:min(b.Len(), MaxSlugLength)], "-")
	if slug == "" {
		return "workspace"
	}
	return slug
}

// localeProblem says what is wrong with a default locale that the server
// has no catalog for.
var localeProblem = "is not a locale that the server has a catalog for: " + strings.Join(localeNames(), ", ")

// localeNames returns the locales that the server has a catalog for.
func localeNames() []string {
	var names []string
	for _, l := range i18n.Default().Locales() {
		names = append(names, l.String())
	}
	return names
}

// catalogLocale returns the locale with a catalog that the BCP 47 tag v asks
// for (i18n.Bundle.Match), and false if v is not a tag or no catalog matches
// it.
func catalogLocale(v string) (language.Tag, bool) {
	tag, err := language.Parse(v)
	if err != nil {
		return language.Und, false
	}
	return i18n.Default().Match(tag)
}

// Get returns the workspace id, and the role that the user userID acts with
// in it, or ErrNotFound if it does not exist, the user's role is none, or the
// request is made with the API token tok, nil for a session, of another
// workspace (FR-AUTH-04 AC3). The token's scopes are the route's to check.
func (s *Service) Get(ctx context.Context, userID ids.UUID, tok *scope.Token, id ids.UUID) (Workspace, role.Role,
	error) {
	if tok != nil && !tok.InWorkspace(id) {
		return Workspace{}, "", ErrNotFound
	}
	var w Workspace
	var r role.Role
	err := s.db.InTx(ctx, database.Scope{WorkspaceID: id, UserID: userID}, func(tx pgx.Tx) error {
		q := queries.New(tx)
		row, err := q.Workspace(ctx, nullUUID(id))
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("workspace: read the workspace: %w", err)
		}
		member, err := q.MembershipRole(ctx, queries.MembershipRoleParams{WorkspaceID: nullUUID(id),
			UserID: nullUUID(userID)})
		if r, err = s.resolve(ctx, q, userID, member, err); err != nil {
			return err
		}
		w = s.workspace(row.ID, row.Name, row.Slug, row.Settings, row.CreatedAt, row.UpdatedAt)
		return nil
	})
	if err != nil {
		return Workspace{}, "", err
	}
	return w, r, nil
}

// resolve returns the role that the user userID acts with in the workspace
// of q's transaction, whose membership's role a query returned as member,
// with err, or ErrNotFound if it is none. It reads whether the user is an
// instance admin only if the policy lets an instance admin act as owner,
// because otherwise the answer changes nothing.
func (s *Service) resolve(ctx context.Context, q *queries.Queries, userID ids.UUID, member string, err error) (
	role.Role, error) {
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		member = string(role.None)
	case err != nil:
		return "", fmt.Errorf("workspace: read the membership: %w", err)
	}
	admin := false
	if s.policy.InstanceAdminAccess {
		admin, err = q.InstanceAdmin(ctx, nullUUID(userID))
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			// The account is no longer active.
			return "", ErrNotFound
		case err != nil:
			return "", fmt.Errorf("workspace: read the account: %w", err)
		}
	}
	r := s.policy.Workspace(admin, role.Role(member))
	if r == role.None {
		return "", ErrNotFound
	}
	return r, nil
}

// Update changes the settings of the workspace id as p says, which the user
// userID asks for, and returns the workspace and the user's role in it. It
// returns ErrNotFound if the workspace does not exist or the user's role in
// it is none, ErrInsufficientRole unless the role is owner or admin (§5.6),
// then input.FieldErrors if the name or the default locale is not valid, a
// *SlugError if the slug is not, and ErrSlugTaken if another workspace has
// it, and changes nothing then.
func (s *Service) Update(ctx context.Context, userID, id ids.UUID, p Patch) (Workspace, role.Role, error) {
	var w Workspace
	var r role.Role
	err := s.db.InTx(ctx, database.Scope{WorkspaceID: id, UserID: userID}, func(tx pgx.Tx) error {
		q := queries.New(tx)
		row, err := q.LockWorkspace(ctx, nullUUID(id))
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("workspace: read the workspace: %w", err)
		}
		member, err := q.LockMembershipRole(ctx, queries.LockMembershipRoleParams{WorkspaceID: nullUUID(id),
			UserID: nullUUID(userID)})
		if r, err = s.resolve(ctx, q, userID, member, err); err != nil {
			return err
		}
		if err := authz.Check(r, authz.ManageWorkspaceSettings); err != nil {
			return err
		}
		w = s.workspace(row.ID, row.Name, row.Slug, row.Settings, row.CreatedAt, row.UpdatedAt)
		changed, err := w.apply(p)
		if err != nil || !changed {
			return err
		}
		updated, err := q.UpdateWorkspace(ctx, queries.UpdateWorkspaceParams{ID: nullUUID(id), Name: w.Name,
			Slug: w.Slug, DefaultLocale: w.DefaultLocale.String()})
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "workspace_slug_key" {
			return ErrSlugTaken
		}
		if err != nil {
			return fmt.Errorf("workspace: change the workspace: %w", err)
		}
		w.UpdatedAt = updated.Time
		return nil
	})
	if err != nil {
		return Workspace{}, "", err
	}
	return w, r, nil
}

// apply changes w as p says, and reports whether that changed anything. It
// returns input.FieldErrors if the name or the default locale is not valid,
// and then a *SlugError if the slug is not.
func (w *Workspace) apply(p Patch) (bool, error) {
	next := *w
	var errs input.FieldErrors
	if p.Name != nil {
		next.Name = strings.TrimSpace(*p.Name)
		if msg := input.CheckName(next.Name, MaxNameLength); msg != "" {
			errs = append(errs, input.FieldError{Field: "name", Message: msg})
		}
	}
	if p.DefaultLocale != nil {
		var ok bool
		if next.DefaultLocale, ok = catalogLocale(*p.DefaultLocale); !ok {
			errs = append(errs, input.FieldError{Field: "default_locale", Message: localeProblem})
		}
	}
	if len(errs) > 0 {
		return false, errs
	}
	if p.Slug != nil && *p.Slug != w.Slug {
		if err := CheckSlug(*p.Slug); err != nil {
			return false, err
		}
		next.Slug = *p.Slug
	}
	changed := next.Name != w.Name || next.Slug != w.Slug || next.DefaultLocale != w.DefaultLocale
	*w = next
	return changed, nil
}

// List returns at most limit of the workspaces that the user userID is a
// member of, with each membership, guests' included, in the order of their
// IDs: the first, if after is zero, or else those after the workspace after.
// A request made with the API token tok, nil for a session, is shown the
// token's workspace alone, in which alone it acts (FR-AUTH-04).
func (s *Service) List(ctx context.Context, userID ids.UUID, tok *scope.Token, after ids.UUID, limit int) (
	[]Membership, error) {
	var only ids.UUID
	if tok != nil {
		only = tok.WorkspaceID
	}
	var out []Membership
	err := s.db.InTx(ctx, database.Scope{UserID: userID}, func(tx pgx.Tx) error {
		rows, err := queries.New(tx).UserWorkspaces(ctx, queries.UserWorkspacesParams{UserID: nullUUID(userID),
			After: nullUUID(after), OnlyWorkspace: nullUUID(only), MaxRows: int32(min(limit, 1<<20))})
		if err != nil {
			return fmt.Errorf("workspace: list the workspaces: %w", err)
		}
		for _, r := range rows {
			out = append(out, Membership{ID: r.MembershipID.Bytes, Role: role.Role(r.Role),
				Workspace: s.workspace(r.ID, r.Name, r.Slug, r.Settings, r.CreatedAt, r.UpdatedAt)})
		}
		return nil
	})
	return out, err
}

// workspace returns the workspace of a row's columns. A workspace whose
// settings name no locale that the server has a catalog for has the
// instance's default locale.
func (s *Service) workspace(id pgtype.UUID, name, slug string, doc []byte,
	created, updated pgtype.Timestamptz) Workspace {
	w := Workspace{ID: id.Bytes, Name: name, Slug: slug, DefaultLocale: s.locale, CreatedAt: created.Time,
		UpdatedAt: updated.Time}
	var set settings
	if json.Unmarshal(doc, &set) == nil {
		if locale, ok := catalogLocale(set.DefaultLocale); ok {
			w.DefaultLocale = locale
		}
	}
	return w
}

// BySlug returns the ID of the workspace with the slug, or ErrNotFound if
// there is none, for the operator, who names a workspace by its slug
// (storycreep grant-role). It resolves no role, so only the operator's
// commands may call it.
func (s *Service) BySlug(ctx context.Context, slug string) (ids.UUID, error) {
	var id ids.UUID
	err := s.db.InTx(ctx, database.Scope{}, func(tx pgx.Tx) error {
		row, err := queries.New(tx).WorkspaceBySlug(ctx, slug)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("workspace: read the workspace: %w", err)
		}
		id = row.Bytes
		return nil
	})
	return id, err
}

// Grant gives the user userID the role r in the workspace id, for the
// operator's storycreep grant-role (FR-ADMIN-06), whoever the operator is,
// so only the operator's commands may call it: it makes the user a member
// with the role, or changes the role of their membership to it, as
// ChangeRole does, the invitations that it revokes included, and records
// that in the audit log, without an actor. It returns the role that the
// membership had before, or role.None if the user was not a member, and
// leaves a membership that has r already as it is. It returns ErrNotFound if
// the workspace does not exist, ErrLastOwner if r is not owner and the user
// is the workspace's last owner whose account is active, and an
// *entitlements.LimitError if the user is not a member and the workspace has
// as many members as its plan allows (FR-ENT-01), and changes nothing then.
// The caller checks that the account is active, and that r is a role of a
// membership.
func (s *Service) Grant(ctx context.Context, id, userID ids.UUID, r role.Role) (role.Role, error) {
	previous := role.None
	err := s.db.InTx(ctx, database.Scope{WorkspaceID: id}, func(tx pgx.Tx) error {
		q := queries.New(tx)
		// The lock serializes the changes of a workspace's owners, so that two
		// cannot each demote one of its last two owners.
		_, err := q.LockWorkspace(ctx, nullUUID(id))
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("workspace: read the workspace: %w", err)
		}
		m, err := q.LockMembership(ctx, queries.LockMembershipParams{WorkspaceID: nullUUID(id),
			UserID: nullUUID(userID)})
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			mid, err := s.addMembership(ctx, tx, id, userID, r, ids.UUID{})
			if err != nil {
				return err
			}
			return audit.Record(ctx, tx, audit.Entry{Action: audit.ActionMembershipCreate, WorkspaceID: id,
				SubjectID: mid, Result: audit.ResultSuccess, Metadata: map[string]any{"role": r}})
		case err != nil:
			return fmt.Errorf("workspace: read the membership: %w", err)
		}
		previous = role.Role(m.Role)
		return setRole(ctx, tx, roleChange{workspaceID: id, membershipID: m.ID.Bytes, userID: userID,
			previous: previous, role: r})
	})
	if err != nil {
		return "", err
	}
	return previous, nil
}

// nullUUID returns id as a uuid column, null if id is zero.
func nullUUID(id ids.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: id != (ids.UUID{})}
}
