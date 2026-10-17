package ginhttp

import (
	"context"
	"io"
	"time"

	gin "github.com/gin-gonic/gin"
	"github.com/moment/moment/internal/archive"
)

const (
	defaultArchiveWriteIdle = 30 * time.Second
)

// ArchiveExporter is the narrow transport-independent streaming use case.
type ArchiveExporter interface {
	Export(context.Context, io.Writer) (archive.Summary, error)
}

// RegisterExportRoutes exposes the owner-authenticated, bodyless v1 download.
// GET is safe for cookie auth under the existing owner contract; bearer auth
// is accepted by the same middleware.
func RegisterExportRoutes(engine *gin.Engine, exporter ArchiveExporter, gate *Gate) {
	backups := engine.Group("/api/v1/backups")
	backups.Use(gate.RequireAdmin())
	backups.GET("/export", streamExport(exporter))
	if personal, ok := exporter.(interface {
		ExportAccount(context.Context, io.Writer, string) (archive.Summary, error)
	}); ok {
		engine.GET("/api/v1/account/export", gate.RequireAccount(), func(ctx *gin.Context) {
			user, ok := CurrentAccount(ctx)
			if !ok {
				unauthorized(ctx)
				return
			}
			preparedExport(func(operation context.Context, destination io.Writer) (archive.Summary, error) {
				return personal.ExportAccount(operation, destination, user.ID)
			}, archive.AccountFormatHeaderValue, "moment-account-export.zip", exportPreparationMax)(ctx)
		})
	}
}

func streamExport(exporter ArchiveExporter) gin.HandlerFunc {
	return preparedExport(exporter.Export, archive.FormatHeaderValue, "moment-export.zip", exportPreparationMax)
}

func streamExportWithBudgets(exporter ArchiveExporter, preparation, idle time.Duration) gin.HandlerFunc {
	return preparedExport(exporter.Export, archive.FormatHeaderValue, "moment-export.zip", preparation, idle)
}
