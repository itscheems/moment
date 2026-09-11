package capabilities

import (
	"testing"

	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/mediatype"
)

func TestDescriptionUsesAuthoritativeLimitsAndDoesNotShareMutableSlices(t *testing.T) {
	got := Describe(nil)
	if got.Limits.MaxPostCodePoints != domain.MaxContentCodePoints || got.Limits.MaxVideoBytes != mediatype.MaxVideoBytes || got.TextLengthUnit != "unicode_code_points" {
		t.Fatalf("%+v", got)
	}
	got.Limits.AcceptedImageTypes[0] = "invalid"
	if Describe(nil).Limits.AcceptedImageTypes[0] == "invalid" {
		t.Fatal("shared mutable capability list")
	}
}
