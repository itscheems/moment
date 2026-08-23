package domain

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxDisplayNameLength is the code-point ceiling a display name is stored with.
//
// Code points rather than bytes, because that is what the users.display_name
// CHECK counts: SQLite's length() on TEXT returns characters. Measuring bytes
// here made Go the stricter of the two for any non-Latin name -- a
// twenty-seven character Chinese name is eighty-one bytes -- so the two agreed
// only for ASCII.
const MaxDisplayNameLength = 80

// MaxBioLength bounds the free-text line an account may publish about itself.
//
// 500 rather than a rounder number because that is what the site bio has always
// been, and the same field is now edited through two routes -- the account's own
// profile and the owner's site settings. A tighter bound here would have meant
// an owner whose bio predates accounts could read it but never save it again,
// and an archive carrying one could import but not round-trip.
const MaxBioLength = 500

// UsernameMinLength and UsernameMaxLength bound the handle an account is
// reached at. Both halves of the range are enforced by the database too; the
// duplication is deliberate, because a value that reaches SQLite malformed
// produces a constraint error rather than a message a person can act on.
const (
	UsernameMinLength = 3
	UsernameMaxLength = 30
)

// UserRole separates the single account that owns the site from everyone else.
//
// The role lives on the account rather than on anything the account writes.
// Storing it beside each comment would freeze a fact that can change: an
// account promoted later would keep rendering under its old role on every row
// written before the promotion.
type UserRole string

const (
	// UserRoleOwner is the account that owns the site. Exactly one exists,
	// and the database enforces that with a partial unique index rather than
	// trusting every write path to remember.
	UserRoleOwner UserRole = "owner"
	// UserRoleMember is any other account.
	UserRoleMember UserRole = "member"
)

// AccountStatus is what an account is currently allowed to do.
type AccountStatus string

const (
	// AccountPending is where a new account lands while registration_mode is
	// "approval": signed in, able to read and to edit its own profile, unable
	// to write anything another person will see.
	AccountPending AccountStatus = "pending"
	// AccountActive is a fully admitted account.
	AccountActive AccountStatus = "active"
	// AccountSuspended cannot sign in at all. An existing session stops
	// working at its next request rather than at its next expiry.
	AccountSuspended AccountStatus = "suspended"
)

// RegistrationMode decides what happens when an unrecognised external identity
// finishes signing in.
type RegistrationMode string

const (
	// RegistrationOpen admits new accounts directly as active.
	RegistrationOpen RegistrationMode = "open"
	// RegistrationApproval admits them as pending, to be reviewed.
	RegistrationApproval RegistrationMode = "approval"
	// RegistrationClosed refuses them; only accounts that already exist can
	// sign in.
	RegistrationClosed RegistrationMode = "closed"
)

// User is one account.
type User struct {
	ID            string
	Username      string
	DisplayName   string
	Bio           string
	AvatarMediaID *string
	CoverMediaID  *string
	Role          UserRole
	Status        AccountStatus
	// UsernameSetAt is nil while the handle is still the one generated from a
	// provider's display name. It is what the one-time "pick a username"
	// prompt keys off, which is why dismissing that prompt still stamps it.
	UsernameSetAtMS *int64
	CreatedAtMS     int64
	UpdatedAtMS     int64
}

// CanWritePublicContent reports whether this account may publish anything
// another visitor will read.
//
// It exists so the answer has one definition. Spreading the same comparison
// across each write path is how one of them ends up missing it.
func (user User) CanWritePublicContent() bool {
	return user.Status == AccountActive
}

// CanSignIn reports whether a credential belonging to this account should be
// honoured at all.
func (user User) CanSignIn() bool {
	return user.Status == AccountPending || user.Status == AccountActive
}

// IsOwner reports whether this account owns the site.
func (user User) IsOwner() bool {
	return user.Role == UserRoleOwner
}

// Identity is one external account that can sign in as a User.
//
// The pair (Provider, ProviderUserID) is the natural key. Email is never part
// of it: addresses change hands, are sometimes absent, and matching on one is
// the account-takeover path this model refuses to open.
type Identity struct {
	Provider         string
	ProviderUserID   string
	UserID           string
	Email            *string
	EmailVerified    bool
	EmailDeliverable bool
	DisplayName      string
	// AvatarURL is kept as a hint for a later capture attempt, never as a
	// render source. Rendering reads managed media only.
	AvatarURL     *string
	CreatedAtMS   int64
	LastLoginAtMS int64
}

// zeroUserID is what the legacy identity fallback synthesises when every
// identity field is blank. It is well-formed hex, so nothing but an explicit
// comparison keeps it from being mistaken for a real account.
const zeroUserID = "00000000000000000000000000000000"

// ValidateUserID accepts the 32 lowercase hex digits an account identifier is
// written with.
func ValidateUserID(value string) error {
	if len(value) != 32 {
		return NewError(CodeInvalidArgument, "account id must be 32 hex digits", map[string]any{"field": "user_id"})
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return NewError(CodeInvalidArgument, "account id must be lowercase hexadecimal", map[string]any{"field": "user_id"})
		}
	}
	if value == zeroUserID {
		return NewError(CodeInvalidArgument, "account id must not be the placeholder identity", map[string]any{"field": "user_id"})
	}
	return nil
}

// reservedUsernames are the handles an account may not take because something
// else already answers at that path.
//
// Profiles live at /<username>, so a handle shares a namespace with every
// top-level route and every bundled asset directory. A name that collides does
// not render badly -- it never renders at all, because the route table or the
// asset handler answers first. That makes this a write-time constraint on
// creating a broken account, not a matter of taste.
//
// The list is checked on write and never on read. An existing account whose
// handle is added here later keeps working; doctor reports the collision so a
// person can decide, which is the opposite of invalidating accounts whenever
// the list grows.
var reservedUsernames = map[string]struct{}{
	// Server routes: /healthz, /readyz, and everything under /api.
	"api": {}, "healthz": {}, "readyz": {},
	// Browser routes declared in web/src/app/routes.ts. "owner" is absent on
	// purpose: only /owner/<something> is a route, bare /owner is not, and it
	// is the handle a fresh install ships with -- reserving the default would
	// mean every new site starts out flagging itself.
	"search": {}, "archive": {}, "profile": {},
	// Bundled asset directories under internal/webassets/dist.
	"assets": {}, "fonts": {}, "icons": {},
	// Held back so the paths stay available without renaming anyone later.
	"about": {}, "account": {}, "admin": {}, "auth": {}, "comment": {},
	"comments": {}, "delete": {}, "edit": {}, "favicon": {}, "feed": {},
	"help": {}, "login": {}, "logout": {}, "manifest": {}, "me": {},
	"media": {}, "moment": {}, "moments": {}, "new": {}, "oauth": {},
	"post": {}, "posts": {}, "public": {}, "register": {}, "robots": {},
	"root": {}, "rss": {}, "settings": {}, "signup": {}, "sitemap": {},
	"static": {}, "support": {}, "user": {}, "users": {}, "www": {},
}

// UsernameReserved reports whether a handle is one the site answers at itself.
func UsernameReserved(value string) bool {
	_, reserved := reservedUsernames[strings.ToLower(value)]
	return reserved
}

// NormalizeUsername lowercases and validates a handle, including the reserved
// list. It is the only gate new handles pass through.
func NormalizeUsername(value string) (string, error) {
	value = strings.TrimSpace(strings.ToLower(value))
	if err := validateUsernameShape(value); err != nil {
		return "", err
	}
	if UsernameReserved(value) {
		return "", NewError(CodeUsernameReserved, "that username is reserved by the site", map[string]any{"field": "username"})
	}
	return value, nil
}

// validateUsernameShape enforces the character rules without consulting the
// reserved list, so stored handles can be re-validated without a list that
// grew after they were created rejecting them.
func validateUsernameShape(value string) error {
	if len(value) < UsernameMinLength || len(value) > UsernameMaxLength {
		return NewError(CodeInvalidArgument, "username must be between 3 and 30 characters", map[string]any{"field": "username"})
	}
	for index, character := range value {
		switch {
		case character >= 'a' && character <= 'z':
		case character >= '0' && character <= '9', character == '_':
			if index == 0 {
				return NewError(CodeInvalidArgument, "username must start with a letter", map[string]any{"field": "username"})
			}
		default:
			return NewError(CodeInvalidArgument, "username may contain only lowercase letters, digits and underscores", map[string]any{"field": "username"})
		}
	}
	return nil
}

// ValidateStoredUsername checks only the shape, for values that are already in
// the database.
func ValidateStoredUsername(value string) error { return validateUsernameShape(value) }

// NormalizeDisplayName trims surrounding whitespace and rejects control
// characters, matching how comment author names are handled.
//
// The match has to be exact, not approximate. CommentService stamps this value
// onto every comment the account writes, where NormalizeCommentAuthorName
// refuses any unicode.IsControl rune -- so a display name this function admits
// and that one refuses is an account that can be saved and can never comment,
// under an error naming a field its owner never typed.
func NormalizeDisplayName(value string) (string, error) {
	if !utf8.ValidString(value) {
		return "", NewError(CodeInvalidArgument, "display name must be valid UTF-8", map[string]any{"field": "display_name"})
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", NewError(CodeInvalidArgument, "display name must not be empty", map[string]any{"field": "display_name"})
	}
	if utf8.RuneCountInString(value) > MaxDisplayNameLength {
		return "", NewError(CodeInvalidArgument, "display name is too long", map[string]any{"field": "display_name"})
	}
	if strings.ContainsFunc(value, unicode.IsControl) {
		return "", NewError(CodeInvalidArgument, "display name must not contain control characters", map[string]any{"field": "display_name"})
	}
	return value, nil
}

// NormalizeBio trims a self-description and bounds it.
func NormalizeBio(value string) (string, error) {
	if !utf8.ValidString(value) {
		return "", NewError(CodeInvalidArgument, "bio must be valid UTF-8", map[string]any{"field": "bio"})
	}
	value = strings.TrimSpace(value)
	if utf8.RuneCountInString(value) > MaxBioLength {
		return "", NewError(CodeInvalidArgument, "bio is too long", map[string]any{"field": "bio"})
	}
	if strings.ContainsFunc(value, isRejectedBioControl) {
		return "", NewError(CodeInvalidArgument, "bio must not contain control characters", map[string]any{"field": "bio"})
	}
	return value, nil
}

// isRejectedBioControl is unicode.IsControl minus the two characters a bio is
// allowed to contain.
//
// A bio is edited in a textarea and is nobody's identity: it is read as a
// paragraph and never flattened into a single line the way a name is, so
// keeping the line breaks somebody typed costs nothing. The exemption stops
// here for that reason -- a display name gets no such allowance.
func isRejectedBioControl(character rune) bool {
	return unicode.IsControl(character) && character != '\n' && character != '\t'
}

// ValidateUserRole accepts the roles an account may hold.
func ValidateUserRole(value UserRole) error {
	switch value {
	case UserRoleOwner, UserRoleMember:
		return nil
	default:
		return NewError(CodeInvalidArgument, "account role is not recognised", map[string]any{"field": "role"})
	}
}

// ValidateAccountStatus accepts the states an account may be in.
func ValidateAccountStatus(value AccountStatus) error {
	switch value {
	case AccountPending, AccountActive, AccountSuspended:
		return nil
	default:
		return NewError(CodeInvalidArgument, "account status is not recognised", map[string]any{"field": "status"})
	}
}

// ValidateRegistrationMode accepts the registration policies a site may set.
func ValidateRegistrationMode(value RegistrationMode) error {
	switch value {
	case RegistrationOpen, RegistrationApproval, RegistrationClosed:
		return nil
	default:
		return NewError(CodeInvalidArgument, "registration mode is not recognised", map[string]any{"field": "registration_mode"})
	}
}

// ProfileURL is the single definition of where an account is reached.
//
// The handle is shown with an @ and linked without one, the way GitHub does
// it. Keeping the two apart in one place is what stops half the site from
// linking to a path that stopped existing.
func ProfileURL(username string) string { return "/" + username }

// UsernameCandidate proposes a handle derived from a provider's display name.
//
// Providers hand over something meant to be read, not something unique and
// URL-safe, so this reduces it to the allowed alphabet and leaves uniqueness
// to the caller, which is the only party that can query for a collision.
// An empty or wholly unusable name yields "" and the caller falls back to a
// random handle rather than to something guessable.
func UsernameCandidate(displayName string) string {
	var builder strings.Builder
	for _, character := range strings.ToLower(displayName) {
		switch {
		case character >= 'a' && character <= 'z',
			character >= '0' && character <= '9',
			character == '_':
			builder.WriteRune(character)
		}
		if builder.Len() >= UsernameMaxLength {
			break
		}
	}
	candidate := builder.String()
	candidate = strings.TrimLeft(candidate, "0123456789_")
	if len(candidate) < UsernameMinLength {
		return ""
	}
	if len(candidate) > UsernameMaxLength {
		candidate = candidate[:UsernameMaxLength]
	}
	if validateUsernameShape(candidate) != nil {
		return ""
	}
	return candidate
}

// SessionRecord is one live sign-in, stored so a single device can be signed
// out without signing out the rest.
//
// The cookie itself stays a signed, stateless value; this row is what the
// signature is checked against. Keeping both is deliberate: the signature
// proves the cookie was issued here without a lookup, and the row decides
// whether it is still supposed to work.
type SessionRecord struct {
	ID           string
	UserID       string
	IssuedAtMS   int64
	ExpiresAtMS  int64
	LastSeenAtMS int64
	RevokedAtMS  *int64
	Surface      string
	Label        *string
}

// Live reports whether this session should still be honoured at the given
// instant.
func (session SessionRecord) Live(nowMS int64) bool {
	return session.RevokedAtMS == nil && session.ExpiresAtMS > nowMS
}
