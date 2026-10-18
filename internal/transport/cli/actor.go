package cli

import (
	"context"
	"strings"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/archive"
	"github.com/moment/moment/internal/domain"
)

func prepareAccountContext(ctx context.Context, invocation Invocation) (context.Context, error) {
	if invocation.Mode() != ModeDirect || strings.TrimSpace(invocation.Options().As) == "" {
		return ctx, nil
	}
	accounts, database, err := openDirectAccounts(ctx, invocation)
	if err != nil {
		return nil, err
	}
	defer func() { _ = database.Close() }()
	user, err := accounts.AccountByUsername(ctx, invocation.Options().As)
	if err != nil {
		return nil, accountCLIError(err)
	}
	if !user.CanSignIn() {
		return nil, canonicalPublicError(string(domain.CodeAccountSuspended), "this account has been suspended", nil, 0)
	}
	return app.WithContentActor(ctx, user), nil
}

func directArchiveSubject(ctx context.Context) archive.Authorization {
	user, ok := app.ContentActor(ctx)
	if !ok || !user.IsAdmin() || user.Status != domain.AccountActive {
		return archive.Authorization{}
	}
	return archive.Authorization{AccountID: user.ID, CredentialID: "local"}
}

func directContentActor(ctx context.Context) (domain.User, error) {
	user, ok := app.ContentActor(ctx)
	if !ok || domain.ValidateUserID(user.ID) != nil || !user.CanWritePublicContent() {
		return domain.User{}, canonicalPublicError(string(domain.CodeUnauthorized), "an active account identity is required", nil, 0)
	}
	return user, nil
}
