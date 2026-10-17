package app

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moment/moment/internal/domain"
)

const testAvatarID = "01ARZ3NDEKTSV4RRFFQ69G5FAY"
const testCoverID = "01ARZ3NDEKTSV4RRFFQ69G5FAX"
const testPinnedPostID = "01ARZ3NDEKTSV4RRFFQ69G5FAW"
const testReplacementPostID = "01ARZ3NDEKTSV4RRFFQ69G5FAV"

type memorySiteTransactions struct {
	mu       sync.Mutex
	settings *SiteSettings
	media    map[string]bool
	entries  map[string]bool
	failPut  bool
}

func (transactions *memorySiteTransactions) Read(_ context.Context, operation func(SiteStore) error) error {
	transactions.mu.Lock()
	defer transactions.mu.Unlock()
	return operation(&memorySiteStore{settings: cloneSettingsPointer(transactions.settings), media: transactions.media, entries: transactions.entries})
}

func (transactions *memorySiteTransactions) Write(_ context.Context, operation func(SiteStore) error) error {
	transactions.mu.Lock()
	defer transactions.mu.Unlock()
	store := &memorySiteStore{
		settings: cloneSettingsPointer(transactions.settings),
		media:    transactions.media,
		entries:  transactions.entries,
		failPut:  transactions.failPut,
	}
	if err := operation(store); err != nil {
		return err
	}
	transactions.settings = cloneSettingsPointer(store.settings)
	return nil
}

type memorySiteStore struct {
	settings *SiteSettings
	media    map[string]bool
	entries  map[string]bool
	failPut  bool
}

func (store *memorySiteStore) SiteSettings(context.Context) (SiteSettings, error) {
	if store.settings == nil {
		return SiteSettings{}, errors.New("missing settings")
	}
	return cloneSiteSettings(*store.settings), nil
}

func (store *memorySiteStore) MediaExists(_ context.Context, id domain.ID) (bool, error) {
	return store.media[id.String()], nil
}

func (store *memorySiteStore) EntryExists(_ context.Context, id domain.ID) (bool, error) {
	return store.entries[id.String()], nil
}

func (store *memorySiteStore) UpdateSiteSettings(_ context.Context, settings SiteSettings) error {
	if store.failPut {
		return errors.New("injected write failure")
	}
	cloned := cloneSiteSettings(settings)
	store.settings = &cloned
	return nil
}

func (store *memorySiteStore) ReconcileMediaReferences(context.Context, []domain.ID, time.Time) error {
	return nil
}

func newTestSiteService(t *testing.T, transactions *memorySiteTransactions, development bool) *SiteService {
	t.Helper()
	service, err := NewSiteService(transactions, SiteOptions{
		AllowDevelopmentLocalHTTP: development,
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func TestSiteDefaultsAndPartialNullableUpdates(t *testing.T) {
	avatar, cover := testAvatarID, testCoverID
	initial := SiteSettings{ProfileUserID: "0123456789abcdef0123456789abcdef", Username: "writer", DisplayName: "Writer", Title: "Moment", Bio: "account biography", Timezone: "UTC", CommentsEnabled: true, AvatarMediaID: &avatar, CoverMediaID: &cover}
	transactions := &memorySiteTransactions{settings: &initial, media: map[string]bool{avatar: true, cover: true}}
	service := newTestSiteService(t, transactions, true)
	title, url, comments := "Site title", "HTTP://LOCALHOST:80/journal/", false
	updated, err := service.Update(context.Background(), SiteUpdate{Title: &title, PublicBaseURL: &NullableString{Value: &url}, CommentsEnabled: &comments})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Title != title || updated.PublicBaseURL == nil || *updated.PublicBaseURL != "http://localhost/journal" || updated.CommentsEnabled {
		t.Fatalf("updated=%+v", updated)
	}
	if updated.Username != initial.Username || updated.DisplayName != initial.DisplayName || updated.Bio != initial.Bio || !reflect.DeepEqual(updated.AvatarMediaID, initial.AvatarMediaID) || !reflect.DeepEqual(updated.CoverMediaID, initial.CoverMediaID) {
		t.Fatal("site update changed the selected account")
	}
	cleared, err := service.Update(context.Background(), SiteUpdate{PublicBaseURL: &NullableString{}})
	if err != nil || cleared.PublicBaseURL != nil || cleared.Title != title || cleared.AvatarMediaID == nil || cleared.CoverMediaID == nil {
		t.Fatalf("URL clear touched account pictures=%+v,%v", cleared, err)
	}
}

func TestSiteUpdateValidationAndAtomicRollback(t *testing.T) {
	initial := SiteSettings{ProfileUserID: "0123456789abcdef0123456789abcdef", Username: "writer", DisplayName: "Moment", Title: "Before", Bio: "kept", Timezone: "UTC", CommentsEnabled: true}
	transactions := &memorySiteTransactions{settings: &initial, media: map[string]bool{}}
	service := newTestSiteService(t, transactions, false)

	for name, update := range map[string]SiteUpdate{
		"empty": {},
		"invalid timezone": {
			Timezone: ptr("UTC+08:00"),
		},
		"local timezone": {
			Timezone: ptr("Local"),
		},
		"invalid URL": {
			PublicBaseURL: &NullableString{Value: ptr("https://user@example.com?q=1#frag")},
		},
		"production HTTP": {
			PublicBaseURL: &NullableString{Value: ptr("http://localhost")},
		},
		"development remote HTTP": {
			PublicBaseURL: &NullableString{Value: ptr("http://example.com")},
		},
		"invalid host": {
			PublicBaseURL: &NullableString{Value: ptr("https://bad_host.example")},
		},
		"invalid port": {
			PublicBaseURL: &NullableString{Value: ptr("https://example.com:bad")},
		},
		"out of range port": {
			PublicBaseURL: &NullableString{Value: ptr("https://example.com:99999")},
		},
		"empty query": {
			PublicBaseURL: &NullableString{Value: ptr("https://example.com?")},
		},
		"empty title": {
			Title: ptr("   "),
		},
		"long title": {
			Title: ptr(strings.Repeat("界", 121)),
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := service.Update(context.Background(), update); !domain.HasCode(err, domain.CodeInvalidArgument) {
				t.Fatalf("error = %v", err)
			}
			if !reflect.DeepEqual(*transactions.settings, initial) {
				t.Fatalf("invalid update changed settings: %#v", transactions.settings)
			}
		})
	}

	changedTitle := "Must roll back"

	transactions.failPut = true
	if _, err := service.Update(context.Background(), SiteUpdate{Title: &changedTitle}); !domain.HasCode(err, domain.CodeInternal) {
		t.Fatalf("write failure = %v", err)
	}
	if !reflect.DeepEqual(*transactions.settings, initial) {
		t.Fatalf("failed write partially committed: %#v", transactions.settings)
	}
}

func TestHostTimezoneDetectionUsesIANAInputsAndFallsBack(t *testing.T) {
	tests := []struct {
		name    string
		sources hostTimezoneSources
		want    string
	}{
		{name: "TZ", sources: hostTimezoneSources{tz: "Asia/Shanghai"}, want: "Asia/Shanghai"},
		{name: "TZ zoneinfo path", sources: hostTimezoneSources{tz: ":/usr/share/zoneinfo/Europe/London"}, want: "Europe/London"},
		{name: "local name", sources: hostTimezoneSources{localName: "America/New_York"}, want: "America/New_York"},
		{name: "zoneinfo symlink", sources: hostTimezoneSources{
			localName: "Local",
			readlink:  func(string) (string, error) { return "../usr/share/zoneinfo/Europe/Paris", nil },
		}, want: "Europe/Paris"},
		{name: "timezone file", sources: hostTimezoneSources{
			localName: "Local",
			readlink:  func(string) (string, error) { return "", errors.New("not a symlink") },
			readFile:  func(string) ([]byte, error) { return []byte("Asia/Tokyo\n"), nil },
		}, want: "Asia/Tokyo"},
		{name: "fixed offset falls back", sources: hostTimezoneSources{tz: "UTC+08:00", localName: "Local"}, want: "UTC"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := detectHostTimezone(test.sources); got != test.want {
				t.Fatalf("timezone = %q, want %q", got, test.want)
			}
		})
	}
}

func TestSiteAcceptsSingleSegmentIANALinks(t *testing.T) {
	initial := SiteSettings{ProfileUserID: "0123456789abcdef0123456789abcdef", Username: "writer", DisplayName: "Moment", Title: "Moment", Timezone: "UTC", CommentsEnabled: true}
	transactions := &memorySiteTransactions{settings: &initial, media: map[string]bool{}}
	service := newTestSiteService(t, transactions, false)
	for _, timezone := range []string{"GMT", "CET", "Singapore", "Turkey", "Zulu"} {
		if _, err := service.Update(context.Background(), SiteUpdate{Timezone: &timezone}); err != nil {
			t.Fatalf("IANA link %q was rejected: %v", timezone, err)
		}
	}
}

func TestSiteConcurrentPartialUpdatesMergeInsideSerializedWrite(t *testing.T) {
	initial := SiteSettings{ProfileUserID: "0123456789abcdef0123456789abcdef", Username: "writer", DisplayName: "Moment", Title: "Before", Timezone: "UTC", CommentsEnabled: true}
	transactions := &memorySiteTransactions{settings: &initial, media: map[string]bool{}}
	service := newTestSiteService(t, transactions, false)
	title := "After"
	timezone := "Europe/Paris"

	var wait sync.WaitGroup
	wait.Add(2)
	errors := make(chan error, 2)
	go func() {
		defer wait.Done()
		_, err := service.Update(context.Background(), SiteUpdate{Title: &title})
		errors <- err
	}()
	go func() {
		defer wait.Done()
		_, err := service.Update(context.Background(), SiteUpdate{Timezone: &timezone})
		errors <- err
	}()
	wait.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	settings, err := service.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if settings.Title != title || settings.Timezone != timezone {
		t.Fatalf("lost partial update: %#v", settings)
	}
}

func TestSitePinReplaceAndUnpinAreAtomicAndIdempotent(t *testing.T) {
	initial := SiteSettings{ProfileUserID: "0123456789abcdef0123456789abcdef", Username: "writer", DisplayName: "Moment", Title: "Moment", Timezone: "UTC", CommentsEnabled: true}
	transactions := &memorySiteTransactions{
		settings: &initial,
		media:    map[string]bool{},
		entries:  map[string]bool{testPinnedPostID: true, testReplacementPostID: true},
	}
	service := newTestSiteService(t, transactions, false)

	first, err := service.Pin(WithContentActor(context.Background(), domain.User{ID: "0123456789abcdef0123456789abcdef", Role: domain.UserRoleAdmin, Status: domain.AccountActive}), domain.ID(testPinnedPostID))
	if err != nil || first.PinnedPostID == nil || *first.PinnedPostID != testPinnedPostID {
		t.Fatalf("first pin = %#v, err=%v", first, err)
	}
	replayed, err := service.Pin(WithContentActor(context.Background(), domain.User{ID: "0123456789abcdef0123456789abcdef", Role: domain.UserRoleAdmin, Status: domain.AccountActive}), domain.ID(testPinnedPostID))
	if err != nil || !reflect.DeepEqual(first, replayed) {
		t.Fatalf("idempotent pin = %#v, err=%v", replayed, err)
	}
	replaced, err := service.Pin(WithContentActor(context.Background(), domain.User{ID: "0123456789abcdef0123456789abcdef", Role: domain.UserRoleAdmin, Status: domain.AccountActive}), domain.ID(testReplacementPostID))
	if err != nil || replaced.PinnedPostID == nil || *replaced.PinnedPostID != testReplacementPostID {
		t.Fatalf("replacement pin = %#v, err=%v", replaced, err)
	}
	if _, err := service.Pin(WithContentActor(context.Background(), domain.User{ID: "0123456789abcdef0123456789abcdef", Role: domain.UserRoleAdmin, Status: domain.AccountActive}), domain.ID("01ARZ3NDEKTSV4RRFFQ69G5FAX")); !domain.HasCode(err, domain.CodePostNotFound) {
		t.Fatalf("missing pin error = %v", err)
	}
	stored, err := service.Get(WithContentActor(context.Background(), domain.User{ID: "0123456789abcdef0123456789abcdef", Role: domain.UserRoleAdmin, Status: domain.AccountActive}))
	if err != nil || stored.PinnedPostID == nil || *stored.PinnedPostID != testReplacementPostID {
		t.Fatalf("failed pin changed state = %#v, err=%v", stored, err)
	}
	cleared, err := service.Unpin(WithContentActor(context.Background(), domain.User{ID: "0123456789abcdef0123456789abcdef", Role: domain.UserRoleAdmin, Status: domain.AccountActive}))
	if err != nil || cleared.PinnedPostID != nil {
		t.Fatalf("unpin = %#v, err=%v", cleared, err)
	}
	repeated, err := service.Unpin(WithContentActor(context.Background(), domain.User{ID: "0123456789abcdef0123456789abcdef", Role: domain.UserRoleAdmin, Status: domain.AccountActive}))
	if err != nil || repeated.PinnedPostID != nil {
		t.Fatalf("idempotent unpin = %#v, err=%v", repeated, err)
	}
}

func cloneSettingsPointer(settings *SiteSettings) *SiteSettings {
	if settings == nil {
		return nil
	}
	cloned := cloneSiteSettings(*settings)
	return &cloned
}

func ptr(value string) *string { return &value }

// The owner's handle is one column with one gate, whichever form reaches it.
//
// This path validated the shape and stopped there, so a name the account route
// answers USERNAME_RESERVED for saved here without complaint -- and a handle
// like "search" or "profile" is matched by the route table before the profile
// route ever sees it, leaving the owner's own page permanently unreachable and
// every profile_url the server emits pointing at the wrong screen.

func TestSiteRegistrationModeIsWrittenAndValidated(t *testing.T) {
	initial := SiteSettings{ProfileUserID: "0123456789abcdef0123456789abcdef", Username: "writer", DisplayName: "Moment", Title: "Moment", Timezone: "UTC", CommentsEnabled: true, RegistrationMode: domain.RegistrationApproval}
	transactions := &memorySiteTransactions{settings: &initial, media: map[string]bool{}}
	service := newTestSiteService(t, transactions, false)

	for _, mode := range []domain.RegistrationMode{domain.RegistrationOpen, domain.RegistrationClosed, domain.RegistrationApproval} {
		value := string(mode)
		saved, err := service.Update(context.Background(), SiteUpdate{RegistrationMode: &value})
		if err != nil {
			t.Fatalf("registration mode %q: %v", mode, err)
		}
		if saved.RegistrationMode != mode || transactions.settings.RegistrationMode != mode {
			t.Fatalf("registration mode = %q stored %q, want %q", saved.RegistrationMode, transactions.settings.RegistrationMode, mode)
		}
	}

	for name, mode := range map[string]string{
		"not a mode":  "everyone",
		"empty":       "",
		"wrong case":  "Open",
		"with spaces": " open ",
	} {
		t.Run(name, func(t *testing.T) {
			before := *transactions.settings
			if _, err := service.Update(context.Background(), SiteUpdate{RegistrationMode: &mode}); !domain.HasCode(err, domain.CodeInvalidArgument) {
				t.Fatalf("error = %v", err)
			}
			if !reflect.DeepEqual(*transactions.settings, before) {
				t.Fatalf("rejected mode changed settings: %#v", transactions.settings)
			}
		})
	}
}
