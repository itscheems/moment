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

func TestSiteSettingsMigrationServiceAndAtomicAvatarValidation(t *testing.T) {
	ctx := context.Background()
	database, runtimeState := openTestDatabase(t, Options{})
	repository, err := NewSiteRepository(database, runtimeState.Locks)
	if err != nil {
		t.Fatal(err)
	}
	service, err := app.NewSiteService(repository, app.SiteOptions{AllowDevelopmentLocalHTTP: true})
	if err != nil {
		t.Fatal(err)
	}

	settings, err := service.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if settings.Title != "Moment" || settings.Bio != "" || settings.Timezone == "" || !settings.CommentsEnabled || settings.PublicBaseURL != nil || settings.AvatarMediaID != nil {
		t.Fatalf("fresh settings = %#v", settings)
	}
	if err := service.Ready(ctx); err != nil {
		t.Fatalf("site readiness: %v", err)
	}

	insertManagedMedia(t, database, mediaOne)
	title := "Portable identity"
	avatar := mediaOne
	comments := false
	updated, err := service.Update(ctx, app.SiteUpdate{
		Title:           &title,
		AvatarMediaID:   &app.NullableString{Value: &avatar},
		CommentsEnabled: &comments,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.AvatarMediaID == nil || *updated.AvatarMediaID != mediaOne || updated.CommentsEnabled {
		t.Fatalf("updated = %#v", updated)
	}

	rollbackTitle := "must not persist"
	missing := mediaTwo
	if _, err := service.Update(ctx, app.SiteUpdate{
		Title:         &rollbackTitle,
		AvatarMediaID: &app.NullableString{Value: &missing},
	}); !domain.HasCode(err, domain.CodeMediaNotFound) {
		t.Fatalf("missing media error = %v", err)
	}
	after, err := service.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after.Title != title || after.AvatarMediaID == nil || *after.AvatarMediaID != mediaOne {
		t.Fatalf("failed update partially persisted: %#v", after)
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

	lease, err := runtimeState.Locks.Acquire(context.Background(), runtimefs.MaintenanceLock, runtimefs.Exclusive)
	if err != nil {
		t.Fatal(err)
	}
	title := "blocked"
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	_, updateErr := service.Update(ctx, app.SiteUpdate{Title: &title})
	if !errors.Is(updateErr, context.DeadlineExceeded) {
		t.Fatalf("maintenance-blocked update = %v", updateErr)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}

	mustWrite(t, database, `DELETE FROM site_settings WHERE id = 1`)
	if _, err := service.Get(context.Background()); !domain.HasCode(err, domain.CodeDataIntegrityFailed) {
		t.Fatalf("missing singleton error = %v", err)
	}
}

func TestSiteSettingsPreheldMaintenanceDoesNotReacquireBehindWriter(t *testing.T) {
	database, runtimeState := openTestDatabase(t, Options{})
	guardedRuntime, maintenance, err := runtimefs.OpenForWriteContext(context.Background(), runtimeState.Root)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := NewSiteRepositoryWithinMaintenance(database, maintenance)
	if err != nil {
		t.Fatal(err)
	}
	service, err := app.NewSiteService(repository, app.SiteOptions{AllowDevelopmentLocalHTTP: guardedRuntime.IsDevelopment()})
	if err != nil {
		t.Fatal(err)
	}

	waiterStarted := make(chan struct{})
	writerAcquired := make(chan *runtimefs.Lease, 1)
	writerContext, cancelWriter := context.WithTimeout(context.Background(), time.Second)
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
	updateContext, cancelUpdate := context.WithTimeout(context.Background(), 250*time.Millisecond)
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
	if _, err := service.Update(context.Background(), app.SiteUpdate{Title: &title}); !domain.HasCode(err, domain.CodeInternal) {
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
