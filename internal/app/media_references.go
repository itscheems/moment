package app

import (
	"context"
	"time"

	"github.com/moment/moment/internal/domain"
)

// MediaReferenceState is the resolver's authoritative view of one managed
// object. Referenced and UnreferencedSince are complementary authority state:
// reachable objects have no clock, while unreachable objects have a continuous
// clock that starts when their last reference disappears.
type MediaReferenceState struct {
	MediaID           domain.ID
	Referenced        bool
	UnreferencedSince *time.Time
}

// MediaReferenceReconciler updates continuous clocks through the central
// transaction-bound resolver. Implementations include entry usages and the
// current site avatar; future reference sources extend this port once instead
// of adding feature-local reachability queries.
type MediaReferenceReconciler interface {
	ReconcileMediaReferences(context.Context, []domain.ID, time.Time) error
}

// EntryMediaReferenceStore is the reusable lifecycle seam for destructive
// Entry changes. Attachment work can use the same reconciliation contract.
type EntryMediaReferenceStore interface {
	MediaReferenceReconciler
	EntryMediaIDs(context.Context, domain.ID) ([]domain.ID, error)
}

// MediaReferenceStore is the complete central resolver used by maintenance
// and later export/diagnostic consumers.
type MediaReferenceStore interface {
	EntryMediaReferenceStore
	ResolveMediaReferences(context.Context) ([]domain.ID, error)
	MediaReferenceStates(context.Context) ([]MediaReferenceState, error)
	PrunableMediaIDs(context.Context, time.Time) ([]domain.ID, error)
	DeleteMediaIfPrunable(context.Context, domain.ID, time.Time) (bool, error)
	DeleteMediaIfUnreferenced(context.Context, domain.ID) (bool, error)
}

func uniqueMediaIDs(ids ...*string) ([]domain.ID, error) {
	seen := make(map[domain.ID]struct{}, len(ids))
	result := make([]domain.ID, 0, len(ids))
	for _, raw := range ids {
		if raw == nil {
			continue
		}
		id, err := domain.ParseID(*raw)
		if err != nil {
			return nil, err
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result, nil
}
