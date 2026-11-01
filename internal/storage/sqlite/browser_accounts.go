package sqlite

import (
	"context"
	"database/sql"
	"errors"

	"github.com/moment/moment/internal/app"
)

var _ app.BrowserAccountStore = accountStore{}

func (store accountStore) CreateBrowserAccountSet(ctx context.Context, set app.BrowserAccountSet) error {
	_, err := store.queries.ExecContext(ctx, `INSERT INTO browser_account_sets(id,credential_hash,csrf_token,session_epoch,expires_at_ms) VALUES(?,?,?,?,?)`, set.ID, set.CredentialHash, set.CSRFToken, set.SessionEpoch, set.ExpiresAtMS)
	return err
}
func (store accountStore) BrowserAccountSetByHash(ctx context.Context, hash []byte) (app.BrowserAccountSet, bool, error) {
	var set app.BrowserAccountSet
	var revoked sql.NullInt64
	err := store.queries.QueryRowContext(ctx, `SELECT id,credential_hash,csrf_token,session_epoch,expires_at_ms,revoked_at_ms FROM browser_account_sets WHERE credential_hash=?`, hash).Scan(&set.ID, &set.CredentialHash, &set.CSRFToken, &set.SessionEpoch, &set.ExpiresAtMS, &revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return set, false, nil
	}
	if revoked.Valid {
		set.RevokedAtMS = &revoked.Int64
	}
	return set, err == nil, err
}
func (store accountStore) BrowserAccountMembers(ctx context.Context, id string) ([]app.BrowserAccountMember, error) {
	rows, err := store.queries.QueryContext(ctx, `SELECT set_id,user_id,session_id,last_used_at_ms FROM browser_account_members WHERE set_id=? ORDER BY last_used_at_ms DESC,user_id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	members := []app.BrowserAccountMember{}
	for rows.Next() {
		var m app.BrowserAccountMember
		if err := rows.Scan(&m.SetID, &m.UserID, &m.SessionID, &m.LastUsedAtMS); err != nil {
			return nil, err
		}
		members = append(members, m)
	}
	return members, rows.Err()
}
func (store accountStore) PutBrowserAccountMember(ctx context.Context, m app.BrowserAccountMember, expires int64) error {
	_, err := store.queries.ExecContext(ctx, `INSERT INTO browser_account_members(set_id,user_id,session_id,last_used_at_ms) VALUES(?,?,?,?) ON CONFLICT(set_id,user_id) DO UPDATE SET session_id=excluded.session_id,last_used_at_ms=excluded.last_used_at_ms`, m.SetID, m.UserID, m.SessionID, m.LastUsedAtMS)
	if err != nil {
		return err
	}
	_, err = store.queries.ExecContext(ctx, `UPDATE browser_account_sets SET expires_at_ms=MAX(expires_at_ms,?) WHERE id=?`, expires, m.SetID)
	return err
}
func (store accountStore) DeleteBrowserAccountMember(ctx context.Context, setID, userID string) error {
	_, err := store.queries.ExecContext(ctx, `DELETE FROM browser_account_members WHERE set_id=? AND user_id=?`, setID, userID)
	return err
}
func (store accountStore) RevokeBrowserAccountSet(ctx context.Context, id string, at int64) error {
	_, err := store.queries.ExecContext(ctx, `UPDATE browser_account_sets SET revoked_at_ms=? WHERE id=?`, at, id)
	return err
}

func (store accountStore) RotateBrowserAccountSet(ctx context.Context, set app.BrowserAccountSet) error {
	_, err := store.queries.ExecContext(ctx, `UPDATE browser_account_sets SET credential_hash=?,csrf_token=? WHERE id=?`, set.CredentialHash, set.CSRFToken, set.ID)
	return err
}
