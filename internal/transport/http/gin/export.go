package ginhttp

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"

	gin "github.com/gin-gonic/gin"
	"github.com/moment/moment/internal/archive"
	"github.com/moment/moment/internal/auth"
	"github.com/moment/moment/internal/domain"
)

const (
	defaultArchivePreparationTimeout = 30 * time.Second
	defaultArchiveWriteIdle          = 30 * time.Second
)

// ArchiveExporter is the narrow transport-independent streaming use case.
type ArchiveExporter interface {
	Export(context.Context, io.Writer) (archive.Summary, error)
}

// RegisterExportRoutes exposes the owner-authenticated, bodyless v1 download.
// GET is safe for cookie auth under the existing owner contract; bearer auth
// is accepted by the same middleware.
func RegisterExportRoutes(engine *gin.Engine, exporter ArchiveExporter, manager *auth.Manager) {
	owner := engine.Group("/api/v1/owner")
	owner.Use(RequireOwner(manager))
	owner.GET("/export", streamExport(exporter))
}

func streamExport(exporter ArchiveExporter) gin.HandlerFunc {
	return streamExportWithBudgets(exporter, defaultArchivePreparationTimeout, defaultArchiveWriteIdle)
}

func streamExportWithBudgets(exporter ArchiveExporter, preparation, idle time.Duration) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		if ctx.Request.URL.RawQuery != "" {
			Failure(ctx, http.StatusBadRequest, "INVALID_ARGUMENT", "archive export does not accept query parameters", map[string]any{"field": "query"})
			return
		}
		if ctx.Request.ContentLength != 0 || len(ctx.Request.TransferEncoding) != 0 {
			Failure(ctx, http.StatusBadRequest, "INVALID_ARGUMENT", "archive export does not accept a request body", map[string]any{"field": "body"})
			return
		}
		ctx.Header("Content-Type", "application/zip")
		ctx.Header("Content-Disposition", `attachment; filename="moment-export.zip"`)
		ctx.Header("Cache-Control", "no-store")
		ctx.Header("X-Archive-Format", archive.FormatHeaderValue)
		stream, err := newIdleDeadlineWriter(ctx.Writer, idle)
		if err != nil {
			prepareArchiveJSONFailure(ctx)
			Failure(ctx, http.StatusInternalServerError, "EXPORT_UNAVAILABLE", "bounded archive streaming is unavailable", nil)
			return
		}
		operationContext, cancel := context.WithCancelCause(ctx.Request.Context())
		defer cancel(nil)
		prepared := newPreparationWriter(operationContext, cancel, stream, preparation)
		defer prepared.Stop()
		_, err = exporter.Export(operationContext, prepared)
		if err == nil {
			if !prepared.Started() {
				prepareArchiveJSONFailure(ctx)
				Failure(ctx, http.StatusInternalServerError, "EXPORT_INVALID", "the portable archive produced no bytes", nil)
			}
			return
		}
		if ctx.Writer.Written() {
			panic(http.ErrAbortHandler)
		}
		prepareArchiveJSONFailure(ctx)
		if errors.Is(context.Cause(operationContext), context.DeadlineExceeded) {
			Failure(ctx, http.StatusGatewayTimeout, "EXPORT_PREPARATION_TIMEOUT", "archive preparation exceeded its time budget", nil)
			return
		}
		if ctx.Request.Context().Err() != nil || errors.Is(err, context.Canceled) {
			Failure(ctx, http.StatusRequestTimeout, "CANCELLED", "archive export was cancelled", nil)
			return
		}
		var domainErr *domain.Error
		if errors.As(err, &domainErr) {
			Failure(ctx, http.StatusInternalServerError, string(domainErr.Code), domainErr.Message, domainErr.Details)
			return
		}
		Failure(ctx, http.StatusInternalServerError, "EXPORT_INVALID", "the portable archive could not be produced", nil)
	}
}

func prepareArchiveJSONFailure(ctx *gin.Context) {
	ctx.Header("Content-Type", "application/json; charset=utf-8")
	ctx.Header("Content-Disposition", "")
	ctx.Header("X-Archive-Format", "")
}

type idleDeadlineWriter struct {
	writer     io.Writer
	controller *http.ResponseController
	idle       time.Duration
}

func newIdleDeadlineWriter(writer http.ResponseWriter, idle time.Duration) (*idleDeadlineWriter, error) {
	if idle <= 0 {
		return nil, errors.New("archive write idle timeout must be positive")
	}
	controller := http.NewResponseController(writer)
	return &idleDeadlineWriter{writer: writer, controller: controller, idle: idle}, nil
}

func (writer *idleDeadlineWriter) Write(data []byte) (int, error) {
	if err := writer.controller.SetWriteDeadline(time.Now().Add(writer.idle)); err != nil {
		return 0, err
	}
	return writer.writer.Write(data)
}

func (writer *idleDeadlineWriter) flushArchive() error { return writer.controller.Flush() }

type preparationWriter struct {
	ctx     context.Context
	cancel  context.CancelCauseFunc
	writer  io.Writer
	mu      sync.Mutex
	timer   *time.Timer
	started bool
	expired bool
}

func newPreparationWriter(ctx context.Context, cancel context.CancelCauseFunc, writer io.Writer, budget time.Duration) *preparationWriter {
	prepared := &preparationWriter{ctx: ctx, cancel: cancel, writer: writer}
	if budget <= 0 {
		prepared.expired = true
		cancel(context.DeadlineExceeded)
		return prepared
	}
	prepared.timer = time.AfterFunc(budget, func() {
		prepared.mu.Lock()
		defer prepared.mu.Unlock()
		if !prepared.started {
			prepared.expired = true
			prepared.cancel(context.DeadlineExceeded)
		}
	})
	return prepared
}

func (writer *preparationWriter) Write(data []byte) (int, error) {
	writer.mu.Lock()
	if writer.expired || writer.ctx.Err() != nil {
		cause := context.Cause(writer.ctx)
		writer.mu.Unlock()
		if cause == nil {
			cause = context.Canceled
		}
		return 0, cause
	}
	first := !writer.started && len(data) > 0
	if first {
		writer.started = true
		if writer.timer != nil {
			writer.timer.Stop()
		}
	}
	writer.mu.Unlock()
	written, err := writer.writer.Write(data)
	if first && written > 0 && err == nil {
		if flusher, ok := writer.writer.(interface{ flushArchive() error }); ok {
			err = flusher.flushArchive()
		}
	}
	return written, err
}

func (writer *preparationWriter) Stop() {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.timer != nil {
		writer.timer.Stop()
	}
}

func (writer *preparationWriter) Started() bool {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return writer.started
}
