package ginhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	gin "github.com/gin-gonic/gin"
	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
)

// RegisterSiteRoutes installs the public settings read and owner-only partial
// mutation over one application service.
func RegisterSiteRoutes(engine *gin.Engine, service *app.SiteService, gate *Gate, instanceID string, instanceInitializedAt time.Time) {
	engine.GET("/api/v1/site", getSiteSettings(service, instanceID, instanceInitializedAt))
	settings := engine.Group("/api/v1")
	settings.Use(gate.RequireAdmin())
	settings.PATCH("/site", updateSiteSettings(service))
	settings.PUT("/site/profile", selectSiteProfile(service, gate))
}

func getSiteSettings(service *app.SiteService, instanceID string, instanceInitializedAt time.Time) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		settings, err := service.Get(ctx.Request.Context())
		if err != nil {
			writeSiteError(ctx, err)
			return
		}
		meta := map[string]any{}
		if instanceID != "" {
			meta["instance_id"] = instanceID
			if !instanceInitializedAt.IsZero() {
				meta["instance_initialized_at"] = instanceInitializedAt.UTC().Format(time.RFC3339Nano)
			}
		}
		Success(ctx, http.StatusOK, settings, meta)
	}
}

func updateSiteSettings(service *app.SiteService) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		body, ok := readBody(ctx)
		if !ok {
			return
		}
		update, err := decodeSiteUpdate(body)
		if err != nil {
			Failure(ctx, http.StatusBadRequest, "INVALID_ARGUMENT", "site settings must be one JSON object with valid fields", nil)
			return
		}
		settings, err := service.Update(ctx.Request.Context(), update)
		if err != nil {
			writeSiteError(ctx, err)
			return
		}
		Success(ctx, http.StatusOK, settings, nil)
	}
}

type optionalStringField struct {
	set   bool
	value *string
}

func (field *optionalStringField) UnmarshalJSON(data []byte) error {
	field.set = true
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		field.value = nil
		return nil
	}
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	field.value = &value
	return nil
}

type requiredStringField struct {
	set   bool
	value string
}

func (field *requiredStringField) UnmarshalJSON(data []byte) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return errors.New("null is not allowed")
	}
	if err := json.Unmarshal(data, &field.value); err != nil {
		return err
	}
	field.set = true
	return nil
}

type requiredBoolField struct {
	set   bool
	value bool
}

func (field *requiredBoolField) UnmarshalJSON(data []byte) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return errors.New("null is not allowed")
	}
	if err := json.Unmarshal(data, &field.value); err != nil {
		return err
	}
	field.set = true
	return nil
}

type siteUpdateRequest struct {
	Title           requiredStringField `json:"title"`
	Timezone        requiredStringField `json:"timezone"`
	PublicBaseURL   optionalStringField `json:"public_base_url"`
	CommentsEnabled requiredBoolField   `json:"comments_enabled"`
	// Accepted on the write and absent from the read: the route is owner-only,
	// while GET /api/v1/site is public and SiteSettings is serialised straight
	// to it. Whether applications are being accepted is the owner's to publish.
	RegistrationMode requiredStringField `json:"registration_mode"`
}

func decodeSiteUpdate(body []byte) (app.SiteUpdate, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var request siteUpdateRequest
	if err := decoder.Decode(&request); err != nil {
		return app.SiteUpdate{}, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return app.SiteUpdate{}, errors.New("request must contain exactly one JSON document")
	}
	update := app.SiteUpdate{}

	if request.Title.set {
		update.Title = &request.Title.value
	}

	if request.Timezone.set {
		update.Timezone = &request.Timezone.value
	}
	if request.PublicBaseURL.set {
		update.PublicBaseURL = &app.NullableString{Value: request.PublicBaseURL.value}
	}

	if request.CommentsEnabled.set {
		update.CommentsEnabled = &request.CommentsEnabled.value
	}
	if request.RegistrationMode.set {
		update.RegistrationMode = &request.RegistrationMode.value
	}
	return update, nil
}

func writeSiteError(ctx *gin.Context, err error) {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		Failure(ctx, http.StatusRequestTimeout, "CANCELLED", "the site settings operation was cancelled", nil)
		return
	}
	var domainErr *domain.Error
	if !errors.As(err, &domainErr) {
		Failure(ctx, http.StatusInternalServerError, "INTERNAL", "site settings operation failed", nil)
		return
	}
	switch domainErr.Code {
	case domain.CodeUnauthorized:
		Failure(ctx, http.StatusForbidden, string(domainErr.Code), domainErr.Message, domainErr.Details)
	case domain.CodeInvalidArgument, domain.CodeUsernameReserved:
		Failure(ctx, http.StatusBadRequest, string(domainErr.Code), domainErr.Message, domainErr.Details)
	// A conflict with the state of the world rather than a fault in what was
	// sent, and the same status the account route answers with -- one handle
	// column, one answer, whichever form the owner reached it from.
	case domain.CodeUsernameTaken:
		Failure(ctx, http.StatusConflict, string(domainErr.Code), domainErr.Message, domainErr.Details)
	case domain.CodeMediaNotFound:
		Failure(ctx, http.StatusNotFound, string(domainErr.Code), domainErr.Message, domainErr.Details)
	case domain.CodePostNotFound:
		Failure(ctx, http.StatusNotFound, string(domainErr.Code), domainErr.Message, domainErr.Details)
	case domain.CodeDataIntegrityFailed:
		Failure(ctx, http.StatusInternalServerError, string(domainErr.Code), domainErr.Message, domainErr.Details)
	default:
		Failure(ctx, http.StatusInternalServerError, "INTERNAL", "site settings operation failed", nil)
	}
}

func selectSiteProfile(service *app.SiteService, gate *Gate) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		body, ok := readBody(ctx)
		if !ok {
			return
		}
		var patch struct {
			UserID string `json:"user_id"`
		}
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&patch); err != nil || decoder.Decode(&struct{}{}) != io.EOF || domain.ValidateUserID(patch.UserID) != nil {
			Failure(ctx, http.StatusBadRequest, "INVALID_ARGUMENT", "an account user_id is required", nil)
			return
		}
		if _, err := gate.accounts.SelectSiteProfile(ctx.Request.Context(), patch.UserID); err != nil {
			writeAdministrativeAccountError(ctx, err)
			return
		}
		settings, err := service.Get(ctx.Request.Context())
		if err != nil {
			writeSiteError(ctx, err)
			return
		}
		Success(ctx, http.StatusOK, settings, nil)
	}
}
