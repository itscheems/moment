package commentdto

import (
	"testing"
	"time"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
)

func TestCommentMediaReadProjectionCarriesPositiveDimensions(t *testing.T) {
	commentID, err := domain.ParseID("01ARZ3NDEKTSV4RRFFQ69G5FAV")
	if err != nil {
		t.Fatal(err)
	}
	postID, err := domain.ParseID("01ARZ3NDEKTSV4RRFFQ69G5FAW")
	if err != nil {
		t.Fatal(err)
	}
	mediaID, err := domain.ParseID("01ARZ3NDEKTSV4RRFFQ69G5FAX")
	if err != nil {
		t.Fatal(err)
	}
	wire := FromComment(app.Comment{
		Comment: domain.Comment{
			ID: commentID, EntryID: postID, AuthorKind: domain.CommentAuthorGuest,
			AuthorName: "Guest", ContentMD: "image comment", CreatedAt: time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC),
		},
		ContentHTML: "<p>image comment</p>\n",
		Media:       []app.MediaUsageProjection{{MediaID: mediaID, Position: 0, MIMEType: "image/jpeg", Width: 640, Height: 480}},
	})
	if len(wire.Media) != 1 || wire.Media[0].Width != 640 || wire.Media[0].Height != 480 {
		t.Fatalf("comment media = %#v", wire.Media)
	}
	if err := ValidateComment(wire); err != nil {
		t.Fatal(err)
	}
	wire.Media[0].Width = 0
	if err := ValidateComment(wire); err == nil {
		t.Fatal("accepted zero media width")
	}
}
