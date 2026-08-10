package sqlite

import (
	"context"
	"errors"

	"github.com/moment/moment/internal/runtimefs"
)

// CorruptDevelopmentFTS removes exactly one derived search row. This is the
// sole intentional corruption fixture boundary; authoritative entries remain
// untouched and the complete destructive lock set is mandatory.
func CorruptDevelopmentFTS(ctx context.Context, database *Database, root string, held *runtimefs.LeaseSet) error {
	if database == nil || held == nil || !held.BelongsTo(root) ||
		!held.Holds(runtimefs.ImportLock, runtimefs.Exclusive) ||
		!held.Holds(runtimefs.ServeLock, runtimefs.Exclusive) ||
		!held.Holds(runtimefs.MaintenanceLock, runtimefs.Exclusive) ||
		!held.Holds(runtimefs.MediaLock, runtimefs.Exclusive) {
		return runtimefs.ErrInsufficientLocks
	}
	result, err := database.write.ExecContext(ctx, `DELETE FROM entry_fts WHERE rowid = (SELECT min(rowid) FROM entry_fts)`)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return errors.New("development FTS corruption fixture did not find exactly one row")
	}
	return nil
}
