package app

import (
	"context"

	"github.com/moment/moment/internal/domain"
)

// Viewer carries the verified account ID, never an administrative read override.
type Viewer struct{ userID string }

// PublicViewer has no account-specific reading privileges.
func PublicViewer() Viewer { return Viewer{} }

// AccountViewer selects an immutable account identity for author-scoped reads.
func AccountViewer(userID string) Viewer {
	if domain.ValidateUserID(userID) != nil {
		return PublicViewer()
	}
	return Viewer{userID: userID}
}

// ViewerFor uses an active verified account or public visibility.
func ViewerFor(user domain.User, signedIn bool) Viewer {
	if !signedIn || !user.CanSignIn() {
		return PublicViewer()
	}
	return AccountViewer(user.ID)
}

// ViewerFromContext derives reading authority from the context account.
func ViewerFromContext(ctx context.Context) Viewer { return ViewerFor(ContentActor(ctx)) }

// UserID returns the account identity used for authorship checks.
func (viewer Viewer) UserID() string { return viewer.userID }

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
