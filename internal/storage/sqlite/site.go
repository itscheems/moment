package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/runtimefs"
)

const hostTimezoneSentinel = "__moment_host_timezone__"

func initializeSiteSettings(ctx context.Context, database *sql.DB, timezone string) error {
	result, err := database.ExecContext(ctx, `
UPDATE site_settings
SET timezone = ?
WHERE id = 1 AND timezone = ?`, timezone, hostTimezoneSentinel)
	if err != nil {
		return fmt.Errorf("initialize site timezone: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect site timezone initialization: %w", err)
	}
	if changed > 1 {
		return fmt.Errorf("initialize site timezone changed %d rows", changed)
	}
	var count int
	if err := database.QueryRowContext(ctx, `SELECT count(*) FROM site_settings WHERE id = 1`).Scan(&count); err != nil {
		return fmt.Errorf("verify site settings singleton: %w", err)
	}
	if count != 1 {
		return fmt.Errorf("%w: site settings singleton count is %d", ErrSchemaIncomplete, count)
	}
	return nil
}

// SiteRepository adapts SQLite and the ordinary-write maintenance boundary to
// the site settings application ports.
type SiteRepository struct {
	transactions Transactor
	locks        *runtimefs.Locker
	maintenance  *runtimefs.Lease
	development  *runtimefs.LeaseSet
	root         string
}

// NewDevelopmentSiteRepository is the explicit fixture boundary for one
// fully locked development generation build.
func NewDevelopmentSiteRepository(transactions Transactor, root string, held *runtimefs.LeaseSet) (*SiteRepository, error) {
	if transactions == nil || held == nil || !held.BelongsTo(root) ||
		!held.Holds(runtimefs.ImportLock, runtimefs.Exclusive) ||
		!held.Holds(runtimefs.ServeLock, runtimefs.Exclusive) ||
		!held.Holds(runtimefs.MaintenanceLock, runtimefs.Exclusive) ||
		!held.Holds(runtimefs.MediaLock, runtimefs.Exclusive) {
		return nil, runtimefs.ErrInsufficientLocks
	}
	return &SiteRepository{transactions: transactions, development: held, root: root}, nil
}

// NewSiteRepositoryWithinMaintenance constructs an adapter for a caller that
// already resolved CURRENT under an active shared maintenance lease. It avoids
// nested acquisition against the writer-preferring local gate.
func NewSiteRepositoryWithinMaintenance(transactions Transactor, maintenance *runtimefs.Lease, locks *runtimefs.Locker) (*SiteRepository, error) {
	if transactions == nil {
		return nil, errors.New("SQLite transactions are required")
	}
	if !maintenance.Holds(runtimefs.MaintenanceLock, runtimefs.Shared) {
		return nil, errors.New("shared maintenance lease is required")
	}
	if locks == nil {
		return nil, errors.New("runtime locks are required")
	}
	return &SiteRepository{transactions: transactions, maintenance: maintenance, locks: locks}, nil
}

var _ app.SiteTransactions = (*SiteRepository)(nil)

// NewSiteRepository constructs the settings adapter. All settings writes take
// maintenance.lock shared so a future import commit can exclude them safely.
func NewSiteRepository(transactions Transactor, locks *runtimefs.Locker) (*SiteRepository, error) {
	if transactions == nil {
		return nil, errors.New("SQLite transactions are required")
	}
	if locks == nil {
		return nil, errors.New("runtime locks are required")
	}
	return &SiteRepository{transactions: transactions, locks: locks}, nil
}

func (repository *SiteRepository) Read(ctx context.Context, operation func(app.SiteStore) error) error {
	if operation == nil {
		return errors.New("site read operation is required")
	}
	return repository.transactions.ReadTx(ctx, func(queries Queries) error {
		return operation(newSiteStore(queries))
	})
}

func (repository *SiteRepository) Write(ctx context.Context, operation func(app.SiteStore) error) error {
	if operation == nil {
		return errors.New("site write operation is required")
	}
	if repository.development != nil {
		if !repository.development.BelongsTo(repository.root) ||
			!repository.development.Holds(runtimefs.MaintenanceLock, runtimefs.Exclusive) ||
			!repository.development.Holds(runtimefs.MediaLock, runtimefs.Exclusive) {
			return runtimefs.ErrInsufficientLocks
		}
		return repository.transactions.WriteTx(ctx, func(queries Queries) error {
			return operation(newSiteStore(queries))
		})
	}
	if repository.maintenance != nil {
		if !repository.maintenance.Holds(runtimefs.MaintenanceLock, runtimefs.Shared) {
			return errors.New("site settings maintenance lease is no longer held")
		}
		media, err := repository.locks.Acquire(ctx, runtimefs.MediaLock, runtimefs.Shared)
		if err != nil {
			return fmt.Errorf("coordinate site media references: %w", err)
		}
		defer func() { _ = media.Release() }()
		return repository.transactions.WriteTx(ctx, func(queries Queries) error {
			return operation(newSiteStore(queries))
		})
	}
	lease, err := repository.locks.AcquireSet(ctx,
		runtimefs.LockRequest{Name: runtimefs.MaintenanceLock, Mode: runtimefs.Shared},
		runtimefs.LockRequest{Name: runtimefs.MediaLock, Mode: runtimefs.Shared},
	)
	if err != nil {
		return fmt.Errorf("coordinate site settings write: %w", err)
	}
	defer func() { _ = lease.Release() }()
	return repository.transactions.WriteTx(ctx, func(queries Queries) error {
		return operation(newSiteStore(queries))
	})
}

type siteStore struct {
	queries Queries
	mediaReferenceStore
}

func newSiteStore(queries Queries) siteStore {
	return siteStore{queries: queries, mediaReferenceStore: newMediaReferenceStore(queries)}
}

var _ app.SiteStore = siteStore{}

func (store siteStore) SiteSettings(ctx context.Context) (app.SiteSettings, error) {
	var (
		settings  app.SiteSettings
		publicURL sql.NullString
		avatarID  sql.NullString
		comments  int
	)
	err := store.queries.QueryRowContext(ctx, `
SELECT title, bio, timezone, public_base_url, avatar_media_id, comments_enabled
FROM site_settings
WHERE id = 1`).Scan(
		&settings.Title, &settings.Bio, &settings.Timezone, &publicURL, &avatarID, &comments,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return app.SiteSettings{}, domain.NewError(domain.CodeDataIntegrityFailed, "site settings singleton is missing", nil)
	}
	if err != nil {
		return app.SiteSettings{}, fmt.Errorf("read site settings: %w", err)
	}
	if publicURL.Valid {
		settings.PublicBaseURL = &publicURL.String
	}
	if avatarID.Valid {
		settings.AvatarMediaID = &avatarID.String
	}
	settings.CommentsEnabled = comments == 1
	return settings, nil
}

func (store siteStore) MediaExists(ctx context.Context, id domain.ID) (bool, error) {
	var exists int
	err := store.queries.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM media WHERE id = ?)`, id.String()).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check avatar media: %w", err)
	}
	return exists == 1, nil
}

func (store siteStore) UpdateSiteSettings(ctx context.Context, settings app.SiteSettings) error {
	result, err := store.queries.ExecContext(ctx, `
UPDATE site_settings
SET title = ?, bio = ?, timezone = ?, public_base_url = ?, avatar_media_id = ?, comments_enabled = ?
WHERE id = 1`,
		settings.Title, settings.Bio, settings.Timezone, optionalString(settings.PublicBaseURL), optionalString(settings.AvatarMediaID), boolInteger(settings.CommentsEnabled),
	)
	if err != nil {
		return fmt.Errorf("update site settings: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect site settings update: %w", err)
	}
	if changed != 1 {
		return fmt.Errorf("site settings update changed %d rows", changed)
	}
	return nil
}

func optionalString(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func boolInteger(value bool) int {
	if value {
		return 1
	}
	return 0
}
