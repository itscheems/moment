package app

import (
	"context"
	"github.com/moment/moment/internal/domain"
)

// Viewer carries the verified account ID, never an administrative read override.
type Viewer struct{ userID string }

func PublicViewer() Viewer { return Viewer{} }
func AccountViewer(userID string) Viewer {
	if domain.ValidateUserID(userID) != nil {
		return PublicViewer()
	}
	return Viewer{userID: userID}
}
func ViewerFor(user domain.User, signedIn bool) Viewer {
	if !signedIn || !user.CanSignIn() {
		return PublicViewer()
	}
	return AccountViewer(user.ID)
}
func ViewerFromContext(ctx context.Context) Viewer { return ViewerFor(ContentActor(ctx)) }
func (viewer Viewer) UserID() string               { return viewer.userID }

// CanReadEntry applies publication and author ownership to the actual resource.
func (viewer Viewer) CanReadEntry(entry domain.Entry) bool {
	if entry.PublicationState == "unpublished" {
		return false
	}
	if entry.Visibility == domain.VisibilityEveryone {
		return true
	}
	return entry.Visibility == domain.VisibilityOnlyMe && viewer.userID != "" && viewer.userID == entry.AuthorUserID
}
func postHiddenFrom(id domain.ID) error { return postNotFound(id) }
