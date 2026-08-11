package cli

import (
	"encoding/json"
	"io"
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
	mediaOutput := filepath.Join(t.TempDir(), "media.png")
	families := []struct {
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
	responses := []struct {
		name string
		body string
	}{
		{name: "incomplete", body: `{"error":{"code":"UNAUTHORIZED","message":"","details":null,"request_id":""}}`},
		{name: "lowercase code", body: `{"error":{"code":"unauthorized","message":"denied","details":{},"request_id":"req"}}`},
		{name: "illegal code", body: `{"error":{"code":"UNAUTHORIZED!","message":"denied","details":{},"request_id":"req"}}`},
	}
	for _, response := range responses {
		t.Run(response.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				writer.WriteHeader(http.StatusUnauthorized)
				_, _ = writer.Write([]byte(response.body))
			}))
			defer server.Close()
			for _, family := range families {
				t.Run(family.name, func(t *testing.T) {
					args := append([]string{"--url", server.URL}, family.args...)
					output := execute(application, "", args...)
					document := decodeOneDocument(t, output.stdout)
					if output.exit != ExitUnavailable || output.stderr != "" ||
						document["error"].(map[string]any)["code"] != "DEPENDENCY_INVALID_RESPONSE" ||
						strings.Contains(output.stdout+output.stderr, token) {
						t.Fatalf("output=%#v document=%#v", output, document)
					}
				})
			}
		})
	}
}

func TestSharedSuccessAdaptersRequireServerRequestID(t *testing.T) {
	application := momentTestApplication()
	postFixture := newPostCLIFixture(t)
	post := commandData(t, execute(application, "", "post", "create", "--data-dir", postFixture.dataDir,
		"--content", "strict response metadata", "--idempotency-key", "strict-response-meta", "--json"))
	postID := post["id"].(string)
	commentFixture := newCommentCLIFixture(t)
	imagePath := writeCLIImage(t)

	missing := func(document map[string]any) { delete(document, "meta") }
	empty := func(document map[string]any) { document["meta"] = map[string]any{} }
	blank := func(document map[string]any) { document["meta"] = map[string]any{"request_id": "  "} }
	wrongType := func(document map[string]any) { document["meta"] = map[string]any{"request_id": 7} }
	tests := []struct {
		name     string
		upstream string
		token    string
		mutate   func(map[string]any)
		args     []string
	}{
		{name: "owner empty", upstream: postFixture.server.URL, token: postFixture.token, mutate: empty, args: []string{"owner", "status", "--json"}},
		{name: "site missing", upstream: postFixture.server.URL, token: postFixture.token, mutate: missing, args: []string{"site", "get", "--json"}},
		{name: "media blank", upstream: postFixture.server.URL, token: postFixture.token, mutate: blank, args: []string{"media", "add", imagePath, "--json"}},
		{name: "post wrong type", upstream: postFixture.server.URL, token: postFixture.token, mutate: wrongType, args: []string{"post", "get", postID, "--json"}},
		{name: "comment empty", upstream: commentFixture.server.URL, token: commentFixture.token, mutate: empty, args: []string{"comment", "list", commentFixture.postID, "--json"}},
		{name: "search blank", upstream: postFixture.server.URL, token: postFixture.token, mutate: blank, args: []string{"search", "query", "--query", "strict", "--json"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			proxy := newMetaMutatingProxy(t, test.upstream, test.mutate)
			defer proxy.Close()
			t.Setenv("MOMENT_TOKEN", test.token)
			args := append([]string{"--url", proxy.URL}, test.args...)
			output := execute(application, "", args...)
			document := decodeOneDocument(t, output.stdout)
			if output.exit != ExitUnavailable || output.stderr != "" ||
				document["error"].(map[string]any)["code"] != "DEPENDENCY_INVALID_RESPONSE" ||
				strings.Contains(output.stdout+output.stderr, test.token) {
				t.Fatalf("output=%#v document=%#v", output, document)
			}
		})
	}
}

func TestSharedSuccessAdaptersRequireExactHTTPStatus(t *testing.T) {
	application := momentTestApplication()
	fixture := newPostCLIFixture(t)
	t.Setenv("MOMENT_TOKEN", fixture.token)
	imagePath := writeCLIImage(t)
	added := commandData(t, execute(application, "", "media", "add", imagePath, "--data-dir", fixture.dataDir, "--json"))
	mediaID := added["media"].(map[string]any)["id"].(string)

	tests := []struct {
		name   string
		status int
		args   []string
	}{
		{name: "owner status", status: http.StatusCreated, args: []string{"owner", "status", "--json"}},
		{name: "site get", status: http.StatusCreated, args: []string{"site", "get", "--json"}},
		{name: "site update", status: http.StatusAccepted, args: []string{"site", "update", "--title", "Exact status", "--json"}},
		{name: "media add", status: http.StatusOK, args: []string{"media", "add", imagePath, "--json"}},
		{name: "media get", status: http.StatusPartialContent, args: []string{"media", "get", mediaID, "--output", filepath.Join(t.TempDir(), "media.png"), "--json"}},
		{name: "owner rotate", status: http.StatusAccepted, args: []string{"owner", "rotate-token", "--json"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			proxy := newStatusMutatingProxy(t, fixture.server.URL, test.status)
			defer proxy.Close()
			args := append([]string{"--url", proxy.URL}, test.args...)
			output := execute(application, "", args...)
			document := decodeOneDocument(t, output.stdout)
			if output.exit != ExitUnavailable || output.stderr != "" ||
				document["error"].(map[string]any)["code"] != "DEPENDENCY_INVALID_RESPONSE" ||
				strings.Contains(output.stdout+output.stderr, fixture.token) {
				t.Fatalf("output=%#v document=%#v", output, document)
			}
		})
	}
}

func TestSharedPostCommentSearchAdaptersRejectNullOrMissingCanonicalScalars(t *testing.T) {
	application := momentTestApplication()
	postFixture := newPostCLIFixture(t)
	post := commandData(t, execute(application, "", "post", "create", "--data-dir", postFixture.dataDir,
		"--content", "strict nullable scalar search", "--idempotency-key", "strict-null-scalars", "--json"))
	postID := post["id"].(string)
	const mediaID = "01ARZ3NDEKTSV4RRFFQ69G5FAZ"

	tests := []struct {
		name     string
		upstream string
		token    string
		args     []string
		mutate   func(map[string]any)
	}{
		{
			name: "post media position null", upstream: postFixture.server.URL, token: postFixture.token,
			args: []string{"post", "get", postID, "--json"},
			mutate: func(document map[string]any) {
				document["data"].(map[string]any)["media"] = []any{map[string]any{"media_id": mediaID, "position": nil, "alt_text": nil}}
			},
		},
		{
			name: "search post position null", upstream: postFixture.server.URL, token: postFixture.token,
			args: []string{"search", "query", "--query", "nullable", "--json"},
			mutate: func(document map[string]any) {
				items := document["data"].(map[string]any)["items"].([]any)
				items[0].(map[string]any)["media"] = []any{map[string]any{"media_id": mediaID, "position": nil, "alt_text": nil}}
			},
		},
	}

	commentFixture := newCommentCLIFixture(t)
	comment := commandData(t, execute(application, "", "comment", "create", commentFixture.postID, "--data-dir", commentFixture.dataDir,
		"--content", "strict comment scalar", "--idempotency-key", "strict-comment-null", "--json"))
	_ = comment
	for _, mutation := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "comment depth null", mutate: func(item map[string]any) { item["depth"] = nil }},
		{name: "comment parent and depth missing", mutate: func(item map[string]any) { delete(item, "parent_id"); delete(item, "depth") }},
	} {
		mutation := mutation
		tests = append(tests, struct {
			name     string
			upstream string
			token    string
			args     []string
			mutate   func(map[string]any)
		}{
			name: mutation.name, upstream: commentFixture.server.URL, token: commentFixture.token,
			args: []string{"comment", "list", commentFixture.postID, "--json"},
			mutate: func(document map[string]any) {
				item := document["data"].(map[string]any)["items"].([]any)[0].(map[string]any)
				mutation.mutate(item)
			},
		})
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			proxy := newMetaMutatingProxy(t, test.upstream, test.mutate)
			defer proxy.Close()
			t.Setenv("MOMENT_TOKEN", test.token)
			output := execute(application, "", append([]string{"--url", proxy.URL}, test.args...)...)
			if output.exit != ExitUnavailable || output.stderr != "" ||
				decodeOneDocument(t, output.stdout)["error"].(map[string]any)["code"] != "DEPENDENCY_INVALID_RESPONSE" {
				t.Fatalf("output=%#v", output)
			}
		})
	}
}

func newMetaMutatingProxy(t *testing.T, upstream string, mutate func(map[string]any)) *httptest.Server {
	t.Helper()
	return newSuccessMutatingProxy(t, upstream, 0, mutate)
}

func newStatusMutatingProxy(t *testing.T, upstream string, status int) *httptest.Server {
	t.Helper()
	return newSuccessMutatingProxy(t, upstream, status, nil)
}

func newSuccessMutatingProxy(t *testing.T, upstream string, status int, mutate func(map[string]any)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		forward, err := http.NewRequestWithContext(request.Context(), request.Method, upstream+request.URL.RequestURI(), request.Body)
		if err != nil {
			t.Errorf("build forwarded request: %v", err)
			writer.WriteHeader(http.StatusInternalServerError)
			return
		}
		forward.Header = request.Header.Clone()
		response, err := http.DefaultClient.Do(forward)
		if err != nil {
			t.Errorf("forward request: %v", err)
			writer.WriteHeader(http.StatusBadGateway)
			return
		}
		defer func() { _ = response.Body.Close() }()
		body, err := io.ReadAll(response.Body)
		if err != nil {
			t.Errorf("read forwarded response: %v", err)
			writer.WriteHeader(http.StatusBadGateway)
			return
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			t.Errorf("upstream did not return success: status=%d body=%q", response.StatusCode, body)
			writer.WriteHeader(http.StatusBadGateway)
			return
		}
		if mutate != nil {
			var document map[string]any
			if json.Unmarshal(body, &document) != nil {
				t.Errorf("upstream did not return a success envelope: status=%d body=%q", response.StatusCode, body)
				writer.WriteHeader(http.StatusBadGateway)
				return
			}
			mutate(document)
			body, err = json.Marshal(document)
			if err != nil {
				t.Errorf("encode mutated response: %v", err)
				writer.WriteHeader(http.StatusInternalServerError)
				return
			}
		}
		for key, values := range response.Header {
			if strings.EqualFold(key, "Content-Length") {
				continue
			}
			writer.Header()[key] = append([]string(nil), values...)
		}
		responseStatus := status
		if responseStatus == 0 {
			responseStatus = response.StatusCode
		}
		writer.WriteHeader(responseStatus)
		_, _ = writer.Write(body)
	}))
}
