package api

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func fixturePDF(t *testing.T) []byte {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("no caller")
	}
	path := filepath.Join(filepath.Dir(file), "..", "..", "testdata", "pdfs", "vec.pdf")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("fixture missing: %v", err)
	}
	return data
}

func testServer(t *testing.T) (*Server, *Store, *Pool) {
	t.Helper()
	dir := t.TempDir()
	store, err := NewStore(dir, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	lim := DefaultLimits()
	pool := NewPool(store, 1, 2, lim.MaxPages, lim.MaxQueueDepth, 30*time.Second)
	t.Cleanup(pool.Shutdown)
	return NewServer(store, pool, lim), store, pool
}

func postJob(t *testing.T, h http.Handler, pdf []byte, fields map[string]string) *http.Response {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	fw, err := w.CreateFormFile("file", "doc.pdf")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(pdf); err != nil {
		t.Fatal(err)
	}
	for k, v := range fields {
		_ = w.WriteField(k, v)
	}
	_ = w.Close()
	req := httptest.NewRequest(http.MethodPost, "/v1/jobs", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Result()
}

func waitDone(t *testing.T, h http.Handler, id string) jobView {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		req := httptest.NewRequest(http.MethodGet, "/v1/jobs/"+id, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d", rec.Code)
		}
		var v jobView
		if err := readJSON(rec.Body, &v); err != nil {
			t.Fatal(err)
		}
		switch v.Status {
		case StatusDone, StatusPartial, StatusFailed:
			return v
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("timeout waiting for job")
	return jobView{}
}

func readJSON(r io.Reader, v any) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

func TestHTMLJobDocument(t *testing.T) {
	srv, _, _ := testServer(t)
	h := srv.Handler()
	pdf := fixturePDF(t)

	res := postJob(t, h, pdf, map[string]string{"format": "html"})
	if res.StatusCode != http.StatusAccepted {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("create: %d %s", res.StatusCode, b)
	}
	var created map[string]string
	if err := readJSON(res.Body, &created); err != nil {
		t.Fatal(err)
	}
	id := created["id"]
	view := waitDone(t, h, id)
	if view.Status != StatusDone {
		t.Fatalf("status=%s err=%s", view.Status, view.Error)
	}
	if view.DocumentURL == "" {
		t.Fatal("missing document_url")
	}
	if view.Options.Format != FormatHTML {
		t.Fatalf("format=%q", view.Options.Format)
	}

	req := httptest.NewRequest(http.MethodGet, view.DocumentURL, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("document: %d", rec.Code)
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/html") {
		t.Fatalf("content-type %q", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "<!DOCTYPE html>") || !strings.Contains(body, "data-page=") {
		t.Fatalf("bad html: %s", body[:min(200, len(body))])
	}

	// PNG page route should 404 for html jobs
	req = httptest.NewRequest(http.MethodGet, "/v1/jobs/"+id+"/pages/1.png", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for png page, got %d", rec.Code)
	}
}

func TestPNGJobUnchanged(t *testing.T) {
	srv, _, _ := testServer(t)
	h := srv.Handler()
	pdf := fixturePDF(t)

	res := postJob(t, h, pdf, map[string]string{"format": "png", "dpi": "72"})
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("create: %d", res.StatusCode)
	}
	var created map[string]string
	_ = readJSON(res.Body, &created)
	view := waitDone(t, h, created["id"])
	if view.Status != StatusDone {
		t.Fatalf("status=%s err=%s", view.Status, view.Error)
	}
	if view.DocumentURL != "" {
		t.Fatalf("png job should not have document_url: %s", view.DocumentURL)
	}
	if len(view.Pages) == 0 || view.Pages[0].URL == "" {
		t.Fatalf("missing page url: %+v", view.Pages)
	}
	req := httptest.NewRequest(http.MethodGet, view.Pages[0].URL, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Content-Type"), "image/png") {
		t.Fatalf("png fetch: %d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
}
