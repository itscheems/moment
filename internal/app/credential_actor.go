package app

import (
	"context"
	"strings"

	"github.com/moment/moment/internal/domain"
)

type credentialActorKey struct{}

// WithCredentialActor binds remote credential mutations to the live credential
// that admitted the request. Offline operations deliberately have no such actor.
func WithCredentialActor(ctx context.Context, credentialID string) context.Context {
	return context.WithValue(ctx, credentialActorKey{}, credentialID)
}

func authorizeCredentialMutation(ctx context.Context, accounts AccountStore, sessions SessionStore, userID string, nowMS int64) error {
	id, remote := ctx.Value(credentialActorKey{}).(string)
	if !remote {
		return nil
	}
	store, err := credentialStore(accounts)
	if err != nil {
		return err
	}
	if strings.HasPrefix(id, "session:") {
		session, found, err := sessions.SessionByID(ctx, strings.TrimPrefix(id, "session:"))
		if err != nil {
			return err
		}
		if !found || session.UserID != userID || !session.Live(nowMS) {
			return domain.NewError(domain.CodeUnauthorized, "this session is no longer valid", nil)
		}
		if session.SourceTokenID == nil {
			return nil
		}
		id = "token:" + *session.SourceTokenID
	}
	if strings.HasPrefix(id, "token:") {
		token, found, err := store.APITokenByID(ctx, strings.TrimPrefix(id, "token:"))
		if err != nil {
			return err
		}
		if found && token.UserID == userID && token.Live() {
			return nil
		}
	}
	return domain.NewError(domain.CodeUnauthorized, "account credential is invalid", nil)
}
