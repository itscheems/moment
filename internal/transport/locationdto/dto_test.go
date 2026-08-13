package locationdto

import (
	"encoding/json"
	"testing"
)

const canonicalLocation = `{"name":"静安寺","display_name":"上海市静安区南京西路1686号","country_code":"CN","country":"中国","region":"上海市","city":"上海市","district":"静安区","category":"风景名胜","provider":"amap","provider_place_id":"B0FFF","latitude":31.223186,"longitude":121.44526,"coordinate_system":"gcj02"}`

func TestLocationStrictJSONRoundTrip(t *testing.T) {
	var wire Location
	if err := json.Unmarshal([]byte(canonicalLocation), &wire); err != nil {
		t.Fatal(err)
	}
	location, err := wire.Domain()
	if err != nil || location.Name != "静安寺" || location.Point == nil || location.Point.LatitudeE7 != 312231860 {
		t.Fatalf("location = %#v, %v", location, err)
	}
	roundTrip := FromDomain(location)
	if err := Validate(roundTrip); err != nil {
		t.Fatal(err)
	}
}

func TestLocationRejectsPartialDuplicateAndUnknownJSON(t *testing.T) {
	for _, value := range []string{
		`{"name":"only"}`,
		`{"name":"one","name":"two"}`,
		`{"name":"a","display_name":"a","country_code":"","country":"","region":"","city":"","district":"","category":"","provider":"manual","provider_place_id":"","latitude":null,"longitude":null,"coordinate_system":null,"extra":true}`,
	} {
		var wire Location
		if err := json.Unmarshal([]byte(value), &wire); err == nil {
			t.Fatalf("accepted %s", value)
		}
	}
}

func TestLocationRejectsPartialCoordinates(t *testing.T) {
	var wire Location
	value := `{"name":"a","display_name":"a","country_code":"","country":"","region":"","city":"","district":"","category":"","provider":"manual","provider_place_id":"","latitude":1,"longitude":null,"coordinate_system":null}`
	if err := json.Unmarshal([]byte(value), &wire); err != nil {
		t.Fatal(err)
	}
	if _, err := wire.Domain(); err == nil {
		t.Fatal("partial coordinates were accepted")
	}
}
