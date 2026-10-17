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
	initial := SiteSettings{ProfileUserID: "0123456789abcdef0123456789abcdef", Username: "writer", DisplayName: "Moment", Title: "Moment", Timezone: "Asia/Shanghai", CommentsEnabled: true}
	transactions := &memorySiteTransactions{settings: &initial, media: map[string]bool{testAvatarID: true, testCoverID: true}}
	service := newTestSiteService(t, transactions, true)

	defaults, err := service.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if defaults.Title != "Moment" || defaults.Timezone != "Asia/Shanghai" || !defaults.CommentsEnabled || defaults.PublicBaseURL != nil || defaults.AvatarMediaID != nil {
		t.Fatalf("defaults = %#v", defaults)
	}

	title := "Jojo's moments"
	publicURL := "HTTP://LOCALHOST:80/journal/"
	avatar := testAvatarID
	cover := testCoverID
	comments := false
	updated, err := service.Update(context.Background(), SiteUpdate{
		Title:           &title,
		PublicBaseURL:   &NullableString{Value: &publicURL},
		AvatarMediaID:   &NullableString{Value: &avatar},
		CoverMediaID:    &NullableString{Value: &cover},
		CommentsEnabled: &comments,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Title != title || updated.PublicBaseURL == nil || *updated.PublicBaseURL != "http://localhost/journal" ||
		updated.AvatarMediaID == nil || *updated.AvatarMediaID != avatar || updated.CoverMediaID == nil || *updated.CoverMediaID != cover || updated.CommentsEnabled {
		t.Fatalf("updated = %#v", updated)
	}

	cleared, err := service.Update(context.Background(), SiteUpdate{
		PublicBaseURL: &NullableString{Value: nil},
		AvatarMediaID: &NullableString{Value: nil},
		CoverMediaID:  &NullableString{Value: nil},
	})
	if err != nil {
		t.Fatal(err)
	}
	if cleared.PublicBaseURL != nil || cleared.AvatarMediaID != nil || cleared.CoverMediaID != nil || cleared.Title != title || cleared.CommentsEnabled {
		t.Fatalf("cleared = %#v", cleared)
	}
	wildcardLocalhost := "http://journal.localhost"
	if _, err := service.Update(context.Background(), SiteUpdate{
		PublicBaseURL: &NullableString{Value: &wildcardLocalhost},
	}); !domain.HasCode(err, domain.CodeInvalidArgument) {
		t.Fatalf("non-exact localhost HTTP error = %v", err)
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
		"long bio": {
			Bio: ptr(strings.Repeat("界", 501)),
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
	missingAvatar := "01ARZ3NDEKTSV4RRFFQ69G5FAZ"
	if _, err := service.Update(context.Background(), SiteUpdate{
		Title:         &changedTitle,
		AvatarMediaID: &NullableString{Value: &missingAvatar},
	}); !domain.HasCode(err, domain.CodeMediaNotFound) {
		t.Fatalf("missing avatar error = %v", err)
	}
	if !reflect.DeepEqual(*transactions.settings, initial) {
		t.Fatalf("missing avatar partially committed: %#v", transactions.settings)
	}
	if _, err := service.Update(context.Background(), SiteUpdate{
		Title:        &changedTitle,
		CoverMediaID: &NullableString{Value: &missingAvatar},
	}); !domain.HasCode(err, domain.CodeMediaNotFound) {
		t.Fatalf("missing cover error = %v", err)
	}
	if !reflect.DeepEqual(*transactions.settings, initial) {
		t.Fatalf("missing cover partially committed: %#v", transactions.settings)
	}

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
	bio := "A durable personal record"

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
		_, err := service.Update(context.Background(), SiteUpdate{Bio: &bio})
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
	if settings.Title != title || settings.Bio != bio {
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

	first, err := service.Pin(context.Background(), domain.ID(testPinnedPostID))
	if err != nil || first.PinnedPostID == nil || *first.PinnedPostID != testPinnedPostID {
		t.Fatalf("first pin = %#v, err=%v", first, err)
	}
	replayed, err := service.Pin(context.Background(), domain.ID(testPinnedPostID))
	if err != nil || !reflect.DeepEqual(first, replayed) {
		t.Fatalf("idempotent pin = %#v, err=%v", replayed, err)
	}
	replaced, err := service.Pin(context.Background(), domain.ID(testReplacementPostID))
	if err != nil || replaced.PinnedPostID == nil || *replaced.PinnedPostID != testReplacementPostID {
		t.Fatalf("replacement pin = %#v, err=%v", replaced, err)
	}
	if _, err := service.Pin(context.Background(), domain.ID("01ARZ3NDEKTSV4RRFFQ69G5FAX")); !domain.HasCode(err, domain.CodePostNotFound) {
		t.Fatalf("missing pin error = %v", err)
	}
	stored, err := service.Get(context.Background())
	if err != nil || stored.PinnedPostID == nil || *stored.PinnedPostID != testReplacementPostID {
		t.Fatalf("failed pin changed state = %#v, err=%v", stored, err)
	}
	cleared, err := service.Unpin(context.Background())
	if err != nil || cleared.PinnedPostID != nil {
		t.Fatalf("unpin = %#v, err=%v", cleared, err)
	}
	repeated, err := service.Unpin(context.Background())
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
func TestSiteHandleWritesPassTheGateTheAccountRouteUses(t *testing.T) {
	initial := SiteSettings{ProfileUserID: "0123456789abcdef0123456789abcdef", Username: "writer", DisplayName: "Moment", Title: "Moment", Timezone: "UTC", CommentsEnabled: true}
	transactions := &memorySiteTransactions{settings: &initial, media: map[string]bool{}}
	service := newTestSiteService(t, transactions, false)

	for _, handle := range []string{"search", "api", "login", "assets", "archive", "profile"} {
		t.Run(handle, func(t *testing.T) {
			if _, err := service.Update(context.Background(), SiteUpdate{Username: &handle}); !domain.HasCode(err, domain.CodeUsernameReserved) {
				t.Fatalf("reserved handle %q error = %v", handle, err)
			}
			if !reflect.DeepEqual(*transactions.settings, initial) {
				t.Fatalf("reserved handle changed settings: %#v", transactions.settings)
			}
		})
	}

	for name, handle := range map[string]string{
		"too short":          "ab",
		"leading digit":      "1cheems",
		"leading underscore": "_cheems",
		"illegal character":  "che-ems",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := service.Update(context.Background(), SiteUpdate{Username: &handle}); !domain.HasCode(err, domain.CodeInvalidArgument) {
				t.Fatalf("malformed handle %q error = %v", handle, err)
			}
		})
	}

	accepted := "  ITSCheems  "
	saved, err := service.Update(context.Background(), SiteUpdate{Username: &accepted})
	if err != nil || saved.Username != "itscheems" {
		t.Fatalf("accepted handle = %q, err = %v", saved.Username, err)
	}
}

// Profile text reaches users.display_name and users.bio from here too, so it
// meets the normalizers the account route meets.
//
// Emptiness and a rune count were the whole of it before, which let a display
// name carrying a newline into the column that CommentService stamps onto every
// comment the owner writes -- an account that saves fine and can never comment.
func TestSiteProfileTextIsNormalizedNotJustMeasured(t *testing.T) {
	initial := SiteSettings{ProfileUserID: "0123456789abcdef0123456789abcdef", Username: "writer", DisplayName: "Moment", Title: "Moment", Timezone: "UTC", CommentsEnabled: true}
	transactions := &memorySiteTransactions{settings: &initial, media: map[string]bool{}}
	service := newTestSiteService(t, transactions, false)

	for name, update := range map[string]SiteUpdate{
		"display name with a newline": {DisplayName: ptr("Che\nems")},
		"display name with a tab":     {DisplayName: ptr("Che\tems")},
		"display name of only space":  {DisplayName: ptr("   ")},
		"bio with a zero byte":        {Bio: ptr("\x00")},
		"bio with a carriage return":  {Bio: ptr("first\rsecond")},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := service.Update(context.Background(), update); !domain.HasCode(err, domain.CodeInvalidArgument) {
				t.Fatalf("error = %v", err)
			}
			if !reflect.DeepEqual(*transactions.settings, initial) {
				t.Fatalf("rejected text changed settings: %#v", transactions.settings)
			}
		})
	}

	// What the normalizers keep, they keep: surrounding whitespace goes, and a
	// bio holds the line breaks somebody typed into the textarea.
	saved, err := service.Update(context.Background(), SiteUpdate{
		DisplayName: ptr("  Cheems  "),
		Bio:         ptr("  first\nsecond  "),
	})
	if err != nil || saved.DisplayName != "Cheems" || saved.Bio != "first\nsecond" {
		t.Fatalf("normalized = %#v, err = %v", saved, err)
	}
}

// registration_mode was a field nothing could write.
//
// SiteUpdate declared it and Empty counted it, so a caller that set it ran the
// whole write transaction; applySiteUpdate then dropped it and the handler
// answered 200 having changed nothing. Every instance stayed on the migration
// default forever, with no way to close registration short of editing the
// database.
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
