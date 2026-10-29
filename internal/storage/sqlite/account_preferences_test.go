package sqlite

import (
	"errors"
	"testing"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
)

func TestAccountPreferencesPersistIndependentlyOfProfileAndExport(t *testing.T) {
	ctx := testAccountContext()
	db, runtimeState := openTestDatabase(t, Options{})
	repository, err := NewAccountRepository(db, runtimeState.Locks)
	if err != nil {
		t.Fatal(err)
	}
	accounts, err := app.NewAccountService(repository)
	if err != nil {
		t.Fatal(err)
	}
	users, err := accounts.List(ctx)
	if err != nil || len(users) == 0 {
		t.Fatalf("fixture users: %v %v", users, err)
	}
	user := users[0]
	if user.UILocale != nil {
		t.Fatal("existing account should not acquire an explicit preference")
	}
	locale := "en"
	saved, err := accounts.UpdatePreferences(ctx, user.ID, &locale)
	if err != nil || saved.UILocale == nil || *saved.UILocale != locale {
		t.Fatalf("saved preference: %+v %v", saved, err)
	}
	name := "Updated profile"
	if _, err = accounts.UpdateProfile(ctx, user.ID, app.ProfileUpdate{DisplayName: &name}); err != nil {
		t.Fatal(err)
	}
	got, err := accounts.AccountByUsername(ctx, user.Username)
	if err != nil || got.DisplayName != name || got.UILocale == nil || *got.UILocale != "en" {
		t.Fatalf("profile overwrote preference: %+v %v", got, err)
	}
	snapshot, err := db.BeginArchiveSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = snapshot.Close() }()
	found := false
	if err := snapshot.Users(ctx, func(value ArchiveUser) error {
		if value.ID == user.ID {
			found = value.UILocale != nil && *value.UILocale == "en"
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("archive projection lost the saved preference")
	}
	bad := "unsupported"
	if _, err := accounts.UpdatePreferences(ctx, user.ID, &bad); err == nil {
		t.Fatal("unsupported locale accepted")
	}
	if _, err := accounts.UpdatePreferences(ctx, user.ID, nil); err != nil {
		t.Fatal(err)
	}
	got, err = accounts.AccountByUsername(ctx, user.Username)
	if err != nil || got.UILocale != nil || got.DisplayName != name {
		t.Fatalf("clearing preference: %+v %v", got, err)
	}
	_, err = accounts.UpdatePreferences(ctx, "00000000000000000000000000000000", &locale)
	var failure *domain.Error
	if !errors.As(err, &failure) || failure.Code != domain.CodeAccountNotFound {
		t.Fatalf("missing account: %v", err)
	}
}
