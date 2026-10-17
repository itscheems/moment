package ginhttp

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	gin "github.com/gin-gonic/gin"
	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/media"
)

// accountView is the public projection of one account.
//
// It deliberately mirrors commentdto.Author and app.MentionSuggestion field for
// field, because all three answer the same question -- who is this -- and a
// browser that can render one should not need a second shape to render another.
type accountView struct {
	UserID        string  `json:"user_id"`
	Username      string  `json:"username"`
	DisplayName   string  `json:"display_name"`
	Bio           string  `json:"bio"`
	AvatarMediaID *string `json:"avatar_media_id"`
	CoverMediaID  *string `json:"cover_media_id"`
	ProfileURL    string  `json:"profile_url"`
}

// privateAccountView adds the fields an account may see about itself and
// nobody else may see about it.
type privateAccountView struct {
	accountView
	Role           string `json:"role"`
	Status         string `json:"status"`
	UsernameChosen bool   `json:"username_chosen"`
}

func publicAccount(user domain.User) accountView {
	return accountView{
		UserID:        user.ID,
		Username:      user.Username,
		DisplayName:   user.DisplayName,
		Bio:           user.Bio,
		AvatarMediaID: user.AvatarMediaID,
		CoverMediaID:  user.CoverMediaID,
		ProfileURL:    domain.ProfileURL(user.Username),
	}
}

func privateAccount(user domain.User) privateAccountView {
	return privateAccountView{
		accountView:    publicAccount(user),
		Role:           string(user.Role),
		Status:         string(user.Status),
		UsernameChosen: user.UsernameSetAtMS != nil,
	}
}

type accountSessionView struct {
	ExpiresAt time.Time          `json:"expires_at"`
	CSRFToken string             `json:"csrf_token"`
	Account   privateAccountView `json:"account"`
	Limits    accountLimitsView  `json:"limits"`
}

// accountLimitsView publishes the bounds a client would otherwise have to
// hard-code a second copy of.
//
// The picture ceiling reached a browser only as details on a rejection, which
// is after the upload has already been spent, so every client that wanted to
// refuse a too-large file before sending it had to carry its own number and
// keep it in step with this one.
type accountLimitsView struct {
	ProfilePictureBytes int64 `json:"profile_picture_bytes"`
	DisplayNameLength   int   `json:"display_name_length"`
	BioLength           int   `json:"bio_length"`
}

type sessionListItem struct {
	ID         string    `json:"id"`
	IssuedAt   time.Time `json:"issued_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	Surface    string    `json:"surface"`
	Current    bool      `json:"current"`
}

type identityView struct {
	Provider    string    `json:"provider"`
	DisplayName string    `json:"display_name"`
	Email       string    `json:"email"`
	LinkedAt    time.Time `json:"linked_at"`
	LastLoginAt time.Time `json:"last_login_at"`
}

type profilePatch struct {
	Username      *string `json:"username"`
	DisplayName   *string `json:"display_name"`
	Bio           *string `json:"bio"`
	AvatarMediaID *string `json:"avatar_media_id"`
	CoverMediaID  *string `json:"cover_media_id"`
}

type statusPatch struct {
	Status string `json:"status"`
}

// maxAccountPictureBytes bounds what an account may upload for itself.
//
// The general media ceiling is sized for a five minute video, which is not a
// portrait. Bounding this separately means a picture that is obviously not one
// is refused before any of it is read, rather than after the pipeline has
// spent the work discovering the same thing.
const maxAccountPictureBytes = 8 << 20

// RegisterAccountRoutes installs the routes an account uses on itself, the
// public account lookup, and the owner's review screens.
func RegisterAccountRoutes(engine *gin.Engine, gate *Gate, pictures *media.Store, cookies CookieOptions, oauth OAuthConfig) {
	engine.GET("/api/v1/users/:username", publicAccountLookup(gate))

	// Pending accounts reach everything in this group. None of it is visible
	// to anyone else, so approval is not what gates it.
	account := engine.Group("/api/v1/account")
	account.Use(gate.RequireAccount())
	account.GET("/session", currentAccountSession(gate))
	account.PATCH("/profile", updateOwnProfile(gate))
	account.GET("/identities", listOwnIdentities(gate))
	account.POST("/identities/:provider", linkOwnIdentity(gate, oauth))
	account.DELETE("/identities/:provider", unlinkOwnIdentity(gate))
	account.GET("/sessions", listOwnSessions(gate))
	account.DELETE("/sessions", revokeOwnSessions(gate, cookies))

	// Uploading is the one thing on an account's own profile that a pending
	// account may not do. Everything else here is text an input validator
	// bounds; this writes bytes to the disk, and an account nobody has admitted
	// yet should not be able to do that. Clearing a picture stays available,
	// because that consumes nothing.
	if pictures != nil {
		uploads := engine.Group("/api/v1/account")
		uploads.Use(gate.RequireActiveAccount())
		uploads.POST("/avatar", setAccountPicture(gate, pictures, "avatar"))
		uploads.POST("/cover", setAccountPicture(gate, pictures, "cover"))
	}

	administration := engine.Group("/api/v1/accounts")
	administration.Use(gate.RequireAdmin())
	administration.GET("", listAllAccounts(gate))
	administration.PATCH("/:id", setAccountStatus(gate))
}

// publicAccountLookup answers /username. It reports only what a visitor may
// see, and reports a pending or suspended account as absent rather than as
// existing-but-hidden, so the endpoint cannot be used to enumerate applicants.
func publicAccountLookup(gate *Gate) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		username := strings.ToLower(strings.TrimSpace(ctx.Param("username")))
		user, err := gate.accounts.AccountByUsername(ctx.Request.Context(), username)
		if err != nil {
			Failure(ctx, http.StatusNotFound, string(domain.CodeAccountNotFound), "no such account", nil)
			return
		}
		if user.Status != domain.AccountActive {
			Failure(ctx, http.StatusNotFound, string(domain.CodeAccountNotFound), "no such account", nil)
			return
		}
		Success(ctx, http.StatusOK, publicAccount(user), nil)
	}
}

// currentAccountSession hands the browser the CSRF token belonging to the
// cookie it already holds.
//
// This is how a federated sign-in finishes. The callback sets the cookie during
// a redirect, but the single-page app keys its signed-in state off a value in
// sessionStorage, so without this read it would hold a valid cookie and still
// believe it was signed out.
//
// Returning the token on a safe method is sound here: there is no CORS
// middleware, so no other origin can read this response; the envelope forces
// Cache-Control: no-store; and the token is already inside the signed cookie
// the same browser is sending.
func currentAccountSession(gate *Gate) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		user, ok := CurrentAccount(ctx)
		if !ok {
			unauthorized(ctx)
			return
		}
		view := accountSessionView{
			Account: privateAccount(user),
			Limits: accountLimitsView{
				ProfilePictureBytes: maxAccountPictureBytes,
				DisplayNameLength:   domain.MaxDisplayNameLength,
				BioLength:           domain.MaxBioLength,
			},
		}
		if session, present := CurrentSession(ctx); present {
			view.ExpiresAt = time.UnixMilli(session.ExpiresAtMS).UTC()
		}
		if cookie, err := ctx.Request.Cookie(SessionCookieName); err == nil {
			if issued, _, authErr := gate.manager.AuthorizeSession(cookie.Value); authErr == nil {
				view.CSRFToken = issued.CSRFToken
				view.ExpiresAt = issued.ExpiresAt
			}
		}
		Success(ctx, http.StatusOK, view, nil)
	}
}

func updateOwnProfile(gate *Gate) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		user, ok := CurrentAccount(ctx)
		if !ok {
			unauthorized(ctx)
			return
		}
		body, read := readBody(ctx)
		if !read {
			return
		}
		var patch profilePatch
		decoder := json.NewDecoder(strings.NewReader(string(body)))
		// Unknown fields are refused rather than ignored. That is what keeps a
		// request carrying "role" or "status" from being silently accepted and
		// quietly doing nothing -- and what makes it obvious it was refused.
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&patch); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
			Failure(ctx, http.StatusBadRequest, "INVALID_ARGUMENT", "a JSON profile update is required", nil)
			return
		}
		update := app.ProfileUpdate{Username: patch.Username, DisplayName: patch.DisplayName, Bio: patch.Bio}
		if patch.AvatarMediaID != nil {
			update.AvatarMediaID = &app.NullableString{Value: emptyToNil(patch.AvatarMediaID)}
		}
		if patch.CoverMediaID != nil {
			update.CoverMediaID = &app.NullableString{Value: emptyToNil(patch.CoverMediaID)}
		}
		updated, err := gate.accounts.UpdateProfile(ctx.Request.Context(), user.ID, update)
		if err != nil {
			writeAccountError(ctx, err)
			return
		}
		Success(ctx, http.StatusOK, privateAccount(updated), nil)
	}
}

func emptyToNil(value *string) *string {
	if value == nil || *value == "" {
		return nil
	}
	return value
}

func listOwnIdentities(gate *Gate) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		user, ok := CurrentAccount(ctx)
		if !ok {
			unauthorized(ctx)
			return
		}
		identities, err := gate.accounts.Identities(ctx.Request.Context(), user.ID)
		if err != nil {
			writeAccountError(ctx, err)
			return
		}
		items := make([]identityView, 0, len(identities))
		for _, identity := range identities {
			item := identityView{
				Provider:    identity.Provider,
				DisplayName: identity.DisplayName,
				LinkedAt:    time.UnixMilli(identity.CreatedAtMS).UTC(),
				LastLoginAt: time.UnixMilli(identity.LastLoginAtMS).UTC(),
			}
			// Both, not either. GitHub can report an address that is real but
			// unproven -- a public profile address it never checked belongs to
			// the account -- and showing that next to one a provider actually
			// verified would make them look like the same kind of fact.
			if identity.Email != nil && identity.EmailVerified && identity.EmailDeliverable {
				item.Email = *identity.Email
			}
			items = append(items, item)
		}
		Success(ctx, http.StatusOK, items, map[string]any{"count": len(items)})
	}
}

func unlinkOwnIdentity(gate *Gate) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		user, ok := CurrentAccount(ctx)
		if !ok {
			unauthorized(ctx)
			return
		}
		provider := strings.ToLower(strings.TrimSpace(ctx.Param("provider")))
		if err := gate.accounts.UnlinkIdentity(ctx.Request.Context(), user, provider); err != nil {
			writeAccountError(ctx, err)
			return
		}
		Success(ctx, http.StatusOK, map[string]string{"unlinked": provider}, nil)
	}
}

func listOwnSessions(gate *Gate) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		user, ok := CurrentAccount(ctx)
		if !ok {
			unauthorized(ctx)
			return
		}
		records, err := gate.accounts.Sessions(ctx.Request.Context(), user.ID)
		if err != nil {
			writeAccountError(ctx, err)
			return
		}
		currentID := ""
		if session, present := CurrentSession(ctx); present {
			currentID = session.ID
		}
		// Revoked rows are gone as credentials; expired ones are too, and they
		// outlive their expiry by the retention grace that keeps "when did this
		// device last sign in" answerable. That grace is for the janitor, not
		// for a list whose whole claim is that these sessions are live.
		nowMS := time.Now().UTC().UnixMilli()
		items := make([]sessionListItem, 0, len(records))
		for _, record := range records {
			if record.RevokedAtMS != nil || record.ExpiresAtMS <= nowMS {
				continue
			}
			items = append(items, sessionListItem{
				ID:         record.ID,
				IssuedAt:   time.UnixMilli(record.IssuedAtMS).UTC(),
				LastSeenAt: time.UnixMilli(record.LastSeenAtMS).UTC(),
				ExpiresAt:  time.UnixMilli(record.ExpiresAtMS).UTC(),
				Surface:    record.Surface,
				Current:    record.ID == currentID,
			})
		}
		Success(ctx, http.StatusOK, items, map[string]any{"count": len(items)})
	}
}

// revokeOwnSessions signs an account out everywhere, including here.
func revokeOwnSessions(gate *Gate, cookies CookieOptions) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		user, ok := CurrentAccount(ctx)
		if !ok {
			unauthorized(ctx)
			return
		}
		revoked, err := gate.accounts.EndAllSessions(ctx.Request.Context(), user.ID)
		if err != nil {
			writeAccountError(ctx, err)
			return
		}
		clearSessionCookie(ctx, cookies)
		Success(ctx, http.StatusOK, map[string]int{"revoked": revoked}, nil)
	}
}

func listAllAccounts(gate *Gate) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		users, err := gate.accounts.List(ctx.Request.Context())
		if err != nil {
			writeAccountError(ctx, err)
			return
		}
		items := make([]privateAccountView, 0, len(users))
		for _, user := range users {
			items = append(items, privateAccount(user))
		}
		mode, err := gate.accounts.CurrentRegistrationMode(ctx.Request.Context())
		if err != nil {
			writeAccountError(ctx, err)
			return
		}
		Success(ctx, http.StatusOK, items, map[string]any{"count": len(items), "registration_mode": string(mode)})
	}
}

// setAccountStatus is the owner's approve, suspend and reinstate control. It is
// the only route in the product that can change an account's status, and it
// admits only the administration.
func setAccountStatus(gate *Gate) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		body, read := readBody(ctx)
		if !read {
			return
		}
		var patch statusPatch
		decoder := json.NewDecoder(strings.NewReader(string(body)))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&patch); err != nil || decoder.Decode(&struct{}{}) != io.EOF || patch.Status == "" {
			Failure(ctx, http.StatusBadRequest, "INVALID_ARGUMENT", "a JSON account status is required", nil)
			return
		}
		updated, err := gate.accounts.SetStatus(ctx.Request.Context(), ctx.Param("id"), domain.AccountStatus(patch.Status))
		if err != nil {
			writeAccountError(ctx, err)
			return
		}
		Success(ctx, http.StatusOK, privateAccount(updated), nil)
	}
}

// setAccountPicture ingests one image and assigns it in the same request.
//
// It exists instead of opening the general media route to members. That route
// adds to a pool nothing points at yet, so admitting members to it would mean
// an approved account could fill the disk with files no account had claimed.
// Ingesting and assigning together keeps every byte a member writes reachable
// from exactly one profile, which is also what makes the picture it replaces
// collectable.
func setAccountPicture(gate *Gate, pictures *media.Store, kind string) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		account, ok := CurrentAccount(ctx)
		if !ok {
			unauthorized(ctx)
			return
		}
		fileHeader, err := ctx.FormFile("file")
		if err != nil {
			Failure(ctx, http.StatusBadRequest, "INVALID_ARGUMENT", "multipart field 'file' is required", map[string]any{"field": "file"})
			return
		}
		if ctx.Request.MultipartForm != nil {
			defer func() { _ = ctx.Request.MultipartForm.RemoveAll() }()
		}
		// Both refusals below go through writeMediaError rather than choosing
		// their own status, so one error code always means one status. Deciding
		// here produced MEDIA_TOO_LARGE as 413 from this pre-check and 400 from
		// the ingest pipeline for the same condition, which leaves a client
		// mapping on status right half the time.
		if fileHeader.Size > maxAccountPictureBytes {
			writeMediaError(ctx, domain.NewError(domain.CodeMediaTooLarge,
				"a profile picture must be 8 MiB or smaller", map[string]any{"limit_bytes": maxAccountPictureBytes}))
			return
		}
		file, err := fileHeader.Open()
		if err != nil {
			writeMediaError(ctx, domain.NewError(domain.CodeMediaCorrupt, "the uploaded picture could not be opened", nil))
			return
		}
		defer func() { _ = file.Close() }()

		value, _, err := pictures.Ingest(ctx.Request.Context(), fileHeader.Filename, file)
		if err != nil {
			writeMediaError(ctx, err)
			return
		}
		// The pipeline accepts video, and a profile is not a place for one.
		// The ingested row is left unreferenced rather than deleted here: it is
		// already what the collector looks for, and deleting media is not this
		// handler's authority.
		if !strings.HasPrefix(value.MIMEType, "image/") {
			writeMediaError(ctx, domain.NewError(domain.CodeMediaTypeUnsupported,
				"a profile picture must be an image", map[string]any{"mime_type": value.MIMEType}))
			return
		}

		assigned := value.ID.String()
		update := app.ProfileUpdate{}
		if kind == "cover" {
			update.CoverMediaID = &app.NullableString{Value: &assigned}
		} else {
			update.AvatarMediaID = &app.NullableString{Value: &assigned}
		}
		updated, err := gate.accounts.UpdateProfile(ctx.Request.Context(), account.ID, update)
		if err != nil {
			writeAccountError(ctx, err)
			return
		}
		Success(ctx, http.StatusOK, privateAccount(updated), nil)
	}
}
