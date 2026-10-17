package app

import (
	"context"
	"strings"

	"github.com/moment/moment/internal/domain"
)

// AccountInitializer creates the account and site together, without a built-in
// identity or a credential that exists outside the account model.
type AccountInitializer interface {
	InitializeSiteAccount(context.Context, domain.User, string) error
}

type AccountAdministrationStore interface {
	SetSiteProfile(context.Context, string) error
	SetRole(context.Context, string, domain.UserRole, int64) error
	HasAuthoredContent(context.Context, string) (bool, error)
}

func (service *AccountService) InitializeSiteAccount(ctx context.Context, username string) (domain.User, error) {
	username, err := domain.NormalizeUsername(username)
	if err != nil {
		return domain.User{}, err
	}
	id, err := service.newID()
	if err != nil {
		return domain.User{}, err
	}
	at := domain.TimeToMillis(domain.NormalizeTime(service.clock()))
	user := domain.User{ID: id, Username: username, DisplayName: username, Role: domain.UserRoleAdmin, Status: domain.AccountActive, UsernameSetAtMS: &at, CreatedAtMS: at, UpdatedAtMS: at}
	err = service.transactions.WriteAccounts(ctx, func(accounts AccountStore, _ SessionStore) error {
		initializer, ok := accounts.(AccountInitializer)
		if !ok {
			return domain.NewError(domain.CodeDataIntegrityFailed, "account initialization storage is unavailable", nil)
		}
		return initializer.InitializeSiteAccount(ctx, user, HostTimezone())
	})
	return user, err
}

func retainActiveAdministrator(ctx context.Context, accounts AccountStore, user domain.User, role domain.UserRole, status domain.AccountStatus) error {
	if !user.IsAdmin() || user.Status != domain.AccountActive || (role == domain.UserRoleAdmin && status == domain.AccountActive) {
		return nil
	}
	users, err := accounts.ListAccounts(ctx)
	if err != nil {
		return err
	}
	for _, other := range users {
		if other.ID != user.ID && other.IsAdmin() && other.Status == domain.AccountActive {
			return nil
		}
	}
	return domain.NewError(domain.CodeLastAdministrator, "at least one active administrator must remain", nil)
}

func (service *AccountService) SetRole(ctx context.Context, userID string, role domain.UserRole) (domain.User, error) {
	if err := domain.ValidateUserRole(role); err != nil {
		return domain.User{}, err
	}
	var result domain.User
	err := service.transactions.WriteAccounts(ctx, func(accounts AccountStore, _ SessionStore) error {
		user, err := accounts.UserByID(ctx, userID)
		if err != nil {
			return err
		}
		if err := retainActiveAdministrator(ctx, accounts, user, role, user.Status); err != nil {
			return err
		}
		store, ok := accounts.(AccountAdministrationStore)
		if !ok {
			return domain.NewError(domain.CodeDataIntegrityFailed, "account administration storage is unavailable", nil)
		}
		at := domain.TimeToMillis(domain.NormalizeTime(service.clock()))
		if err := store.SetRole(ctx, userID, role, at); err != nil {
			return err
		}
		user.Role = role
		user.UpdatedAtMS = at
		result = user
		return nil
	})
	return result, err
}

// ValidateAdministrativeCredential rechecks authority after a coordinated operation
// has waited for in-flight requests. Role changes and revocations take effect here.
func (service *AccountService) ValidateAdministrativeCredential(ctx context.Context, userID, credentialID string, local bool) error {
	return service.transactions.ReadAccounts(ctx, func(accounts AccountStore, sessions SessionStore) error {
		refused := domain.NewError(domain.CodeUnauthorized, "administrator account or credential is no longer valid", nil)
		user, err := accounts.UserByID(ctx, userID)
		if err != nil {
			return err
		}
		if !user.IsAdmin() || user.Status != domain.AccountActive {
			return refused
		}
		if credentialID == "local" && local {
			return nil
		}
		if strings.HasPrefix(credentialID, "session:") {
			session, found, err := sessions.SessionByID(ctx, strings.TrimPrefix(credentialID, "session:"))
			if err != nil {
				return err
			}
			if found && session.UserID == userID && session.Live(service.clock().UnixMilli()) {
				return nil
			}
			return refused
		}
		if strings.HasPrefix(credentialID, "token:") {
			store, err := credentialStore(accounts)
			if err != nil {
				return err
			}
			token, found, err := store.APITokenByID(ctx, strings.TrimPrefix(credentialID, "token:"))
			if err != nil {
				return err
			}
			if found && token.UserID == userID && token.Live() {
				return nil
			}
		}
		return refused
	})
}

// SelectSiteProfile chooses the public display account independently of its role.
func (service *AccountService) SelectSiteProfile(ctx context.Context, userID string) (domain.User, error) {
	var result domain.User
	err := service.transactions.WriteAccounts(ctx, func(accounts AccountStore, _ SessionStore) error {
		user, err := accounts.UserByID(ctx, userID)
		if err != nil {
			return err
		}
		if user.Status != domain.AccountActive {
			return domain.NewError(domain.CodeInvalidArgument, "the site profile must be an active account", map[string]any{"field": "user_id"})
		}
		store, ok := accounts.(AccountAdministrationStore)
		if !ok {
			return domain.NewError(domain.CodeDataIntegrityFailed, "account administration storage is unavailable", nil)
		}
		if err := store.SetSiteProfile(ctx, userID); err != nil {
			return err
		}
		result = user
		return nil
	})
	return result, err
}
