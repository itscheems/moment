package ginhttp

import (
	"context"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/auth"
	"github.com/moment/moment/internal/domain"
)

// signInPolicy answers which credential kinds an account may present.
//
// The decision itself belongs to app.SignInPolicy: transport packages map
// syntax and DTOs and do not own business decisions (docs/architecture.md).
// What lives here is the wiring, because the answer is kept in secrets.json,
// which the credential manager owns and the app package deliberately does not
// import.
func (gate *Gate) signInPolicy() (*app.SignInPolicy, error) {
	return app.NewSignInPolicy(func() (bool, error) {
		status, err := gate.manager.Status()
		if err != nil {
			return false, err
		}
		return status.PasswordEnabled, nil
	})
}

// SignInMethods reports the credential kinds an account can present.
func (gate *Gate) SignInMethods(_ context.Context, user domain.User) ([]domain.CredentialKind, error) {
	policy, err := gate.signInPolicy()
	if err != nil {
		return []domain.CredentialKind{}, err
	}
	return policy.MethodsFor(user)
}

// IssueSignIn verifies a secret against the account it is offered for and
// returns the session it earns.
//
// The account is passed rather than looked up because the caller has already
// resolved who is signing in, and because the verification below is only
// correct for one account today: the manager checks secrets.json, which belongs
// to the owner and names nobody. The policy is what keeps any other account
// from reaching that check, and the guard here is what keeps a future change to
// the policy from quietly turning every account's password into the owner's.
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
		// Unreachable while the policy answers nothing for a member, and
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
