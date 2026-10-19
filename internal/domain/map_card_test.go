package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMapCardRoundTrip(t *testing.T) {
	raw := `{"places":[{"name":"银座","display_name":"日本东京银座","latitude":35.6712345,"longitude":139.7654321,"coordinate_system":"wgs84"}],"connect_points":false}`
	card, err := ParseMapCard([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if card.Places[0].Point.LatitudeE7 != 356712345 {
		t.Fatal("coordinate precision lost")
	}
	data, err := json.Marshal(card)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != raw {
		t.Fatalf("round trip changed snapshot: %s", data)
	}
	clone := CloneMapCard(card)
	clone.Places[0].Name = "changed"
	if card.Places[0].Name != "银座" {
		t.Fatal("clone shares places")
	}
	if !strings.Contains(card.Text(), "银座") {
		t.Fatal("missing searchable place text")
	}
}

func TestMapCardRejectsInvalidSnapshots(t *testing.T) {
	point := `{"name":"地点","display_name":"地点","latitude":31,"longitude":121,"coordinate_system":"gcj02"}`
	for _, raw := range []string{
		`null`, `{"places":[]}`, `{"places":[` + point + `],"connect_points":true}`,
		`{"places":[` + strings.Replace(point, `"latitude":31`, `"latitude":91`, 1) + `]}`,
		`{"places":[` + strings.Replace(point, `"latitude":31`, `"latitude":31,"latitude":32`, 1) + `]}`,
		`{"places":[` + point + `],"unexpected":true}`,
		`{"places":[` + strings.TrimSuffix(strings.Repeat(point+",", 21), ",") + `]}`,
	} {
		if _, err := ParseMapCard([]byte(raw)); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	card, err := ParseMapCard([]byte(`{"places":[` + point + `]}`))
	if err != nil || card.ConnectPoints || card.Places[0].Point.System != CoordinateGCJ02 {
		t.Fatal("valid source coordinates or default connection changed", err)
	}
}
