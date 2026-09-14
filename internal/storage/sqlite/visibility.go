package sqlite

import (
	"strings"

	"github.com/moment/moment/internal/app"
)

// visibilityPredicate translates a viewer into SQL. The policy stays in
// app.Viewer; this only spells out the values it allows, so a query can never
// name a visibility the viewer was not granted.
//
// The predicate always binds at least one value: a viewer with nothing to see
// would produce `column IN ()`, which SQLite rejects, so it becomes a
// contradiction instead.
func visibilityPredicate(viewer app.Viewer, column string) (string, []any) {
	visible := viewer.Visibilities()
	if len(visible) == 0 {
		return "0 = 1", nil
	}
	arguments := make([]any, 0, len(visible))
	for _, visibility := range visible {
		arguments = append(arguments, string(visibility))
	}
	return column + " IN (" + strings.TrimSuffix(strings.Repeat("?, ", len(visible)), ", ") + ")", arguments
}
