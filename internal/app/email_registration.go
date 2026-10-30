package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"math/big"
	"net/mail"
	"strings"
	"time"

	"github.com/moment/moment/internal/domain"
)

const (
	CodeEmailUnavailable    domain.Code = "EMAIL_UNAVAILABLE"
	CodeEmailTaken          domain.Code = "EMAIL_TAKEN"
	CodeVerificationInvalid domain.Code = "VERIFICATION_INVALID"
	CodeVerificationExpired domain.Code = "VERIFICATION_EXPIRED"
)

// VerificationSender delivers one immutable challenge; channels never mint codes.
type VerificationSender interface {
	SendVerification(context.Context, string, string, string) error
}

func WithVerificationSender(sender VerificationSender) AccountOption {
	return func(service *AccountService) { service.verificationSender = sender }
}

// RegistrationChallenge is short-lived operational state. Only digests persist.
type RegistrationChallenge struct {
	ID, Email, RequesterHash   string
	CodeHash, ProofHash        []byte
	RequestedAtMS, ExpiresAtMS int64
	Attempts                   int
	Verified, Consumed         bool
}

type EmailRegistrationStore interface {
	InsertRegistrationChallenge(context.Context, RegistrationChallenge) error
	RegistrationChallenge(context.Context, string, bool) (RegistrationChallenge, bool, error)
	SaveRegistrationChallenge(context.Context, RegistrationChallenge) error
	RegistrationRequestBudget(context.Context, string, string, int64) (int, int, int64, error)
}

func emailStore(accounts AccountStore) (EmailRegistrationStore, error) {
	store, ok := accounts.(EmailRegistrationStore)
	if !ok {
		return nil, domain.NewError(CodeEmailUnavailable, "email registration is unavailable", nil)
	}
	return store, nil
}

func NormalizeEmail(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	parsed, err := mail.ParseAddress(value)
	if err != nil || parsed.Address != value || len(value) > 254 || !strings.Contains(strings.SplitN(value, "@", 2)[len(strings.SplitN(value, "@", 2))-1], ".") || strings.ContainsAny(value, "\r\n\x00") {
		return "", domain.NewError(domain.CodeInvalidArgument, "a valid email address is required", map[string]any{"field": "email"})
	}
	return value, nil
}

func (service *AccountService) CheckRegistrationAccount(ctx context.Context, username, password string) (string, error) {
	username, err := domain.NormalizeUsername(username)
	if err != nil {
		return "", err
	}
	if err = validateAccountPassword(password); err != nil {
		return "", err
	}
	err = service.transactions.ReadAccounts(ctx, func(accounts AccountStore, _ SessionStore) error {
		mode, err := accounts.RegistrationMode(ctx)
		if err != nil {
			return err
		}
		if mode == domain.RegistrationClosed {
			return domain.NewError(domain.CodeRegistrationClosed, "registration is closed", nil)
		}
		available, err := accounts.UsernameAvailable(ctx, username)
		if err != nil {
			return err
		}
		if !available {
			return domain.NewError(domain.CodeUsernameTaken, "username is already in use", map[string]any{"field": "username"})
		}
		return nil
	})
	return username, err
}

type EmailChallengeReceipt struct {
	ChallengeID string    `json:"challenge_id"`
	ExpiresAt   time.Time `json:"expires_at"`
	ResendAt    time.Time `json:"resend_at"`
}

type VerifiedEmailReceipt struct {
	Token     string    `json:"verification_token"`
	Email     string    `json:"email"`
	ExpiresAt time.Time `json:"expires_at"`
}

func verificationSecret() (string, error) {
	var bytes [32]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes[:]), nil
}
func verificationDigest(value string) []byte {
	digest := sha256.Sum256([]byte(value))
	return digest[:]
}

func (service *AccountService) RequestRegistrationEmail(ctx context.Context, address, requester string) (EmailChallengeReceipt, error) {
	email, err := NormalizeEmail(address)
	if err != nil {
		return EmailChallengeReceipt{}, err
	}
	if service.verificationSender == nil {
		return EmailChallengeReceipt{}, domain.NewError(CodeEmailUnavailable, "email delivery is not configured", nil)
	}
	id, err := verificationSecret()
	if err != nil {
		return EmailChallengeReceipt{}, err
	}
	number, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return EmailChallengeReceipt{}, err
	}
	code := fmt.Sprintf("%06d", number.Int64())
	now := service.clock().UTC()
	expires := now.Add(10 * time.Minute)
	requesterHash := hex.EncodeToString(verificationDigest(requester))
	challenge := RegistrationChallenge{ID: id, Email: email, CodeHash: verificationDigest(id + ":" + code), RequestedAtMS: now.UnixMilli(), ExpiresAtMS: expires.UnixMilli(), RequesterHash: requesterHash}
	err = service.transactions.WriteAccounts(ctx, func(accounts AccountStore, _ SessionStore) error {
		mode, err := accounts.RegistrationMode(ctx)
		if err != nil {
			return err
		}
		if mode == domain.RegistrationClosed {
			return domain.NewError(domain.CodeRegistrationClosed, "registration is closed", nil)
		}
		if _, found, err := accounts.IdentityLookup(ctx, "email", email); err != nil {
			return err
		} else if found {
			return domain.NewError(CodeEmailTaken, "email is already registered", map[string]any{"field": "email"})
		}
		store, err := emailStore(accounts)
		if err != nil {
			return err
		}
		emailCount, requesterCount, last, err := store.RegistrationRequestBudget(ctx, email, requesterHash, now.Add(-time.Hour).UnixMilli())
		if err != nil {
			return err
		}
		if emailCount >= 5 || requesterCount >= 20 || last > now.Add(-time.Minute).UnixMilli() {
			retry := int64(60)
			if emailCount >= 5 || requesterCount >= 20 {
				retry = 3600
			}
			return domain.NewError(domain.CodeRateLimited, "wait before requesting another code", map[string]any{"retry_after_seconds": retry})
		}
		return store.InsertRegistrationChallenge(ctx, challenge)
	})
	if err != nil {
		return EmailChallengeReceipt{}, err
	}
	if err = service.verificationSender.SendVerification(ctx, email, code, id); err != nil {
		return EmailChallengeReceipt{}, domain.NewError(CodeEmailUnavailable, "email delivery could not be confirmed; retry later", nil)
	}
	return EmailChallengeReceipt{ChallengeID: id, ExpiresAt: expires, ResendAt: now.Add(time.Minute)}, nil
}

func (service *AccountService) VerifyRegistrationEmail(ctx context.Context, id, code string) (VerifiedEmailReceipt, error) {
	if len(id) != 64 || len(code) != 6 || strings.IndexFunc(code, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
		return VerifiedEmailReceipt{}, domain.NewError(CodeVerificationInvalid, "verification code is invalid", map[string]any{"field": "code"})
	}
	proof, err := verificationSecret()
	if err != nil {
		return VerifiedEmailReceipt{}, err
	}
	now := service.clock().UnixMilli()
	var refusal error
	var receipt VerifiedEmailReceipt
	err = service.transactions.WriteAccounts(ctx, func(accounts AccountStore, _ SessionStore) error {
		store, err := emailStore(accounts)
		if err != nil {
			return err
		}
		challenge, found, err := store.RegistrationChallenge(ctx, id, false)
		if err != nil {
			return err
		}
		if !found || challenge.Consumed || challenge.Attempts >= 5 {
			refusal = domain.NewError(CodeVerificationInvalid, "verification code is invalid or exhausted", map[string]any{"field": "code"})
			return nil
		}
		if challenge.ExpiresAtMS <= now {
			refusal = domain.NewError(CodeVerificationExpired, "verification has expired", map[string]any{"field": "code"})
			return nil
		}
		if subtle.ConstantTimeCompare(challenge.CodeHash, verificationDigest(id+":"+code)) != 1 {
			challenge.Attempts++
			refusal = domain.NewError(CodeVerificationInvalid, "verification code is incorrect", map[string]any{"field": "code"})
			return store.SaveRegistrationChallenge(ctx, challenge)
		}
		challenge.Verified = true
		challenge.ProofHash = verificationDigest(proof)
		if err = store.SaveRegistrationChallenge(ctx, challenge); err != nil {
			return err
		}
		receipt = VerifiedEmailReceipt{Token: proof, Email: challenge.Email, ExpiresAt: time.UnixMilli(challenge.ExpiresAtMS).UTC()}
		return nil
	})
	if err != nil {
		return VerifiedEmailReceipt{}, err
	}
	return receipt, refusal
}

func (service *AccountService) RegisterEmailAccount(ctx context.Context, username, password, name, proof string) (domain.User, error) {
	if len(proof) != 64 {
		return domain.User{}, domain.NewError(CodeVerificationInvalid, "a verified email is required", map[string]any{"field": "email"})
	}
	return service.createPasswordAccount(ctx, username, password, strings.TrimSpace(name), func(accounts AccountStore, user domain.User) error {
		store, err := emailStore(accounts)
		if err != nil {
			return err
		}
		challenge, found, err := store.RegistrationChallenge(ctx, hex.EncodeToString(verificationDigest(proof)), true)
		if err != nil {
			return err
		}
		if !found || !challenge.Verified || challenge.Consumed {
			return domain.NewError(CodeVerificationInvalid, "a verified email is required", map[string]any{"field": "email"})
		}
		if challenge.ExpiresAtMS <= service.clock().UnixMilli() {
			return domain.NewError(CodeVerificationExpired, "verification has expired", map[string]any{"field": "email"})
		}
		if _, found, err := accounts.IdentityLookup(ctx, "email", challenge.Email); err != nil {
			return err
		} else if found {
			return domain.NewError(CodeEmailTaken, "email is already registered", map[string]any{"field": "email"})
		}
		email := challenge.Email
		if err = accounts.LinkIdentity(ctx, domain.Identity{Provider: "email", ProviderUserID: email, UserID: user.ID, Email: &email, EmailVerified: true, EmailDeliverable: true, CreatedAtMS: user.CreatedAtMS, LastLoginAtMS: user.CreatedAtMS}); err != nil {
			return err
		}
		challenge.Consumed = true
		return store.SaveRegistrationChallenge(ctx, challenge)
	})
}
