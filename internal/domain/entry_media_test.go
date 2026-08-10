package domain

import (
	"strings"
	"testing"
)

func TestEntryMediaUsageOrderedAltContract(t *testing.T) {
	t.Parallel()
	id, err := ParseID("01ARZ3NDEKTSV4RRFFQ69G5FAV")
	if err != nil {
		t.Fatal(err)
	}
	empty := ""
	provided := "  猫 beside a window  "
	maximum := strings.Repeat("界", MaxMediaAltCodePoints)
	usages := make([]EntryMediaUsage, MaxEntryMediaUsages)
	for index := range usages {
		usages[index] = EntryMediaUsage{MediaID: id, Position: index}
	}
	usages[1].AltText = &empty
	usages[2].AltText = &provided
	usages[8].AltText = &maximum
	if err := ValidateEntryMediaUsages(usages); err != nil {
		t.Fatalf("valid duplicate usages with alt tri-state: %v", err)
	}
	if err := ValidateEntryMediaUsages(nil); err != nil {
		t.Fatalf("zero usages: %v", err)
	}

	cloned := CloneEntryMediaUsages(usages)
	*cloned[2].AltText = "changed"
	if *usages[2].AltText != provided {
		t.Fatal("usage clone shared an alt pointer")
	}
}

func TestEntryMediaUsageRejectsNonCanonicalValues(t *testing.T) {
	t.Parallel()
	id, _ := ParseID("01ARZ3NDEKTSV4RRFFQ69G5FAV")
	visible := "visible"
	blank := "\u3000\t"
	tooLong := strings.Repeat("界", MaxMediaAltCodePoints+1)
	maximum := make([]EntryMediaUsage, MaxEntryMediaUsages)
	for index := range maximum {
		maximum[index] = EntryMediaUsage{MediaID: id, Position: index, AltText: &visible}
	}
	tooMany := append(CloneEntryMediaUsages(maximum), EntryMediaUsage{MediaID: id, Position: MaxEntryMediaUsages})
	tests := []struct {
		name   string
		usages []EntryMediaUsage
	}{
		{name: "too many", usages: tooMany},
		{name: "gap", usages: []EntryMediaUsage{{MediaID: id, Position: 1}}},
		{name: "position nine", usages: []EntryMediaUsage{{MediaID: id, Position: 9}}},
		{name: "invalid id", usages: []EntryMediaUsage{{MediaID: "not-an-id", Position: 0}}},
		{name: "blank provided", usages: []EntryMediaUsage{{MediaID: id, Position: 0, AltText: &blank}}},
		{name: "too long", usages: []EntryMediaUsage{{MediaID: id, Position: 0, AltText: &tooLong}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if err := ValidateEntryMediaUsages(test.usages); !HasCode(err, CodeInvalidArgument) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}
