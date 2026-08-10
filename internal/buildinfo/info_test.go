package buildinfo

import (
	"encoding/json"
	"testing"
)

func TestCurrentHasStableJSONFields(t *testing.T) {
	data, err := json.Marshal(Current())
	if err != nil {
		t.Fatal(err)
	}

	var got map[string]string
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"version", "commit", "build_date"} {
		if _, ok := got[key]; !ok {
			t.Fatalf("missing JSON field %q in %s", key, data)
		}
	}
}
