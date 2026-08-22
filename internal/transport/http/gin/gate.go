package ginhttp

import (
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
		user, ok := gate.resolve(ctx)
		if !ok {
			return
		}
		if failure := check(user); failure != nil {
			writeAccountError(ctx, failure)
			return
		}
		ctx.Next()
	}
}

// resolve authenticates the request and stores the account on the context. It
// writes its own failure response and reports false when it does.
func (gate *Gate) resolve(ctx *gin.Context) (domain.User, bool) {
	authorizationValues := ctx.Request.Header.Values("Authorization")
	if len(authorizationValues) > 1 {
		unauthorized(ctx)
		return domain.User{}, false
	}

	if len(authorizationValues) == 1 {
		// A bearer token is the owner's own credential, kept outside the
		// database so it still works when the database does not. It names no
		// account, so the owner account is looked up rather than carried.
		if strings.TrimSpace(authorizationValues[0]) == "" {
			unauthorized(ctx)
			return domain.User{}, false
		}
		parts := strings.Fields(authorizationValues[0])
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			unauthorized(ctx)
			return domain.User{}, false
		}
		grant, err := gate.manager.AuthorizeBearer(parts[1])
		if err != nil {
			unauthorized(ctx)
			return domain.User{}, false
		}
		owner, err := gate.accounts.OwnerAccount(ctx.Request.Context())
		if err != nil {
			writeAccountError(ctx, err)
			return domain.User{}, false
		}
		ctx.Set(ownerSourceKey, "bearer")
		ctx.Set(ownerGrantKey, grant)
		ctx.Set(accountKey, owner)
		return owner, true
	}

	cookie, err := ctx.Request.Cookie(SessionCookieName)
	if err != nil {
		unauthorized(ctx)
		return domain.User{}, false
	}
	session, grant, err := gate.manager.AuthorizeSession(cookie.Value)
	if err != nil {
		unauthorized(ctx)
		return domain.User{}, false
	}
	if isUnsafeMethod(ctx.Request.Method) && !constantTimeEqual(ctx.GetHeader(CSRFHeaderName), session.CSRFToken) {
		Failure(ctx, http.StatusForbidden, "CSRF_INVALID", "a valid CSRF token is required", nil)
		return domain.User{}, false
	}

	resolved, err := gate.accounts.ResolveSession(ctx.Request.Context(), session.SessionID)
	if err != nil {
		writeAccountError(ctx, err)
		return domain.User{}, false
	}
	// The signature already proves the cookie was issued here, so a mismatch
	// between the two halves is not something an attacker can reach. It would
	// mean the session row and the signed payload disagree about whose session
	// this is, which is corruption rather than a request to be answered.
	if resolved.Session.UserID != session.UserID {
		unauthorized(ctx)
		return domain.User{}, false
	}
	// Best effort: a request that cannot record its own recency is still a
	// valid request.
	_ = gate.accounts.TouchSession(ctx.Request.Context(), resolved.Session)

	ctx.Set(ownerSourceKey, "session")
	ctx.Set(ownerGrantKey, grant)
	ctx.Set(accountKey, resolved.User)
	ctx.Set(accountSessionKey, resolved.Session)
	return resolved.User, true
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
	var domainErr *domain.Error
	if !errors.As(err, &domainErr) {
		Failure(ctx, http.StatusInternalServerError, "INTERNAL", "account lookup failed", nil)
		return
	}
	switch domainErr.Code {
	case domain.CodeUnauthorized, domain.CodeAccountNotFound:
		unauthorized(ctx)
	case domain.CodeAccountSuspended:
		Failure(ctx, http.StatusForbidden, string(domainErr.Code), domainErr.Message, domainErr.Details)
	case domain.CodeAccountNotApproved:
		Failure(ctx, http.StatusForbidden, string(domainErr.Code), domainErr.Message, domainErr.Details)
	case domain.CodeInvalidArgument, domain.CodeUsernameTaken, domain.CodeUsernameReserved,
		domain.CodeIdentityAlreadyLinked, domain.CodeLastCredential:
		status := http.StatusBadRequest
		if domainErr.Code == domain.CodeUsernameTaken || domainErr.Code == domain.CodeIdentityAlreadyLinked {
			status = http.StatusConflict
		}
		Failure(ctx, status, string(domainErr.Code), domainErr.Message, domainErr.Details)
	case domain.CodeRegistrationClosed:
		Failure(ctx, http.StatusForbidden, string(domainErr.Code), domainErr.Message, domainErr.Details)
	default:
		Failure(ctx, http.StatusInternalServerError, "INTERNAL", "account operation failed", nil)
	}
}
