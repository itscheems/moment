package app

import "github.com/moment/moment/internal/domain"

// Viewer is the visibility policy's one input: how much of the site the
// reader is allowed to see.
//
// Every read that can return an entry -- timeline, detail, quote, search,
// archive counts, a comment thread, a media file -- is handed a Viewer and
// asks it. Nothing else decides. The zero value is a stranger, so a caller
// that forgets to say who is reading gets the narrowest answer rather than
// the widest; that is the only direction a forgotten argument may fail in.
//
// There are two readers today because there are two visibilities: the owner,
// who wrote everything and reads everything, and everyone else, who reads
// what the owner published to everyone. A signed-in member is "everyone else"
// -- membership is about writing comments, not about reading more than a
// visitor can.
type Viewer struct {
	owner  bool
	userID string
}

// PublicViewer is a reader with no standing: a visitor, a crawler, a member.
func PublicViewer() Viewer { return Viewer{} }

// OwnerViewer is the site owner reading their own site.
func OwnerViewer() Viewer { return Viewer{owner: true} }

// ViewerFor turns an admitted account into a reader. It is the single place
// where "who is this" becomes "what may they read"; transports resolve the
// account and pass it here rather than comparing roles themselves.
//
// signedIn is false when no credential named anybody, in which case user is
// ignored. An owner credential whose account row could not be read still
// carries the owner role, so it reads as the owner.
func ViewerFor(user domain.User, signedIn bool) Viewer {
	if signedIn {
		return Viewer{owner: user.IsOwner(), userID: user.ID}
	}
	return PublicViewer()
}

// IsOwner reports whether this reader is the site owner.
func (viewer Viewer) IsOwner() bool { return viewer.owner }

// CanRead is the visibility decision for one stored value.
func (viewer Viewer) CanRead(visibility domain.Visibility) bool {
	switch visibility {
	case domain.VisibilityEveryone:
		return true
	case domain.VisibilityOnlyMe:
		return viewer.owner
	default:
		// An unknown value is never readable. Storage rejects it, so reaching
		// here means the data is not what this build understands.
		return false
	}
}

// Visibilities lists every stored value this reader may see, for set queries
// that have to filter in the database rather than after the fact. The list
// is derived from CanRead so the two can never disagree.
func (viewer Viewer) Visibilities() []domain.Visibility {
	visible := make([]domain.Visibility, 0, len(domain.Visibilities))
	for _, visibility := range domain.Visibilities {
		if viewer.CanRead(visibility) {
			visible = append(visible, visibility)
		}
	}
	return visible
}

// postHiddenFrom answers a read of an entry the viewer may not see exactly
// as if the entry did not exist. Whether a post is absent or merely private
// is not something a reader without standing gets to learn.
func postHiddenFrom(id domain.ID) error {
	return postNotFound(id)
}

// UserID identifies the reader; it never grants authority over another author.
func (viewer Viewer) UserID() string { return viewer.userID }

func (viewer Viewer) CanReadEntry(entry domain.Entry) bool {
	if entry.Visibility == domain.VisibilityEveryone {
		return true
	}
	if entry.Visibility != domain.VisibilityOnlyMe {
		return false
	}
	if viewer.userID != "" {
		return viewer.userID == entry.AuthorUserID
	}
	return viewer.owner && entry.AuthorUserID == ""
}
