package app

import (
	"github.com/moment/moment/internal/domain"
)

// MediaUsageProjection is one public, ordered use of managed media enriched
// with the authoritative display dimensions stored for the underlying blob.
// Write commands deliberately continue to use domain.EntryMediaUsage so
// layout-only fields cannot leak into mutation contracts.
type MediaUsageProjection struct {
	MediaID  domain.ID
	Position int
	AltText  *string
	MIMEType string
	Width    uint32
	Height   uint32
}

func cloneMediaUsageProjections(usages []MediaUsageProjection) []MediaUsageProjection {
	cloned := make([]MediaUsageProjection, len(usages))
	for index, usage := range usages {
		cloned[index] = usage
		if usage.AltText != nil {
			alt := *usage.AltText
			cloned[index].AltText = &alt
		}
	}
	return cloned
}

func mediaUsageInputs(usages []MediaUsageProjection) []domain.EntryMediaUsage {
	inputs := make([]domain.EntryMediaUsage, len(usages))
	for index, usage := range usages {
		inputs[index] = domain.EntryMediaUsage{
			MediaID:  usage.MediaID,
			Position: usage.Position,
			AltText:  usage.AltText,
		}
	}
	return inputs
}

func validatePostMediaProjections(usages []MediaUsageProjection) error {
	if err := domain.ValidateEntryMediaUsages(mediaUsageInputs(usages)); err != nil {
		return err
	}
	return validateMediaProjectionDimensions(usages)
}

func validateCommentMediaProjections(usages []MediaUsageProjection) error {
	if err := domain.ValidateCommentMediaUsages(mediaUsageInputs(usages)); err != nil {
		return err
	}
	return validateMediaProjectionDimensions(usages)
}

func validateMediaProjectionDimensions(usages []MediaUsageProjection) error {
	for index, usage := range usages {
		if usage.Width == 0 || usage.Height == 0 || (usage.MIMEType != "image/jpeg" && usage.MIMEType != "image/png" && usage.MIMEType != "image/webp" && usage.MIMEType != "image/gif" && usage.MIMEType != "image/heic" && usage.MIMEType != "video/mp4") {
			return domain.NewError(domain.CodeDataIntegrityFailed, "stored media dimensions are invalid", map[string]any{
				"field":    "media",
				"index":    index,
				"media_id": usage.MediaID.String(),
			})
		}
	}
	return nil
}
