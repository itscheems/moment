package mediatype

import "testing"

func TestCapabilitiesKeepContainerSpecificOriginalContracts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		mimeType  string
		extension string
		kind      Kind
		maxBytes  int64
	}{
		{mimeType: JPEG, extension: "jpg", kind: Image, maxBytes: MaxImageBytes},
		{mimeType: HEIC, extension: "heic", kind: Image, maxBytes: MaxImageBytes},
		{mimeType: MP4, extension: "mp4", kind: Video, maxBytes: MaxVideoBytes},
		{mimeType: QuickTime, extension: "mov", kind: Video, maxBytes: MaxVideoBytes},
	}
	for _, test := range tests {
		test := test
		t.Run(test.mimeType, func(t *testing.T) {
			t.Parallel()
			capability, ok := Lookup(test.mimeType)
			if !ok {
				t.Fatalf("Lookup(%q) was not supported", test.mimeType)
			}
			if capability.Extension != test.extension || capability.Kind != test.kind || capability.MaxBytes != test.maxBytes {
				t.Fatalf("Lookup(%q) = %+v", test.mimeType, capability)
			}
		})
	}
}

func TestVideoOriginalBudgetIs300MiB(t *testing.T) {
	t.Parallel()

	if MaxVideoBytes != 300<<20 || MaxInputBytes != MaxVideoBytes {
		t.Fatalf("video budget = %d input budget = %d", MaxVideoBytes, MaxInputBytes)
	}
}

func TestUnknownCapabilityIsRejected(t *testing.T) {
	t.Parallel()

	if IsSupported("video/unknown") || IsVideo("video/unknown") {
		t.Fatal("unknown video MIME type must not be accepted")
	}
	if _, ok := Extension("video/unknown"); ok {
		t.Fatal("unknown video MIME type must not have a canonical extension")
	}
	if _, ok := MaxBytes("video/unknown"); ok {
		t.Fatal("unknown video MIME type must not have a byte budget")
	}
}
