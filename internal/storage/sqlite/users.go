package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/runtimefs"
)

// AccountRepository adapts SQLite and the ordinary-write maintenance boundary
// to the account application ports.
//
// Account writes take the media lock the same way site writes do, because an
// avatar or cover assignment changes what the media collector considers
// reachable. Skipping it would let a picture be collected between the moment
// an account claims it and the moment the claim is visible.
type AccountRepository struct {
	transactions Transactor
	locks        *runtimefs.Locker
	maintenance  *runtimefs.Lease
	development  *runtimefs.LeaseSet
	root         string
}

var _ app.AccountTransactions = (*AccountRepository)(nil)

// NewAccountRepository constructs the account adapter.
func NewAccountRepository(transactions Transactor, locks *runtimefs.Locker) (*AccountRepository, error) {
	if transactions == nil {
		return nil, errors.New("SQLite transactions are required")
	}
	if locks == nil {
		return nil, errors.New("runtime locks are required")
	}
	return &AccountRepository{transactions: transactions, locks: locks}, nil
}

// NewAccountRepositoryWithinMaintenance constructs an adapter for a caller
// that already holds a shared maintenance lease.
func NewAccountRepositoryWithinMaintenance(transactions Transactor, maintenance *runtimefs.Lease, locks *runtimefs.Locker) (*AccountRepository, error) {
	if transactions == nil {
		return nil, errors.New("SQLite transactions are required")
	}
	if !maintenance.Holds(runtimefs.MaintenanceLock, runtimefs.Shared) {
		return nil, errors.New("shared maintenance lease is required")
	}
	if locks == nil {
		return nil, errors.New("runtime locks are required")
	}
	return &AccountRepository{transactions: transactions, maintenance: maintenance, locks: locks}, nil
}

// NewDevelopmentAccountRepository is the explicit fixture boundary for one
// fully locked development generation build.
func NewDevelopmentAccountRepository(transactions Transactor, root string, held *runtimefs.LeaseSet) (*AccountRepository, error) {
	if transactions == nil || held == nil || !held.BelongsTo(root) ||
		!held.Holds(runtimefs.ImportLock, runtimefs.Exclusive) ||
		!held.Holds(runtimefs.ServeLock, runtimefs.Exclusive) ||
		!held.Holds(runtimefs.MaintenanceLock, runtimefs.Exclusive) ||
		!held.Holds(runtimefs.MediaLock, runtimefs.Exclusive) {
		return nil, runtimefs.ErrInsufficientLocks
	}
	return &AccountRepository{transactions: transactions, development: held, root: root}, nil
}

// ReadAccounts runs a read-only account operation.
func (repository *AccountRepository) ReadAccounts(ctx context.Context, operation func(app.AccountStore, app.SessionStore) error) error {
	if operation == nil {
		return errors.New("account read operation is required")
	}
	return repository.transactions.ReadTx(ctx, func(queries Queries) error {
		store := newAccountStore(queries)
		return operation(store, store)
	})
}

// WriteAccounts runs an account write under the locks account writes need.
func (repository *AccountRepository) WriteAccounts(ctx context.Context, operation func(app.AccountStore, app.SessionStore) error) error {
	if operation == nil {
		return errors.New("account write operation is required")
	}
	run := func() error {
		return repository.transactions.WriteTx(ctx, func(queries Queries) error {
			store := newAccountStore(queries)
			return operation(store, store)
		})
	}
	if repository.development != nil {
		if !repository.development.BelongsTo(repository.root) ||
			!repository.development.Holds(runtimefs.MaintenanceLock, runtimefs.Exclusive) ||
			!repository.development.Holds(runtimefs.MediaLock, runtimefs.Exclusive) {
			return runtimefs.ErrInsufficientLocks
		}
		return run()
	}
	if repository.maintenance != nil {
		if !repository.maintenance.Holds(runtimefs.MaintenanceLock, runtimefs.Shared) {
			return errors.New("account maintenance lease is no longer held")
		}
		media, err := repository.locks.Acquire(ctx, runtimefs.MediaLock, runtimefs.Shared)
		if err != nil {
			return fmt.Errorf("coordinate account media references: %w", err)
		}
		defer func() { _ = media.Release() }()
		return run()
	}
	lease, err := repository.locks.AcquireSet(ctx,
		runtimefs.LockRequest{Name: runtimefs.MaintenanceLock, Mode: runtimefs.Shared},
		runtimefs.LockRequest{Name: runtimefs.MediaLock, Mode: runtimefs.Shared},
	)
	if err != nil {
		return fmt.Errorf("coordinate account write: %w", err)
	}
	defer func() { _ = lease.Release() }()
	return run()
}

type accountStore struct {
	queries Queries
	mediaReferenceStore
}

func newAccountStore(queries Queries) accountStore {
	return accountStore{queries: queries, mediaReferenceStore: newMediaReferenceStore(queries)}
}

var (
	_ app.AccountStore = accountStore{}
	_ app.SessionStore = accountStore{}
)

// userColumns is shared by every account read so a column added to the table
// cannot reach one query and miss another.
const userColumns = `users.id, users.username, users.display_name, users.bio, users.avatar_media_id,
       users.cover_media_id, users.role, users.status, users.username_set_at_ms,
       users.created_at_ms, users.updated_at_ms`

func scanUser(scanner interface{ Scan(...any) error }) (domain.User, error) {
	var (
		user       domain.User
		avatar     sql.NullString
		cover      sql.NullString
		usernameAt sql.NullInt64
		role       string
		status     string
	)
	if err := scanner.Scan(&user.ID, &user.Username, &user.DisplayName, &user.Bio, &avatar, &cover,
		&role, &status, &usernameAt, &user.CreatedAtMS, &user.UpdatedAtMS); err != nil {
		return domain.User{}, err
	}
	if avatar.Valid {
		user.AvatarMediaID = &avatar.String
	}
	if cover.Valid {
		user.CoverMediaID = &cover.String
	}
	if usernameAt.Valid {
		value := usernameAt.Int64
		user.UsernameSetAtMS = &value
	}
	user.Role = domain.UserRole(role)
	user.Status = domain.AccountStatus(status)
	return user, nil
}

func (store accountStore) UserByID(ctx context.Context, id string) (domain.User, error) {
	row := store.queries.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE id = ?`, id)
	user, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.User{}, domain.NewError(domain.CodeAccountNotFound, "no such account", nil)
	}
	if err != nil {
		return domain.User{}, fmt.Errorf("read account: %w", err)
	}
	return user, nil
}

func (store accountStore) UserByUsername(ctx context.Context, username string) (domain.User, error) {
	row := store.queries.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE username = ? COLLATE NOCASE`, username)
	user, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.User{}, domain.NewError(domain.CodeAccountNotFound, "no such account", nil)
	}
	if err != nil {
		return domain.User{}, fmt.Errorf("read account by username: %w", err)
	}
	return user, nil
}

// UsersByIDs is the batch shape comment author resolution has always called
// with. 0020 wrote the column that makes it answerable; this is the query it
// was waiting for.
func (store accountStore) UsersByIDs(ctx context.Context, ids []string) (map[string]domain.User, error) {
	result := make(map[string]domain.User, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	placeholders := make([]string, 0, len(ids))
	arguments := make([]any, 0, len(ids))
	for _, id := range ids {
		placeholders = append(placeholders, "?")
		arguments = append(arguments, id)
	}
	rows, err := store.queries.QueryContext(ctx,
		`SELECT `+userColumns+` FROM users WHERE id IN (`+strings.Join(placeholders, ", ")+`)`, arguments...)
	if err != nil {
		return nil, fmt.Errorf("read accounts: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		user, err := scanUser(rows)
		if err != nil {
			return nil, fmt.Errorf("scan account: %w", err)
		}
		result[user.ID] = user
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate accounts: %w", err)
	}
	return result, nil
}

// MediaExists reports whether a managed media id names a real row, so a bad
// reference is refused with an answer rather than with a constraint failure.
func (store accountStore) MediaExists(ctx context.Context, id domain.ID) (bool, error) {
	var exists int
	err := store.queries.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM media WHERE id = ?)`, id.String()).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check account media: %w", err)
	}
	return exists == 1, nil
}

func (store accountStore) UsernameAvailable(ctx context.Context, username string) (bool, error) {
	var exists int
	err := store.queries.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM users WHERE username = ? COLLATE NOCASE)`, username).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check username availability: %w", err)
	}
	return exists == 0, nil
}

const identityColumns = `provider, provider_user_id, user_id, email, email_verified, email_deliverable,
       display_name, avatar_url, created_at_ms, last_login_at_ms`

func scanIdentity(scanner interface{ Scan(...any) error }) (domain.Identity, error) {
	var (
		identity    domain.Identity
		email       sql.NullString
		avatarURL   sql.NullString
		verified    int
		deliverable int
	)
	if err := scanner.Scan(&identity.Provider, &identity.ProviderUserID, &identity.UserID, &email,
		&verified, &deliverable, &identity.DisplayName, &avatarURL,
		&identity.CreatedAtMS, &identity.LastLoginAtMS); err != nil {
		return domain.Identity{}, err
	}
	if email.Valid {
		identity.Email = &email.String
	}
	if avatarURL.Valid {
		identity.AvatarURL = &avatarURL.String
	}
	identity.EmailVerified = verified == 1
	identity.EmailDeliverable = deliverable == 1
	return identity, nil
}

func (store accountStore) IdentityLookup(ctx context.Context, provider, providerUserID string) (domain.Identity, bool, error) {
	row := store.queries.QueryRowContext(ctx,
		`SELECT `+identityColumns+` FROM identities WHERE provider = ? AND provider_user_id = ?`, provider, providerUserID)
	identity, err := scanIdentity(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Identity{}, false, nil
	}
	if err != nil {
		return domain.Identity{}, false, fmt.Errorf("read identity: %w", err)
	}
	return identity, true, nil
}

func (store accountStore) IdentitiesForUser(ctx context.Context, userID string) ([]domain.Identity, error) {
	rows, err := store.queries.QueryContext(ctx,
		`SELECT `+identityColumns+` FROM identities WHERE user_id = ? ORDER BY provider`, userID)
	if err != nil {
		return nil, fmt.Errorf("read account identities: %w", err)
	}
	defer func() { _ = rows.Close() }()
	identities := make([]domain.Identity, 0, 2)
	for rows.Next() {
		identity, err := scanIdentity(rows)
		if err != nil {
			return nil, fmt.Errorf("scan identity: %w", err)
		}
		identities = append(identities, identity)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate identities: %w", err)
	}
	return identities, nil
}

func (store accountStore) CreateAccountWithIdentity(ctx context.Context, user domain.User, identity domain.Identity) error {
	if err := store.insertUser(ctx, user); err != nil {
		return err
	}
	return store.LinkIdentity(ctx, identity)
}

func (store accountStore) insertUser(ctx context.Context, user domain.User) error {
	_, err := store.queries.ExecContext(ctx, `
INSERT INTO users (id, username, display_name, bio, avatar_media_id, cover_media_id,
                   role, status, username_set_at_ms, created_at_ms, updated_at_ms)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		user.ID, user.Username, user.DisplayName, user.Bio, optionalString(user.AvatarMediaID),
		optionalString(user.CoverMediaID), string(user.Role), string(user.Status),
		optionalInt64(user.UsernameSetAtMS), user.CreatedAtMS, user.UpdatedAtMS)
	if err != nil {
		if isUniqueViolation(err) {
			return domain.NewError(domain.CodeUsernameTaken, "that username is taken", map[string]any{"field": "username"})
		}
		return fmt.Errorf("insert account: %w", err)
	}
	return nil
}

func (store accountStore) LinkIdentity(ctx context.Context, identity domain.Identity) error {
	_, err := store.queries.ExecContext(ctx, `
INSERT INTO identities (provider, provider_user_id, user_id, email, email_verified, email_deliverable,
                        display_name, avatar_url, created_at_ms, last_login_at_ms)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		identity.Provider, identity.ProviderUserID, identity.UserID, optionalString(identity.Email),
		boolInteger(identity.EmailVerified), boolInteger(identity.EmailDeliverable),
		identity.DisplayName, optionalString(identity.AvatarURL),
		identity.CreatedAtMS, identity.LastLoginAtMS)
	if err != nil {
		if isUniqueViolation(err) {
			return domain.NewError(domain.CodeIdentityAlreadyLinked, "that provider account is already linked", map[string]any{"provider": identity.Provider})
		}
		return fmt.Errorf("link identity: %w", err)
	}
	return nil
}

func (store accountStore) UnlinkIdentity(ctx context.Context, userID, provider string) error {
	result, err := store.queries.ExecContext(ctx,
		`DELETE FROM identities WHERE user_id = ? AND provider = ?`, userID, provider)
	if err != nil {
		return fmt.Errorf("unlink identity: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect identity removal: %w", err)
	}
	if changed == 0 {
		return domain.NewError(domain.CodeAccountNotFound, "that provider is not linked to this account", map[string]any{"provider": provider})
	}
	return nil
}

func (store accountStore) RecordIdentityLogin(ctx context.Context, provider, providerUserID string, atMS int64) error {
	_, err := store.queries.ExecContext(ctx,
		`UPDATE identities SET last_login_at_ms = ? WHERE provider = ? AND provider_user_id = ?`,
		atMS, provider, providerUserID)
	if err != nil {
		return fmt.Errorf("record identity login: %w", err)
	}
	return nil
}

// UpdateProfile writes only the fields an account owns about itself. Role and
// status are absent from this statement on purpose: an account editing its own
// profile must not be able to reach them, and the safest way to guarantee that
// is for the SQL never to mention them.
func (store accountStore) UpdateProfile(ctx context.Context, user domain.User) error {
	result, err := store.queries.ExecContext(ctx, `
UPDATE users
SET username = ?, display_name = ?, bio = ?, avatar_media_id = ?, cover_media_id = ?,
    username_set_at_ms = ?, updated_at_ms = ?
WHERE id = ?`,
		user.Username, user.DisplayName, user.Bio, optionalString(user.AvatarMediaID),
		optionalString(user.CoverMediaID), optionalInt64(user.UsernameSetAtMS), user.UpdatedAtMS, user.ID)
	if err != nil {
		if isUniqueViolation(err) {
			return domain.NewError(domain.CodeUsernameTaken, "that username is taken", map[string]any{"field": "username"})
		}
		return fmt.Errorf("update account profile: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect account profile update: %w", err)
	}
	if changed != 1 {
		return domain.NewError(domain.CodeAccountNotFound, "no such account", nil)
	}
	return nil
}

func (store accountStore) SetStatus(ctx context.Context, userID string, status domain.AccountStatus, atMS int64) error {
	result, err := store.queries.ExecContext(ctx,
		`UPDATE users SET status = ?, updated_at_ms = ? WHERE id = ?`, string(status), atMS, userID)
	if err != nil {
		return fmt.Errorf("update account status: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect account status update: %w", err)
	}
	if changed != 1 {
		return domain.NewError(domain.CodeAccountNotFound, "no such account", nil)
	}
	return nil
}

func (store accountStore) ListAccounts(ctx context.Context) ([]domain.User, error) {
	rows, err := store.queries.QueryContext(ctx,
		`SELECT `+userColumns+` FROM users ORDER BY created_at_ms, id`)
	if err != nil {
		return nil, fmt.Errorf("list accounts: %w", err)
	}
	defer func() { _ = rows.Close() }()
	users := make([]domain.User, 0, 8)
	for rows.Next() {
		user, err := scanUser(rows)
		if err != nil {
			return nil, fmt.Errorf("scan account: %w", err)
		}
		users = append(users, user)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate accounts: %w", err)
	}
	return users, nil
}

func (store accountStore) OwnerUserID(ctx context.Context) (string, error) {
	var ownerID string
	err := store.queries.QueryRowContext(ctx, `SELECT owner_id FROM site_settings WHERE id = 1`).Scan(&ownerID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", domain.NewError(domain.CodeDataIntegrityFailed, "site settings singleton is missing", nil)
	}
	if err != nil {
		return "", fmt.Errorf("read owner account id: %w", err)
	}
	return ownerID, nil
}

func (store accountStore) RegistrationMode(ctx context.Context) (domain.RegistrationMode, error) {
	var mode string
	err := store.queries.QueryRowContext(ctx, `SELECT registration_mode FROM site_settings WHERE id = 1`).Scan(&mode)
	if errors.Is(err, sql.ErrNoRows) {
		return "", domain.NewError(domain.CodeDataIntegrityFailed, "site settings singleton is missing", nil)
	}
	if err != nil {
		return "", fmt.Errorf("read registration mode: %w", err)
	}
	return domain.RegistrationMode(mode), nil
}

// --- sessions ---

func (store accountStore) CreateSession(ctx context.Context, session domain.SessionRecord) error {
	_, err := store.queries.ExecContext(ctx, `
INSERT INTO sessions (id, user_id, issued_at_ms, expires_at_ms, last_seen_at_ms, revoked_at_ms, surface, label)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		session.ID, session.UserID, session.IssuedAtMS, session.ExpiresAtMS, session.LastSeenAtMS,
		optionalInt64(session.RevokedAtMS), session.Surface, optionalString(session.Label))
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

func (store accountStore) SessionByID(ctx context.Context, id string) (domain.SessionRecord, bool, error) {
	var (
		session domain.SessionRecord
		revoked sql.NullInt64
		label   sql.NullString
	)
	err := store.queries.QueryRowContext(ctx, `
SELECT id, user_id, issued_at_ms, expires_at_ms, last_seen_at_ms, revoked_at_ms, surface, label
FROM sessions WHERE id = ?`, id).Scan(&session.ID, &session.UserID, &session.IssuedAtMS,
		&session.ExpiresAtMS, &session.LastSeenAtMS, &revoked, &session.Surface, &label)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.SessionRecord{}, false, nil
	}
	if err != nil {
		return domain.SessionRecord{}, false, fmt.Errorf("read session: %w", err)
	}
	if revoked.Valid {
		value := revoked.Int64
		session.RevokedAtMS = &value
	}
	if label.Valid {
		session.Label = &label.String
	}
	return session, true, nil
}

func (store accountStore) RevokeSession(ctx context.Context, id string, atMS int64) error {
	_, err := store.queries.ExecContext(ctx,
		`UPDATE sessions SET revoked_at_ms = ? WHERE id = ? AND revoked_at_ms IS NULL`, atMS, id)
	if err != nil {
		return fmt.Errorf("revoke session: %w", err)
	}
	return nil
}

func (store accountStore) RevokeUserSessions(ctx context.Context, userID string, atMS int64) (int, error) {
	result, err := store.queries.ExecContext(ctx,
		`UPDATE sessions SET revoked_at_ms = ? WHERE user_id = ? AND revoked_at_ms IS NULL`, atMS, userID)
	if err != nil {
		return 0, fmt.Errorf("revoke account sessions: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("inspect session revocation: %w", err)
	}
	return int(changed), nil
}

func (store accountStore) TouchSession(ctx context.Context, id string, atMS int64) error {
	_, err := store.queries.ExecContext(ctx,
		`UPDATE sessions SET last_seen_at_ms = ? WHERE id = ?`, atMS, id)
	if err != nil {
		return fmt.Errorf("touch session: %w", err)
	}
	return nil
}

func (store accountStore) ListUserSessions(ctx context.Context, userID string) ([]domain.SessionRecord, error) {
	rows, err := store.queries.QueryContext(ctx, `
SELECT id, user_id, issued_at_ms, expires_at_ms, last_seen_at_ms, revoked_at_ms, surface, label
FROM sessions WHERE user_id = ? ORDER BY issued_at_ms DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	sessions := make([]domain.SessionRecord, 0, 4)
	for rows.Next() {
		var (
			session domain.SessionRecord
			revoked sql.NullInt64
			label   sql.NullString
		)
		if err := rows.Scan(&session.ID, &session.UserID, &session.IssuedAtMS, &session.ExpiresAtMS,
			&session.LastSeenAtMS, &revoked, &session.Surface, &label); err != nil {
			return nil, fmt.Errorf("scan session: %w", err)
		}
		if revoked.Valid {
			value := revoked.Int64
			session.RevokedAtMS = &value
		}
		if label.Valid {
			session.Label = &label.String
		}
		sessions = append(sessions, session)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sessions: %w", err)
	}
	return sessions, nil
}

func (store accountStore) PurgeExpiredSessions(ctx context.Context, beforeMS int64) (int, error) {
	result, err := store.queries.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at_ms < ?`, beforeMS)
	if err != nil {
		return 0, fmt.Errorf("purge expired sessions: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("inspect session purge: %w", err)
	}
	return int(changed), nil
}

func optionalInt64(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

// isUniqueViolation recognises the constraint failures that carry a message a
// person can act on, so they are reported as conflicts rather than as internal
// errors. Matching on text is what the comment store already does for its
// trigger guards; SQLite offers no portable code through database/sql here.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unique constraint failed") ||
		strings.Contains(message, "constraint failed: unique")
}
