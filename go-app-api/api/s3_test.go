package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// AWS's published SigV4 example "GET Bucket Lifecycle" (S3 API reference,
// "Signature Calculations for the Authorization Header").
func TestS3SignKnownAnswer(t *testing.T) {
	s := &S3{Region: "us-east-1", AccessKey: "AKIAIOSFODNN7EXAMPLE", SecretKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"}
	req, _ := http.NewRequest(http.MethodGet, "https://examplebucket.s3.amazonaws.com/?lifecycle=", nil)
	s.sign(req, nil, time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC))
	const want = "Signature=fea454ca298b7da1c68078a5d1bdbfbbe0d65c699e0f91ac7a200a0136783543"
	if got := req.Header.Get("Authorization"); !strings.HasSuffix(got, want) {
		t.Fatalf("Authorization = %q, want suffix %q", got, want)
	}
}

func TestPNGJobUploadsPages(t *testing.T) {
	var mu sync.Mutex
	puts := map[string]int{} // path → body size
	fails := 1               // first PUT gets a 503: must be retried
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		defer mu.Unlock()
		if fails > 0 {
			fails--
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if r.Method != http.MethodPut || !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 Credential=ak/") {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		puts[r.URL.Path] = len(body)
	}))
	defer fake.Close()

	srv, _, pool := testServer(t)
	pool.S3 = &S3{Endpoint: fake.URL, Region: "us-east-1", Bucket: "bkt", AccessKey: "ak", SecretKey: "sk", Client: fake.Client()}
	h := srv.Handler()
	pdf := fixturePDF(t)

	if res := postJob(t, h, pdf, map[string]string{"key_prefix": "../x"}); res.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad key_prefix: %d, want 400", res.StatusCode)
	}

	res := postJob(t, h, pdf, map[string]string{"dpi": "72", "pages": "1", "key_prefix": "docs/7"})
	var created map[string]string
	_ = readJSON(res.Body, &created)
	v := waitDone(t, h, created["id"])
	if v.Status != StatusDone || len(v.Pages) != 1 || v.Pages[0].Key != "docs/7-1.png" {
		t.Fatalf("status=%s err=%s pages=%+v", v.Status, v.Error, v.Pages)
	}
	mu.Lock()
	n := puts["/bkt/docs/7-1.png"]
	mu.Unlock()
	if n == 0 {
		t.Fatalf("page not uploaded; puts=%v", puts)
	}

	// max_pages below the document's page count fails the job up front
	res = postJob(t, h, readFixture(t, "broken-xref.pdf"), map[string]string{"max_pages": "1"})
	_ = readJSON(res.Body, &created)
	if v = waitDone(t, h, created["id"]); v.Status != StatusFailed || !strings.Contains(v.Error, "limit is 1") {
		t.Fatalf("max_pages: status=%s err=%q", v.Status, v.Error)
	}

	// no prefix → job ID; upload refused → page (and job) failed, no key
	pool.S3.AccessKey = "wrong"
	res = postJob(t, h, pdf, map[string]string{"dpi": "72", "pages": "1"})
	_ = readJSON(res.Body, &created)
	v = waitDone(t, h, created["id"])
	if v.Status != StatusFailed || v.Pages[0].Key != "" || !strings.Contains(v.Pages[0].Error, "s3 put "+created["id"]+"-1.png") {
		t.Fatalf("status=%s pages=%+v", v.Status, v.Pages)
	}
}
