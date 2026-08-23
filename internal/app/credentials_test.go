package app

import (
	"errors"
	"testing"

	"github.com/moment/moment/internal/domain"
)

func ownerUser() domain.User {
	return domain.User{ID: "0123456789abcdef0123456789abcdef", Username: "owner", Role: domain.UserRoleOwner}
}

func memberUser() domain.User {
	return domain.User{ID: "fedcba9876543210fedcba9876543210", Username: "someone", Role: domain.UserRoleMember}
}

// Which credential kinds an account may present is one decision, and this is
// where it is made. A handler that worked it out from a role would be answering
// the same question in a second voice, and the two would drift the first time
// an account other than the owner carried a credential of its own.
func TestSignInPolicyAnswersFromWhereCredentialsAreKept(t *testing.T) {
	for name, testCase := range map[string]struct {
		passwordSet bool
		user        domain.User
		want        []domain.CredentialKind
	}{
		"an owner with a password may use either": {
			passwordSet: true, user: ownerUser(),
			want: []domain.CredentialKind{domain.CredentialPassword, domain.CredentialToken},
		},
		// The token is issued at initialization and can only be rotated, so it
		// is the one credential that always exists.
		"an owner with no password still has the token": {
			passwordSet: false, user: ownerUser(),
			want: []domain.CredentialKind{domain.CredentialToken},
		},
		// Not a statement about rank: nothing but the owner has anywhere to
		// keep a secret yet, and this is the function that learns otherwise.
		"an account with nowhere to keep a secret presents nothing": {
			passwordSet: true, user: memberUser(),
			want: []domain.CredentialKind{},
		},
	} {
		t.Run(name, func(t *testing.T) {
			policy, err := NewSignInPolicy(func() (bool, error) { return testCase.passwordSet, nil })
			if err != nil {
				t.Fatal(err)
			}
			methods, err := policy.MethodsFor(testCase.user)
			if err != nil {
				t.Fatal(err)
			}
			if len(methods) != len(testCase.want) {
				t.Fatalf("methods = %v, want %v", methods, testCase.want)
			}
			for index, kind := range testCase.want {
				if methods[index] != kind {
					t.Fatalf("methods = %v, want %v", methods, testCase.want)
				}
			}
			// Empty rather than nil: this set is reported inside a refusal, and
			// a nil slice serialises as null, which would make "carries
			// nothing" a second shape every reader has to handle.
			if methods == nil {
				t.Fatal("methods is nil, which reaches a client as null")
			}
		})
	}
}

func TestSignInPolicyRefusesToGuessWhenTheCredentialStoreCannotAnswer(t *testing.T) {
	failure := errors.New("secrets unreadable")
	policy, err := NewSignInPolicy(func() (bool, error) { return false, failure })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := policy.MethodsFor(ownerUser()); !errors.Is(err, failure) {
		t.Fatalf("error = %v, want the store's own failure", err)
	}
	// A member's answer needs no store at all, so an unreadable store must not
	// turn "presents nothing" into an error somebody has to interpret.
	methods, err := policy.MethodsFor(memberUser())
	if err != nil {
		t.Fatalf("member lookup consulted the store: %v", err)
	}
	if len(methods) != 0 {
		t.Fatalf("methods = %v, want none", methods)
	}
}

func TestSignInPolicyNeedsACredentialSource(t *testing.T) {
	if _, err := NewSignInPolicy(nil); err == nil {
		t.Fatal("a policy with no credential source was accepted")
	}
}
