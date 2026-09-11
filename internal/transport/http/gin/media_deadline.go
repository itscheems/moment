package ginhttp

import (
	"errors"
	"io"
	"net/http"
	"time"

	gin "github.com/gin-gonic/gin"
)

const (
	mediaTransferIdle   = 30 * time.Second
	mediaTransferMax    = 2 * time.Hour
	mediaPreparationMax = 5 * time.Minute
)

// mediaDeadlines replaces the server's whole-request IO deadlines only for
// media routes. Progress renews the idle budget; total transfer time stays bounded.
func mediaDeadlines(upload bool, idle, maximum time.Duration) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		controller := http.NewResponseController(ctx.Writer)
		end := time.Now().Add(maximum)
		if err := controller.SetWriteDeadline(time.Now().Add(mediaPreparationMax)); err != nil && !errors.Is(err, http.ErrNotSupported) {
			Failure(ctx, http.StatusInternalServerError, "MEDIA_UNAVAILABLE", "media transfer deadline is unavailable", nil)
			return
		}
		if upload {
			// No response is written while the body arrives. Keep the write
			// deadline alive for the same bounded transfer, then narrow it again
			// before CPU/processor work begins in uploadMedia.
			_ = controller.SetWriteDeadline(end)
			ctx.Request.Body = &mediaDeadlineReader{ReadCloser: ctx.Request.Body, controller: controller, idle: idle, end: end}
		}
		ctx.Writer = &mediaDeadlineWriter{ResponseWriter: ctx.Writer, controller: controller, idle: idle, end: end}
		ctx.Next()
	}
}

func transferDeadline(idle time.Duration, end time.Time) time.Time {
	return minTime(time.Now().Add(idle), end)
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

type mediaDeadlineReader struct {
	io.ReadCloser
	controller *http.ResponseController
	idle       time.Duration
	end        time.Time
}

func (reader *mediaDeadlineReader) Read(data []byte) (int, error) {
	if err := reader.controller.SetReadDeadline(transferDeadline(reader.idle, reader.end)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return 0, err
	}
	return reader.ReadCloser.Read(data)
}

type mediaDeadlineWriter struct {
	gin.ResponseWriter
	controller *http.ResponseController
	idle       time.Duration
	end        time.Time
}

func (writer *mediaDeadlineWriter) Unwrap() http.ResponseWriter { return writer.ResponseWriter }

func (writer *mediaDeadlineWriter) Write(data []byte) (int, error) {
	if err := writer.controller.SetWriteDeadline(transferDeadline(writer.idle, writer.end)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return 0, err
	}
	return writer.ResponseWriter.Write(data)
}

func (writer *mediaDeadlineWriter) WriteString(value string) (int, error) {
	return writer.Write([]byte(value))
}
