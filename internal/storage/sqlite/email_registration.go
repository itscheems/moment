package sqlite

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"

	"github.com/moment/moment/internal/app"
)

var _ app.EmailRegistrationStore = accountStore{}

func (store accountStore) InsertRegistrationChallenge(ctx context.Context, c app.RegistrationChallenge) error {
	if _, err := store.queries.ExecContext(ctx, `DELETE FROM registration_challenges WHERE requested_at_ms < ?`, c.RequestedAtMS-24*time.Hour.Milliseconds()); err != nil {
		return err
	}
	// A resend supersedes older unconsumed challenges for this address.
	if _, err := store.queries.ExecContext(ctx, `UPDATE registration_challenges SET consumed=1 WHERE email=?`, c.Email); err != nil {
		return err
	}
	_, err := store.queries.ExecContext(ctx, `INSERT INTO registration_challenges(id,email,code_hash,requested_at_ms,expires_at_ms,requester_hash) VALUES(?,?,?,?,?,?)`, c.ID, c.Email, c.CodeHash, c.RequestedAtMS, c.ExpiresAtMS, c.RequesterHash)
	return err
}
func (store accountStore) RegistrationChallenge(ctx context.Context, key string, proof bool) (app.RegistrationChallenge, bool, error) {
	column := "id"
	var lookup any = key
	if proof {
		column = "proof_hash"
		bytes, err := hex.DecodeString(key)
		if err != nil {
			return app.RegistrationChallenge{}, false, err
		}
		lookup = bytes
	}
	var c app.RegistrationChallenge
	err := store.queries.QueryRowContext(ctx, `SELECT id,email,code_hash,proof_hash,requested_at_ms,expires_at_ms,attempts,verified,consumed,requester_hash FROM registration_challenges WHERE `+column+`=?`, lookup).Scan(&c.ID, &c.Email, &c.CodeHash, &c.ProofHash, &c.RequestedAtMS, &c.ExpiresAtMS, &c.Attempts, &c.Verified, &c.Consumed, &c.RequesterHash)
	if errors.Is(err, sql.ErrNoRows) {
		return c, false, nil
	}
	return c, err == nil, err
}
func (store accountStore) SaveRegistrationChallenge(ctx context.Context, c app.RegistrationChallenge) error {
	_, err := store.queries.ExecContext(ctx, `UPDATE registration_challenges SET proof_hash=?,attempts=?,verified=?,consumed=? WHERE id=?`, c.ProofHash, c.Attempts, c.Verified, c.Consumed, c.ID)
	return err
}
func (store accountStore) RegistrationRequestBudget(ctx context.Context, email, requester string, since int64) (int, int, int64, error) {
	var addressCount, requesterCount int
	var last int64
	err := store.queries.QueryRowContext(ctx, `SELECT COALESCE(sum(email=?),0),COALESCE(sum(requester_hash=?),0),COALESCE(max(CASE WHEN email=? THEN requested_at_ms ELSE 0 END),0) FROM registration_challenges WHERE requested_at_ms>=?`, email, requester, email, since).Scan(&addressCount, &requesterCount, &last)
	return addressCount, requesterCount, last, err
}

// ReserveEmailBudget commits before network IO so concurrent sends cannot overspend.
func (repository *AccountRepository) ReserveEmailBudget(ctx context.Context, daily int, now time.Time) (bool, error) {
	if daily <= 0 {
		return false, nil
	}
	allowed := false
	period := now.UTC().Format("2006-01-02")
	err := repository.WriteAccounts(ctx, func(accounts app.AccountStore, _ app.SessionStore) error {
		store := accounts.(accountStore)
		var used int
		if err := store.queries.QueryRowContext(ctx, `SELECT COALESCE((SELECT used FROM email_delivery_usage WHERE provider='gmail' AND period=?),0)`, period).Scan(&used); err != nil {
			return err
		}
		if used >= daily {
			return nil
		}
		if _, err := store.queries.ExecContext(ctx, `INSERT INTO email_delivery_usage(provider,period,used)VALUES('gmail',?,1) ON CONFLICT(provider,period) DO UPDATE SET used=used+1`, period); err != nil {
			return err
		}
		allowed = true
		return nil
	})
	return allowed, err
}
