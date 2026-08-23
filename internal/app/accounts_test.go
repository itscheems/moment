package app

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/linkfetch"
)

// A fold that is one-way about the address and two-way about what was proven
// about it is not one-way.
//
// GitHub answers /user/emails with 403 whenever the token was issued without
// the address scope or the call was rate limited, and the adapter then falls
// back to the public profile address, which nothing has verified. Copying that
// answer over the flags turned a single bad minute into a permanently
// unverified address -- the exact erasure this fold exists to prevent, arriving
// through the fields beside the one it was guarding.
func TestRefreshedIdentityNeverDemotesAKnownAddress(t *testing.T) {
	address := func(value string) *string { return &value }
	stored := domain.Identity{
		Provider:         "github",
		ProviderUserID:   "216108319",
		UserID:           "0123456789abcdef0123456789abcdef",
		Email:            address("visitor@example.test"),
		EmailVerified:    true,
		EmailDeliverable: true,
		CreatedAtMS:      1,
		LastLoginAtMS:    1,
	}

	for name, probe := range map[string]struct {
		stored          domain.Identity
		external        ExternalIdentity
		wantEmail       string
		wantVerified    bool
		wantDeliverable bool
	}{
		"a rate limited read falls back to the same address and keeps what was proven": {
			stored:          stored,
			external:        ExternalIdentity{Email: "visitor@example.test"},
			wantEmail:       "visitor@example.test",
			wantVerified:    true,
			wantDeliverable: true,
		},
		"the same address in another casing is the same address": {
			stored:          stored,
			external:        ExternalIdentity{Email: "Visitor@Example.test"},
			wantEmail:       "Visitor@Example.test",
			wantVerified:    true,
			wantDeliverable: true,
		},
		"a different address carries none of the old answer with it": {
			stored:          stored,
			external:        ExternalIdentity{Email: "someone-else@example.test"},
			wantEmail:       "someone-else@example.test",
			wantVerified:    false,
			wantDeliverable: false,
		},
		"a later read still repairs an address that was never verified": {
			stored: domain.Identity{
				Email: address("visitor@example.test"), EmailVerified: false, EmailDeliverable: false,
			},
			external:        ExternalIdentity{Email: "visitor@example.test", EmailVerified: true, EmailDeliverable: true},
			wantEmail:       "visitor@example.test",
			wantVerified:    true,
			wantDeliverable: true,
		},
		"no address at all leaves the stored one alone": {
			stored:          stored,
			external:        ExternalIdentity{},
			wantEmail:       "visitor@example.test",
			wantVerified:    true,
			wantDeliverable: true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			refreshed := refreshedIdentity(probe.stored, probe.external, 99)
			if refreshed.Email == nil || *refreshed.Email != probe.wantEmail {
				t.Fatalf("email = %v, want %q", refreshed.Email, probe.wantEmail)
			}
			if refreshed.EmailVerified != probe.wantVerified || refreshed.EmailDeliverable != probe.wantDeliverable {
				t.Fatalf("verified = %v, deliverable = %v, want %v and %v",
					refreshed.EmailVerified, refreshed.EmailDeliverable, probe.wantVerified, probe.wantDeliverable)
			}
			if refreshed.LastLoginAtMS != 99 {
				t.Fatalf("last login = %d, want the sign-in to be recorded", refreshed.LastLoginAtMS)
			}
		})
	}
}

// unwrittenAccounts is enough of the port for a capture that never reaches the
// profile write.
type unwrittenAccounts struct{}

var errAccountWriteReached = errors.New("the profile write was reached")

func (unwrittenAccounts) ReadAccounts(context.Context, func(AccountStore, SessionStore) error) error {
	return errAccountWriteReached
}

func (unwrittenAccounts) WriteAccounts(context.Context, func(AccountStore, SessionStore) error) error {
	return errAccountWriteReached
}

type recordingAvatarSource struct{ ctx context.Context }

func (source *recordingAvatarSource) Get(ctx context.Context, _ string, _ []string, _ int64) (linkfetch.Response, error) {
	source.ctx = ctx
	return linkfetch.Response{ContentType: "image/png", Body: []byte("not really a png")}, nil
}

type recordingAvatarStore struct{ ctx context.Context }

func (store *recordingAvatarStore) Ingest(ctx context.Context, _ string, _ io.Reader) (domain.ID, string, uint32, uint32, error) {
	store.ctx = ctx
	id, err := domain.ParseID("01ARZ3NDEKTSV4RRFFQ69G5FAY")
	return id, "image/png", 64, 64, err
}

// The download's budget is not the write's budget.
//
// Three seconds bounds how long somebody signing in waits for a provider's
// image host, which is a statement about a network nobody here controls.
// Charging the local ingest to whatever is left of it makes a picture fail for
// having been slow to arrive rather than for anything about the picture, and
// the closer the download comes to the ceiling the likelier that is.
func TestCaptureAvatarBoundsTheDownloadWithoutBoundingTheWrite(t *testing.T) {
	source := &recordingAvatarSource{}
	store := &recordingAvatarStore{}
	service, err := NewAccountService(unwrittenAccounts{}, WithAvatarCapture(source, store))
	if err != nil {
		t.Fatal(err)
	}

	caller, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()
	err = service.CaptureAvatar(caller, "0123456789abcdef0123456789abcdef", "https://avatars.githubusercontent.com/u/1?v=4")
	if !errors.Is(err, errAccountWriteReached) {
		t.Fatalf("capture = %v, want it to have reached the profile write", err)
	}

	fetchDeadline, fetched := source.ctx.Deadline()
	if !fetched {
		t.Fatal("the download ran with no budget at all")
	}
	if until := time.Until(fetchDeadline); until <= 0 || until > avatarCaptureBudget {
		t.Fatalf("download budget = %v, want at most %v", until, avatarCaptureBudget)
	}

	callerDeadline, _ := caller.Deadline()
	ingestDeadline, bounded := store.ctx.Deadline()
	if !bounded || !ingestDeadline.Equal(callerDeadline) {
		t.Fatalf("ingest deadline = %v (bounded = %v), want the caller's %v",
			ingestDeadline, bounded, callerDeadline)
	}
}
