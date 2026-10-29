package sqlite

import (
	"context"
	"fmt"

	"github.com/moment/moment/internal/domain"
)

func (store accountStore) UpdatePreferences(ctx context.Context, userID string, locale *string, atMS int64) error {
	result, err := store.queries.ExecContext(ctx,
		`UPDATE users SET ui_locale = ?, updated_at_ms = ? WHERE id = ?`, optionalString(locale), atMS, userID)
	if err != nil {
		return fmt.Errorf("update account preferences: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect account preferences update: %w", err)
	}
	if changed != 1 {
		return domain.NewError(domain.CodeAccountNotFound, "no such account", nil)
	}
	return nil
}
