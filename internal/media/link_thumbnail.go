package media

import (
	"context"
	"io"

	"github.com/moment/moment/internal/domain"
)

// LinkThumbnailAdapter captures preview images through managed ingestion.
type LinkThumbnailAdapter struct {
	store *Store
}

// NewLinkThumbnailAdapter wraps a media store.
func NewLinkThumbnailAdapter(store *Store) *LinkThumbnailAdapter {
	if store == nil {
		return nil
	}
	return &LinkThumbnailAdapter{store: store}
}

// Ingest commits the downloaded bytes and reports the managed identity.
func (adapter *LinkThumbnailAdapter) Ingest(ctx context.Context, originalName string, input io.Reader) (domain.ID, string, uint32, uint32, error) {
	value, _, err := adapter.store.Ingest(ctx, originalName, input)
	if err != nil {
		return "", "", 0, 0, err
	}
	return value.ID, value.MIMEType, value.Width, value.Height, nil
}
