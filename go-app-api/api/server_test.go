package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBearerToken(t *testing.T) {
	srv, _, _ := testServerToken(t, "s3cret")
	h := srv.Handler()
	pdf := fixturePDF(t)

	do := func(req *http.Request, auth string) *httptest.ResponseRecorder {
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	if rec := do(httptest.NewRequest(http.MethodGet, "/healthz", nil), ""); rec.Code != http.StatusOK {
		t.Fatalf("healthz without token: %d", rec.Code)
	}
	for _, auth := range []string{"", "Bearer wrong", "s3cret", "Bearer s3cret "} {
		if rec := do(newJobRequest(t, pdf, nil), auth); rec.Code != http.StatusUnauthorized {
			t.Errorf("POST with auth %q: %d, want 401", auth, rec.Code)
		}
	}
	if rec := do(httptest.NewRequest(http.MethodGet, "/v1/jobs/x", nil), ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("GET job without token: %d, want 401", rec.Code)
	}

	rec := do(newJobRequest(t, pdf, map[string]string{"dpi": "72"}), "Bearer s3cret")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("POST with token: %d %s", rec.Code, rec.Body)
	}
	var created map[string]string
	if err := readJSON(rec.Body, &created); err != nil {
		t.Fatal(err)
	}
	rec = do(httptest.NewRequest(http.MethodGet, "/v1/jobs/"+created["id"], nil), "Bearer s3cret")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET job with token: %d", rec.Code)
	}
}

func TestMaxDPI(t *testing.T) {
	srv, _, _ := testServer(t)
	h := srv.Handler()
	pdf := fixturePDF(t)
	capDPI := DefaultLimits().MaxDPI

	for _, f := range []map[string]string{
		{"dpi": "301"},
		{"dpi": "1200"},
		{"scale": "4.2"}, // 302.4 dpi: scale must not bypass the cap
		{"dpi": "NaN"},
	} {
		res := postJob(t, h, pdf, f)
		b, _ := io.ReadAll(res.Body)
		if res.StatusCode != http.StatusBadRequest || !strings.Contains(string(b), "out of range") {
			t.Errorf("%v: %d %s, want 400 out of range", f, res.StatusCode, b)
		}
	}

	res := postJob(t, h, pdf, map[string]string{"dpi": "300"})
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("dpi at cap %g: %d", capDPI, res.StatusCode)
	}
	var created map[string]string
	_ = readJSON(res.Body, &created)
	if v := waitDone(t, h, created["id"]); v.Status != StatusDone || v.Options.DPI != capDPI {
		t.Fatalf("status=%s dpi=%g err=%s", v.Status, v.Options.DPI, v.Error)
	}
}

func TestNewStoreRemovesStaleJobDirs(t *testing.T) {
	dir := t.TempDir()
	stale := filepath.Join(dir, NewJobID())
	keep := []string{".gitignore", "notes.txt", "not-a-job-dir"}
	if err := os.MkdirAll(stale, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stale, "input.pdf"), []byte("%PDF"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range keep[:2] {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, keep[2]), 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := NewStore(dir, time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale job dir still present (err=%v)", err)
	}
	for _, name := range keep {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s should be kept: %v", name, err)
		}
	}
}
