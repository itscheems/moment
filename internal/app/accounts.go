package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/linkfetch"
)

// AccountStore is the persistence port for accounts and the external
// identities that sign in as them.
//
// The two are separated everywhere because they answer different questions and
// change at different times: an identity is what a provider told us, an account
// is who the site decided that is. Attaching a second provider must not create
// a second person, and a provider renaming its user must not rename ours.
type AccountStore interface {
	UserByID(ctx context.Context, id string) (domain.User, error)
	UserByUsername(ctx context.Context, username string) (domain.User, error)
	UsersByIDs(ctx context.Context, ids []string) (map[string]domain.User, error)
	UsernameAvailable(ctx context.Context, username string) (bool, error)

	IdentityLookup(ctx context.Context, provider, providerUserID string) (domain.Identity, bool, error)
	IdentitiesForUser(ctx context.Context, userID string) ([]domain.Identity, error)
	CreateAccountWithIdentity(ctx context.Context, user domain.User, identity domain.Identity) error
	LinkIdentity(ctx context.Context, identity domain.Identity) error
	UnlinkIdentity(ctx context.Context, identity domain.Identity) error
	RefreshIdentity(ctx context.Context, identity domain.Identity) error

	UpdateProfile(ctx context.Context, user domain.User) error
	SetStatus(ctx context.Context, userID string, status domain.AccountStatus, atMS int64) error
	DeleteAccount(ctx context.Context, userID string) error
	ListAccounts(ctx context.Context) ([]domain.User, error)
	SearchAccounts(ctx context.Context, query string, limit int) ([]domain.User, error)

	ProfileUserID(ctx context.Context) (string, error)
	RegistrationMode(ctx context.Context) (domain.RegistrationMode, error)

	// The two media operations an account write needs, and no more. Widening
	// this to the whole reference store would make every implementation carry
	// methods it never calls.
	MediaExists(ctx context.Context, id domain.ID) (bool, error)
	MediaAvailableForAccount(context.Context, string, domain.ID) (bool, error)
	ReconcileMediaReferences(ctx context.Context, ids []domain.ID, now time.Time) error
}

// SessionStore persists live sign-ins.
type SessionStore interface {
	CreateSession(ctx context.Context, session domain.SessionRecord) error
	SessionByID(ctx context.Context, id string) (domain.SessionRecord, bool, error)
	RevokeSession(ctx context.Context, id string, atMS int64) error
	RevokeUserSessions(ctx context.Context, userID string, atMS int64) (int, error)
	TouchSession(ctx context.Context, id string, atMS int64) error
	ListUserSessions(ctx context.Context, userID string) ([]domain.SessionRecord, error)
	PurgeExpiredSessions(ctx context.Context, beforeMS int64) (int, error)
}

// AccountTransactions binds account work to the storage transaction boundary.
type AccountTransactions interface {
	ReadAccounts(ctx context.Context, operation func(AccountStore, SessionStore) error) error
	WriteAccounts(ctx context.Context, operation func(AccountStore, SessionStore) error) error
}

// AccountService owns every decision about who exists and what they may do.
//
// It deliberately holds no knowledge of OAuth. A provider adapter reduces
// whatever it learned to an ExternalIdentity, and everything after that point
// is the same regardless of which provider produced it -- which is what keeps
// adding a third provider from touching any of this.
type AccountService struct {
	transactions AccountTransactions
	clock        func() time.Time
	newID        func() (string, error)
	avatarSource AvatarSource
	avatars      AvatarStore
}

// WithAvatarCapture enables storing a provider's account picture as managed
// media. Both halves are required; without them an account simply starts with
// no picture.
func WithAvatarCapture(source AvatarSource, store AvatarStore) AccountOption {
	return func(service *AccountService) {
		if source != nil && store != nil {
			service.avatarSource = source
			service.avatars = store
		}
	}
}

// AccountOption customises an AccountService.
type AccountOption func(*AccountService)

// WithAccountClock overrides the clock, for deterministic tests.
func WithAccountClock(clock func() time.Time) AccountOption {
	return func(service *AccountService) {
		if clock != nil {
			service.clock = clock
		}
	}
}

// WithAccountIDGenerator overrides identifier generation, for deterministic
// tests.
func WithAccountIDGenerator(generator func() (string, error)) AccountOption {
	return func(service *AccountService) {
		if generator != nil {
			service.newID = generator
		}
	}
}

// NewAccountService binds the service to its ports.
func NewAccountService(transactions AccountTransactions, options ...AccountOption) (*AccountService, error) {
	if transactions == nil {
		return nil, fmt.Errorf("account service requires transactions")
	}
	service := &AccountService{
		transactions: transactions,
		clock:        time.Now,
		newID:        NewAccountID,
	}
	for _, option := range options {
		option(service)
	}
	return service, nil
}

// NewAccountID mints the 32 lowercase hex digits an account is identified by.
func NewAccountID() (string, error) {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("generate account id: %w", err)
	}
	value := hex.EncodeToString(buffer)
	if err := domain.ValidateUserID(value); err != nil {
		return "", err
	}
	return value, nil
}

// ExternalIdentity is what a provider adapter reports after a successful
// exchange. It is the only shape the account layer accepts from a provider.
type ExternalIdentity struct {
	Provider         string
	ProviderUserID   string
	Email            string
	EmailVerified    bool
	EmailDeliverable bool
	DisplayName      string
	AvatarURL        string
}

// SignInOutcome reports what a completed provider exchange did.
type SignInOutcome struct {
	User    domain.User
	Created bool
	// LinkedTo is set when the identity was attached to an account the caller
	// was already signed in as, rather than signing in on its own.
	LinkedTo bool
}

// SignIn resolves an external identity to an account, creating one when the
// site's registration policy allows it.
//
// The whole policy lives here, in one transaction, because every branch that
// decides whether someone may exist has to see the same registration mode. A
// check performed before the transaction opens is a check another request can
// invalidate before this one writes.
func (service *AccountService) SignIn(ctx context.Context, external ExternalIdentity) (SignInOutcome, error) {
	if external.Provider == "" || external.ProviderUserID == "" {
		return SignInOutcome{}, domain.NewError(domain.CodeOAuthIdentityUnusable, "the provider returned no stable account identifier", nil)
	}
	now := domain.NormalizeTime(service.clock())
	nowMS := domain.TimeToMillis(now)

	var outcome SignInOutcome
	err := service.transactions.WriteAccounts(ctx, func(accounts AccountStore, _ SessionStore) error {
		existing, found, err := accounts.IdentityLookup(ctx, external.Provider, external.ProviderUserID)
		if err != nil {
			return err
		}
		if found {
			user, err := accounts.UserByID(ctx, existing.UserID)
			if err != nil {
				return err
			}
			if user.Status == domain.AccountSuspended {
				return domain.NewError(domain.CodeAccountSuspended, "this account has been suspended", nil)
			}
			if err := accounts.RefreshIdentity(ctx, refreshedIdentity(existing, external, nowMS)); err != nil {
				return err
			}
			outcome = SignInOutcome{User: user}
			return nil
		}

		mode, err := accounts.RegistrationMode(ctx)
		if err != nil {
			return err
		}
		if mode == domain.RegistrationClosed {
			return domain.NewError(domain.CodeRegistrationClosed, "this site is not accepting new accounts", nil)
		}

		username, err := service.allocateUsername(ctx, accounts, external.DisplayName)
		if err != nil {
			return err
		}
		displayName := external.DisplayName
		if normalized, normalizeErr := domain.NormalizeDisplayName(displayName); normalizeErr == nil {
			displayName = normalized
		} else {
			// A provider display name is not ours to reject. When it cannot be
			// stored as-is the handle is the next most recognisable thing, and
			// the person can change it immediately.
			displayName = username
		}

		id, err := service.newID()
		if err != nil {
			return err
		}
		status := domain.AccountPending
		if mode == domain.RegistrationOpen {
			status = domain.AccountActive
		}
		user := domain.User{
			ID:          id,
			Username:    username,
			DisplayName: displayName,
			Role:        domain.UserRoleMember,
			Status:      status,
			CreatedAtMS: nowMS,
			UpdatedAtMS: nowMS,
		}
		identity := identityFrom(external, id, nowMS)
		if err := accounts.CreateAccountWithIdentity(ctx, user, identity); err != nil {
			return err
		}
		outcome = SignInOutcome{User: user, Created: true}
		return nil
	})
	if err != nil {
		return SignInOutcome{}, err
	}
	return outcome, nil
}

// LinkIdentity links a verified external identity to an existing account.
func (service *AccountService) LinkIdentity(ctx context.Context, userID string, external ExternalIdentity) error {
	if external.Provider == "" || external.ProviderUserID == "" {
		return domain.NewError(domain.CodeOAuthIdentityUnusable, "the provider returned no stable account identifier", nil)
	}
	nowMS := domain.TimeToMillis(domain.NormalizeTime(service.clock()))
	return service.transactions.WriteAccounts(ctx, func(accounts AccountStore, _ SessionStore) error {
		// Whether this account may take on a credential is asked here and now,
		// rather than inherited from whatever authorised the flow that led
		// here. A federated link is authorised minutes before it completes, and
		// an account suspended in between must not still be collecting
		// permanent new ways in.
		user, err := accounts.UserByID(ctx, userID)
		if err != nil {
			return err
		}
		if !user.CanSignIn() {
			return domain.NewError(domain.CodeAccountSuspended, "this account has been suspended", nil)
		}
		existing, found, err := accounts.IdentityLookup(ctx, external.Provider, external.ProviderUserID)
		if err != nil {
			return err
		}
		if found {
			if existing.UserID == userID {
				return accounts.RefreshIdentity(ctx, refreshedIdentity(existing, external, nowMS))
			}
			return domain.NewError(domain.CodeIdentityAlreadyLinked, "that provider account is already linked to another account here", map[string]any{"provider": external.Provider})
		}
		// One identity per provider per account, decided here rather than by a
		// unique constraint. 0022 wrote down why the schema does not narrow
		// after the fact: the archive importer writes stored rows back
		// verbatim, so a constraint an older version could already have
		// violated makes those backups unimportable. Legality that lives in Go
		// can tighten without invalidating history.
		//
		// Two GitHub identities on one account cannot be told apart by the
		// person holding them -- the list names a provider, the remove route is
		// keyed by one, and neither can say which -- so the second is refused.
		linked, err := accounts.IdentitiesForUser(ctx, userID)
		if err != nil {
			return err
		}
		for _, identity := range linked {
			if identity.Provider == external.Provider {
				return domain.NewError(domain.CodeIdentityAlreadyLinked,
					"this account already signs in with "+external.Provider,
					map[string]any{"provider": external.Provider})
			}
		}
		return accounts.LinkIdentity(ctx, identityFrom(external, userID, nowMS))
	})
}

// UnlinkIdentity detaches one provider identity from an account, refusing to
// remove the last way that account can get back in.
//
// The row is chosen here and then removed by name. Counting providers and then
// deleting by provider was one rule stated twice, and the two disagreed the
// moment an account held two identities from the same provider: the count read
// a pair as "your only way in" and refused, while the delete that follows it
// took both rows for one click.
func (service *AccountService) UnlinkIdentity(ctx context.Context, user domain.User, provider string) error {
	return service.transactions.WriteAccounts(ctx, func(accounts AccountStore, _ SessionStore) error {
		identities, err := accounts.IdentitiesForUser(ctx, user.ID)
		if err != nil {
			return err
		}
		var target domain.Identity
		found := false
		remaining := 0
		for _, identity := range identities {
			if !found && identity.Provider == provider {
				target, found = identity, true
				continue
			}
			remaining++
		}
		if !found {
			// Its own code rather than ACCOUNT_NOT_FOUND, which is what this
			// said and which the transport maps to a 401. The account doing the
			// asking exists -- it was authenticated to get here -- and the
			// thing that is missing is one of its identities. Reporting that as
			// "you are not signed in" told the caller to do the one thing that
			// cannot help, and on a client that treats a 401 as a dead session
			// it would end the session over a row that was already gone.
			return domain.NewError(domain.CodeIdentityNotLinked, "that provider is not linked to this account", map[string]any{"provider": provider})
		}
		if remaining == 0 {
			hasLocal := false
			if credentials, ok := accounts.(AccountCredentialStore); ok {
				_, found, err := credentials.PasswordHash(ctx, user.ID)
				if err != nil {
					return err
				}
				hasLocal = found
				tokens, err := credentials.ListAPITokens(ctx, user.ID)
				if err != nil {
					return err
				}
				for _, token := range tokens {
					hasLocal = hasLocal || token.Live()
				}
			}
			if !hasLocal {
				return domain.NewError(domain.CodeLastCredential, "this is the only way to sign in to this account", map[string]any{"provider": provider})
			}
		}
		return accounts.UnlinkIdentity(ctx, target)
	})
}

// allocateUsername derives a free handle from a provider display name.
//
// The derived form is tried first because it is the one a person recognises.
// Uniqueness cannot be decided outside the transaction, so collisions are
// resolved here with a bounded number of suffixed attempts and then by a random
// handle, which always succeeds and is never guessable.
func (service *AccountService) allocateUsername(ctx context.Context, accounts AccountStore, displayName string) (string, error) {
	candidate := domain.UsernameCandidate(displayName)
	if candidate != "" && !domain.UsernameReserved(candidate) {
		free, err := accounts.UsernameAvailable(ctx, candidate)
		if err != nil {
			return "", err
		}
		if free {
			return candidate, nil
		}
	}
	if candidate != "" {
		base := candidate
		if len(base) > domain.UsernameMaxLength-5 {
			base = base[:domain.UsernameMaxLength-5]
		}
		for attempt := 0; attempt < 8; attempt++ {
			suffix, err := randomDigits(4)
			if err != nil {
				return "", err
			}
			proposed := base + suffix
			if domain.UsernameReserved(proposed) {
				continue
			}
			free, err := accounts.UsernameAvailable(ctx, proposed)
			if err != nil {
				return "", err
			}
			if free {
				return proposed, nil
			}
		}
	}
	for attempt := 0; attempt < 8; attempt++ {
		buffer := make([]byte, 4)
		if _, err := rand.Read(buffer); err != nil {
			return "", fmt.Errorf("generate username: %w", err)
		}
		proposed := "user_" + hex.EncodeToString(buffer)
		free, err := accounts.UsernameAvailable(ctx, proposed)
		if err != nil {
			return "", err
		}
		if free {
			return proposed, nil
		}
	}
	return "", domain.NewError(domain.CodeInternal, "could not allocate a username", nil)
}

func randomDigits(count int) (string, error) {
	buffer := make([]byte, count)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("generate suffix: %w", err)
	}
	digits := make([]byte, count)
	for index, value := range buffer {
		digits[index] = '0' + value%10
	}
	return string(digits), nil
}

// refreshedIdentity folds what a provider just said into what was stored.
//
// Every sign-in re-reads the provider, so a first sign-in that was rate limited
// or ran without the address scope is repaired by the next one rather than
// being a permanent fact about the account. The fold is deliberately one-way:
// a fresh answer replaces a stored one only when it carries at least as much,
// because the same transient failure that produced the gap would otherwise
// erase an address that had already been read successfully.
func refreshedIdentity(stored domain.Identity, external ExternalIdentity, nowMS int64) domain.Identity {
	refreshed := stored
	refreshed.LastLoginAtMS = nowMS

	if external.Email != "" {
		email := external.Email
		// The flags fold the same way the address does, and for the same
		// reason. GitHub answers /user/emails with 403 often enough that the
		// adapter falls back to the public profile address, which nothing has
		// verified -- and overwriting the flags with that answer turns an
		// address a provider once vouched for into an unverified one forever,
		// which is exactly the erasure this function exists to prevent.
		//
		// What was proven is proven about an address, not about an account, so
		// it only carries forward while the address is the same one.
		sameAddress := stored.Email != nil && strings.EqualFold(*stored.Email, email)
		refreshed.Email = &email
		refreshed.EmailVerified = external.EmailVerified || (sameAddress && stored.EmailVerified)
		refreshed.EmailDeliverable = external.EmailDeliverable || (sameAddress && stored.EmailDeliverable)
	}
	if external.DisplayName != "" {
		refreshed.DisplayName = external.DisplayName
	}
	if external.AvatarURL != "" {
		avatar := external.AvatarURL
		refreshed.AvatarURL = &avatar
	}
	return refreshed
}

func identityFrom(external ExternalIdentity, userID string, nowMS int64) domain.Identity {
	identity := domain.Identity{
		Provider:         external.Provider,
		ProviderUserID:   external.ProviderUserID,
		UserID:           userID,
		EmailVerified:    external.EmailVerified,
		EmailDeliverable: external.EmailDeliverable,
		DisplayName:      external.DisplayName,
		CreatedAtMS:      nowMS,
		LastLoginAtMS:    nowMS,
	}
	if external.Email != "" {
		email := external.Email
		identity.Email = &email
	}
	if external.AvatarURL != "" {
		avatar := external.AvatarURL
		identity.AvatarURL = &avatar
	}
	return identity
}

// Account reads one account by id.
func (service *AccountService) Account(ctx context.Context, id string) (domain.User, error) {
	var user domain.User
	err := service.transactions.ReadAccounts(ctx, func(accounts AccountStore, _ SessionStore) error {
		value, err := accounts.UserByID(ctx, id)
		if err != nil {
			return err
		}
		user = value
		return nil
	})
	return user, err
}

// AccountByUsername reads one account by handle.
func (service *AccountService) AccountByUsername(ctx context.Context, username string) (domain.User, error) {
	if err := domain.ValidateStoredUsername(username); err != nil {
		return domain.User{}, domain.NewError(domain.CodeAccountNotFound, "no such account", nil)
	}
	var user domain.User
	err := service.transactions.ReadAccounts(ctx, func(accounts AccountStore, _ SessionStore) error {
		value, err := accounts.UserByUsername(ctx, username)
		if err != nil {
			return err
		}
		user = value
		return nil
	})
	return user, err
}

// Identities lists the providers linked to an account.
func (service *AccountService) Identities(ctx context.Context, userID string) ([]domain.Identity, error) {
	var identities []domain.Identity
	err := service.transactions.ReadAccounts(ctx, func(accounts AccountStore, _ SessionStore) error {
		value, err := accounts.IdentitiesForUser(ctx, userID)
		if err != nil {
			return err
		}
		identities = value
		return nil
	})
	return identities, err
}

// List returns private account facts for administration.
func (service *AccountService) List(ctx context.Context) ([]domain.User, error) {
	var users []domain.User
	err := service.transactions.ReadAccounts(ctx, func(accounts AccountStore, _ SessionStore) error {
		value, err := accounts.ListAccounts(ctx)
		if err != nil {
			return err
		}
		users = value
		return nil
	})
	return users, err
}

// ProfileUpdate is a partial profile edit an account performs on itself.
type ProfileUpdate struct {
	Username      *string
	DisplayName   *string
	Bio           *string
	AvatarMediaID *NullableString
	CoverMediaID  *NullableString
}

// UpdateProfile changes personal profile fields and validates account media grants.
func (service *AccountService) UpdateProfile(ctx context.Context, userID string, update ProfileUpdate) (domain.User, error) {
	nowMS := domain.TimeToMillis(domain.NormalizeTime(service.clock()))
	var result domain.User
	err := service.transactions.WriteAccounts(ctx, func(accounts AccountStore, _ SessionStore) error {
		user, err := accounts.UserByID(ctx, userID)
		if err != nil {
			return err
		}
		if update.Username != nil {
			username, err := domain.NormalizeUsername(*update.Username)
			if err != nil {
				return err
			}
			if username != user.Username {
				free, err := accounts.UsernameAvailable(ctx, username)
				if err != nil {
					return err
				}
				if !free {
					return domain.NewError(domain.CodeUsernameTaken, "that username is taken", map[string]any{"field": "username"})
				}
				user.Username = username
			}
			stamped := nowMS
			user.UsernameSetAtMS = &stamped
		}
		if update.DisplayName != nil {
			displayName, err := domain.NormalizeDisplayName(*update.DisplayName)
			if err != nil {
				return err
			}
			user.DisplayName = displayName
		}
		if update.Bio != nil {
			bio, err := domain.NormalizeBio(*update.Bio)
			if err != nil {
				return err
			}
			user.Bio = bio
		}
		previousAvatar, previousCover := user.AvatarMediaID, user.CoverMediaID
		if update.AvatarMediaID != nil {
			user.AvatarMediaID = update.AvatarMediaID.Value
		}
		if update.CoverMediaID != nil {
			user.CoverMediaID = update.CoverMediaID.Value
		}
		for field, id := range map[string]*string{
			"avatar_media_id": user.AvatarMediaID,
			"cover_media_id":  user.CoverMediaID,
		} {
			if id == nil {
				continue
			}
			mediaID, parseErr := domain.ParseID(*id)
			if parseErr != nil {
				return domain.NewError(domain.CodeInvalidArgument, field+" must be a valid managed media ID", map[string]any{"field": field})
			}
			exists, lookupErr := accounts.MediaAvailableForAccount(ctx, user.ID, mediaID)
			if lookupErr != nil {
				return lookupErr
			}
			if !exists {
				return domain.NewError(domain.CodeMediaNotFound, "media "+mediaID.String()+" does not exist", map[string]any{"media_id": mediaID.String()})
			}
		}
		user.UpdatedAtMS = nowMS
		if err := accounts.UpdateProfile(ctx, user); err != nil {
			return err
		}
		// A picture that stopped being someone's avatar has to be told so.
		// Reachability is recomputed at collection time, so skipping this
		// cannot delete a picture that is in use -- but it does leave a
		// replaced one referenced forever, which is a leak rather than a loss.
		if update.AvatarMediaID != nil || update.CoverMediaID != nil {
			affected, affectedErr := uniqueMediaIDs(previousAvatar, user.AvatarMediaID, previousCover, user.CoverMediaID)
			if affectedErr != nil {
				return domain.NewError(domain.CodeDataIntegrityFailed, "stored profile media ID is invalid", nil)
			}
			if err := accounts.ReconcileMediaReferences(ctx, affected, domain.NormalizeTime(service.clock())); err != nil {
				return err
			}
		}
		result = user
		return nil
	})
	return result, err
}

// SetStatus changes account status and revokes credentials when access is suspended.
func (service *AccountService) SetStatus(ctx context.Context, userID string, status domain.AccountStatus) (domain.User, error) {
	if err := domain.ValidateAccountStatus(status); err != nil {
		return domain.User{}, err
	}
	nowMS := domain.TimeToMillis(domain.NormalizeTime(service.clock()))
	var result domain.User
	err := service.transactions.WriteAccounts(ctx, func(accounts AccountStore, sessions SessionStore) error {
		user, err := accounts.UserByID(ctx, userID)
		if err != nil {
			return err
		}
		if err := retainActiveAdministrator(ctx, accounts, user, user.Role, status); err != nil {
			return err
		}
		if status != domain.AccountActive {
			profileID, err := accounts.ProfileUserID(ctx)
			if err != nil {
				return err
			}
			if profileID == user.ID {
				return domain.NewError(domain.CodeAccountInUse, "select another active site profile before changing this account's status", nil)
			}
		}
		if err := accounts.SetStatus(ctx, userID, status, nowMS); err != nil {
			return err
		}
		if status == domain.AccountSuspended {
			// Suspension that leaves live sessions behind is not suspension.
			if _, err := sessions.RevokeUserSessions(ctx, userID, nowMS); err != nil {
				return err
			}
			if credentials, ok := accounts.(AccountCredentialStore); ok {
				if err := credentials.RevokeUserAPITokens(ctx, userID, nowMS); err != nil {
					return err
				}
			}
		}
		user.Status = status
		user.UpdatedAtMS = nowMS
		result = user
		return nil
	})
	return result, err
}

// SessionResolution is everything a request needs to know about its caller.
type SessionResolution struct {
	Session domain.SessionRecord
	User    domain.User
}

// ResolveSession answers who a live session belongs to.
//
// Both halves matter and neither is redundant. The cookie's signature already
// proves this server issued it; the row decides whether it is still supposed to
// work, and the account decides what that person may do now rather than what
// they could do when they signed in.
func (service *AccountService) ResolveSession(ctx context.Context, sessionID string) (SessionResolution, error) {
	nowMS := domain.TimeToMillis(domain.NormalizeTime(service.clock()))
	var resolved SessionResolution
	err := service.transactions.ReadAccounts(ctx, func(accounts AccountStore, sessions SessionStore) error {
		session, found, err := sessions.SessionByID(ctx, sessionID)
		if err != nil {
			return err
		}
		if !found || !session.Live(nowMS) {
			return domain.NewError(domain.CodeUnauthorized, "this session is no longer valid", nil)
		}
		user, err := accounts.UserByID(ctx, session.UserID)
		if err != nil {
			return err
		}
		if !user.CanSignIn() {
			return domain.NewError(domain.CodeAccountSuspended, "this account has been suspended", nil)
		}
		resolved = SessionResolution{Session: session, User: user}
		return nil
	})
	return resolved, err
}

// sessionTouchIntervalMS bounds how often a request updates last_seen_at_ms.
//
// SQLite serialises writes through a single connection here, so writing on
// every authenticated request would make the write pool the bottleneck for
// reads. Five minutes is precise enough for "when was this device last used"
// and cheap enough to be free.
const sessionTouchIntervalMS = 5 * 60 * 1000

// TouchSession records that a session was used, at most once per interval.
func (service *AccountService) TouchSession(ctx context.Context, session domain.SessionRecord) error {
	nowMS := domain.TimeToMillis(domain.NormalizeTime(service.clock()))
	if nowMS-session.LastSeenAtMS < sessionTouchIntervalMS {
		return nil
	}
	return service.transactions.WriteAccounts(ctx, func(_ AccountStore, sessions SessionStore) error {
		return sessions.TouchSession(ctx, session.ID, nowMS)
	})
}

// ProfileAccount reads the account selected for the public site profile.
func (service *AccountService) ProfileAccount(ctx context.Context) (domain.User, error) {
	var user domain.User
	err := service.transactions.ReadAccounts(ctx, func(accounts AccountStore, _ SessionStore) error {
		profileID, err := accounts.ProfileUserID(ctx)
		if err != nil {
			return err
		}
		value, err := accounts.UserByID(ctx, profileID)
		if err != nil {
			return err
		}
		user = value
		return nil
	})
	return user, err
}

// StartSession records a new sign-in and returns the row it created.
func (service *AccountService) StartSession(ctx context.Context, userID, sessionID, surface string, expiresAtMS int64) (domain.SessionRecord, error) {
	nowMS := domain.TimeToMillis(domain.NormalizeTime(service.clock()))
	record := domain.SessionRecord{
		ID:           sessionID,
		UserID:       userID,
		IssuedAtMS:   nowMS,
		ExpiresAtMS:  expiresAtMS,
		LastSeenAtMS: nowMS,
		Surface:      surface,
	}
	err := service.transactions.WriteAccounts(ctx, func(_ AccountStore, sessions SessionStore) error {
		return sessions.CreateSession(ctx, record)
	})
	return record, err
}

// EndSession revokes one sign-in.
func (service *AccountService) EndSession(ctx context.Context, sessionID string) error {
	nowMS := domain.TimeToMillis(domain.NormalizeTime(service.clock()))
	return service.transactions.WriteAccounts(ctx, func(_ AccountStore, sessions SessionStore) error {
		return sessions.RevokeSession(ctx, sessionID, nowMS)
	})
}

// EndAllSessions revokes every sign-in for an account.
func (service *AccountService) EndAllSessions(ctx context.Context, userID string) (int, error) {
	nowMS := domain.TimeToMillis(domain.NormalizeTime(service.clock()))
	var revoked int
	err := service.transactions.WriteAccounts(ctx, func(_ AccountStore, sessions SessionStore) error {
		count, err := sessions.RevokeUserSessions(ctx, userID, nowMS)
		revoked = count
		return err
	})
	return revoked, err
}

// Sessions lists an account's sign-ins, newest first.
func (service *AccountService) Sessions(ctx context.Context, userID string) ([]domain.SessionRecord, error) {
	var records []domain.SessionRecord
	err := service.transactions.ReadAccounts(ctx, func(_ AccountStore, sessions SessionStore) error {
		value, err := sessions.ListUserSessions(ctx, userID)
		records = value
		return err
	})
	return records, err
}

// NewSessionID mints the identifier a session row is found by.
func NewSessionID() (string, error) { return NewAccountID() }

// AvatarSource downloads a bounded remote image. It is the same seam the link
// previewer uses, which is what gives an avatar the same address filtering,
// redirect stripping and byte ceiling a link thumbnail already gets.
type AvatarSource interface {
	Get(ctx context.Context, rawURL string, accepted []string, maxBytes int64) (linkfetch.Response, error)
}

// AvatarStore ingests downloaded bytes as managed media.
type AvatarStore interface {
	Ingest(ctx context.Context, originalName string, input io.Reader) (domain.ID, string, uint32, uint32, error)
}

// avatarHostAllowlist bounds where an avatar may be fetched from.
//
// The URL arrives inside a provider's answer, but it is still a value someone
// else chose, and the fetcher's own address filter only proves a host is
// publicly routable. Pinning the two hosts these providers actually serve
// pictures from costs nothing and removes the question.
var avatarHostAllowlist = map[string]string{
	"lh3.googleusercontent.com":     "google",
	"avatars.githubusercontent.com": "github",
}

// avatarCaptureBudget bounds how long sign-in will wait for a picture.
//
// A person signing in is waiting on this request. A provider's image host
// being slow, or unreachable from where this server runs, must cost them a
// default avatar rather than a failed login.
const avatarCaptureBudget = 3 * time.Second

// CaptureAvatar downloads an account picture and stores it as managed media.
//
// Failure is not an error the caller should react to: an account with no
// picture renders its initial, and the person can upload one whenever they
// like. The signature returns an error only so the reason can be logged.
func (service *AccountService) CaptureAvatar(ctx context.Context, userID, avatarURL string) error {
	if service.avatars == nil || service.avatarSource == nil || avatarURL == "" {
		return nil
	}
	parsed, err := url.Parse(avatarURL)
	if err != nil || parsed.Scheme != "https" {
		return fmt.Errorf("avatar url is not usable")
	}
	if _, allowed := avatarHostAllowlist[strings.ToLower(parsed.Hostname())]; !allowed {
		return fmt.Errorf("avatar host is not allowed")
	}

	fetchCtx, cancel := context.WithTimeout(ctx, avatarCaptureBudget)
	defer cancel()
	response, err := service.avatarSource.Get(fetchCtx, avatarURL, []string{"image/*"}, linkfetch.DefaultMaxImageBytes)
	if err != nil {
		return err
	}
	// The request context, not the download's. The budget above bounds how
	// long sign-in waits for a provider's image host; ingesting the bytes
	// already in hand is local work, and charging it to what is left of a
	// three second network budget makes a picture fail for having been slow to
	// arrive rather than for anything about the picture.
	id, _, _, _, err := service.avatars.Ingest(ctx, "account-avatar", bytes.NewReader(response.Body))
	if err != nil {
		return err
	}

	nowMS := domain.TimeToMillis(domain.NormalizeTime(service.clock()))
	mediaID := id.String()
	return service.transactions.WriteAccounts(ctx, func(accounts AccountStore, _ SessionStore) error {
		user, err := accounts.UserByID(ctx, userID)
		if err != nil {
			return err
		}
		// Never replace a picture the person chose. This runs on first sign-in,
		// and a later provider change must not overwrite an upload.
		if user.AvatarMediaID != nil {
			return nil
		}
		user.AvatarMediaID = &mediaID
		user.UpdatedAtMS = nowMS
		return accounts.UpdateProfile(ctx, user)
	})
}

// CurrentRegistrationMode reports the policy new accounts meet.
func (service *AccountService) CurrentRegistrationMode(ctx context.Context) (domain.RegistrationMode, error) {
	var mode domain.RegistrationMode
	err := service.transactions.ReadAccounts(ctx, func(accounts AccountStore, _ SessionStore) error {
		value, err := accounts.RegistrationMode(ctx)
		mode = value
		return err
	})
	return mode, err
}

// expiredSessionGrace keeps a just-expired row around long enough that "when
// did this device last sign in" is still answerable for a while after it
// stopped working.
const expiredSessionGrace = 7 * 24 * time.Hour

// SweepExpiredSessions removes rows that stopped meaning anything.
//
// Nothing reads an expired session -- authorisation compares against the signed
// cookie's own expiry first -- so this is housekeeping, not enforcement. It
// exists because the table otherwise only grows: every sign-in adds a row and
// nothing ever removed one.
func (service *AccountService) SweepExpiredSessions(ctx context.Context) (int, error) {
	cutoff := domain.TimeToMillis(domain.NormalizeTime(service.clock().Add(-expiredSessionGrace)))
	var removed int
	err := service.transactions.WriteAccounts(ctx, func(_ AccountStore, sessions SessionStore) error {
		count, err := sessions.PurgeExpiredSessions(ctx, cutoff)
		removed = count
		return err
	})
	return removed, err
}

// Delete removes an account only when site and authored-content references allow it.
func (service *AccountService) Delete(ctx context.Context, userID string) (domain.User, error) {
	var removed domain.User
	err := service.transactions.WriteAccounts(ctx, func(accounts AccountStore, _ SessionStore) error {
		user, err := accounts.UserByID(ctx, userID)
		if err != nil {
			return err
		}
		if err := retainActiveAdministrator(ctx, accounts, user, domain.UserRoleMember, domain.AccountSuspended); err != nil {
			return err
		}
		profileID, err := accounts.ProfileUserID(ctx)
		if err != nil {
			return err
		}
		if profileID == user.ID {
			return domain.NewError(domain.CodeAccountInUse, "select another site profile before deleting this account", nil)
		}
		admin, ok := accounts.(AccountAdministrationStore)
		if !ok {
			return domain.NewError(domain.CodeDataIntegrityFailed, "account administration storage is unavailable", nil)
		}
		hasContent, err := admin.HasAuthoredContent(ctx, user.ID)
		if err != nil {
			return err
		}
		if hasContent {
			return domain.NewError(domain.CodeAccountInUse, "remove this account's posts and drafts before deleting it", nil)
		}
		if err := accounts.DeleteAccount(ctx, userID); err != nil {
			return err
		}
		// The pictures it held are now referenced by nobody. Saying so is what
		// makes them collectable; without it they stay on disk forever, which
		// is the same leak an unreconciled avatar change used to cause.
		if user.AvatarMediaID != nil || user.CoverMediaID != nil {
			affected, affectedErr := uniqueMediaIDs(user.AvatarMediaID, user.CoverMediaID)
			if affectedErr != nil {
				return domain.NewError(domain.CodeDataIntegrityFailed, "stored profile media ID is invalid", nil)
			}
			if err := accounts.ReconcileMediaReferences(ctx, affected, domain.NormalizeTime(service.clock())); err != nil {
				return err
			}
		}
		removed = user
		return nil
	})
	return removed, err
}

// SuggestMentions answers a composer's autocomplete.
//
// It lives here rather than on the site service because "who can be mentioned"
// is a question about accounts. While there was one account the site singleton
// was both the lightest and the only possible answer; with more than one it
// became the wrong table, and a member nobody could mention.
func (service *AccountService) SuggestMentions(ctx context.Context, query string, limit int) ([]MentionSuggestion, error) {
	if limit < 1 || limit > 20 {
		return nil, domain.NewError(domain.CodeInvalidArgument, "limit must be between 1 and 20", map[string]any{"field": "limit"})
	}
	query = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(query, "@")))
	if len([]rune(query)) > domain.UsernameMaxLength {
		return nil, domain.NewError(domain.CodeInvalidArgument, "mention query must not exceed 30 characters", map[string]any{"field": "query"})
	}
	var found []domain.User
	err := service.transactions.ReadAccounts(ctx, func(accounts AccountStore, _ SessionStore) error {
		value, err := accounts.SearchAccounts(ctx, query, limit)
		found = value
		return err
	})
	if err != nil {
		return nil, err
	}
	suggestions := make([]MentionSuggestion, 0, len(found))
	for _, user := range found {
		suggestions = append(suggestions, MentionSuggestion{
			UserID:        user.ID,
			Username:      user.Username,
			DisplayName:   user.DisplayName,
			AvatarMediaID: cloneString(user.AvatarMediaID),
			ProfileURL:    domain.ProfileURL(user.Username),
		})
	}
	return suggestions, nil
}
