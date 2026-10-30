package sqlite

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
)

type capturedVerification struct {
	code string
	fail bool
}

func (sender *capturedVerification) SendVerification(_ context.Context, _, code, _ string) error {
	sender.code = code
	if sender.fail {
		return errors.New("provider unavailable")
	}
	return nil
}

func emailRegistrationFixture(t *testing.T) (*Database, *app.AccountService, *capturedVerification, *time.Time, *AccountRepository) {
	t.Helper()
	db, runtime := openTestDatabase(t, Options{})
	repository, err := NewAccountRepository(db, runtime.Locks)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 30, 1, 0, 0, 0, time.UTC)
	sender := &capturedVerification{}
	service, err := app.NewAccountService(repository, app.WithVerificationSender(sender), app.WithAccountClock(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	return db, service, sender, &now, repository
}
func emailProof(t *testing.T, service *app.AccountService, sender *capturedVerification, email string) app.VerifiedEmailReceipt {
	t.Helper()
	ctx := context.Background()
	receipt, err := service.RequestRegistrationEmail(ctx, email, "requester")
	if err != nil {
		t.Fatal(err)
	}
	verified, err := service.VerifyRegistrationEmail(ctx, receipt.ChallengeID, sender.code)
	if err != nil {
		t.Fatal(err)
	}
	return verified
}

func TestEmailRegistrationTransactionAndPasswordLogin(t *testing.T) {
	db, service, sender, _, _ := emailRegistrationFixture(t)
	ctx := context.Background()
	proof := emailProof(t, service, sender, "Reader@Example.test")
	// The username can change after verification; no account exists until finish.
	assertCount(t, db.read, `SELECT count(*) FROM users`, 1)
	user, err := service.RegisterEmailAccount(ctx, "reader", "password123", "林间", proof.Token)
	if err != nil {
		t.Fatal(err)
	}
	if user.DisplayName != "林间" || user.Role != domain.UserRoleMember || user.Status != domain.AccountPending {
		t.Fatalf("account=%+v", user)
	}
	verified, err := service.VerifyPasswordCredential(ctx, " READER@example.test ", "password123")
	if err != nil || verified.User.ID != user.ID {
		t.Fatalf("email login=%+v %v", verified, err)
	}
	if _, err = service.VerifyPassword(ctx, "reader@example.test", "wrong-password"); !domain.HasCode(err, domain.CodeUnauthorized) {
		t.Fatalf("wrong password=%v", err)
	}
	if _, err = service.RegisterEmailAccount(ctx, "another", "password123", "", proof.Token); !domain.HasCode(err, app.CodeVerificationInvalid) {
		t.Fatalf("proof reuse=%v", err)
	}
	assertCount(t, db.read, `SELECT count(*) FROM users WHERE username='another'`, 0)
	if _, err = service.RequestRegistrationEmail(ctx, "reader@example.test", "requester"); !domain.HasCode(err, app.CodeEmailTaken) {
		t.Fatalf("duplicate email=%v", err)
	}
	if err := service.UnlinkIdentity(ctx, user, "email"); !domain.HasCode(err, domain.CodeInvalidArgument) {
		t.Fatalf("local email exposed through generic unlink: %v", err)
	}
	// The verified email uses the existing portable identity representation.
	snapshot, err := db.BeginArchiveSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = snapshot.Close() }()
	found := false
	err = snapshot.Identities(ctx, func(identity ArchiveIdentity) error {
		if identity.Provider == "email" && identity.UserID == user.ID {
			found = identity.ProviderUserID == "reader@example.test" && identity.EmailVerified
		}
		return nil
	})
	if err != nil || !found {
		t.Fatalf("portable email identity=%v %v", found, err)
	}
}

func TestEmailVerificationAttemptsExpiryResendAndRollback(t *testing.T) {
	db, service, sender, now, _ := emailRegistrationFixture(t)
	ctx := context.Background()
	receipt, err := service.RequestRegistrationEmail(ctx, "reader@example.test", "requester")
	if err != nil {
		t.Fatal(err)
	}
	correct := sender.code
	wrong := "000000"
	if correct == wrong {
		wrong = "111111"
	}
	for i := 0; i < 5; i++ {
		if _, err = service.VerifyRegistrationEmail(ctx, receipt.ChallengeID, wrong); !domain.HasCode(err, app.CodeVerificationInvalid) {
			t.Fatal(err)
		}
	}
	if _, err = service.VerifyRegistrationEmail(ctx, receipt.ChallengeID, correct); !domain.HasCode(err, app.CodeVerificationInvalid) {
		t.Fatalf("exhausted attempts=%v", err)
	}
	if _, err = service.RequestRegistrationEmail(ctx, "reader@example.test", "requester"); !domain.HasCode(err, domain.CodeRateLimited) {
		t.Fatalf("resend limit=%v", err)
	}
	*now = now.Add(61 * time.Second)
	receipt, err = service.RequestRegistrationEmail(ctx, "reader@example.test", "requester")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.VerifyRegistrationEmail(ctx, receipt.ChallengeID, sender.code); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(11 * time.Minute)
	if _, err = service.VerifyRegistrationEmail(ctx, receipt.ChallengeID, sender.code); !domain.HasCode(err, app.CodeVerificationExpired) {
		t.Fatalf("expired=%v", err)
	}
	proof := emailProof(t, service, sender, "second@example.test")
	// Occupied username must not consume a valid proof or leave a partial user.
	if _, err = service.RegisterEmailAccount(ctx, "writer", "password123", "", proof.Token); !domain.HasCode(err, domain.CodeUsernameTaken) {
		t.Fatalf("conflict=%v", err)
	}
	user, err := service.RegisterEmailAccount(ctx, "reader", "password123", "", proof.Token)
	if err != nil || user.DisplayName != "reader" {
		t.Fatalf("retry=%+v %v", user, err)
	}
	assertCount(t, db.read, `SELECT count(*) FROM users WHERE username='reader'`, 1)
}

func TestEmailDeliveryLimitsPersistAndReserveConcurrently(t *testing.T) {
	_, _, _, now, repository := emailRegistrationFixture(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	results := make(chan bool, 8)
	for range 8 {
		wg.Go(func() {
			ok, err := repository.ReserveEmailBudget(ctx, "resend", 3, 4, *now)
			if err != nil {
				t.Error(err)
			}
			results <- ok
		})
	}
	wg.Wait()
	close(results)
	allowed := 0
	for ok := range results {
		if ok {
			allowed++
		}
	}
	if allowed != 3 {
		t.Fatalf("reserved=%d", allowed)
	}
	tomorrow := now.Add(24 * time.Hour)
	ok, err := repository.ReserveEmailBudget(ctx, "resend", 3, 4, tomorrow)
	if err != nil || !ok {
		t.Fatalf("new day=%v %v", ok, err)
	}
	ok, err = repository.ReserveEmailBudget(ctx, "resend", 3, 4, tomorrow)
	if err != nil || ok {
		t.Fatalf("monthly cap=%v %v", ok, err)
	}
}

func TestEmailRegistrationPolicyAndProviderFailure(t *testing.T) {
	db, service, sender, _, _ := emailRegistrationFixture(t)
	ctx := context.Background()
	sender.fail = true
	if _, err := service.RequestRegistrationEmail(ctx, "reader@example.test", "requester"); !domain.HasCode(err, app.CodeEmailUnavailable) {
		t.Fatalf("provider failure=%v", err)
	}
	assertCount(t, db.read, `SELECT count(*) FROM users`, 1)
	for _, email := range []string{"reader", "Reader <reader@example.test>", "reader@localhost", strings.Repeat("a", 255) + "@example.test"} {
		if _, err := app.NormalizeEmail(email); !domain.HasCode(err, domain.CodeInvalidArgument) {
			t.Fatalf("invalid email accepted: %s", email)
		}
	}
	setRegistrationMode(t, db, domain.RegistrationClosed)
	if _, err := service.RequestRegistrationEmail(ctx, "other@example.test", "requester"); !domain.HasCode(err, domain.CodeRegistrationClosed) {
		t.Fatalf("closed=%v", err)
	}
}

func TestProviderQuotaRefusalBlocksOnlyTheAffectedPeriod(t *testing.T) {
	_, _, _, now, repository := emailRegistrationFixture(t)
	ctx := context.Background()
	if err := repository.ExhaustEmailBudget(ctx, "resend", now.UTC().Format("2006-01-02"), 100); err != nil {
		t.Fatal(err)
	}
	allowed, err := repository.ReserveEmailBudget(ctx, "resend", 100, 3000, *now)
	if err != nil || allowed {
		t.Fatalf("exhausted provider still selected: %v %v", allowed, err)
	}
	allowed, err = repository.ReserveEmailBudget(ctx, "brevo", 300, 0, *now)
	if err != nil || !allowed {
		t.Fatalf("backup budget unavailable: %v %v", allowed, err)
	}
	allowed, err = repository.ReserveEmailBudget(ctx, "resend", 100, 3000, now.Add(24*time.Hour))
	if err != nil || !allowed {
		t.Fatalf("daily budget did not reset: %v %v", allowed, err)
	}
}
