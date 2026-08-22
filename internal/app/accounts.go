package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/moment/moment/internal/domain"
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
	UnlinkIdentity(ctx context.Context, userID, provider string) error
	RecordIdentityLogin(ctx context.Context, provider, providerUserID string, atMS int64) error

	UpdateProfile(ctx context.Context, user domain.User) error
	SetStatus(ctx context.Context, userID string, status domain.AccountStatus, atMS int64) error
	ListAccounts(ctx context.Context) ([]domain.User, error)

	OwnerUserID(ctx context.Context) (string, error)
	RegistrationMode(ctx context.Context) (domain.RegistrationMode, error)
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
			if err := accounts.RecordIdentityLogin(ctx, external.Provider, external.ProviderUserID, nowMS); err != nil {
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

// LinkIdentity attaches a provider identity to an account that is already
// signed in.
//
// This is the path that exists so a person can add a second way to sign in
// without becoming a second person. It refuses an identity already claimed
// elsewhere rather than moving it, because moving it would sign the previous
// owner out of their own account.
func (service *AccountService) LinkIdentity(ctx context.Context, userID string, external ExternalIdentity) error {
	if external.Provider == "" || external.ProviderUserID == "" {
		return domain.NewError(domain.CodeOAuthIdentityUnusable, "the provider returned no stable account identifier", nil)
	}
	nowMS := domain.TimeToMillis(domain.NormalizeTime(service.clock()))
	return service.transactions.WriteAccounts(ctx, func(accounts AccountStore, _ SessionStore) error {
		existing, found, err := accounts.IdentityLookup(ctx, external.Provider, external.ProviderUserID)
		if err != nil {
			return err
		}
		if found {
			if existing.UserID == userID {
				return accounts.RecordIdentityLogin(ctx, external.Provider, external.ProviderUserID, nowMS)
			}
			return domain.NewError(domain.CodeIdentityAlreadyLinked, "that provider account is already linked to another account here", map[string]any{"provider": external.Provider})
		}
		return accounts.LinkIdentity(ctx, identityFrom(external, userID, nowMS))
	})
}

// UnlinkIdentity detaches a provider from an account, refusing to remove the
// last way that account can get back in.
func (service *AccountService) UnlinkIdentity(ctx context.Context, user domain.User, provider string) error {
	return service.transactions.WriteAccounts(ctx, func(accounts AccountStore, _ SessionStore) error {
		identities, err := accounts.IdentitiesForUser(ctx, user.ID)
		if err != nil {
			return err
		}
		remaining := 0
		present := false
		for _, identity := range identities {
			if identity.Provider == provider {
				present = true
				continue
			}
			remaining++
		}
		if !present {
			return domain.NewError(domain.CodeAccountNotFound, "that provider is not linked to this account", map[string]any{"provider": provider})
		}
		// The owner keeps a password and a bearer token in secrets.json, so
		// unlinking every provider still leaves a way in. Nobody else has one.
		if remaining == 0 && !user.IsOwner() {
			return domain.NewError(domain.CodeLastCredential, "this is the only way to sign in to this account", map[string]any{"provider": provider})
		}
		return accounts.UnlinkIdentity(ctx, user.ID, provider)
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

// List reports every account, for the owner's review screens.
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

// UpdateProfile applies an account's edit to itself.
//
// Every field here is one the account owns. Role and status are absent by
// construction rather than by filtering: a struct that cannot express "make me
// an owner" cannot be tricked into carrying it.
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
		if update.AvatarMediaID != nil {
			user.AvatarMediaID = update.AvatarMediaID.Value
		}
		if update.CoverMediaID != nil {
			user.CoverMediaID = update.CoverMediaID.Value
		}
		user.UpdatedAtMS = nowMS
		if err := accounts.UpdateProfile(ctx, user); err != nil {
			return err
		}
		result = user
		return nil
	})
	return result, err
}

// SetStatus is the owner's decision about another account.
//
// It refuses to touch the owner's own row. Suspending the only account that can
// administer the site is not a decision worth being able to make by accident.
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
		if user.IsOwner() {
			return domain.NewError(domain.CodeInvalidArgument, "the owner account's status cannot be changed", map[string]any{"field": "status"})
		}
		if err := accounts.SetStatus(ctx, userID, status, nowMS); err != nil {
			return err
		}
		if status == domain.AccountSuspended {
			// Suspension that leaves live sessions behind is not suspension.
			if _, err := sessions.RevokeUserSessions(ctx, userID, nowMS); err != nil {
				return err
			}
		}
		user.Status = status
		user.UpdatedAtMS = nowMS
		result = user
		return nil
	})
	return result, err
}
