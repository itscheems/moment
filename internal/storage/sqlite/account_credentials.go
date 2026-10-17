package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
)

var _ app.AccountCredentialStore = accountStore{}

func (store accountStore) PasswordHash(ctx context.Context, userID string) ([]byte, bool, error) {
	var hash []byte
	err := store.queries.QueryRowContext(ctx, `SELECT password_hash FROM account_passwords WHERE user_id = ?`, userID).Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read account password: %w", err)
	}
	return hash, true, nil
}

func (store accountStore) PutPasswordHash(ctx context.Context, userID string, hash []byte, atMS int64) error {
	_, err := store.queries.ExecContext(ctx, `INSERT INTO account_passwords(user_id, password_hash, updated_at_ms) VALUES (?, ?, ?)
ON CONFLICT(user_id) DO UPDATE SET password_hash = excluded.password_hash, updated_at_ms = excluded.updated_at_ms`, userID, hash, atMS)
	return err
}

func (store accountStore) InsertAPIToken(ctx context.Context, token domain.APIToken) error {
	_, err := store.queries.ExecContext(ctx, `INSERT INTO api_tokens(id, user_id, label, token_hash, created_at_ms) VALUES (?, ?, ?, ?, ?)`,
		token.ID, token.UserID, token.Label, token.Hash, token.CreatedAtMS)
	return err
}

const tokenColumns = `id, user_id, label, token_hash, created_at_ms, revoked_at_ms`

func scanAPIToken(row interface{ Scan(...any) error }) (domain.APIToken, error) {
	var token domain.APIToken
	var revoked sql.NullInt64
	err := row.Scan(&token.ID, &token.UserID, &token.Label, &token.Hash, &token.CreatedAtMS, &revoked)
	if revoked.Valid {
		token.RevokedAtMS = &revoked.Int64
	}
	return token, err
}

func (store accountStore) APITokenByHash(ctx context.Context, hash []byte) (domain.APIToken, bool, error) {
	token, err := scanAPIToken(store.queries.QueryRowContext(ctx, `SELECT `+tokenColumns+` FROM api_tokens WHERE token_hash = ?`, hash))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.APIToken{}, false, nil
	}
	return token, err == nil, err
}

func (store accountStore) APITokenByID(ctx context.Context, id string) (domain.APIToken, bool, error) {
	token, err := scanAPIToken(store.queries.QueryRowContext(ctx, `SELECT `+tokenColumns+` FROM api_tokens WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.APIToken{}, false, nil
	}
	return token, err == nil, err
}

func (store accountStore) ListAPITokens(ctx context.Context, userID string) ([]domain.APIToken, error) {
	rows, err := store.queries.QueryContext(ctx, `SELECT `+tokenColumns+` FROM api_tokens WHERE user_id = ? ORDER BY created_at_ms DESC, id`, userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	tokens := []domain.APIToken{}
	for rows.Next() {
		token, err := scanAPIToken(rows)
		if err != nil {
			return nil, err
		}
		tokens = append(tokens, token)
	}
	return tokens, rows.Err()
}

func (store accountStore) RevokeAPIToken(ctx context.Context, id string, atMS int64) error {
	_, err := store.queries.ExecContext(ctx, `UPDATE api_tokens SET revoked_at_ms = COALESCE(revoked_at_ms, ?) WHERE id = ?`, atMS, id)
	return err
}

func (store accountStore) RevokeUserAPITokens(ctx context.Context, userID string, atMS int64) error {
	_, err := store.queries.ExecContext(ctx, `UPDATE api_tokens SET revoked_at_ms = ? WHERE user_id = ? AND revoked_at_ms IS NULL`, atMS, userID)
	return err
}
