package ginhttp

import (
	"context"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/runtimefs"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
)

// memoryAccounts is an in-memory app.AccountTransactions for transport tests.
//
// The transport tests exist to pin down status codes, cookies and redirects,
// so they run against real locking and a real SQLite file only where storage
// behaviour is the thing under test. Here the store is a map, which keeps a
// route test from failing for reasons that have nothing to do with the route.
type memoryAccounts struct {
	mu         sync.Mutex
	users      map[string]domain.User
	identities map[string]domain.Identity
	sessions   map[string]domain.SessionRecord
	ownerID    string
	mode       domain.RegistrationMode
}

func newMemoryAccounts(owner domain.User) *memoryAccounts {
	return &memoryAccounts{
		users:      map[string]domain.User{owner.ID: owner},
		identities: map[string]domain.Identity{},
		sessions:   map[string]domain.SessionRecord{},
		ownerID:    owner.ID,
		mode:       domain.RegistrationApproval,
	}
}

func (store *memoryAccounts) ReadAccounts(ctx context.Context, operation func(app.AccountStore, app.SessionStore) error) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	return operation(store, store)
}

func (store *memoryAccounts) WriteAccounts(ctx context.Context, operation func(app.AccountStore, app.SessionStore) error) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	return operation(store, store)
}

func identityKey(provider, providerUserID string) string { return provider + "\x00" + providerUserID }

func (store *memoryAccounts) UserByID(_ context.Context, id string) (domain.User, error) {
	user, found := store.users[id]
	if !found {
		return domain.User{}, domain.NewError(domain.CodeAccountNotFound, "no such account", nil)
	}
	return user, nil
}

func (store *memoryAccounts) UserByUsername(_ context.Context, username string) (domain.User, error) {
	for _, user := range store.users {
		if strings.EqualFold(user.Username, username) {
			return user, nil
		}
	}
	return domain.User{}, domain.NewError(domain.CodeAccountNotFound, "no such account", nil)
}

func (store *memoryAccounts) UsersByIDs(_ context.Context, ids []string) (map[string]domain.User, error) {
	result := make(map[string]domain.User, len(ids))
	for _, id := range ids {
		if user, found := store.users[id]; found {
			result[id] = user
		}
	}
	return result, nil
}

func (store *memoryAccounts) UsernameAvailable(_ context.Context, username string) (bool, error) {
	for _, user := range store.users {
		if strings.EqualFold(user.Username, username) {
			return false, nil
		}
	}
	return true, nil
}

func (store *memoryAccounts) IdentityLookup(_ context.Context, provider, providerUserID string) (domain.Identity, bool, error) {
	identity, found := store.identities[identityKey(provider, providerUserID)]
	return identity, found, nil
}

func (store *memoryAccounts) IdentitiesForUser(_ context.Context, userID string) ([]domain.Identity, error) {
	found := make([]domain.Identity, 0, 2)
	for _, identity := range store.identities {
		if identity.UserID == userID {
			found = append(found, identity)
		}
	}
	sort.Slice(found, func(left, right int) bool { return found[left].Provider < found[right].Provider })
	return found, nil
}

func (store *memoryAccounts) CreateAccountWithIdentity(ctx context.Context, user domain.User, identity domain.Identity) error {
	if _, taken := store.users[user.ID]; taken {
		return domain.NewError(domain.CodeUsernameTaken, "account exists", nil)
	}
	store.users[user.ID] = user
	return store.LinkIdentity(ctx, identity)
}

func (store *memoryAccounts) LinkIdentity(_ context.Context, identity domain.Identity) error {
	key := identityKey(identity.Provider, identity.ProviderUserID)
	if _, taken := store.identities[key]; taken {
		return domain.NewError(domain.CodeIdentityAlreadyLinked, "already linked", nil)
	}
	store.identities[key] = identity
	return nil
}

func (store *memoryAccounts) UnlinkIdentity(_ context.Context, userID, provider string) error {
	for key, identity := range store.identities {
		if identity.UserID == userID && identity.Provider == provider {
			delete(store.identities, key)
			return nil
		}
	}
	return domain.NewError(domain.CodeAccountNotFound, "not linked", nil)
}

func (store *memoryAccounts) RefreshIdentity(_ context.Context, identity domain.Identity) error {
	key := identityKey(identity.Provider, identity.ProviderUserID)
	if _, found := store.identities[key]; found {
		store.identities[key] = identity
	}
	return nil
}

func (store *memoryAccounts) UpdateProfile(_ context.Context, user domain.User) error {
	if _, found := store.users[user.ID]; !found {
		return domain.NewError(domain.CodeAccountNotFound, "no such account", nil)
	}
	for id, existing := range store.users {
		if id != user.ID && strings.EqualFold(existing.Username, user.Username) {
			return domain.NewError(domain.CodeUsernameTaken, "that username is taken", nil)
		}
	}
	store.users[user.ID] = user
	return nil
}

func (store *memoryAccounts) SetStatus(_ context.Context, userID string, status domain.AccountStatus, atMS int64) error {
	user, found := store.users[userID]
	if !found {
		return domain.NewError(domain.CodeAccountNotFound, "no such account", nil)
	}
	user.Status = status
	user.UpdatedAtMS = atMS
	store.users[userID] = user
	return nil
}

func (store *memoryAccounts) DeleteAccount(_ context.Context, userID string) error {
	if _, found := store.users[userID]; !found {
		return domain.NewError(domain.CodeAccountNotFound, "no such account", nil)
	}
	delete(store.users, userID)
	for key, identity := range store.identities {
		if identity.UserID == userID {
			delete(store.identities, key)
		}
	}
	for key, session := range store.sessions {
		if session.UserID == userID {
			delete(store.sessions, key)
		}
	}
	return nil
}

func (store *memoryAccounts) ListAccounts(_ context.Context) ([]domain.User, error) {
	users := make([]domain.User, 0, len(store.users))
	for _, user := range store.users {
		users = append(users, user)
	}
	sort.Slice(users, func(left, right int) bool { return users[left].ID < users[right].ID })
	return users, nil
}

// MediaExists and ReconcileMediaReferences are inert here. These tests are
// about routes and authorisation; media reachability has its own tests against
// real SQLite, where the predicate actually means something.
func (store *memoryAccounts) MediaExists(_ context.Context, _ domain.ID) (bool, error) {
	return true, nil
}

func (store *memoryAccounts) ReconcileMediaReferences(_ context.Context, _ []domain.ID, _ time.Time) error {
	return nil
}

func (store *memoryAccounts) OwnerUserID(_ context.Context) (string, error) {
	return store.ownerID, nil
}

func (store *memoryAccounts) RegistrationMode(_ context.Context) (domain.RegistrationMode, error) {
	return store.mode, nil
}

func (store *memoryAccounts) CreateSession(_ context.Context, session domain.SessionRecord) error {
	store.sessions[session.ID] = session
	return nil
}

func (store *memoryAccounts) SessionByID(_ context.Context, id string) (domain.SessionRecord, bool, error) {
	session, found := store.sessions[id]
	return session, found, nil
}

func (store *memoryAccounts) RevokeSession(_ context.Context, id string, atMS int64) error {
	if session, found := store.sessions[id]; found && session.RevokedAtMS == nil {
		session.RevokedAtMS = &atMS
		store.sessions[id] = session
	}
	return nil
}

func (store *memoryAccounts) RevokeUserSessions(_ context.Context, userID string, atMS int64) (int, error) {
	revoked := 0
	for id, session := range store.sessions {
		if session.UserID == userID && session.RevokedAtMS == nil {
			stamp := atMS
			session.RevokedAtMS = &stamp
			store.sessions[id] = session
			revoked++
		}
	}
	return revoked, nil
}

func (store *memoryAccounts) TouchSession(_ context.Context, id string, atMS int64) error {
	if session, found := store.sessions[id]; found {
		session.LastSeenAtMS = atMS
		store.sessions[id] = session
	}
	return nil
}

func (store *memoryAccounts) ListUserSessions(_ context.Context, userID string) ([]domain.SessionRecord, error) {
	found := make([]domain.SessionRecord, 0, 2)
	for _, session := range store.sessions {
		if session.UserID == userID {
			found = append(found, session)
		}
	}
	sort.Slice(found, func(left, right int) bool { return found[left].IssuedAtMS > found[right].IssuedAtMS })
	return found, nil
}

func (store *memoryAccounts) PurgeExpiredSessions(_ context.Context, beforeMS int64) (int, error) {
	purged := 0
	for id, session := range store.sessions {
		if session.ExpiresAtMS < beforeMS {
			delete(store.sessions, id)
			purged++
		}
	}
	return purged, nil
}

// testOwnerAccount is the account every transport fixture signs in as.
func testOwnerAccount() domain.User {
	return domain.User{
		ID:          "0123456789abcdef0123456789abcdef",
		Username:    "owner",
		DisplayName: "Owner",
		Role:        domain.UserRoleOwner,
		Status:      domain.AccountActive,
		CreatedAtMS: 1,
		UpdatedAtMS: 1,
	}
}

// testAccounts builds an account service over the same SQLite generation a
// fixture is already using, for tests where accounts have to be real rows.
func testAccounts(t *testing.T, database *sqlitestore.Database, runtimeState *runtimefs.Runtime) *app.AccountService {
	t.Helper()
	repository, err := sqlitestore.NewAccountRepository(database, runtimeState.Locks)
	if err != nil {
		t.Fatal(err)
	}
	service, err := app.NewAccountService(repository)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

// testAccountsForRuntime opens its own generation for a fixture that has no
// database of its own.
func testAccountsForRuntime(t *testing.T, runtimeState *runtimefs.Runtime) *app.AccountService {
	t.Helper()
	database, err := sqlitestore.Open(context.Background(), runtimeState, sqlitestore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return testAccounts(t, database, runtimeState)
}
