package app

import (
	"context"
	"crypto/sha256"
	"sort"

	"github.com/moment/moment/internal/domain"
)

// BrowserAccountSet proves possession of this browser's independently verified sign-ins.
// It never grants content authority without resolving a member session.
type BrowserAccountSet struct {
	ID             string
	CredentialHash []byte
	CSRFToken      string
	SessionEpoch   uint64
	ExpiresAtMS    int64
	RevokedAtMS    *int64
}

// BrowserAccountMember binds a retained identity to its verified session.
type BrowserAccountMember struct {
	SetID        string
	UserID       string
	SessionID    string
	LastUsedAtMS int64
}

// BrowserAccountEntry describes a retained identity and its session availability.
type BrowserAccountEntry struct {
	User      domain.User
	Member    BrowserAccountMember
	Available bool
}

// BrowserAccountStore participates in the existing account transaction boundary.
type BrowserAccountStore interface {
	CreateBrowserAccountSet(context.Context, BrowserAccountSet) error
	BrowserAccountSetByHash(context.Context, []byte) (BrowserAccountSet, bool, error)
	BrowserAccountMembers(context.Context, string) ([]BrowserAccountMember, error)
	PutBrowserAccountMember(context.Context, BrowserAccountMember, int64) error
	RotateBrowserAccountSet(context.Context, BrowserAccountSet) error
	DeleteBrowserAccountMember(context.Context, string, string) error
	RevokeBrowserAccountSet(context.Context, string, int64) error
}

func browserAccountStore(accounts AccountStore) (BrowserAccountStore, error) {
	store, ok := accounts.(BrowserAccountStore)
	if !ok {
		return nil, domain.NewError(domain.CodeDataIntegrityFailed, "browser session storage is unavailable", nil)
	}
	return store, nil
}

func browserSet(ctx context.Context, store BrowserAccountStore, secret string, epoch uint64, nowMS int64) (BrowserAccountSet, error) {
	if len(secret) < 32 || len(secret) > 128 {
		return BrowserAccountSet{}, browserUnauthorized()
	}
	hash := sha256.Sum256([]byte(secret))
	set, found, err := store.BrowserAccountSetByHash(ctx, hash[:])
	if err != nil {
		return set, err
	}
	if !found || set.RevokedAtMS != nil || set.SessionEpoch != epoch || set.ExpiresAtMS <= nowMS {
		return set, browserUnauthorized()
	}
	return set, nil
}

func browserUnauthorized() error {
	return domain.NewError(domain.CodeUnauthorized, "this browser sign-in is no longer valid", nil)
}

func resolveBrowserMember(ctx context.Context, accounts AccountStore, sessions SessionStore, id string, nowMS int64) (SessionResolution, error) {
	session, found, err := sessions.SessionByID(ctx, id)
	if err != nil {
		return SessionResolution{}, err
	}
	if !found || !session.Live(nowMS) {
		return SessionResolution{}, browserUnauthorized()
	}
	if session.SourceTokenID != nil {
		credentials, err := credentialStore(accounts)
		if err != nil {
			return SessionResolution{}, err
		}
		token, found, err := credentials.APITokenByID(ctx, *session.SourceTokenID)
		if err != nil {
			return SessionResolution{}, err
		}
		if !found || !token.Live() || token.UserID != session.UserID {
			return SessionResolution{}, browserUnauthorized()
		}
	}
	user, err := accounts.UserByID(ctx, session.UserID)
	if err != nil {
		return SessionResolution{}, err
	}
	if !user.CanSignIn() {
		return SessionResolution{}, domain.NewError(domain.CodeAccountSuspended, "this account has been suspended", nil)
	}
	return SessionResolution{Session: session, User: user}, nil
}

func browserMemberUnavailable(err error) bool {
	return domain.HasCode(err, domain.CodeUnauthorized) || domain.HasCode(err, domain.CodeAccountSuspended) || domain.HasCode(err, domain.CodeAccountNotFound)
}

// BootstrapBrowserAccounts adopts an existing authenticated session without extending it.
func (service *AccountService) BootstrapBrowserAccounts(ctx context.Context, secret, csrf string, epoch uint64, sessionID string) (BrowserAccountSet, error) {
	nowMS := domain.TimeToMillis(domain.NormalizeTime(service.clock()))
	id, err := service.newID()
	if err != nil {
		return BrowserAccountSet{}, err
	}
	hash := sha256.Sum256([]byte(secret))
	set := BrowserAccountSet{ID: id, CredentialHash: hash[:], CSRFToken: csrf, SessionEpoch: epoch}
	err = service.transactions.WriteAccounts(ctx, func(accounts AccountStore, sessions SessionStore) error {
		store, err := browserAccountStore(accounts)
		if err != nil {
			return err
		}
		resolved, err := resolveBrowserMember(ctx, accounts, sessions, sessionID, nowMS)
		if err != nil {
			return err
		}
		set.ExpiresAtMS = resolved.Session.AbsoluteExpiryMS()
		if err := store.CreateBrowserAccountSet(ctx, set); err != nil {
			return err
		}
		return store.PutBrowserAccountMember(ctx, BrowserAccountMember{SetID: id, UserID: resolved.User.ID, SessionID: sessionID, LastUsedAtMS: nowMS}, set.ExpiresAtMS)
	})
	return set, err
}

// BrowserAccounts returns only identities previously authenticated in this browser.
func (service *AccountService) BrowserAccounts(ctx context.Context, secret string, epoch uint64) (BrowserAccountSet, []BrowserAccountEntry, error) {
	nowMS := domain.TimeToMillis(domain.NormalizeTime(service.clock()))
	var set BrowserAccountSet
	entries := []BrowserAccountEntry{}
	err := service.transactions.ReadAccounts(ctx, func(accounts AccountStore, sessions SessionStore) error {
		store, err := browserAccountStore(accounts)
		if err != nil {
			return err
		}
		set, err = browserSet(ctx, store, secret, epoch, nowMS)
		if err != nil {
			return err
		}
		members, err := store.BrowserAccountMembers(ctx, set.ID)
		if err != nil {
			return err
		}
		for _, member := range members {
			user, err := accounts.UserByID(ctx, member.UserID)
			if err != nil {
				return err
			}
			_, err = resolveBrowserMember(ctx, accounts, sessions, member.SessionID, nowMS)
			if err != nil && !browserMemberUnavailable(err) {
				return err
			}
			entries = append(entries, BrowserAccountEntry{User: user, Member: member, Available: err == nil})
		}
		return nil
	})
	return set, entries, err
}

// AddBrowserAccount attaches a fresh credential-verified session, replacing duplicates.
func (service *AccountService) AddBrowserAccount(ctx context.Context, secret, nextSecret, csrf string, epoch uint64, sessionID string) (BrowserAccountSet, error) {
	if len(nextSecret) < 32 || len(nextSecret) > 128 || len(csrf) < 32 || len(csrf) > 128 || nextSecret == secret {
		return BrowserAccountSet{}, browserUnauthorized()
	}
	nowMS := domain.TimeToMillis(domain.NormalizeTime(service.clock()))
	var set BrowserAccountSet
	err := service.transactions.WriteAccounts(ctx, func(accounts AccountStore, sessions SessionStore) error {
		store, err := browserAccountStore(accounts)
		if err != nil {
			return err
		}
		set, err = browserSet(ctx, store, secret, epoch, nowMS)
		if err != nil {
			return err
		}
		if csrf == set.CSRFToken {
			return browserUnauthorized()
		}
		resolved, err := resolveBrowserMember(ctx, accounts, sessions, sessionID, nowMS)
		if err != nil {
			return err
		}
		members, err := store.BrowserAccountMembers(ctx, set.ID)
		if err != nil {
			return err
		}
		for _, member := range members {
			if member.UserID == resolved.User.ID && member.SessionID != sessionID {
				if err := sessions.RevokeSession(ctx, member.SessionID, nowMS); err != nil {
					return err
				}
			}
		}
		if resolved.Session.AbsoluteExpiryMS() > set.ExpiresAtMS {
			set.ExpiresAtMS = resolved.Session.AbsoluteExpiryMS()
		}
		if err := store.PutBrowserAccountMember(ctx, BrowserAccountMember{SetID: set.ID, UserID: resolved.User.ID, SessionID: sessionID, LastUsedAtMS: nowMS}, set.ExpiresAtMS); err != nil {
			return err
		}
		hash := sha256.Sum256([]byte(nextSecret))
		set.CredentialHash = hash[:]
		set.CSRFToken = csrf
		return store.RotateBrowserAccountSet(ctx, set)
	})
	return set, err
}

// ActivateBrowserAccount resolves an available member without extending its session.
func (service *AccountService) ActivateBrowserAccount(ctx context.Context, secret string, epoch uint64, userID string) (SessionResolution, error) {
	nowMS := domain.TimeToMillis(domain.NormalizeTime(service.clock()))
	var resolved SessionResolution
	err := service.transactions.WriteAccounts(ctx, func(accounts AccountStore, sessions SessionStore) error {
		store, err := browserAccountStore(accounts)
		if err != nil {
			return err
		}
		set, err := browserSet(ctx, store, secret, epoch, nowMS)
		if err != nil {
			return err
		}
		members, err := store.BrowserAccountMembers(ctx, set.ID)
		if err != nil {
			return err
		}
		for _, member := range members {
			if member.UserID == userID {
				resolved, err = resolveBrowserMember(ctx, accounts, sessions, member.SessionID, nowMS)
				if err != nil {
					return err
				}
				member.LastUsedAtMS = nowMS
				return store.PutBrowserAccountMember(ctx, member, set.ExpiresAtMS)
			}
		}
		return browserUnauthorized()
	})
	return resolved, err
}

// RemoveBrowserAccounts ends only this browser's member sessions. An empty userID
// removes all members; currentID controls whether a replacement identity is needed.
func (service *AccountService) RemoveBrowserAccounts(ctx context.Context, secret string, epoch uint64, userID, currentID string) (*SessionResolution, bool, error) {
	nowMS := domain.TimeToMillis(domain.NormalizeTime(service.clock()))
	var next *SessionResolution
	empty := false
	err := service.transactions.WriteAccounts(ctx, func(accounts AccountStore, sessions SessionStore) error {
		store, err := browserAccountStore(accounts)
		if err != nil {
			return err
		}
		set, err := browserSet(ctx, store, secret, epoch, nowMS)
		if err != nil {
			return err
		}
		members, err := store.BrowserAccountMembers(ctx, set.ID)
		if err != nil {
			return err
		}
		remaining := []BrowserAccountMember{}
		found := userID == ""
		for _, member := range members {
			if userID == "" || member.UserID == userID {
				found = true
				if err := sessions.RevokeSession(ctx, member.SessionID, nowMS); err != nil {
					return err
				}
				if err := store.DeleteBrowserAccountMember(ctx, set.ID, member.UserID); err != nil {
					return err
				}
			} else {
				remaining = append(remaining, member)
			}
		}
		if !found {
			return browserUnauthorized()
		}
		empty = len(remaining) == 0
		if empty {
			return store.RevokeBrowserAccountSet(ctx, set.ID, nowMS)
		}
		if userID != "" && userID != currentID {
			return nil
		}
		sort.SliceStable(remaining, func(i, j int) bool { return remaining[i].LastUsedAtMS > remaining[j].LastUsedAtMS })
		for _, member := range remaining {
			resolved, err := resolveBrowserMember(ctx, accounts, sessions, member.SessionID, nowMS)
			if browserMemberUnavailable(err) {
				continue
			}
			if err != nil {
				return err
			}
			next = &resolved
			member.LastUsedAtMS = nowMS
			return store.PutBrowserAccountMember(ctx, member, set.ExpiresAtMS)
		}
		return nil
	})
	return next, empty, err
}
