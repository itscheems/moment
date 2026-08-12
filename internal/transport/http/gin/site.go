package ginhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	gin "github.com/gin-gonic/gin"
	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/auth"
	"github.com/moment/moment/internal/domain"
)

// RegisterSiteRoutes installs the public settings read and owner-only partial
// mutation over one application service.
func RegisterSiteRoutes(engine *gin.Engine, service *app.SiteService, manager *auth.Manager, instanceID string) {
	engine.GET("/api/v1/site", getSiteSettings(service, instanceID))
	owner := engine.Group("/api/v1/owner")
	owner.Use(RequireOwner(manager))
	owner.PATCH("/site", updateSiteSettings(service))
}

func getSiteSettings(service *app.SiteService, instanceID string) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		settings, err := service.Get(ctx.Request.Context())
		if err != nil {
			writeSiteError(ctx, err)
			return
		}
		meta := map[string]any{}
		if instanceID != "" {
			meta["instance_id"] = instanceID
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
	Bio             requiredStringField `json:"bio"`
	Timezone        requiredStringField `json:"timezone"`
	PublicBaseURL   optionalStringField `json:"public_base_url"`
	AvatarMediaID   optionalStringField `json:"avatar_media_id"`
	CoverMediaID    optionalStringField `json:"cover_media_id"`
	CommentsEnabled requiredBoolField   `json:"comments_enabled"`
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
	if request.Bio.set {
		update.Bio = &request.Bio.value
	}
	if request.Timezone.set {
		update.Timezone = &request.Timezone.value
	}
	if request.PublicBaseURL.set {
		update.PublicBaseURL = &app.NullableString{Value: request.PublicBaseURL.value}
	}
	if request.AvatarMediaID.set {
		update.AvatarMediaID = &app.NullableString{Value: request.AvatarMediaID.value}
	}
	if request.CoverMediaID.set {
		update.CoverMediaID = &app.NullableString{Value: request.CoverMediaID.value}
	}
	if request.CommentsEnabled.set {
		update.CommentsEnabled = &request.CommentsEnabled.value
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
	case domain.CodeInvalidArgument:
		Failure(ctx, http.StatusBadRequest, string(domainErr.Code), domainErr.Message, domainErr.Details)
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
