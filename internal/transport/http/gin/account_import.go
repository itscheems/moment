package ginhttp

import (
	"context"
	"io"
	"net/http"
	"time"

	gin "github.com/gin-gonic/gin"
	"github.com/moment/moment/internal/archive"
)

type accountImporter interface {
	ImportAccount(context.Context, io.Reader, string, archive.AccountImportOptions) (archive.AccountImportResult, error)
}

func registerAccountImportRoutes(engine *gin.Engine, importer accountImporter, gate *Gate) {
	account := engine.Group("/api/v1/account/import")
	account.Use(gate.RequireActiveAccount())
	handler := func(dry bool) gin.HandlerFunc {
		return importArchiveUpload("personal import", !dry, func(ctx context.Context, source io.Reader, sha string, subject archive.Authorization) (any, error) {
			bounded, cancel := context.WithTimeout(ctx, 5*time.Minute)
			defer cancel()
			return importer.ImportAccount(bounded, source, subject.AccountID, archive.AccountImportOptions{DryRun: dry, ExpectedSHA: sha})
		}, http.StatusOK)
	}
	account.POST("/plan", handler(true))
	account.POST("/restore", handler(false))
}
