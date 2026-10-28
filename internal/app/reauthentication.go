package app

import (
	"context"
	"crypto/sha256"

	"github.com/moment/moment/internal/domain"
	"golang.org/x/crypto/bcrypt"
)

// VerifySensitiveCredential requires the current password when one is configured.
// A passwordless account may prove possession of an API token; a browser session
// alone is never sufficient. Local offline recovery remains available.
func (service *AccountService) VerifySensitiveCredential(ctx context.Context, userID, password, token string) error {
	return service.transactions.ReadAccounts(ctx, func(accounts AccountStore, _ SessionStore) error {
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
		hash, hasPassword, err := store.PasswordHash(ctx, userID)
		if err != nil {
			return err
		}
		if hasPassword {
			if password != "" && token == "" && bcrypt.CompareHashAndPassword(hash, []byte(password)) == nil {
				return nil
			}
		} else if token != "" && password == "" {
			digest := sha256.Sum256([]byte(token))
			credential, found, err := store.APITokenByHash(ctx, digest[:])
			if err != nil {
				return err
			}
			if found && credential.UserID == userID && credential.Live() {
				return nil
			}
		}
		return domain.NewError(domain.CodeUnauthorized, "credential confirmation failed", nil)
	})
}

// RevokeAccountCredentials atomically ends all API and browser access for an
// account. This is intentionally distinct from logging out or revoking one token.
func (service *AccountService) RevokeAccountCredentials(ctx context.Context, userID string) error {
	atMS := domain.TimeToMillis(domain.NormalizeTime(service.clock()))
	return service.transactions.WriteAccounts(ctx, func(accounts AccountStore, sessions SessionStore) error {
		if err := authorizeCredentialMutation(ctx, accounts, sessions, userID, atMS); err != nil {
			return err
		}
		if _, err := accounts.UserByID(ctx, userID); err != nil {
			return err
		}
		store, err := credentialStore(accounts)
		if err != nil {
			return err
		}
		if err := store.RevokeUserAPITokens(ctx, userID, atMS); err != nil {
			return err
		}
		_, err = sessions.RevokeUserSessions(ctx, userID, atMS)
		return err
	})
}

// VerifyLinkedCredential confirms an existing provider identity for a
// passwordless account. It never creates or attaches a new identity.
func (service *AccountService) VerifyLinkedCredential(ctx context.Context, userID string, external ExternalIdentity) error {
	return service.transactions.ReadAccounts(ctx, func(accounts AccountStore, _ SessionStore) error {
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
		_, hasPassword, err := store.PasswordHash(ctx, userID)
		if err != nil {
			return err
		}
		if !hasPassword {
			identity, found, err := accounts.IdentityLookup(ctx, external.Provider, external.ProviderUserID)
			if err != nil {
				return err
			}
			if found && identity.UserID == userID {
				return nil
			}
		}
		return domain.NewError(domain.CodeUnauthorized, "credential confirmation failed", nil)
	})
}
