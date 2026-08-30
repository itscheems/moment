package ginhttp

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	gin "github.com/gin-gonic/gin"
	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/auth"
	"github.com/moment/moment/internal/domain"
)

const (
	accountKey        = "moment.account"
	accountSessionKey = "moment.account_session"
)

// Gate turns a credential into the account behind it, and decides what that
// account is allowed to reach.
//
// There are three admission levels and they are strictly nested, each adding a
// single test to the one below it. Keeping them here, as three methods over one
// resolver, is the whole point: an authorisation rule that appears in more than
// one place is a rule that will eventually disagree with itself, and the way
// that shows up is a route that forgot to apply it.
//
//	RequireAccount        signed in, not suspended            -- read yourself, edit yourself
//	RequireActiveAccount  + approved                          -- write things others see
//	RequireOwner          + owns the site                     -- everything else
//
// VerifiedAccount is not a fourth level. It is the same resolution the three
// share, handed to a route that has to stay open to a caller with no session
// at all, so that route can still learn who is signed in without asking the
// cookie itself.
//
// No handler is permitted to inspect role or status for itself. If a new rule
// is needed it belongs in this file.
type Gate struct {
	manager  *auth.Manager
	accounts *app.AccountService
	now      func() time.Time
}

// NewGate binds the gate to the credential manager and the account service.
func NewGate(manager *auth.Manager, accounts *app.AccountService) *Gate {
	if manager == nil || accounts == nil {
		return nil
	}
	return &Gate{manager: manager, accounts: accounts, now: time.Now}
}

// RequireAccount admits any signed-in account that has not been suspended,
// including one still awaiting approval.
//
// Pending accounts are admitted here on purpose. Someone waiting to be approved
// can still read the site and correct the name a provider guessed for them;
// neither of those reaches another person.
func (gate *Gate) RequireAccount() gin.HandlerFunc {
	return gate.admit(func(domain.User) error { return nil })
}

// RequireActiveAccount admits only approved accounts. Every route that writes
// something another visitor will see uses this.
func (gate *Gate) RequireActiveAccount() gin.HandlerFunc {
	return gate.admit(func(user domain.User) error {
		if !user.CanWritePublicContent() {
			return domain.NewError(domain.CodeAccountNotApproved, "this account is waiting to be approved by the site owner", nil)
		}
		return nil
	})
}

// RequireOwner admits only the account that owns the site.
func (gate *Gate) RequireOwner() gin.HandlerFunc {
	return gate.admit(func(user domain.User) error {
		if !user.CanWritePublicContent() {
			return domain.NewError(domain.CodeAccountNotApproved, "this account is waiting to be approved by the site owner", nil)
		}
		if !user.IsOwner() {
			return domain.NewError(domain.CodeUnauthorized, "owner access is required", nil)
		}
		return nil
	})
}

// admit runs the shared credential resolution and then one extra test.
func (gate *Gate) admit(check func(domain.User) error) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		user, err := gate.authenticate(ctx)
		if err != nil {
			writeGateError(ctx, err)
			return
		}
		if failure := check(user); failure != nil {
			writeAccountError(ctx, failure)
			return
		}
		ctx.Next()
	}
}

// VerifiedAccount reports who is signed in, and reports nobody without
// answering the request.
//
// A route that must remain reachable without a session still has to know when
// one is present -- starting a federated sign-in is the whole example, because
// doing it while signed in means "attach this provider to me". Reading that
// from the cookie is how a signed-out or suspended account keeps being treated
// as signed in: the signature proves this server issued the value, never that
// it is still supposed to work. Coming through here instead is what keeps the
// answer to "who is this" in one place.
func (gate *Gate) VerifiedAccount(ctx *gin.Context) (domain.User, bool) {
	user, err := gate.authenticate(ctx)
	if err != nil {
		return domain.User{}, false
	}
	return user, true
}

// errNoCredential is "nothing here names a caller".
//
// It stays a sentinel rather than a domain error because it describes no
// account: whether none was presented or the one presented is not honoured is
// not a difference the answer is allowed to reveal.
var errNoCredential = errors.New("no usable credential was presented")

// errCSRFRequired is a browser session used for a write without the token
// bound into it.
var errCSRFRequired = errors.New("a CSRF token is required")

// authenticate turns the request's credential into an account and stores it on
// the context. It writes nothing; every refusal is returned.
func (gate *Gate) authenticate(ctx *gin.Context) (domain.User, error) {
	authorizationValues := ctx.Request.Header.Values("Authorization")
	if len(authorizationValues) > 1 {
		return domain.User{}, errNoCredential
	}

	if len(authorizationValues) == 1 {
		// A bearer token is the owner's own credential, kept outside the
		// database so it still works when the database does not.
		if strings.TrimSpace(authorizationValues[0]) == "" {
			return domain.User{}, errNoCredential
		}
		parts := strings.Fields(authorizationValues[0])
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			return domain.User{}, errNoCredential
		}
		grant, err := gate.manager.AuthorizeBearer(parts[1])
		if err != nil {
			return domain.User{}, errNoCredential
		}
		ctx.Set(ownerSourceKey, "bearer")
		ctx.Set(ownerGrantKey, grant)
		// The token names no account, so the owner row is read to put a name to
		// it -- but that read does not get to decide admission. Requiring it
		// would take doctor and credential repair away at exactly the moment
		// they are wanted, which is the moment the database is what is broken,
		// and the token was kept outside the database precisely so that could
		// not happen. A caller admitted without the row keeps every power the
		// token carries and loses only its name, so a handler that has to
		// attribute work to an account finds none and refuses on its own.
		owner, err := gate.accounts.OwnerAccount(ctx.Request.Context())
		if err != nil {
			return unnamedOwner, nil
		}
		ctx.Set(accountKey, owner)
		return owner, nil
	}

	cookie, err := ctx.Request.Cookie(SessionCookieName)
	if err != nil {
		return domain.User{}, errNoCredential
	}
	session, grant, err := gate.manager.AuthorizeSession(cookie.Value)
	if err != nil {
		return domain.User{}, errNoCredential
	}
	if isUnsafeMethod(ctx.Request.Method) && !constantTimeEqual(ctx.GetHeader(CSRFHeaderName), session.CSRFToken) {
		return domain.User{}, errCSRFRequired
	}

	resolved, err := gate.accounts.ResolveSession(ctx.Request.Context(), session.SessionID)
	if err != nil {
		return domain.User{}, err
	}
	// The signature already proves the cookie was issued here, so a mismatch
	// between the two halves is not something an attacker can reach. It would
	// mean the session row and the signed payload disagree about whose session
	// this is, which is corruption rather than a request to be answered.
	if resolved.Session.UserID != session.UserID {
		return domain.User{}, errNoCredential
	}
	// Best effort: a request that cannot record its own recency is still a
	// valid request.
	_ = gate.accounts.TouchSession(ctx.Request.Context(), resolved.Session)

	ctx.Set(ownerSourceKey, "session")
	ctx.Set(ownerGrantKey, grant)
	ctx.Set(accountKey, resolved.User)
	ctx.Set(accountSessionKey, resolved.Session)
	return resolved.User, nil
}

// unnamedOwner is everything a bearer token proves without reading a row: the
// owner's authority, and no identity to attribute anything to.
//
// It is deliberately never stored as the current account. An id of "" that
// reached CurrentAccount would be authored content signed by nobody.
var unnamedOwner = domain.User{Role: domain.UserRoleOwner, Status: domain.AccountActive}

// writeGateError answers a refusal authenticate returned.
func writeGateError(ctx *gin.Context, err error) {
	switch {
	case errors.Is(err, errNoCredential):
		unauthorized(ctx)
	case errors.Is(err, errCSRFRequired):
		Failure(ctx, http.StatusForbidden, "CSRF_INVALID", "a valid CSRF token is required", nil)
	default:
		writeAccountError(ctx, err)
	}
}

// CurrentAccount reports the account a gate admitted for this request.
func CurrentAccount(ctx *gin.Context) (domain.User, bool) {
	value, present := ctx.Get(accountKey)
	if !present {
		return domain.User{}, false
	}
	user, ok := value.(domain.User)
	return user, ok
}

// CurrentSession reports the session row a gate admitted, when the request
// arrived on a browser cookie rather than a bearer token.
func CurrentSession(ctx *gin.Context) (domain.SessionRecord, bool) {
	value, present := ctx.Get(accountSessionKey)
	if !present {
		return domain.SessionRecord{}, false
	}
	session, ok := value.(domain.SessionRecord)
	return session, ok
}

// writeAccountError maps account failures onto the response envelope.
func writeAccountError(ctx *gin.Context, err error) {
	// A cancelled request is the caller leaving, not a failure of ours. Every
	// other write*Error in this package makes the same distinction, and one
	// that did not would report a disconnect as a server fault.
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		Failure(ctx, http.StatusRequestTimeout, "CANCELLED", "the account operation was cancelled", nil)
		return
	}
	var domainErr *domain.Error
	if !errors.As(err, &domainErr) {
		Failure(ctx, http.StatusInternalServerError, "INTERNAL", "account lookup failed", nil)
		return
	}
	switch domainErr.Code {
	case domain.CodeUnauthorized, domain.CodeAccountNotFound:
		unauthorized(ctx)
	// A refusal about which credential the account carries, not about whether
	// the secret was right. It stays 401 because nobody was signed in either
	// way, and keeps its own code so the form can point at what would work.
	case domain.CodeCredentialUnavailable:
		Failure(ctx, http.StatusUnauthorized, string(domainErr.Code), domainErr.Message, domainErr.Details)
	// A sub-resource of an account that is itself present. The caller is signed
	// in and allowed; the provider they named simply is not attached.
	case domain.CodeIdentityNotLinked:
		Failure(ctx, http.StatusNotFound, string(domainErr.Code), domainErr.Message, domainErr.Details)
	case domain.CodeAccountSuspended:
		Failure(ctx, http.StatusForbidden, string(domainErr.Code), domainErr.Message, domainErr.Details)
	case domain.CodeAccountNotApproved:
		Failure(ctx, http.StatusForbidden, string(domainErr.Code), domainErr.Message, domainErr.Details)
	case domain.CodeInvalidArgument, domain.CodeUsernameTaken, domain.CodeUsernameReserved,
		domain.CodeIdentityAlreadyLinked, domain.CodeLastCredential:
		status := http.StatusBadRequest
		switch domainErr.Code {
		// These three are conflicts with the state of the world, not faults in
		// what was sent. "This is the only way back into your account" is a
		// true statement about a well-formed request, and a client should be
		// able to tell it apart from a malformed one without reading the code.
		case domain.CodeUsernameTaken, domain.CodeIdentityAlreadyLinked, domain.CodeLastCredential:
			status = http.StatusConflict
		}
		Failure(ctx, status, string(domainErr.Code), domainErr.Message, domainErr.Details)
	case domain.CodeRegistrationClosed:
		Failure(ctx, http.StatusForbidden, string(domainErr.Code), domainErr.Message, domainErr.Details)
	default:
		Failure(ctx, http.StatusInternalServerError, "INTERNAL", "account operation failed", nil)
	}
}
