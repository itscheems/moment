package media

import (
	"bytes"
	"context"
	"testing"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/storage/sqlite"
)

func TestDeduplicatedUploadsGrantEachUploaderWithoutMakingTheObjectPublic(t *testing.T) {
	store, state, db := newTestStore(t, Limits{})
	repo, _ := sqlite.NewAccountRepository(db, state.Locks)
	accounts, _ := app.NewAccountService(repo)
	first, err := accounts.ProfileAccount(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := accounts.SignIn(context.Background(), app.ExternalIdentity{Provider: "google", ProviderUserID: "second-uploader", DisplayName: "Second Uploader"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := accounts.SetStatus(context.Background(), outcome.User.ID, domain.AccountActive)
	if err != nil {
		t.Fatal(err)
	}
	firstCtx := app.WithContentActor(context.Background(), first)
	secondCtx := app.WithContentActor(context.Background(), second)
	contents := encodeTransparentPNG(t, 20, 20)
	original, _, err := store.Ingest(firstCtx, "first.png", bytes.NewReader(contents))
	if err != nil {
		t.Fatal(err)
	}
	for _, viewer := range []app.Viewer{app.PublicViewer(), app.AccountViewer(second.ID)} {
		if allowed, err := store.CanRead(context.Background(), original.ID, viewer); err != nil || allowed {
			t.Fatalf("unrelated reader saw an unreferenced upload=%t,%v", allowed, err)
		}
	}
	if allowed, err := store.CanRead(context.Background(), original.ID, app.AccountViewer(first.ID)); err != nil || !allowed {
		t.Fatalf("uploader cannot preview=%t,%v", allowed, err)
	}
	duplicate, _, err := store.Ingest(secondCtx, "second.png", bytes.NewReader(contents))
	if err != nil {
		t.Fatal(err)
	}
	if duplicate.ID != original.ID {
		t.Fatal("per-account rights disabled global deduplication")
	}
	if allowed, err := store.CanRead(context.Background(), original.ID, app.AccountViewer(second.ID)); err != nil || !allowed {
		t.Fatalf("duplicate upload did not grant its uploader=%t,%v", allowed, err)
	}
	refs, _ := sqlite.NewMediaReferenceRepository(db)
	if err := refs.Read(context.Background(), func(s app.MediaReferenceStore) error {
		states, err := s.MediaReferenceStates(context.Background())
		if err != nil {
			return err
		}
		if len(states) != 1 || states[0].Referenced {
			t.Fatal("upload permission made abandoned bytes permanently reachable")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPublicMediaIsReadableButReuseStillRequiresAnAccountGrant(t *testing.T) {
	store, state, db := newTestStore(t, Limits{})
	repo, _ := sqlite.NewAccountRepository(db, state.Locks)
	accounts, _ := app.NewAccountService(repo)
	first, err := accounts.ProfileAccount(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := accounts.SignIn(context.Background(), app.ExternalIdentity{Provider: "google", ProviderUserID: "administrator", DisplayName: "Another Admin"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := accounts.SetStatus(context.Background(), outcome.User.ID, domain.AccountActive)
	if err != nil {
		t.Fatal(err)
	}
	second, err = accounts.SetRole(context.Background(), second.ID, domain.UserRoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	firstCtx := app.WithContentActor(context.Background(), first)
	secondCtx := app.WithContentActor(context.Background(), second)
	contents := encodeTransparentPNG(t, 21, 21)
	object, _, err := store.Ingest(firstCtx, "published.png", bytes.NewReader(contents))
	if err != nil {
		t.Fatal(err)
	}
	postRepo, _ := sqlite.NewPostRepository(db, state.Locks)
	posts, _ := app.NewPostService(postRepo)
	usage := []domain.EntryMediaUsage{{MediaID: object.ID, Position: 0}}
	if _, err := posts.Create(firstCtx, app.CreatePostInput{ContentMD: "public image", Media: usage}); err != nil {
		t.Fatal(err)
	}
	if allowed, err := store.CanRead(context.Background(), object.ID, app.PublicViewer()); err != nil || !allowed {
		t.Fatal("published media stopped being public")
	}
	if _, err := posts.Create(secondCtx, app.CreatePostInput{ContentMD: "another administrator cannot adopt it", Media: usage}); !domain.HasCode(err, domain.CodeMediaNotFound) {
		t.Fatalf("role bypassed reuse grant=%v", err)
	}
	raw := object.ID.String()
	if _, err := accounts.UpdateProfile(secondCtx, second.ID, app.ProfileUpdate{AvatarMediaID: &app.NullableString{Value: &raw}}); !domain.HasCode(err, domain.CodeMediaNotFound) {
		t.Fatalf("profile bypassed reuse grant=%v", err)
	}
	if duplicate, _, err := store.Ingest(secondCtx, "my copy.png", bytes.NewReader(contents)); err != nil || duplicate.ID != object.ID {
		t.Fatalf("duplicate upload=%+v,%v", duplicate, err)
	}
	if _, err := posts.Create(secondCtx, app.CreatePostInput{ContentMD: "my explicit upload", Media: usage}); err != nil {
		t.Fatal(err)
	}
	if _, err := accounts.UpdateProfile(secondCtx, second.ID, app.ProfileUpdate{AvatarMediaID: &app.NullableString{Value: &raw}}); err != nil {
		t.Fatal(err)
	}
}
