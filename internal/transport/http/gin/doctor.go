package ginhttp

import (
	"context"
	"errors"
	"net/http"

	gin "github.com/gin-gonic/gin"
	"github.com/moment/moment/internal/doctor"
)

// DoctorReporter is the transport-facing read-only diagnostic port.
type DoctorReporter interface {
	Run(context.Context, doctor.Mode) (doctor.Report, error)
}

// RegisterDoctorRoutes exposes only the explicitly allowlisted summary mode.
// Deep topology checks remain a local/direct CLI capability.
func RegisterDoctorRoutes(engine *gin.Engine, reporter DoctorReporter, gate *Gate) {
	owner := engine.Group("/api/v1/owner")
	owner.Use(gate.RequireOwner())
	owner.GET("/doctor", doctorSummary(reporter))
}

func doctorSummary(reporter DoctorReporter) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		if ctx.Request.URL.RawQuery != "" {
			Failure(ctx, http.StatusBadRequest, "INVALID_ARGUMENT", "doctor summary does not accept query parameters", nil)
			return
		}
		if ctx.Request.Body != nil && (ctx.Request.ContentLength != 0 || len(ctx.Request.TransferEncoding) != 0) {
			Failure(ctx, http.StatusBadRequest, "INVALID_ARGUMENT", "doctor summary does not accept a request body", nil)
			return
		}
		report, err := reporter.Run(ctx.Request.Context(), doctor.ModeSummary)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				Failure(ctx, http.StatusRequestTimeout, "TIMEOUT", "doctor summary did not complete", nil)
				return
			}
			Failure(ctx, http.StatusServiceUnavailable, "DOCTOR_UNAVAILABLE", "doctor summary could not be completed safely", nil)
			return
		}
		if err := doctor.ValidateReport(report, doctor.ModeSummary); err != nil {
			Failure(ctx, http.StatusInternalServerError, "DOCTOR_INVALID_REPORT", "doctor summary produced an invalid report", nil)
			return
		}
		Success(ctx, http.StatusOK, report, nil)
	}
}
