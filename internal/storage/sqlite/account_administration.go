package sqlite

import (
	"context"

	"github.com/moment/moment/internal/domain"
)

func (store accountStore) InitializeSiteAccount(ctx context.Context, user domain.User, timezone string) error {
	var initialized bool
	if err := store.queries.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users UNION ALL SELECT 1 FROM site_settings)`).Scan(&initialized); err != nil {
		return err
	}
	if initialized {
		return domain.NewError(domain.CodeInstanceAlreadyInitialized, "this instance already contains accounts or site settings", nil)
	}
	if err := store.insertUser(ctx, user); err != nil {
		return err
	}
	_, err := store.queries.ExecContext(ctx, `INSERT INTO site_settings(id,title,timezone,comments_enabled,profile_user_id,registration_mode) VALUES(1,'Moment',?,1,?,'approval')`, timezone, user.ID)
	return err
}

func (store accountStore) SetRole(ctx context.Context, userID string, role domain.UserRole, at int64) error {
	_, err := store.queries.ExecContext(ctx, `UPDATE users SET role=?,updated_at_ms=? WHERE id=?`, string(role), at, userID)
	return err
}

func (store accountStore) HasAuthoredContent(ctx context.Context, userID string) (bool, error) {
	var exists bool
	err := store.queries.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM entries WHERE author_user_id=? UNION ALL SELECT 1 FROM article_drafts WHERE author_user_id=?)`, userID, userID).Scan(&exists)
	return exists, err
}

func (store accountStore) SetSiteProfile(ctx context.Context, userID string) error {
	_, err := store.queries.ExecContext(ctx, `UPDATE site_settings SET profile_user_id=? WHERE id=1`, userID)
	return err
}
