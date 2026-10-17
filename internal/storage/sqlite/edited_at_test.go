package sqlite

import (
	"testing"
	"time"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
)

func TestEditedAtTracksContentAndPreservesRepostSemantics(t *testing.T) {
	db, _ := openTestDatabase(t, Options{})
	service := newTestPostService(t, db, func() time.Time { return time.UnixMilli(1700000000000) }, sequenceIDs(entryOne))
	ctx := testAccountContext()
	created, err := service.Create(ctx, app.CreatePostInput{ContentMD: "original"})
	if err != nil {
		t.Fatal(err)
	}
	id := created.Post.Entry.ID
	for _, active := range []bool{true, false} {
		post, err := service.SetReposted(ctx, id, active)
		if err != nil || post.Entry.EditedAt != nil {
			t.Fatalf("repost edit: %+v %v", post, err)
		}
	}
	post, err := service.Update(ctx, app.UpdatePostInput{ID: id, ContentMD: "original"})
	if err != nil || post.Entry.EditedAt != nil {
		t.Fatalf("no-op edit: %+v %v", post, err)
	}
	post, err = service.Update(ctx, app.UpdatePostInput{ID: id, ContentMD: "changed"})
	if err != nil || post.Entry.EditedAt == nil {
		t.Fatalf("real edit: %+v %v", post, err)
	}
	edited := *post.Entry.EditedAt
	post, err = service.Update(ctx, app.UpdatePostInput{ID: id, ContentMD: "changed"})
	if err != nil || !post.Entry.EditedAt.Equal(edited) {
		t.Fatal("no-op moved edit time")
	}
	for _, active := range []bool{true, false} {
		post, err = service.SetReposted(ctx, id, active)
		if err != nil || !post.Entry.EditedAt.Equal(edited) {
			t.Fatal("repost moved edit time")
		}
	}
	post, err = service.Get(ctx, id, app.AccountViewer(testAccountID))
	if err != nil || !post.Entry.EditedAt.Equal(edited) {
		t.Fatal("read lost edit time")
	}
	location := &domain.Location{Name: "Here", DisplayName: "Here", Provider: "manual"}
	post, err = service.Update(ctx, app.UpdatePostInput{ID: id, PreserveContent: true, Location: &location})
	if err != nil || !post.Entry.EditedAt.After(edited) {
		t.Fatalf("location edit: %+v %v", post, err)
	}
}
