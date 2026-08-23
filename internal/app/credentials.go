package app

import "github.com/moment/moment/internal/domain"

// OwnerPasswordSet reports whether this instance holds a password for its
// owner. It is a port because the answer lives in secrets.json, which the
// credential manager owns and this package deliberately does not import.
type OwnerPasswordSet func() (bool, error)

// SignInPolicy decides which credential kinds an account may present.
//
// This is the only place that decides it. A handler that compared a handle or a
// role to work out whether a password was worth checking would be answering the
// same question in a second voice, and the two would drift the first time an
// account other than the owner carried a credential of its own.
//
// Today exactly one account does. The owner's password and token live outside
// the database so that signing in still works when the database is what needs
// repairing, and nothing else has anywhere to keep a secret. That is a fact
// about where credentials are stored, not a rule about who deserves one: when
// accounts carry their own, this type is what learns it, and every refusal
// above it keeps its current shape.
type SignInPolicy struct {
	ownerPasswordSet OwnerPasswordSet
}

// NewSignInPolicy builds the policy over the credential store's answer.
func NewSignInPolicy(ownerPasswordSet OwnerPasswordSet) (*SignInPolicy, error) {
	if ownerPasswordSet == nil {
		return nil, domain.NewError(domain.CodeInvalidArgument, "a sign-in policy needs a credential source", nil)
	}
	return &SignInPolicy{ownerPasswordSet: ownerPasswordSet}, nil
}

// MethodsFor reports the credential kinds an account can present.
//
// Empty rather than nil, all the way through: this set is reported to a client
// inside a refusal, and a nil slice serialises as null, which would make
// "carries nothing" a second shape every reader has to handle.
func (policy *SignInPolicy) MethodsFor(user domain.User) ([]domain.CredentialKind, error) {
	if !user.IsOwner() {
		return []domain.CredentialKind{}, nil
	}
	passwordSet, err := policy.ownerPasswordSet()
	if err != nil {
		return []domain.CredentialKind{}, err
	}
	// The token always exists; it is issued at initialization and can only be
	// rotated, never removed. A password is optional and may not be set yet.
	methods := make([]domain.CredentialKind, 0, 2)
	if passwordSet {
		methods = append(methods, domain.CredentialPassword)
	}
	methods = append(methods, domain.CredentialToken)
	return methods, nil
}

// Permits reports whether an account may present a kind.
func (policy *SignInPolicy) Permits(user domain.User, kind domain.CredentialKind) (bool, error) {
	methods, err := policy.MethodsFor(user)
	if err != nil {
		return false, err
	}
	return domain.CredentialKindsContain(methods, kind), nil
}
