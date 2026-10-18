package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/moment/moment/internal/domain"
)

// A map is a published value of its entry, never a provider lookup on read.
func (store postStore) entryMapCard(ctx context.Context, id domain.ID) (*domain.MapCard, error) {
	var encoded string
	err := store.queries.QueryRowContext(ctx, `SELECT map_card_json FROM entry_map_card WHERE entry_id = ?`, id.String()).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read entry map: %w", err)
	}
	card, err := domain.ParseMapCard([]byte(encoded))
	if err != nil {
		return nil, domain.NewError(domain.CodeDataIntegrityFailed, "stored map card is invalid", map[string]any{"post_id": id.String()})
	}
	return card, nil
}

func (store postStore) replaceEntryMapCard(ctx context.Context, id domain.ID, card *domain.MapCard) error {
	if card != nil {
		if err := card.Validate(); err != nil {
			return err
		}
	}
	if _, err := store.queries.ExecContext(ctx, `DELETE FROM entry_map_card WHERE entry_id = ?`, id.String()); err != nil {
		return fmt.Errorf("clear entry map: %w", err)
	}
	if card == nil {
		return nil
	}
	encoded, err := json.Marshal(card)
	if err != nil {
		return err
	}
	_, err = store.queries.ExecContext(ctx, `INSERT INTO entry_map_card(entry_id, map_card_json) VALUES (?, ?)`, id.String(), string(encoded))
	if err != nil {
		return fmt.Errorf("write entry map: %w", err)
	}
	return nil
}
