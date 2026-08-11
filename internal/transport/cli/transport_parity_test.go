package cli

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func canonicalParityDocument(t *testing.T, output commandOutput) map[string]any {
	t.Helper()
	if output.stderr != "" {
		t.Fatalf("machine command wrote stderr: %q", output.stderr)
	}
	document := decodeOneDocument(t, output.stdout)
	if meta, ok := document["meta"].(map[string]any); ok {
		for _, key := range []string{"request_id", "duration_ms", "transport"} {
			delete(meta, key)
		}
		if len(meta) == 0 {
			delete(document, "meta")
		}
	}
	return document
}

func assertCanonicalParity(t *testing.T, direct, api commandOutput) {
	t.Helper()
	if direct.exit != api.exit {
		t.Fatalf("exit mismatch: direct=%d api=%d\ndirect=%s\napi=%s", direct.exit, api.exit, direct.stdout, api.stdout)
	}
	directDocument := canonicalParityDocument(t, direct)
	apiDocument := canonicalParityDocument(t, api)
	if !reflect.DeepEqual(directDocument, apiDocument) {
		t.Fatalf("canonical envelope mismatch\ndirect=%#v\napi=%#v", directDocument, apiDocument)
	}
}

func TestDirectAPIErrorParityPreservesCompleteCanonicalErrors(t *testing.T) {
	t.Run("post and media", func(t *testing.T) {
		fixture := newPostCLIFixture(t)
		application := momentTestApplication()
		t.Setenv("MOMENT_TOKEN", fixture.token)
		missingPost := "01ARZ3NDEKTSV4RRFFQ69G5FAY"
		assertCanonicalParity(t,
			execute(application, "", "post", "get", missingPost, "--data-dir", fixture.dataDir, "--json"),
			execute(application, "", "--url", fixture.server.URL, "post", "get", missingPost, "--json"),
		)
		assertCanonicalParity(t,
			execute(application, "", "post", "create", "--content", "![external](https://example.test/x.png)", "--data-dir", fixture.dataDir, "--json"),
			execute(application, "", "--url", fixture.server.URL, "post", "create", "--content", "![external](https://example.test/x.png)", "--json"),
		)
		missingMedia := "01ARZ3NDEKTSV4RRFFQ69G5FAX"
		directPath := t.TempDir() + "/missing.png"
		apiPath := directPath
		assertCanonicalParity(t,
			execute(application, "", "media", "get", missingMedia, "--output", directPath, "--data-dir", fixture.dataDir, "--json"),
			execute(application, "", "--url", fixture.server.URL, "media", "get", missingMedia, "--output", apiPath, "--json"),
		)
		assertCanonicalParity(t,
			execute(application, "", "site", "update", "--avatar-media-id", missingMedia, "--data-dir", fixture.dataDir, "--json"),
			execute(application, "", "--url", fixture.server.URL, "site", "update", "--avatar-media-id", missingMedia, "--json"),
		)
	})

	t.Run("comment", func(t *testing.T) {
		fixture := newCommentCLIFixture(t)
		application := momentTestApplication()
		t.Setenv("MOMENT_TOKEN", fixture.token)
		missingComment := "01ARZ3NDEKTSV4RRFFQ69G5FAX"
		assertCanonicalParity(t,
			execute(application, "", "comment", "delete", missingComment, "--yes", "--data-dir", fixture.dataDir, "--json"),
			execute(application, "", "--url", fixture.server.URL, "comment", "delete", missingComment, "--yes", "--json"),
		)
	})
}

func TestParityNormalizationNeverRemovesSubstantiveFields(t *testing.T) {
	direct := commandOutput{exit: ExitSuccess, stdout: `{"ok":true,"data":{"id":"direct"},"meta":{"request_id":"a","duration_ms":1,"next_cursor":"cursor-a"}}`}
	api := commandOutput{exit: ExitSuccess, stdout: `{"ok":true,"data":{"id":"api"},"meta":{"request_id":"b","duration_ms":2,"next_cursor":"cursor-b","transport":"http"}}`}
	directDocument := canonicalParityDocument(t, direct)
	apiDocument := canonicalParityDocument(t, api)
	if reflect.DeepEqual(directDocument, apiDocument) || directDocument["data"].(map[string]any)["id"] != "direct" ||
		directDocument["meta"].(map[string]any)["next_cursor"] != "cursor-a" || apiDocument["meta"].(map[string]any)["next_cursor"] != "cursor-b" {
		t.Fatalf("normalization hid substantive fields: direct=%#v api=%#v", directDocument, apiDocument)
	}
}

func TestAllAPIFamiliesRejectIncompleteSharedErrorEnvelope(t *testing.T) {
	const token = "CANARY_SHARED_OWNER_TOKEN"
	t.Setenv("MOMENT_TOKEN", token)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusUnauthorized)
		_, _ = writer.Write([]byte(`{"error":{"code":"UNAUTHORIZED","message":"","details":null,"request_id":""}}`))
	}))
	defer server.Close()

	mediaOutput := filepath.Join(t.TempDir(), "media.png")
	tests := []struct {
		name string
		args []string
	}{
		{name: "owner", args: []string{"owner", "status", "--json"}},
		{name: "post", args: []string{"post", "get", "01ARZ3NDEKTSV4RRFFQ69G5FAV", "--json"}},
		{name: "comment", args: []string{"comment", "list", "01ARZ3NDEKTSV4RRFFQ69G5FAV", "--json"}},
		{name: "media", args: []string{"media", "get", "01ARZ3NDEKTSV4RRFFQ69G5FAV", "--output", mediaOutput, "--json"}},
		{name: "search", args: []string{"search", "query", "--query", "moment", "--json"}},
		{name: "site", args: []string{"site", "get", "--json"}},
		{name: "backup", args: []string{"backup", "status", "--json"}},
		{name: "doctor", args: []string{"doctor", "--json"}},
	}
	application := momentTestApplication()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			args := append([]string{"--url", server.URL}, test.args...)
			output := execute(application, "", args...)
			document := decodeOneDocument(t, output.stdout)
			if output.exit != ExitUnavailable || output.stderr != "" ||
				document["error"].(map[string]any)["code"] != "DEPENDENCY_INVALID_RESPONSE" ||
				strings.Contains(output.stdout+output.stderr, token) {
				t.Fatalf("output=%#v document=%#v", output, document)
			}
		})
	}
}
