package ginhttp

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"

	gin "github.com/gin-gonic/gin"
	"github.com/moment/moment/internal/archive"
	"github.com/moment/moment/internal/domain"
)

const exportPreparationMax = 5 * time.Minute

type exportProducer func(context.Context, io.Writer) (archive.Summary, error)

// preparedExport finishes the consistent snapshot before publishing a 200.
// Private temporary disk storage makes length exact without buffering the archive
// in memory. Each request is a fresh snapshot, so cross-request ranges are not
// advertised. Deferred cleanup covers success, disconnects and preparation errors.
func preparedExport(produce exportProducer, format, filename string, budget time.Duration, idleOverride ...time.Duration) gin.HandlerFunc {
	idle := defaultArchiveWriteIdle
	if len(idleOverride) > 0 {
		idle = idleOverride[0]
	}
	return func(ctx *gin.Context) {
		if ctx.Request.URL.RawQuery != "" || ctx.Request.ContentLength != 0 || len(ctx.Request.TransferEncoding) != 0 {
			Failure(ctx, 400, "INVALID_ARGUMENT", "export does not accept query parameters or a body", nil)
			return
		}
		controller := http.NewResponseController(ctx.Writer)
		if err := controller.SetWriteDeadline(time.Now().Add(budget + idle)); err != nil && !errors.Is(err, http.ErrNotSupported) {
			Failure(ctx, 500, "EXPORT_UNAVAILABLE", "export deadline is unavailable", nil)
			return
		}
		file, err := os.CreateTemp("", "moment-export-*.zip")
		if err != nil {
			Failure(ctx, 500, "EXPORT_UNAVAILABLE", "export temporary storage is unavailable", nil)
			return
		}
		defer func() { _ = file.Close(); _ = os.Remove(file.Name()) }()
		operation, cancel := context.WithTimeout(ctx.Request.Context(), budget)
		defer cancel()
		summary, err := produce(operation, &exportContextWriter{ctx: operation, writer: file})
		if err == nil {
			err = operation.Err()
		}
		if err != nil {
			if errors.Is(operation.Err(), context.DeadlineExceeded) {
				Failure(ctx, 504, "EXPORT_PREPARATION_TIMEOUT", "archive preparation exceeded its time budget", nil)
				return
			}
			if errors.Is(err, context.Canceled) || ctx.Request.Context().Err() != nil {
				Failure(ctx, 408, "CANCELLED", "archive export was cancelled", nil)
				return
			}
			var domainErr *domain.Error
			if errors.As(err, &domainErr) {
				Failure(ctx, 500, string(domainErr.Code), domainErr.Message, domainErr.Details)
				return
			}
			Failure(ctx, 500, "EXPORT_INVALID", "the archive could not be produced", nil)
			return
		}
		info, err := file.Stat()
		if err != nil || info.Size() == 0 {
			Failure(ctx, 500, "EXPORT_INVALID", "the archive produced no bytes", nil)
			return
		}
		if summary.ArchiveBytes != 0 && summary.ArchiveBytes != info.Size() {
			Failure(ctx, 500, "EXPORT_INVALID", "archive size does not match its summary", nil)
			return
		}
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			Failure(ctx, 500, "EXPORT_UNAVAILABLE", "archive could not be read", nil)
			return
		}
		ctx.Header("Content-Type", "application/zip")
		ctx.Header("Content-Disposition", `attachment; filename="`+filename+`"`)
		ctx.Header("Content-Length", strconv.FormatInt(info.Size(), 10))
		ctx.Header("Cache-Control", "no-store")
		ctx.Header("Accept-Ranges", "none")
		ctx.Header("X-Archive-Format", format)
		if summary.ArchiveSHA256 != "" {
			ctx.Header("X-Archive-SHA256", summary.ArchiveSHA256)
		}
		writer := &mediaDeadlineWriter{ResponseWriter: ctx.Writer, controller: controller, idle: idle, end: time.Now().Add(mediaTransferMax)}
		if _, err := io.CopyBuffer(writer, file, make([]byte, 128<<10)); err != nil {
			panic(http.ErrAbortHandler)
		}
	}
}

type exportContextWriter struct {
	ctx    context.Context
	writer io.Writer
}

func (writer *exportContextWriter) Write(data []byte) (int, error) {
	if err := writer.ctx.Err(); err != nil {
		return 0, err
	}
	return writer.writer.Write(data)
}
