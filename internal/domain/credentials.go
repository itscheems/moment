package domain

// CredentialKind is a way of proving an account is yours.
//
// Third-party providers are deliberately absent. A provider round trip proves
// the same thing, but it is a redirect rather than a secret typed into a form,
// and the routes that carry it answer separately -- folding it in here would
// invite a caller to POST "provider" as though it were a password.
type CredentialKind string

const (
	// CredentialPassword is a secret the person chose.
	CredentialPassword CredentialKind = "password"
	// CredentialToken is a secret this instance generated. It is the credential
	// that has to keep working when the database does not, which is why the
	// owner's copy lives outside it.
	CredentialToken CredentialKind = "token"
)

// ValidCredentialKind reports whether a value names a credential kind.
func ValidCredentialKind(value CredentialKind) bool {
	switch value {
	case CredentialPassword, CredentialToken:
		return true
	default:
		return false
	}
}

// CredentialKindsContain reports whether a set of kinds includes one.
func CredentialKindsContain(kinds []CredentialKind, wanted CredentialKind) bool {
	for _, kind := range kinds {
		if kind == wanted {
			return true
		}
	}
	return false
}
