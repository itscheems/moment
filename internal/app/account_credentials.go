package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"unicode/utf8"

	"github.com/moment/moment/internal/domain"
	"golang.org/x/crypto/bcrypt"
)

// AccountCredentialStore shares the account transaction so a credential and
// the account's current status are always resolved together.
type AccountCredentialStore interface {
	PasswordHash(context.Context, string) ([]byte, bool, error)
	PutPasswordHash(context.Context, string, []byte, int64) error
	InsertAPIToken(context.Context, domain.APIToken) error
	APITokenByHash(context.Context, []byte) (domain.APIToken, bool, error)
	APITokenByID(context.Context, string) (domain.APIToken, bool, error)
	ListAPITokens(context.Context, string) ([]domain.APIToken, error)
	RevokeAPIToken(context.Context, string, int64) error
	RevokeUserAPITokens(context.Context, string, int64) error
}

const (
	// PasswordMinBytes is the shortest accepted UTF-8 password in bytes.
	PasswordMinBytes = 8
	// PasswordMaxBytes matches bcrypt's maximum password input size.
	PasswordMaxBytes = 72
)

func validateAccountPassword(password string) error {
	if !utf8.ValidString(password) || len(password) < PasswordMinBytes || len(password) > PasswordMaxBytes || strings.TrimSpace(password) != password {
		return domain.NewError(domain.CodeInvalidArgument, "password must contain 8 to 72 UTF-8 bytes without surrounding whitespace", map[string]any{"field": "password"})
	}
	return nil
}

// IssuedAPIToken returns a one-time plaintext credential alongside its persisted metadata.
type IssuedAPIToken struct {
	Token domain.APIToken `json:"credential"`
	Value string          `json:"token"`
}

// TokenAuthorization binds an API credential to the live account it represents.
type TokenAuthorization struct {
	User  domain.User
	Token domain.APIToken
}

func credentialStore(accounts AccountStore) (AccountCredentialStore, error) {
	store, ok := accounts.(AccountCredentialStore)
	if !ok {
		return nil, domain.NewError(domain.CodeDataIntegrityFailed, "account credential storage is unavailable", nil)
	}
	return store, nil
}

// VerifyPassword proves one named account, independently of its role.
func (service *AccountService) VerifyPassword(ctx context.Context, username, password string) (domain.User, error) {
	var user domain.User
	err := service.transactions.ReadAccounts(ctx, func(accounts AccountStore, _ SessionStore) error {
		account, err := accounts.UserByUsername(ctx, strings.ToLower(strings.TrimSpace(username)))
		if err != nil {
			return err
		}
		store, err := credentialStore(accounts)
		if err != nil {
			return err
		}
		hash, found, err := store.PasswordHash(ctx, account.ID)
		if err != nil {
			return err
		}
		if !found || bcrypt.CompareHashAndPassword(hash, []byte(password)) != nil {
			return domain.NewError(domain.CodeUnauthorized, "account credential is invalid", nil)
		}
		if !account.CanSignIn() {
			return domain.NewError(domain.CodeAccountSuspended, "this account has been suspended", nil)
		}
		user = account
		return nil
	})
	return user, err
}

// SetAccountPassword atomically replaces a password and revokes this account's
// browser sessions. API credentials have their own explicit revocation.
func (service *AccountService) SetAccountPassword(ctx context.Context, userID, password string) error {
	if err := validateAccountPassword(password); err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	atMS := domain.TimeToMillis(domain.NormalizeTime(service.clock()))
	return service.transactions.WriteAccounts(ctx, func(accounts AccountStore, sessions SessionStore) error {
		user, err := accounts.UserByID(ctx, userID)
		if err != nil {
			return err
		}
		if !user.CanSignIn() {
			return domain.NewError(domain.CodeAccountSuspended, "this account has been suspended", nil)
		}
		store, err := credentialStore(accounts)
		if err != nil {
			return err
		}
		if err := store.PutPasswordHash(ctx, userID, hash, atMS); err != nil {
			return err
		}
		_, err = sessions.RevokeUserSessions(ctx, userID, atMS)
		return err
	})
}

// CreateAPIToken issues a separately revocable credential for an active account.
func (service *AccountService) CreateAPIToken(ctx context.Context, userID, label string) (IssuedAPIToken, error) {
	label = strings.TrimSpace(label)
	if !utf8.ValidString(label) || utf8.RuneCountInString(label) > 80 || strings.ContainsAny(label, "\r\n\x00") {
		return IssuedAPIToken{}, domain.NewError(domain.CodeInvalidArgument, "token label is invalid", map[string]any{"field": "label"})
	}
	var bytes [32]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return IssuedAPIToken{}, err
	}
	value := base64.RawURLEncoding.EncodeToString(bytes[:])
	digest := sha256.Sum256([]byte(value))
	id, err := service.newID()
	if err != nil {
		return IssuedAPIToken{}, err
	}
	token := domain.APIToken{ID: id, UserID: userID, Label: label, Hash: digest[:], CreatedAtMS: domain.TimeToMillis(domain.NormalizeTime(service.clock()))}
	err = service.transactions.WriteAccounts(ctx, func(accounts AccountStore, _ SessionStore) error {
		user, err := accounts.UserByID(ctx, userID)
		if err != nil {
			return err
		}
		if !user.CanWritePublicContent() {
			return domain.NewError(domain.CodeAccountNotApproved, "an active account is required to create API credentials", nil)
		}
		store, err := credentialStore(accounts)
		if err != nil {
			return err
		}
		return store.InsertAPIToken(ctx, token)
	})
	if err != nil {
		return IssuedAPIToken{}, err
	}
	return IssuedAPIToken{Token: token, Value: value}, nil
}

// ResolveAPIToken checks the stored credential and current account on every
// request. No credential can authorize a missing or suspended account.
func (service *AccountService) ResolveAPIToken(ctx context.Context, value string) (TokenAuthorization, error) {
	var result TokenAuthorization
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(decoded) != 32 {
		return result, domain.NewError(domain.CodeUnauthorized, "account credential is invalid", nil)
	}
	digest := sha256.Sum256([]byte(value))
	err = service.transactions.ReadAccounts(ctx, func(accounts AccountStore, _ SessionStore) error {
		store, err := credentialStore(accounts)
		if err != nil {
			return err
		}
		token, found, err := store.APITokenByHash(ctx, digest[:])
		if err != nil {
			return err
		}
		if !found || !token.Live() {
			return domain.NewError(domain.CodeUnauthorized, "account credential is invalid", nil)
		}
		user, err := accounts.UserByID(ctx, token.UserID)
		if err != nil {
			return err
		}
		if !user.CanSignIn() {
			return domain.NewError(domain.CodeAccountSuspended, "this account has been suspended", nil)
		}
		result = TokenAuthorization{User: user, Token: token}
		return nil
	})
	return result, err
}

// APITokens lists credential metadata without returning plaintext tokens.
func (service *AccountService) APITokens(ctx context.Context, userID string) ([]domain.APIToken, error) {
	var tokens []domain.APIToken
	err := service.transactions.ReadAccounts(ctx, func(accounts AccountStore, _ SessionStore) error {
		if _, err := accounts.UserByID(ctx, userID); err != nil {
			return err
		}
		store, err := credentialStore(accounts)
		if err != nil {
			return err
		}
		tokens, err = store.ListAPITokens(ctx, userID)
		return err
	})
	return tokens, err
}

// RevokeAPIToken revokes one credential belonging to the selected account.
func (service *AccountService) RevokeAPIToken(ctx context.Context, userID, tokenID string) error {
	return service.transactions.WriteAccounts(ctx, func(accounts AccountStore, _ SessionStore) error {
		store, err := credentialStore(accounts)
		if err != nil {
			return err
		}
		token, found, err := store.APITokenByID(ctx, tokenID)
		if err != nil {
			return err
		}
		if !found || token.UserID != userID {
			return domain.NewError(domain.CodeAPITokenNotFound, "API credential does not exist for this account", nil)
		}
		return store.RevokeAPIToken(ctx, tokenID, domain.TimeToMillis(domain.NormalizeTime(service.clock())))
	})
}

// AccountSignInMethods reports the configured credential kinds for an account.
func (service *AccountService) AccountSignInMethods(ctx context.Context, userID string) ([]domain.CredentialKind, error) {
	methods := []domain.CredentialKind{}
	err := service.transactions.ReadAccounts(ctx, func(accounts AccountStore, _ SessionStore) error {
		if _, err := accounts.UserByID(ctx, userID); err != nil {
			return err
		}
		store, err := credentialStore(accounts)
		if err != nil {
			return err
		}
		if _, found, err := store.PasswordHash(ctx, userID); err != nil {
			return err
		} else if found {
			methods = append(methods, domain.CredentialPassword)
		}
		tokens, err := store.ListAPITokens(ctx, userID)
		if err != nil {
			return err
		}
		for _, token := range tokens {
			if token.Live() {
				methods = append(methods, domain.CredentialToken)
				break
			}
		}
		return nil
	})
	return methods, err
}
