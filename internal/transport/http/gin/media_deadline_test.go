package ginhttp

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	gin "github.com/gin-gonic/gin"
)

func TestMediaDeadlinesAllowProgressPastServerBudget(t *testing.T) {
	router := gin.New()
	router.POST("/upload", mediaDeadlines(true, 250*time.Millisecond, 5*time.Second), func(ctx *gin.Context) {
		data, err := io.ReadAll(ctx.Request.Body)
		if err != nil {
			ctx.String(408, "read failed")
			return
		}
		ctx.Data(200, "application/octet-stream", data)
	})
	router.GET("/download", mediaDeadlines(false, 250*time.Millisecond, 5*time.Second), func(ctx *gin.Context) {
		for range 8 {
			time.Sleep(40 * time.Millisecond)
			_, _ = ctx.Writer.Write([]byte("x"))
			ctx.Writer.Flush()
		}
	})
	server := httptest.NewUnstartedServer(router)
	server.Config.ReadTimeout = 100 * time.Millisecond
	server.Config.WriteTimeout = 100 * time.Millisecond
	server.Start()
	defer server.Close()
	reader, writer := io.Pipe()
	go func() {
		defer func() { _ = writer.Close() }()
		for range 8 {
			time.Sleep(40 * time.Millisecond)
			if _, err := writer.Write([]byte("x")); err != nil {
				return
			}
		}
	}()
	response, err := server.Client().Post(server.URL+"/upload", "application/octet-stream", reader)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil || response.StatusCode != 200 || !bytes.Equal(body, []byte("xxxxxxxx")) {
		t.Fatalf("upload: %d %q %v", response.StatusCode, body, err)
	}
	response, err = server.Client().Get(server.URL + "/download")
	if err != nil {
		t.Fatal(err)
	}
	body, err = io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil || string(body) != "xxxxxxxx" {
		t.Fatalf("download: %q %v", body, err)
	}
}

func TestMediaUploadIdleTimeoutIsNotMissingFile(t *testing.T) {
	_, store, _, _ := newMediaHTTPFixture(t)
	router := gin.New()
	router.POST("/upload", mediaDeadlines(true, 60*time.Millisecond, time.Second), uploadMedia(store))
	server := httptest.NewServer(router)
	defer server.Close()
	reader, writer := io.Pipe()
	defer func() { _ = reader.Close(); _ = writer.Close() }()
	go func() {
		_, _ = io.WriteString(writer, "--boundary\r\nContent-Disposition: form-data; name=\"file\"; filename=\"a.png\"\r\n\r\nx")
		time.Sleep(200 * time.Millisecond)
		_ = writer.Close()
	}()
	request, _ := http.NewRequest(http.MethodPost, server.URL+"/upload", reader)
	request.Header.Set("Content-Type", "multipart/form-data; boundary=boundary")
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != 408 || !bytes.Contains(body, []byte("MEDIA_UPLOAD_TIMEOUT")) {
		t.Fatalf("%d %s", response.StatusCode, body)
	}
}
