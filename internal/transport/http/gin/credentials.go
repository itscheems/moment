package ginhttp

import (
	"context"

	"github.com/moment/moment/internal/auth"
	"github.com/moment/moment/internal/domain"
)

// SignInMethods reports the credential kinds an account can present.
//
// This is the only place that decides it. A handler that compared a handle or a
// role to work out whether a password was worth checking would be answering the
// same question in a second voice, and the two would drift the first time an
// account other than the owner carried a credential of its own.
//
// Today exactly one account does. The owner's password and token live in
// secrets.json rather than in the database, so that signing in still works when
// the database is what needs repairing, and nothing else has anywhere to keep a
// secret. That is a fact about where credentials are stored, not a rule about
// who deserves one: when accounts carry their own, this function is what learns
// it, and every refusal above it keeps its current shape.
func (gate *Gate) SignInMethods(_ context.Context, user domain.User) ([]domain.CredentialKind, error) {
	// Empty rather than nil, all the way through: this set is reported to a
	// client inside a refusal, and a nil slice serialises as null, which would
	// make "carries nothing" a second shape every reader has to handle.
	if !user.IsOwner() {
		return []domain.CredentialKind{}, nil
	}
	status, err := gate.manager.Status()
	if err != nil {
		return []domain.CredentialKind{}, err
	}
	// The token always exists; it is issued at initialization and can only be
	// rotated, never removed. A password is optional and may not be set yet.
	methods := make([]domain.CredentialKind, 0, 2)
	if status.PasswordEnabled {
		methods = append(methods, domain.CredentialPassword)
	}
	methods = append(methods, domain.CredentialToken)
	return methods, nil
}

// IssueSignIn verifies a secret against the account it is offered for and
// returns the session it earns.
//
// The account is passed rather than looked up because the caller has already
// resolved who is signing in, and because the verification below is only
// correct for one account today: the manager checks secrets.json, which belongs
// to the owner and names nobody. SignInMethods is what keeps any other account
// from reaching that check, and the guard here is what keeps a future change to
// SignInMethods from quietly turning every account's password into the owner's.
func (gate *Gate) IssueSignIn(
	ctx context.Context,
	user domain.User,
	kind domain.CredentialKind,
	secret string,
	subject auth.Subject,
) (string, auth.Session, error) {
	methods, err := gate.SignInMethods(ctx, user)
	if err != nil {
		return "", auth.Session{}, err
	}
	if !domain.CredentialKindsContain(methods, kind) {
		return "", auth.Session{}, domain.NewError(domain.CodeCredentialUnavailable,
			"that account does not sign in that way here",
			map[string]any{"available": methods})
	}
	if !user.IsOwner() {
		// Unreachable while SignInMethods answers nothing for a member, and
		// deliberately loud rather than defaulting to the owner's secret if it
		// ever stops answering nothing.
		return "", auth.Session{}, domain.NewError(domain.CodeCredentialUnavailable,
			"that account does not sign in that way here",
			map[string]any{"available": []domain.CredentialKind{}})
	}
	switch kind {
	case domain.CredentialPassword:
		return gate.manager.IssuePasswordSession(secret, subject)
	case domain.CredentialToken:
		return gate.manager.IssueSession(secret, subject)
	default:
		return "", auth.Session{}, domain.NewError(domain.CodeCredentialUnavailable,
			"that is not a way to sign in", map[string]any{"available": methods})
	}
}
