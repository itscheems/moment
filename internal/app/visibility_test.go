package app

import (
	"github.com/moment/moment/internal/domain"
	"testing"
)

func TestVisibilityBelongsToTheAuthorRatherThanAdministratorRole(t *testing.T) {
	author := domain.User{ID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Role: domain.UserRoleMember, Status: domain.AccountActive}
	admin := domain.User{ID: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Role: domain.UserRoleAdmin, Status: domain.AccountActive}
	private := domain.Entry{AuthorUserID: author.ID, Visibility: domain.VisibilityOnlyMe, PublicationState: "published"}
	if !ViewerFor(author, true).CanReadEntry(private) {
		t.Fatal("author cannot read own private content")
	}
	for _, viewer := range []Viewer{PublicViewer(), ViewerFor(admin, true), ViewerFor(author, false), AccountViewer("")} {
		if viewer.CanReadEntry(private) {
			t.Fatal("non-author reads private content")
		}
	}
	private.PublicationState = "unpublished"
	if ViewerFor(author, true).CanReadEntry(private) {
		t.Fatal("unfinished work appeared in reader APIs")
	}
}

func TestInvalidOrSuspendedSubjectsDoNotCreatePrivateViewers(t *testing.T) {
	for _, user := range []domain.User{{Role: domain.UserRoleAdmin, Status: domain.AccountActive}, {ID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Role: domain.UserRoleAdmin, Status: domain.AccountSuspended}} {
		if viewer := ViewerFor(user, true); viewer.UserID() != "" {
			t.Fatal("invalid subject became a viewer")
		}
	}
	if (Viewer{}) != PublicViewer() {
		t.Fatal("anonymous viewer has privileged state")
	}
}
