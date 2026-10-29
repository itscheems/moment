package ginhttp

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	gin "github.com/gin-gonic/gin"
	"github.com/moment/moment/internal/transport/accountdto"
)

func ownPreferences(gate *Gate, update bool) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		user, ok := CurrentAccount(ctx)
		if !ok {
			unauthorized(ctx)
			return
		}
		if update {
			body, read := readBody(ctx)
			if !read {
				return
			}
			var patch struct {
				UILocale optionalStringField `json:"ui_locale"`
			}
			decoder := json.NewDecoder(strings.NewReader(string(body)))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&patch); err != nil || decoder.Decode(&struct{}{}) != io.EOF || !patch.UILocale.set {
				Failure(ctx, http.StatusBadRequest, "INVALID_ARGUMENT", "a JSON ui_locale preference is required", nil)
				return
			}
			updated, err := gate.accounts.UpdatePreferences(ctx.Request.Context(), user.ID, patch.UILocale.value)
			if err != nil {
				writeAccountError(ctx, err)
				return
			}
			user = updated
		}
		Success(ctx, http.StatusOK, accountdto.PreferencesFromUser(user), nil)
	}
}
