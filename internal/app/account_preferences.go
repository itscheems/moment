package app

import (
	"context"

	"github.com/moment/moment/internal/domain"
)

// UpdatePreferences changes only private account preferences. Profile edits and
// preference writes cannot overwrite each other's fields.
func (service *AccountService) UpdatePreferences(ctx context.Context, userID string, locale *string) (domain.User, error) {
	if err := domain.ValidateUILocale(locale); err != nil {
		return domain.User{}, err
	}
	var result domain.User
	err := service.transactions.WriteAccounts(ctx, func(accounts AccountStore, _ SessionStore) error {
		if _, err := accounts.UserByID(ctx, userID); err != nil {
			return err
		}
		if err := accounts.UpdatePreferences(ctx, userID, locale, domain.TimeToMillis(service.clock())); err != nil {
			return err
		}
		var err error
		result, err = accounts.UserByID(ctx, userID)
		return err
	})
	return result, err
}
