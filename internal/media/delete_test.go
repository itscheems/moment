package media

import (
	"bytes"
	"context"
	"testing"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/storage/sqlite"
)

func TestDeleteRefusesReferencesAndSupportsDryRun(t *testing.T) {
	store, runtimeState, database := newTestStore(t, Limits{})
	ctx := app.WithContentActor(context.Background(), domain.User{ID: "0123456789abcdef0123456789abcdef", Role: domain.UserRoleAdmin, Status: domain.AccountActive})
	value, _, err := store.Ingest(ctx, "image.png", bytes.NewReader(encodeTransparentPNG(t, 20, 20)))
	if err != nil {
		t.Fatal(err)
	}
	result, err := store.Delete(ctx, value.ID, true)
	if err != nil || result.Deleted || !result.DryRun {
		t.Fatalf("dry run: %+v %v", result, err)
	}
	repository, _ := sqlite.NewAccountRepository(database, runtimeState.Locks)
	service, _ := app.NewAccountService(repository)
	id := value.ID.String()
	if _, err := service.UpdateProfile(ctx, "0123456789abcdef0123456789abcdef", app.ProfileUpdate{AvatarMediaID: &app.NullableString{Value: &id}}); err != nil {
		t.Fatal(err)
	}
	for _, dry := range []bool{true, false} {
		if _, err := store.Delete(ctx, value.ID, dry); !domain.HasCode(err, domain.CodeMediaInUse) {
			t.Fatalf("referenced delete %t: %v", dry, err)
		}
	}
	asset, err := store.OpenOriginal(ctx, value.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := asset.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := service.UpdateProfile(ctx, "0123456789abcdef0123456789abcdef", app.ProfileUpdate{AvatarMediaID: &app.NullableString{Value: nil}}); err != nil {
		t.Fatal(err)
	}
	result, err = store.Delete(ctx, value.ID, false)
	if err != nil || !result.Deleted || result.CleanupFailures != 0 {
		t.Fatalf("delete: %+v %v", result, err)
	}
	if _, err := store.Delete(ctx, value.ID, false); !domain.HasCode(err, domain.CodeMediaNotFound) {
		t.Fatalf("repeat delete: %v", err)
	}
}
