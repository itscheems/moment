package sqlite

import (
	"context"
	"fmt"

	"github.com/moment/moment/internal/app"
)

func (store postStore) AuthorIdentities(ctx context.Context, ids []string) (map[string]app.CommentAuthorIdentity, error) {
	return newCommentStore(store.queries).CommentAuthorIdentities(ctx, ids)
}

func (store commentStore) AuthorIdentities(ctx context.Context, ids []string) (map[string]app.CommentAuthorIdentity, error) {
	return store.CommentAuthorIdentities(ctx, ids)
}

func (store commentStore) ReadableEntry(ctx context.Context, id string, viewer app.Viewer) (bool, error) {
	predicate, args := visibilityPredicate(viewer, "visibility")
	var readable bool
	args = append([]any{id}, args...)
	err := store.queries.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM entries WHERE id = ? AND `+predicate+`)`, args...).Scan(&readable)
	if err != nil {
		return false, fmt.Errorf("read comment entry audience: %w", err)
	}
	return readable, nil
}
