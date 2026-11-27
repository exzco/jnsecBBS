package handler_test

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/exzco/bbs/internal/models"
)

func TestUploadImageAndAttachment(t *testing.T) {
	t.Cleanup(func() {
		_ = os.RemoveAll("assets")
	})
	g, _, router := profileTestServer(t)
	_ = g.AutoMigrate(&models.UploadFile{})

	// 1. Upload an image
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "test-photo.png")
	if err != nil {
		t.Fatal(err)
	}
	// fake png bytes
	_, _ = part.Write([]byte("\x89PNG\r\n\x1a\nfake image data"))
	_ = writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/upload", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var res struct {
		URL      string `json:"url"`
		UUID     string `json:"uuid"`
		Name     string `json:"name"`
		IsImage  bool   `json:"isImage"`
		Markdown string `json:"markdown"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if !res.IsImage || !strings.Contains(res.Markdown, "![test-photo.png]") {
		t.Fatalf("unexpected image upload response: %+v", res)
	}
	if res.UUID == "" || !strings.HasPrefix(res.URL, "/assets/files/") {
		t.Fatalf("invalid url or uuid: %+v", res)
	}

	// 2. Download via /api/fof/download/:uuid
	reqDl := httptest.NewRequest(http.MethodGet, "/api/fof/download/"+res.UUID, nil)
	wDl := httptest.NewRecorder()
	router.ServeHTTP(wDl, reqDl)
	if wDl.Code != http.StatusOK {
		t.Fatalf("expected 200 downloading file, got %d: %s", wDl.Code, wDl.Body.String())
	}
	if !strings.Contains(wDl.Header().Get("Content-Disposition"), "test-photo.png") {
		t.Fatalf("unexpected Content-Disposition: %s", wDl.Header().Get("Content-Disposition"))
	}

	// 3. Upload a document (zip)
	body.Reset()
	writer = multipart.NewWriter(&body)
	part, err = writer.CreateFormFile("file", "archive.zip")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte("PK\x03\x04fake zip content"))
	_ = writer.Close()

	reqZip := httptest.NewRequest(http.MethodPost, "/api/upload", &body)
	reqZip.Header.Set("Content-Type", writer.FormDataContentType())
	wZip := httptest.NewRecorder()
	router.ServeHTTP(wZip, reqZip)

	if wZip.Code != http.StatusOK {
		t.Fatalf("expected 200 for zip upload, got %d: %s", wZip.Code, wZip.Body.String())
	}
	var resZip struct {
		URL      string `json:"url"`
		Name     string `json:"name"`
		IsImage  bool   `json:"isImage"`
		Markdown string `json:"markdown"`
	}
	if err := json.Unmarshal(wZip.Body.Bytes(), &resZip); err != nil {
		t.Fatal(err)
	}
	if resZip.IsImage || !strings.Contains(resZip.Markdown, "[📎 archive.zip") {
		t.Fatalf("unexpected zip upload response: %+v", resZip)
	}

	// 4. Reject dangerous file
	body.Reset()
	writer = multipart.NewWriter(&body)
	part, _ = writer.CreateFormFile("file", "shell.php")
	_, _ = part.Write([]byte("<?php echo 1;"))
	_ = writer.Close()

	reqBad := httptest.NewRequest(http.MethodPost, "/api/upload", &body)
	reqBad.Header.Set("Content-Type", writer.FormDataContentType())
	wBad := httptest.NewRecorder()
	router.ServeHTTP(wBad, reqBad)
	if wBad.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad extension, got %d", wBad.Code)
	}
}
