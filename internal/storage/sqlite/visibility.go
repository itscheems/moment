package sqlite

import (
	"strings"

	"github.com/moment/moment/internal/app"
)

// A private row belongs to its author, never to an administrator role.
func visibilityPredicate(viewer app.Viewer, column string) (string, []any) {
	prefix := strings.TrimSuffix(column, "visibility")
	author := prefix + "author_user_id"
	published := prefix + "publication_state = 'published' AND "
	if viewer.UserID() != "" {
		return published + "(" + column + " = ? OR (" + column + " = ? AND " + author + " = ?))", []any{"everyone", "only_me", viewer.UserID()}
	}
	if viewer.IsOwner() {
		return published + "(" + column + " = ? OR (" + column + " = ? AND " + author + " = (SELECT owner_id FROM site_settings WHERE id = 1)))", []any{"everyone", "only_me"}
	}
	return published + column + " = ?", []any{"everyone"}
}
