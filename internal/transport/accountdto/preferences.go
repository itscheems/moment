package accountdto

import "github.com/moment/moment/internal/domain"

// Preferences is private to the account itself and is never part of a profile.
type Preferences struct {
	UILocale *string `json:"ui_locale"`
}

// PreferencesFromUser projects the account's private interface preferences.
func PreferencesFromUser(user domain.User) Preferences {
	return Preferences{UILocale: user.UILocale}
}
