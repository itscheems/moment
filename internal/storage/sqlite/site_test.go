package sqlite

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/runtimefs"
)

func TestSiteConfigurationDoesNotWriteTheSelectedAccount(t *testing.T) {
	ctx := testAccountContext()
	db, state := openTestDatabase(t, Options{})
	repo, _ := NewSiteRepository(db, state.Locks)
	site, _ := app.NewSiteService(repo, app.SiteOptions{})
	title := "Site configuration"
	before, err := site.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := site.Update(ctx, app.SiteUpdate{Title: &title})
	if err != nil || updated.Title != title || updated.Username != before.Username || updated.DisplayName != before.DisplayName || updated.Bio != before.Bio {
		t.Fatalf("site update=%+v,%v", updated, err)
	}
	if err := repo.Write(ctx, func(store app.SiteStore) error {
		settings, err := store.SiteSettings(ctx)
		if err != nil {
			return err
		}
		settings.Username = "unrelated"
		settings.DisplayName = "Changed person"
		return store.UpdateSiteSettings(ctx, settings)
	}); err != nil {
		t.Fatal(err)
	}
	after, err := site.Get(ctx)
	if err != nil || after.Username != before.Username || after.DisplayName != before.DisplayName {
		t.Fatalf("site persistence changed an account=%+v,%v", after, err)
	}
}

func TestSiteSettingsReadFailsClosedAndUpdateHonorsMaintenanceLock(t *testing.T) {
	database, runtimeState := openTestDatabase(t, Options{})
	repository, err := NewSiteRepository(database, runtimeState.Locks)
	if err != nil {
		t.Fatal(err)
	}
	service, err := app.NewSiteService(repository, app.SiteOptions{AllowDevelopmentLocalHTTP: true})
	if err != nil {
		t.Fatal(err)
	}

	lease, err := runtimeState.Locks.Acquire(testAccountContext(), runtimefs.MaintenanceLock, runtimefs.Exclusive)
	if err != nil {
		t.Fatal(err)
	}
	title := "blocked"
	ctx, cancel := context.WithTimeout(testAccountContext(), 25*time.Millisecond)
	defer cancel()
	_, updateErr := service.Update(ctx, app.SiteUpdate{Title: &title})
	if !errors.Is(updateErr, context.DeadlineExceeded) {
		t.Fatalf("maintenance-blocked update = %v", updateErr)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}

	mustWrite(t, database, `DELETE FROM site_settings WHERE id = 1`)
	if _, err := service.Get(testAccountContext()); !domain.HasCode(err, domain.CodeDataIntegrityFailed) {
		t.Fatalf("missing singleton error = %v", err)
	}
}

func TestSiteSettingsPreheldMaintenanceDoesNotReacquireBehindWriter(t *testing.T) {
	database, runtimeState := openTestDatabase(t, Options{})
	guardedRuntime, maintenance, err := runtimefs.OpenForWriteContext(testAccountContext(), runtimeState.Root)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := NewSiteRepositoryWithinMaintenance(database, maintenance, runtimeState.Locks)
	if err != nil {
		t.Fatal(err)
	}
	service, err := app.NewSiteService(repository, app.SiteOptions{AllowDevelopmentLocalHTTP: guardedRuntime.IsDevelopment()})
	if err != nil {
		t.Fatal(err)
	}

	waiterStarted := make(chan struct{})
	writerAcquired := make(chan *runtimefs.Lease, 1)
	writerContext, cancelWriter := context.WithTimeout(testAccountContext(), time.Second)
	defer cancelWriter()
	go func() {
		close(waiterStarted)
		lease, acquireErr := runtimeState.Locks.Acquire(writerContext, runtimefs.MaintenanceLock, runtimefs.Exclusive)
		if acquireErr == nil {
			writerAcquired <- lease
		}
	}()
	<-waiterStarted
	select {
	case lease := <-writerAcquired:
		_ = lease.Release()
		t.Fatal("exclusive writer acquired while shared lease was held")
	case <-time.After(25 * time.Millisecond):
	}

	title := "No nested lock"
	updateContext, cancelUpdate := context.WithTimeout(testAccountContext(), 250*time.Millisecond)
	defer cancelUpdate()
	if _, err := service.Update(updateContext, app.SiteUpdate{Title: &title}); err != nil {
		t.Fatalf("preheld update waited behind later writer: %v", err)
	}
	if err := maintenance.Release(); err != nil {
		t.Fatal(err)
	}
	select {
	case lease := <-writerAcquired:
		if err := lease.Release(); err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("exclusive writer did not acquire after shared release")
	}
	if _, err := service.Update(testAccountContext(), app.SiteUpdate{Title: &title}); !domain.HasCode(err, domain.CodeInternal) {
		t.Fatalf("released maintenance lease allowed a write: %v", err)
	}
}

func insertManagedMedia(t *testing.T, database *Database, id string) {
	t.Helper()
	sha := strings.Repeat("a", 64)
	path := "media/original/aa/" + sha + ".jpg"
	mustWrite(t, database, `
INSERT INTO media (
    id, sha256, storage_path, original_name, mime_type,
    byte_size, width, height, created_at_ms, unreferenced_since_ms
)
VALUES (?, ?, ?, 'avatar.jpg', 'image/jpeg', 10, 32, 32, 1, 1)`, id, sha, path)
}

// The owner renaming onto a member's handle is a conflict, not a server fault.
//
// users_username_unique guards this column for every writer, and the account
// route reports its refusal as USERNAME_TAKEN. The owner's route wrapped the
// same constraint failure as an opaque error, so the settings form was told
// "operation failed" for a refusal it already carries the words for.
func TestAccountRenameOntoAnotherHandleIsReportedAsAConflict(t *testing.T) {
	ctx := testAccountContext()
	database, runtimeState := openTestDatabase(t, Options{})

	accountRepository, err := NewAccountRepository(database, runtimeState.Locks)
	if err != nil {
		t.Fatal(err)
	}
	accounts, err := app.NewAccountService(accountRepository)
	if err != nil {
		t.Fatal(err)
	}
	member, err := accounts.SignIn(ctx, app.ExternalIdentity{
		Provider: "google", ProviderUserID: "member-1", DisplayName: "Meihua",
	})
	if err != nil {
		t.Fatal(err)
	}
	taken := "meihua"
	if _, err := accounts.UpdateProfile(ctx, member.User.ID, app.ProfileUpdate{Username: &taken}); err != nil {
		t.Fatal(err)
	}

	siteRepository, err := NewSiteRepository(database, runtimeState.Locks)
	if err != nil {
		t.Fatal(err)
	}
	site, err := app.NewSiteService(siteRepository, app.SiteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := accounts.UpdateProfile(ctx, testAccountID, app.ProfileUpdate{Username: &taken}); !domain.HasCode(err, domain.CodeUsernameTaken) {
		t.Fatalf("rename onto a held handle = %v", err)
	}

	// The refused write left both rows alone, and a free handle still lands.
	free := "itscheems"
	saved, err := accounts.UpdateProfile(ctx, testAccountID, app.ProfileUpdate{Username: &free})
	if err != nil || saved.Username != free {
		t.Fatalf("free handle = %#v, err = %v", saved, err)
	}
	if projected, err := site.Get(ctx); err != nil || projected.Username != free {
		t.Fatalf("site did not project the selected account: %+v,%v", projected, err)
	}
	held, err := accounts.Account(ctx, member.User.ID)
	if err != nil || held.Username != taken {
		t.Fatalf("member handle = %#v, err = %v", held, err)
	}
}

// registration_mode is site policy the owner sets, and the row has to keep it.
func TestSiteRegistrationModeRoundTripsThroughTheRow(t *testing.T) {
	ctx := testAccountContext()
	database, runtimeState := openTestDatabase(t, Options{})
	repository, err := NewSiteRepository(database, runtimeState.Locks)
	if err != nil {
		t.Fatal(err)
	}
	service, err := app.NewSiteService(repository, app.SiteOptions{})
	if err != nil {
		t.Fatal(err)
	}

	// The migration default is the owner's deliberate choice, not a placeholder.
	fresh, err := service.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.RegistrationMode != domain.RegistrationApproval {
		t.Fatalf("fresh registration mode = %q", fresh.RegistrationMode)
	}

	closed := string(domain.RegistrationClosed)
	if _, err := service.Update(ctx, app.SiteUpdate{RegistrationMode: &closed}); err != nil {
		t.Fatal(err)
	}
	reread, err := service.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if reread.RegistrationMode != domain.RegistrationClosed {
		t.Fatalf("stored registration mode = %q", reread.RegistrationMode)
	}

	// Everything the site row holds beside it is untouched by that write.
	title := "Portable identity"
	if _, err := service.Update(ctx, app.SiteUpdate{Title: &title}); err != nil {
		t.Fatal(err)
	}
	after, err := service.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after.RegistrationMode != domain.RegistrationClosed || after.Title != title {
		t.Fatalf("after unrelated write = %#v", after)
	}
}

func TestAccountProfileTextValidationAndNormalizationRemainAtomic(t *testing.T) {
	_, accounts := newAccountFixture(t)
	ctx := testAccountContext()
	before, err := accounts.Account(ctx, testAccountID)
	if err != nil {
		t.Fatal(err)
	}
	ptr := func(v string) *string { return &v }
	for name, update := range map[string]app.ProfileUpdate{
		"newline name":        {DisplayName: ptr("Che\nems")},
		"tab name":            {DisplayName: ptr("Che\tems")},
		"empty name":          {DisplayName: ptr("   ")},
		"zero byte bio":       {Bio: ptr("\x00")},
		"carriage return bio": {Bio: ptr("first\rsecond")},
		"long bio":            {Bio: ptr(strings.Repeat("界", 501))},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := accounts.UpdateProfile(ctx, before.ID, update); !domain.HasCode(err, domain.CodeInvalidArgument) {
				t.Fatalf("invalid profile=%v", err)
			}
			after, err := accounts.Account(ctx, before.ID)
			if err != nil || after.Username != before.Username || after.DisplayName != before.DisplayName || after.Bio != before.Bio {
				t.Fatal("invalid profile update partially persisted")
			}
		})
	}
	saved, err := accounts.UpdateProfile(ctx, before.ID, app.ProfileUpdate{DisplayName: ptr("  Cheems  "), Bio: ptr(" first\nsecond ")})
	if err != nil || saved.DisplayName != "Cheems" || saved.Bio != "first\nsecond" {
		t.Fatalf("normalization=%+v,%v", saved, err)
	}
	for _, handle := range []string{"search", "api", "login", "assets", "archive", "profile"} {
		if _, err := accounts.UpdateProfile(ctx, before.ID, app.ProfileUpdate{Username: &handle}); !domain.HasCode(err, domain.CodeUsernameReserved) {
			t.Fatalf("reserved handle %q=%v", handle, err)
		}
	}
}
