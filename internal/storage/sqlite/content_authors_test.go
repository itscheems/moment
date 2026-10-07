package sqlite

import (
	"context"
	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
	"testing"
	"time"
)

func addContentTestAccount(t *testing.T, db *Database, id, username string) domain.User {
	t.Helper()
	err := db.WriteTx(context.Background(), func(q Queries) error {
		_, err := q.ExecContext(context.Background(), `INSERT INTO users(id, username, display_name, role, status, created_at_ms, updated_at_ms) VALUES(?, ?, ?, 'member', 'active', 1, 1)`, id, username, username)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return domain.User{ID: id, Username: username, DisplayName: username, Role: domain.UserRoleMember, Status: domain.AccountActive}
}

func TestContentAuthorshipDoesNotFollowAdministratorRole(t *testing.T) {
	ctx := context.Background()
	db, service := articleServiceFixture(t, time.UnixMilli(1000))
	alice := addContentTestAccount(t, db, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "alice")
	bob := addContentTestAccount(t, db, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "bob")
	private, err := service.Create(app.WithContentActor(ctx, alice), app.CreatePostInput{ContentMD: "alice private", Visibility: domain.VisibilityOnlyMe})
	if err != nil {
		t.Fatal(err)
	}
	if private.Post.Entry.AuthorUserID != alice.ID {
		t.Fatalf("author = %q", private.Post.Entry.AuthorUserID)
	}
	aliceView := app.ViewerFor(alice, true)
	read, err := service.Get(ctx, private.Post.Entry.ID, aliceView)
	if err != nil || read.Author == nil || read.Author.UserID != alice.ID {
		t.Fatalf("author read = %+v, %v", read, err)
	}
	for _, viewer := range []app.Viewer{app.PublicViewer(), app.ViewerFor(bob, true), app.OwnerViewer()} {
		if _, err := service.Get(ctx, private.Post.Entry.ID, viewer); err == nil {
			t.Fatal("non-author read private content")
		}
		page, err := service.List(ctx, app.ListPostsInput{Viewer: viewer})
		if err != nil || len(page.Items) != 0 {
			t.Fatalf("non-author timeline = %+v, %v", page, err)
		}
	}
	// Admin identity changes must not transfer existing content to the new admin.
	err = db.WriteTx(ctx, func(q Queries) error {
		if _, err := q.ExecContext(ctx, `UPDATE users SET role='member' WHERE role='owner'`); err != nil {
			return err
		}
		_, err := q.ExecContext(ctx, `UPDATE users SET role='owner' WHERE id=?`, bob.ID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	bob.Role = domain.UserRoleOwner
	if _, err := service.Get(ctx, private.Post.Entry.ID, app.ViewerFor(bob, true)); err == nil {
		t.Fatal("new admin read another author's private content")
	}
	if _, err := service.Get(ctx, private.Post.Entry.ID, aliceView); err != nil {
		t.Fatal(err)
	}
}

func TestContentWriteRejectsUnattributedRecoveryIdentity(t *testing.T) {
	ctx := context.Background()
	_, service := articleServiceFixture(t, time.UnixMilli(1000))
	actor := domain.User{Role: domain.UserRoleOwner, Status: domain.AccountActive}
	if _, err := service.Create(app.WithContentActor(ctx, actor), app.CreatePostInput{ContentMD: "unattributed"}); err == nil {
		t.Fatal("recovery identity created content without an account")
	}
}
