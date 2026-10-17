package archive

import (
	"context"
	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/runtimefs"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
)

// Authorization binds a staged operation to its initiating account and credential.
// Instance identity and expiration are recorded separately in staging metadata.
type Authorization struct {
	AccountID    string `json:"account_id"`
	CredentialID string `json:"credential_id"`
}

func (subject Authorization) Valid() bool {
	return domain.ValidateUserID(subject.AccountID) == nil && subject.CredentialID != "" && len(subject.CredentialID) <= 128
}

func (committer *Committer) validateAuthorization(ctx context.Context, state *runtimefs.Runtime, db *sqlitestore.Database, subject Authorization, local bool) error {
	if !subject.Valid() {
		return domain.NewError(domain.CodeUnauthorized, "a real account and credential are required", nil)
	}
	repository, err := sqlitestore.NewAccountRepository(db, state.Locks)
	if err != nil {
		return err
	}
	accounts, err := app.NewAccountService(repository, app.WithAccountClock(committer.now))
	if err != nil {
		return err
	}
	return accounts.ValidateAdministrativeCredential(ctx, subject.AccountID, subject.CredentialID, local)
}
