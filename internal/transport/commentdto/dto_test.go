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
		Author:      app.CommentAuthor{DisplayName: "Guest"},
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

// A legacy guest row has a name and nothing else; an owner row has the full
// reference set. Anything between the two would render a link to nowhere.
func TestCommentAuthorRejectsPartiallyResolvedIdentity(t *testing.T) {
	username := "cheems"
	userID := "0123456789abcdef0123456789abcdef"
	profileURL := "/@cheems"
	if err := validateAuthor(Author{DisplayName: "Legacy visitor"}); err != nil {
		t.Fatalf("rejected a legacy guest identity: %v", err)
	}
	if err := validateAuthor(Author{UserID: &userID, Username: &username, DisplayName: "Cheems", ProfileURL: &profileURL}); err != nil {
		t.Fatalf("rejected a fully resolved identity: %v", err)
	}
	if err := validateAuthor(Author{Username: &username, DisplayName: "Cheems"}); err == nil {
		t.Fatal("accepted a username without a profile URL")
	}
	mismatched := "/@someone-else"
	if err := validateAuthor(Author{UserID: &userID, Username: &username, DisplayName: "Cheems", ProfileURL: &mismatched}); err == nil {
		t.Fatal("accepted a profile URL that does not match the username")
	}
	if err := validateAuthor(Author{DisplayName: ""}); err == nil {
		t.Fatal("accepted an empty display name")
	}
}
