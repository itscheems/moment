package app

import (
	"context"

	"github.com/moment/moment/internal/domain"
	"golang.org/x/crypto/bcrypt"
)

// PasswordAccountStore writes a self-registered account and its password hash
// together inside AccountTransactions.WriteAccounts.
type PasswordAccountStore interface {
	CreateAccountWithPassword(context.Context, domain.User, []byte) error
}

// RegisterPasswordAccount creates a member with the status selected by the
// site's current registration policy. Only the server supplies role and status.
func (service *AccountService) RegisterPasswordAccount(ctx context.Context, username, password string) (domain.User, error) {
	return service.createPasswordAccount(ctx, username, password, "", nil)
}

func (service *AccountService) createPasswordAccount(ctx context.Context, username, password, name string, bind func(AccountStore, domain.User) error) (domain.User, error) {
	username, err := domain.NormalizeUsername(username)
	if err != nil {
		return domain.User{}, err
	}
	if err := validateAccountPassword(password); err != nil {
		return domain.User{}, err
	}
	if name == "" {
		name = username
	} else {
		name, err = domain.NormalizeDisplayName(name)
		if err != nil {
			return domain.User{}, err
		}
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return domain.User{}, err
	}
	id, err := service.newID()
	if err != nil {
		return domain.User{}, err
	}
	nowMS := domain.TimeToMillis(domain.NormalizeTime(service.clock()))
	var created domain.User
	err = service.transactions.WriteAccounts(ctx, func(accounts AccountStore, _ SessionStore) error {
		mode, err := accounts.RegistrationMode(ctx)
		if err != nil {
			return err
		}
		if err := domain.ValidateRegistrationMode(mode); err != nil {
			return domain.NewError(domain.CodeDataIntegrityFailed, "stored registration mode is invalid", nil)
		}
		if mode == domain.RegistrationClosed {
			return domain.NewError(domain.CodeRegistrationClosed, "this site is not accepting new accounts", nil)
		}
		status := domain.AccountPending
		if mode == domain.RegistrationOpen {
			status = domain.AccountActive
		}
		created = domain.User{
			ID: id, Username: username, DisplayName: name,
			Role: domain.UserRoleMember, Status: status,
			UsernameSetAtMS: &nowMS, CreatedAtMS: nowMS, UpdatedAtMS: nowMS,
		}
		store, ok := accounts.(PasswordAccountStore)
		if !ok {
			return domain.NewError(domain.CodeDataIntegrityFailed, "password account storage is unavailable", nil)
		}
		if err := store.CreateAccountWithPassword(ctx, created, hash); err != nil {
			return err
		}
		if bind != nil {
			return bind(accounts, created)
		}
		return nil
	})
	if err != nil {
		return domain.User{}, err
	}
	return created, nil
}
