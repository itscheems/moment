package app

import (
	"context"

	"github.com/moment/moment/internal/domain"
)

type contentActorKey struct{}

// WithContentActor carries an identity already verified by the transport.
// Resource ownership is checked by the application, independently of role.
func WithContentActor(ctx context.Context, user domain.User) context.Context {
	return context.WithValue(ctx, contentActorKey{}, user)
}

func ContentActor(ctx context.Context) (domain.User, bool) {
	user, ok := ctx.Value(contentActorKey{}).(domain.User)
	return user, ok
}

// ContentAuthorStore resolves the original local author for trusted direct
// calls and batches public identity projections for reads.
type ContentAuthorStore interface {
	PrimaryAuthorID(context.Context) (string, error)
	AuthorIdentities(context.Context, []string) (map[string]CommentAuthorIdentity, error)
}

func contentActorID(ctx context.Context, store any) (string, error) {
	if user, present := ContentActor(ctx); present {
		if domain.ValidateUserID(user.ID) != nil || !user.CanWritePublicContent() {
			return "", domain.NewError(domain.CodeUnauthorized, "an active account identity is required", nil)
		}
		return user.ID, nil
	}
	// Direct application calls retain their original account, not whichever
	// account happens to have an administrator role later.
	if authors, ok := store.(ContentAuthorStore); ok {
		return authors.PrimaryAuthorID(ctx)
	}
	return "", nil
}

func resolvedViewer(ctx context.Context, store any, viewer Viewer) (Viewer, error) {
	if viewer.owner && viewer.userID == "" {
		if authors, ok := store.(ContentAuthorStore); ok {
			id, err := authors.PrimaryAuthorID(ctx)
			viewer.userID = id
			return viewer, err
		}
	}
	return viewer, nil
}

func requireContentAuthor(ctx context.Context, store any, authorID string) error {
	id, err := contentActorID(ctx, store)
	if err != nil {
		return err
	}
	if id != authorID {
		return domain.NewError(domain.CodeUnauthorized, "only the article author may manage it", nil)
	}
	return nil
}

func readContentAuthors(ctx context.Context, store any, entries []domain.Entry) (map[string]CommentAuthorIdentity, error) {
	ids := make([]string, 0, len(entries))
	seen := make(map[string]bool)
	for _, entry := range entries {
		if entry.AuthorUserID != "" && !seen[entry.AuthorUserID] {
			ids = append(ids, entry.AuthorUserID)
			seen[entry.AuthorUserID] = true
		}
	}
	if authors, ok := store.(ContentAuthorStore); ok {
		return authors.AuthorIdentities(ctx, ids)
	}
	return nil, nil
}

func contentAuthor(authors map[string]CommentAuthorIdentity, id string) *CommentAuthor {
	author, ok := authors[id]
	if !ok {
		return nil
	}
	return &CommentAuthor{UserID: author.UserID, Username: author.Username, DisplayName: author.DisplayName, AvatarMediaID: author.AvatarMediaID, ProfileURL: "/" + author.Username}
}

func attachContentAuthor(ctx context.Context, store any, post *Post) error {
	authors, err := readContentAuthors(ctx, store, []domain.Entry{post.Entry})
	if err == nil {
		post.Author = contentAuthor(authors, post.Entry.AuthorUserID)
	}
	return err
}

// ResolveContentActorID lets transaction-bound repositories constrain their
// collection queries to the verified actor, including trusted direct calls.
func ResolveContentActorID(ctx context.Context, store ContentAuthorStore) (string, error) {
	return contentActorID(ctx, store)
}
