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

// ContentActor reports the identity verified by the current transport.
func ContentActor(ctx context.Context) (domain.User, bool) {
	user, ok := ctx.Value(contentActorKey{}).(domain.User)
	return user, ok
}

// ContentAuthorStore batches real account identity projections for reads.
type ContentAuthorStore interface {
	AuthorIdentities(context.Context, []string) (map[string]CommentAuthorIdentity, error)
}

func contentActorID(ctx context.Context, _ any) (string, error) {
	user, present := ContentActor(ctx)
	if !present || domain.ValidateUserID(user.ID) != nil || !user.CanWritePublicContent() {
		return "", domain.NewError(domain.CodeUnauthorized, "an active account identity is required", nil)
	}
	return user.ID, nil
}

func requireContentAuthor(ctx context.Context, store any, authorID string) error {
	id, err := contentActorID(ctx, store)
	if err != nil {
		return err
	}
	if id != authorID {
		return domain.NewError(domain.CodeUnauthorized, "only the content author may manage it", nil)
	}
	return nil
}

// requireContentRemoval separates an author's control from public moderation.
// Administrative authority never grants access to another author's private work.
func requireContentRemoval(ctx context.Context, store any, entry domain.Entry) error {
	id, err := contentActorID(ctx, store)
	if err != nil {
		return err
	}
	if id == entry.AuthorUserID {
		return nil
	}
	if user, present := ContentActor(ctx); present && user.IsAdmin() &&
		entry.Visibility == domain.VisibilityEveryone && entry.PublicationState != "unpublished" {
		return nil
	}
	return domain.NewError(domain.CodeUnauthorized, "only the author or a public content moderator may remove this post", nil)
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
