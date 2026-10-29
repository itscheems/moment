package ginhttp

import (
	"bytes"
	"context"
	"errors"
	"slices"
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
	mu               sync.Mutex
	users            map[string]domain.User
	identities       map[string]domain.Identity
	sessions         map[string]domain.SessionRecord
	passwords        map[string][]byte
	tokens           map[string]domain.APIToken
	primaryAccountID string
	mode             domain.RegistrationMode
	failSession      bool
}

func newMemoryAccounts(primaryAccount domain.User) *memoryAccounts {
	return &memoryAccounts{
		users:            map[string]domain.User{primaryAccount.ID: primaryAccount},
		identities:       map[string]domain.Identity{},
		sessions:         map[string]domain.SessionRecord{},
		passwords:        map[string][]byte{},
		tokens:           map[string]domain.APIToken{},
		primaryAccountID: primaryAccount.ID,
		mode:             domain.RegistrationApproval,
	}
}

func (store *memoryAccounts) ReadAccounts(_ context.Context, operation func(app.AccountStore, app.SessionStore) error) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	return operation(store, store)
}

func (store *memoryAccounts) WriteAccounts(_ context.Context, operation func(app.AccountStore, app.SessionStore) error) error {
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
	// (provider, provider_user_id), which is what the SQL orders by. Provider
	// alone leaves two identities from one provider in map order, and the
	// caller that removes one of them names only the provider.
	sort.Slice(found, func(left, right int) bool {
		if found[left].Provider != found[right].Provider {
			return found[left].Provider < found[right].Provider
		}
		return found[left].ProviderUserID < found[right].ProviderUserID
	})
	return found, nil
}

func (store *memoryAccounts) CreateAccountWithIdentity(ctx context.Context, user domain.User, identity domain.Identity) error {
	if _, taken := store.users[user.ID]; taken {
		return domain.NewError(domain.CodeUsernameTaken, "account exists", nil)
	}
	// users.username carries a case-insensitive unique index, so the real store
	// refuses this even when the id is free. A fake that accepted it would let
	// the allocator's collision handling look correct here while the same input
	// failed against SQLite.
	for _, existing := range store.users {
		if strings.EqualFold(existing.Username, user.Username) {
			return domain.NewError(domain.CodeUsernameTaken, "that username is taken", map[string]any{"field": "username"})
		}
	}
	store.users[user.ID] = user
	return store.LinkIdentity(ctx, identity)
}

func (store *memoryAccounts) CreateAccountWithPassword(ctx context.Context, user domain.User, hash []byte) error {
	for _, existing := range store.users {
		if strings.EqualFold(existing.Username, user.Username) {
			return domain.NewError(domain.CodeUsernameTaken, "that username is taken", map[string]any{"field": "username"})
		}
	}
	store.users[user.ID] = user
	return store.PutPasswordHash(ctx, user.ID, hash, user.CreatedAtMS)
}

func (store *memoryAccounts) LinkIdentity(_ context.Context, identity domain.Identity) error {
	key := identityKey(identity.Provider, identity.ProviderUserID)
	if _, taken := store.identities[key]; taken {
		return domain.NewError(domain.CodeIdentityAlreadyLinked, "already linked", nil)
	}
	store.identities[key] = identity
	return nil
}

func (store *memoryAccounts) UnlinkIdentity(_ context.Context, identity domain.Identity) error {
	key := identityKey(identity.Provider, identity.ProviderUserID)
	if stored, found := store.identities[key]; found && stored.UserID == identity.UserID {
		delete(store.identities, key)
		return nil
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
	sort.Slice(users, func(left, right int) bool {
		if users[left].CreatedAtMS != users[right].CreatedAtMS {
			return users[left].CreatedAtMS < users[right].CreatedAtMS
		}
		return users[left].ID < users[right].ID
	})
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

func (store *memoryAccounts) SearchAccounts(_ context.Context, query string, limit int) ([]domain.User, error) {
	found := make([]domain.User, 0, limit)
	for _, user := range store.users {
		if user.Status != domain.AccountActive {
			continue
		}
		if query != "" &&
			!strings.HasPrefix(strings.ToLower(user.Username), query) &&
			!strings.Contains(strings.ToLower(user.DisplayName), query) {
			continue
		}
		found = append(found, user)
	}
	sort.Slice(found, func(left, right int) bool {
		if found[left].IsAdmin() != found[right].IsAdmin() {
			return found[left].IsAdmin()
		}
		return found[left].Username < found[right].Username
	})
	if len(found) > limit {
		found = found[:limit]
	}
	return found, nil
}

func (store *memoryAccounts) ProfileUserID(_ context.Context) (string, error) {
	return store.primaryAccountID, nil
}

func (store *memoryAccounts) RegistrationMode(_ context.Context) (domain.RegistrationMode, error) {
	return store.mode, nil
}

func (store *memoryAccounts) CreateSession(_ context.Context, session domain.SessionRecord) error {
	if store.failSession {
		return errors.New("session storage unavailable")
	}
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

func TestMemoryAccountsAgreesWithSQLiteOnOrderAndUniqueness(t *testing.T) {
	ctx := context.Background()
	runtimeState, err := runtimefs.Initialize(ctx, runtimefs.Config{DataDir: t.TempDir(), Environment: runtimefs.EnvironmentDevelopment})
	if err != nil {
		t.Fatal(err)
	}
	database, err := sqlitestore.Open(ctx, runtimeState, sqlitestore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	repository, err := sqlitestore.NewAccountRepository(database, runtimeState.Locks)
	if err != nil {
		t.Fatal(err)
	}

	// Ids that sort against the order the accounts are created in, which is the
	// only arrangement that can tell the two orderings apart. Real ids are
	// random hex, so this is the ordinary case rather than a contrived one.
	ids := []string{"ffffffffffffffffffffffffffffff01", "00000000000000000000000000000002"}
	now := time.UnixMilli(1000).UTC()
	build := func(transactions app.AccountTransactions) *app.AccountService {
		t.Helper()
		index := 0
		service, err := app.NewAccountService(transactions,
			app.WithAccountClock(func() time.Time { return now }),
			app.WithAccountIDGenerator(func() (string, error) {
				id := ids[index]
				index++
				return id, nil
			}))
		if err != nil {
			t.Fatal(err)
		}
		return service
	}

	bootstrap, err := app.NewAccountService(repository, app.WithAccountClock(func() time.Time { return time.UnixMilli(1) }))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bootstrap.InitializeSiteAccount(ctx, "writer"); err != nil {
		t.Fatal(err)
	}
	stored := build(repository)
	primaryAccount, err := stored.ProfileAccount(ctx)
	if err != nil {
		t.Fatal(err)
	}
	store := newMemoryAccounts(primaryAccount)
	remembered := build(store)

	for _, seed := range []struct {
		providerUserID string
		displayName    string
		atMS           int64
	}{
		{"1", "Ada Lovelace", 1000},
		{"2", "Grace Hopper", 2000},
	} {
		now = time.UnixMilli(seed.atMS).UTC()
		for _, service := range []*app.AccountService{stored, remembered} {
			if _, err := service.SignIn(ctx, app.ExternalIdentity{
				Provider: "google", ProviderUserID: seed.providerUserID, DisplayName: seed.displayName,
			}); err != nil {
				t.Fatal(err)
			}
		}
	}

	listed := func(service *app.AccountService) []string {
		t.Helper()
		accounts, err := service.List(ctx)
		if err != nil {
			t.Fatal(err)
		}
		order := make([]string, 0, len(accounts))
		for _, account := range accounts {
			order = append(order, account.Username)
		}
		return order
	}
	fromSQLite, fromMemory := listed(stored), listed(remembered)
	if !slices.Equal(fromSQLite, fromMemory) {
		t.Fatalf("list order: sqlite = %v, memory = %v", fromSQLite, fromMemory)
	}
	if len(fromSQLite) != 3 || fromSQLite[0] != primaryAccount.Username {
		t.Fatalf("list = %v, want the oldest account first", fromSQLite)
	}

	// Two identities from one provider list in the same order in both, which
	// is what decides which of them a remove keyed by a provider name takes.
	member, err := stored.Account(ctx, ids[0])
	if err != nil {
		t.Fatal(err)
	}
	listedIdentities := func(transactions app.AccountTransactions) []string {
		t.Helper()
		var order []string
		if err := transactions.WriteAccounts(ctx, func(accounts app.AccountStore, _ app.SessionStore) error {
			for _, providerUserID := range []string{"999999999", "216108319"} {
				if err := accounts.LinkIdentity(ctx, domain.Identity{
					Provider: "github", ProviderUserID: providerUserID, UserID: member.ID,
					CreatedAtMS: 4000, LastLoginAtMS: 4000,
				}); err != nil {
					return err
				}
			}
			identities, err := accounts.IdentitiesForUser(ctx, member.ID)
			if err != nil {
				return err
			}
			for _, identity := range identities {
				order = append(order, identity.Provider+":"+identity.ProviderUserID)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		return order
	}
	if fromSQLite, fromMemory := listedIdentities(repository), listedIdentities(store); !slices.Equal(fromSQLite, fromMemory) {
		t.Fatalf("identity order: sqlite = %v, memory = %v", fromSQLite, fromMemory)
	}

	// The handle is unique in the database whether or not the caller checked.
	duplicate := func(transactions app.AccountTransactions) error {
		return transactions.WriteAccounts(ctx, func(accounts app.AccountStore, _ app.SessionStore) error {
			return accounts.CreateAccountWithIdentity(ctx,
				domain.User{
					ID: "0123456789abcdef0123456789abcdef", Username: primaryAccount.Username, DisplayName: "Impostor",
					Role: domain.UserRoleMember, Status: domain.AccountPending, CreatedAtMS: 3000, UpdatedAtMS: 3000,
				},
				domain.Identity{
					Provider: "google", ProviderUserID: "3", UserID: "0123456789abcdef0123456789abcdef",
					CreatedAtMS: 3000, LastLoginAtMS: 3000,
				})
		})
	}
	for name, transactions := range map[string]app.AccountTransactions{"sqlite": repository, "memory": store} {
		var refusal *domain.Error
		err := duplicate(transactions)
		if !errors.As(err, &refusal) || refusal.Code != domain.CodeUsernameTaken {
			t.Fatalf("%s accepted a duplicate handle: %v", name, err)
		}
	}
}

// testProfileAccount is the account every transport fixture signs in as.
func testProfileAccount() domain.User {
	return domain.User{
		ID:          "0123456789abcdef0123456789abcdef",
		Username:    "owner",
		DisplayName: "Owner",
		Role:        domain.UserRoleAdmin,
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
	users, err := service.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(users) == 0 {
		bootstrap, err := app.NewAccountService(repository, app.WithAccountIDGenerator(func() (string, error) { return "0123456789abcdef0123456789abcdef", nil }))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := bootstrap.InitializeSiteAccount(context.Background(), "writer"); err != nil {
			t.Fatal(err)
		}
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

func (store *memoryAccounts) PasswordHash(_ context.Context, userID string) ([]byte, bool, error) {
	hash, ok := store.passwords[userID]
	return bytes.Clone(hash), ok, nil
}
func (store *memoryAccounts) PutPasswordHash(_ context.Context, userID string, hash []byte, _ int64) error {
	store.passwords[userID] = bytes.Clone(hash)
	return nil
}
func (store *memoryAccounts) InsertAPIToken(_ context.Context, token domain.APIToken) error {
	store.tokens[token.ID] = token
	return nil
}
func (store *memoryAccounts) APITokenByHash(_ context.Context, hash []byte) (domain.APIToken, bool, error) {
	for _, t := range store.tokens {
		if bytes.Equal(t.Hash, hash) {
			return t, true, nil
		}
	}
	return domain.APIToken{}, false, nil
}
func (store *memoryAccounts) APITokenByID(_ context.Context, id string) (domain.APIToken, bool, error) {
	token, ok := store.tokens[id]
	return token, ok, nil
}
func (store *memoryAccounts) ListAPITokens(_ context.Context, userID string) ([]domain.APIToken, error) {
	tokens := []domain.APIToken{}
	for _, t := range store.tokens {
		if t.UserID == userID {
			tokens = append(tokens, t)
		}
	}
	sort.Slice(tokens, func(i, j int) bool { return tokens[i].ID < tokens[j].ID })
	return tokens, nil
}
func (store *memoryAccounts) RevokeAPIToken(_ context.Context, id string, at int64) error {
	token, ok := store.tokens[id]
	if ok {
		token.RevokedAtMS = &at
		store.tokens[id] = token
	}
	return nil
}
func (store *memoryAccounts) RevokeUserAPITokens(ctx context.Context, userID string, at int64) error {
	for _, t := range store.tokens {
		if t.UserID == userID {
			if err := store.RevokeAPIToken(ctx, t.ID, at); err != nil {
				return err
			}
		}
	}
	return nil
}

func fixtureContentContext(t *testing.T, accounts *app.AccountService) context.Context {
	t.Helper()
	user, err := accounts.ProfileAccount(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return app.WithContentActor(context.Background(), user)
}

func (store *memoryAccounts) MediaAvailableForAccount(ctx context.Context, _ string, id domain.ID) (bool, error) {
	return store.MediaExists(ctx, id)
}

func (store *memoryAccounts) UpdatePreferences(_ context.Context, userID string, locale *string, atMS int64) error {
	user, ok := store.users[userID]
	if !ok {
		return domain.NewError(domain.CodeAccountNotFound, "no such account", nil)
	}
	user.UILocale, user.UpdatedAtMS = locale, atMS
	store.users[userID] = user
	return nil
}
