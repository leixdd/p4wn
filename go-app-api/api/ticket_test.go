package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	testToken  = "static-token"
	testSecret = "0123456789abcdef0123456789abcdef" // exactly MinTicketSecretLen
)

// ticketServer: bearer token + tickets, optional CORS origins.
func ticketServer(t *testing.T, lim Limits, origins ...string) (http.Handler, *Tickets, *Pool) {
	t.Helper()
	store, err := NewStore(t.TempDir(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	pool := NewPool(store, 1, 2, lim.MaxPages, lim.MaxQueueDepth, 30*time.Second)
	t.Cleanup(pool.Shutdown)
	srv := NewServer(store, pool, lim, testToken)
	tk, err := NewTickets(testSecret)
	if err != nil {
		t.Fatal(err)
	}
	srv.EnableTickets(tk)
	srv.AllowOrigins(origins)
	return srv.Handler(), tk, pool
}

func claims(jti string) TicketClaims {
	return TicketClaims{V: 1, Exp: time.Now().Add(5 * time.Minute).Unix(), JTI: jti}
}

func serve(h http.Handler, req *http.Request, auth string) *httptest.ResponseRecorder {
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// bearer adds the static token to requests that carry no Authorization.
func bearer(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			r.Header.Set("Authorization", "Bearer "+testToken)
		}
		h.ServeHTTP(w, r)
	})
}

func jobID(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	if rec.Code != http.StatusAccepted {
		t.Fatalf("POST /v1/jobs: %d %s, want 202", rec.Code, rec.Body)
	}
	var created map[string]string
	if err := readJSON(rec.Body, &created); err != nil {
		t.Fatal(err)
	}
	return created["id"]
}

func TestTicketPinsJobOptionsAndKeyPrefix(t *testing.T) {
	var mu sync.Mutex
	var puts []string
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		puts = append(puts, r.URL.Path)
		mu.Unlock()
	}))
	defer fake.Close()

	h, tk, pool := ticketServer(t, DefaultLimits())
	pool.S3 = &S3{Endpoint: fake.URL, Region: "us-east-1", Bucket: "bkt", AccessKey: "ak", SecretKey: "sk", Client: fake.Client()}
	c := claims("t-pin")
	c.KeyPrefix, c.DPI, c.Pages, c.MaxPages = "uploads/42", 72, "1", 50
	id := jobID(t, serve(h, newJobRequest(t, fixturePDF(t), nil), "Ticket "+tk.Sign(c)))

	v := waitDone(t, bearer(h), id)
	if v.Status != StatusDone || v.Options.KeyPrefix != "uploads/42" || v.Options.DPI != 72 || v.Options.MaxPages != 50 || v.Options.Pages != "1" {
		t.Fatalf("status=%s err=%s options=%+v", v.Status, v.Error, v.Options)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(puts) != 1 || puts[0] != "/bkt/uploads/42-1.png" {
		t.Fatalf("S3 puts = %v, want the ticket's prefix", puts)
	}
}

func TestTicketDefaultsWhenOptionsUnset(t *testing.T) {
	h, tk, _ := ticketServer(t, DefaultLimits())
	id := jobID(t, serve(h, newJobRequest(t, fixturePDF(t), nil), "Ticket "+tk.Sign(claims("t-def"))))
	if v := waitDone(t, bearer(h), id); v.Options.DPI != 150 || v.Options.Format != FormatPNG || v.Options.KeyPrefix != "" {
		t.Fatalf("options = %+v, want server defaults", v.Options)
	}
}

func TestTicketRefusals(t *testing.T) {
	h, tk, _ := ticketServer(t, DefaultLimits())
	pdf := fixturePDF(t)
	other, _ := NewTickets(strings.Repeat("x", 40))

	valid := tk.Sign(claims("t-valid"))
	payload, sig, _ := strings.Cut(valid, ".")
	tampered := func() string {
		c := claims("t-valid")
		c.KeyPrefix = "someone/else"
		b, _ := json.Marshal(c)
		return base64.RawURLEncoding.EncodeToString(b) + "." + sig
	}()
	rawSigned := func(payload string) string {
		p := base64.RawURLEncoding.EncodeToString([]byte(payload))
		return p + "." + base64.RawURLEncoding.EncodeToString(ticketMAC([]byte(testSecret), p))
	}
	now := time.Now().Unix()
	with := func(f func(*TicketClaims)) string {
		c := claims("t-x")
		f(&c)
		return tk.Sign(c)
	}

	for name, tc := range map[string]struct{ ticket, want string }{
		"other secret":       {other.Sign(claims("t-o")), "bad ticket signature"},
		"tampered payload":   {tampered, "bad ticket signature"},
		"no signature":       {payload, "malformed ticket"},
		"empty":              {"", "malformed ticket"},
		"three parts":        {valid + ".x", "malformed ticket"},
		"bad base64 sig":     {payload + ".***", "malformed ticket"},
		"not json":           {rawSigned("nope"), "malformed ticket payload"},
		"trailing json":      {rawSigned(`{"v":1,"exp":` + itoa(now+60) + `,"jti":"a"}{}`), "malformed ticket payload"},
		"unknown field":      {rawSigned(`{"v":1,"exp":` + itoa(now+60) + `,"jti":"a","max_files":3}`), "malformed ticket payload"},
		"wrong version":      {with(func(c *TicketClaims) { c.V = 2 }), "unsupported ticket version"},
		"no jti":             {with(func(c *TicketClaims) { c.JTI = "" }), "jti missing"},
		"bad jti":            {with(func(c *TicketClaims) { c.JTI = "a b" }), "jti missing"},
		"no exp":             {with(func(c *TicketClaims) { c.Exp = 0 }), "exp missing"},
		"expired":            {with(func(c *TicketClaims) { c.Exp = now - 31 }), "ticket expired"},
		"lifetime too long":  {with(func(c *TicketClaims) { c.Exp = now + 2*3600 }), "lifetime exceeds"},
		"negative max_bytes": {with(func(c *TicketClaims) { c.MaxBytes = -1 }), "negative"},
	} {
		rec := serve(h, newJobRequest(t, pdf, nil), "Ticket "+tc.ticket)
		if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), tc.want) {
			t.Errorf("%s: %d %s, want 401 %q", name, rec.Code, rec.Body, tc.want)
		}
	}

	// within the clock-skew tolerance: still accepted
	jobID(t, serve(h, newJobRequest(t, pdf, nil), "Ticket "+with(func(c *TicketClaims) { c.JTI, c.Exp = "t-skew", now-10 })))

	// single use
	jobID(t, serve(h, newJobRequest(t, pdf, nil), "Ticket "+valid))
	if rec := serve(h, newJobRequest(t, pdf, nil), "Ticket "+valid); rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "already used") {
		t.Fatalf("replay: %d %s, want 401 already used", rec.Code, rec.Body)
	}
	// "Ticket" is its own scheme: not a bearer token, and not case-folded
	if rec := serve(h, newJobRequest(t, pdf, nil), "Bearer "+tk.Sign(claims("t-b"))); rec.Code != http.StatusUnauthorized {
		t.Fatalf("ticket as bearer: %d", rec.Code)
	}
	if rec := serve(h, newJobRequest(t, pdf, nil), "ticket "+tk.Sign(claims("t-c"))); rec.Code != http.StatusUnauthorized {
		t.Fatalf("lower-case scheme: %d", rec.Code)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// A ticket opens POST /v1/jobs only; the static token still opens everything.
func TestTicketOnlyForCreateJob(t *testing.T) {
	h, tk, _ := ticketServer(t, DefaultLimits())
	id := jobID(t, serve(h, newJobRequest(t, fixturePDF(t), map[string]string{"dpi": "72", "pages": "1"}), "Bearer "+testToken))
	waitDone(t, bearer(h), id)

	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/v1/jobs/" + id},
		{http.MethodGet, "/v1/jobs/" + id + "/pages/1.png"},
		{http.MethodGet, "/v1/jobs/" + id + "/archive.zip"},
		{http.MethodGet, "/v1/jobs/" + id + "/document.html"},
		{http.MethodDelete, "/v1/jobs/" + id},
		{http.MethodGet, "/v1/jobs"},
		{http.MethodPut, "/v1/jobs"},
	} {
		rec := serve(h, httptest.NewRequest(route.method, route.path, nil), "Ticket "+tk.Sign(claims("t-route")))
		if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "only valid for POST /v1/jobs") {
			t.Errorf("%s %s with a ticket: %d %s, want 401", route.method, route.path, rec.Code, rec.Body)
		}
	}
	// the refused ticket was not spent: it still works where it is valid
	jobID(t, serve(h, newJobRequest(t, fixturePDF(t), nil), "Ticket "+tk.Sign(claims("t-route"))))

	for _, route := range []struct {
		method, path string
		want         int
	}{
		{http.MethodGet, "/v1/jobs/" + id, http.StatusOK},
		{http.MethodGet, "/v1/jobs/" + id + "/pages/1.png", http.StatusOK},
		{http.MethodGet, "/v1/jobs/" + id + "/archive.zip", http.StatusOK},
		{http.MethodDelete, "/v1/jobs/" + id, http.StatusNoContent},
	} {
		if rec := serve(h, httptest.NewRequest(route.method, route.path, nil), "Bearer "+testToken); rec.Code != route.want {
			t.Errorf("%s %s with the token: %d, want %d", route.method, route.path, rec.Code, route.want)
		}
	}
	if rec := serve(h, httptest.NewRequest(http.MethodGet, "/healthz", nil), ""); rec.Code != http.StatusOK {
		t.Errorf("healthz: %d", rec.Code)
	}
}

func TestTicketsDisabled(t *testing.T) {
	srv, _, _ := testServerToken(t, testToken)
	tk, _ := NewTickets(testSecret)
	rec := serve(srv.Handler(), newJobRequest(t, fixturePDF(t), nil), "Ticket "+tk.Sign(claims("t-off")))
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "not enabled") {
		t.Fatalf("tickets off: %d %s", rec.Code, rec.Body)
	}
	if _, err := NewTickets(testSecret[1:]); err == nil {
		t.Fatalf("a %d-byte secret was accepted", len(testSecret)-1)
	}
}

func TestTicketRequestTakesNoOptions(t *testing.T) {
	h, tk, _ := ticketServer(t, DefaultLimits())
	pdf := fixturePDF(t)
	c := claims("t-q")
	c.KeyPrefix = "uploads/1"

	req := newJobRequest(t, pdf, nil)
	req.URL.RawQuery = "key_prefix=uploads/2"
	if rec := serve(h, req, "Ticket "+tk.Sign(c)); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "key_prefix") {
		t.Fatalf("query option: %d %s, want 400", rec.Code, rec.Body)
	}
	c.JTI = "t-f"
	if rec := serve(h, newJobRequest(t, pdf, map[string]string{"dpi": "300"}), "Ticket "+tk.Sign(c)); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "dpi") {
		t.Fatalf("form option: %d %s, want 400", rec.Code, rec.Body)
	}
	// options the ticket pins are validated like request parameters
	c.JTI, c.DPI = "t-dpi", 1200
	if rec := serve(h, newJobRequest(t, pdf, nil), "Ticket "+tk.Sign(c)); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "ticket: dpi") {
		t.Fatalf("ticket dpi over the cap: %d %s, want 400", rec.Code, rec.Body)
	}
	c.JTI, c.DPI, c.KeyPrefix = "t-kp", 0, "../x"
	if rec := serve(h, newJobRequest(t, pdf, nil), "Ticket "+tk.Sign(c)); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "key_prefix") {
		t.Fatalf("ticket key_prefix: %d %s, want 400", rec.Code, rec.Body)
	}
}

func TestTicketLimits(t *testing.T) {
	lim := DefaultLimits()
	lim.MaxUploadBytes = 1 << 20
	h, tk, _ := ticketServer(t, lim)
	pdf := fixturePDF(t)

	// max_bytes lowers the server limit...
	c := claims("t-small")
	c.MaxBytes = int64(len(pdf)) - 1
	rec := serve(h, newJobRequest(t, pdf, nil), "Ticket "+tk.Sign(c))
	if rec.Code != http.StatusRequestEntityTooLarge || !strings.Contains(rec.Body.String(), "limit "+itoa(c.MaxBytes)+" bytes") {
		t.Fatalf("over ticket max_bytes: %d %s, want 413", rec.Code, rec.Body)
	}
	c.JTI, c.MaxBytes = "t-exact", int64(len(pdf))
	jobID(t, serve(h, newJobRequest(t, pdf, map[string]string{}), "Ticket "+tk.Sign(c)))

	// ...but never raises it
	c.JTI, c.MaxBytes = "t-big", 10<<20
	big := append(append([]byte(nil), pdf...), bytes.Repeat([]byte{'\n'}, 1<<20)...)
	if rec := serve(h, newJobRequest(t, big, nil), "Ticket "+tk.Sign(c)); rec.Code != http.StatusRequestEntityTooLarge || !strings.Contains(rec.Body.String(), "limit 1 MB") {
		t.Fatalf("ticket max_bytes above the server limit: %d %s, want 413 at 1 MB", rec.Code, rec.Body)
	}

	// max_pages fails the job before rendering
	c = claims("t-pages")
	c.MaxPages = 1
	id := jobID(t, serve(h, newJobRequest(t, readFixture(t, "broken-xref.pdf"), nil), "Ticket "+tk.Sign(c)))
	if v := waitDone(t, bearer(h), id); v.Status != StatusFailed || !strings.Contains(v.Error, "limit is 1") || v.PagesDone != 0 {
		t.Fatalf("ticket max_pages: status=%s err=%q done=%d", v.Status, v.Error, v.PagesDone)
	}
}

// countingBody records whether anything read the request body.
type countingBody struct {
	r    io.Reader
	read int
}

func (b *countingBody) Read(p []byte) (int, error) {
	n, err := b.r.Read(p)
	b.read += n
	return n, err
}

func (b *countingBody) Close() error { return nil }

func TestTicketRefusedBeforeBodyIsRead(t *testing.T) {
	lim := DefaultLimits()
	lim.MaxUploadBytes = 1 << 20
	h, tk, _ := ticketServer(t, lim)
	pdf := fixturePDF(t)
	// larger than the ticket's max_bytes plus the multipart allowance: the
	// Content-Length alone decides
	padded := append(append([]byte(nil), pdf...), bytes.Repeat([]byte{'\n'}, 80<<10)...)

	small := claims("t-cl")
	small.MaxBytes = 100
	for name, tc := range map[string]struct {
		auth string
		want int
	}{
		"bad signature": {"Ticket " + tk.Sign(claims("t-1"))[1:], http.StatusUnauthorized},
		"expired":       {"Ticket " + tk.Sign(TicketClaims{V: 1, Exp: 1, JTI: "t-2"}), http.StatusUnauthorized},
		"no auth":       {"", http.StatusUnauthorized},
		"too large":     {"Ticket " + tk.Sign(small), http.StatusRequestEntityTooLarge},
	} {
		req := newJobRequest(t, padded, nil)
		body := &countingBody{r: req.Body}
		req.Body = body
		rec := serve(h, req, tc.auth)
		if rec.Code != tc.want || body.read != 0 {
			t.Errorf("%s: %d (want %d), %d body bytes read (want 0)", name, rec.Code, tc.want, body.read)
		}
	}
}

func TestCORS(t *testing.T) {
	const site = "https://app.example.com"
	h, tk, _ := ticketServer(t, DefaultLimits(), site, "http://localhost:3000")

	preflight := func(h http.Handler, origin, method string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodOptions, "/v1/jobs", nil)
		req.Header.Set("Origin", origin)
		req.Header.Set("Access-Control-Request-Method", method)
		req.Header.Set("Access-Control-Request-Headers", "authorization")
		return serve(h, req, "")
	}

	rec := preflight(h, site, http.MethodPost)
	hd := rec.Header()
	if rec.Code != http.StatusNoContent || hd.Get("Access-Control-Allow-Origin") != site ||
		!strings.Contains(hd.Get("Access-Control-Allow-Headers"), "Authorization") ||
		hd.Get("Access-Control-Allow-Methods") != "POST" || hd.Get("Access-Control-Allow-Credentials") != "" ||
		!strings.Contains(strings.Join(hd.Values("Vary"), ","), "Origin") {
		t.Fatalf("allowed preflight: %d %v", rec.Code, hd)
	}
	for name, rec := range map[string]*httptest.ResponseRecorder{
		"other origin": preflight(h, "https://evil.example", http.MethodPost),
		"other method": preflight(h, site, http.MethodDelete),
	} {
		if rec.Code != http.StatusForbidden || rec.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Errorf("%s preflight: %d %v, want 403 without CORS headers", name, rec.Code, rec.Header())
		}
	}

	post := func(origin, auth string) *httptest.ResponseRecorder {
		req := newJobRequest(t, fixturePDF(t), nil)
		req.Header.Set("Origin", origin)
		return serve(h, req, auth)
	}
	// allowed origin: success and error answers are readable
	if rec := post(site, "Ticket "+tk.Sign(claims("t-cors"))); rec.Code != http.StatusAccepted || rec.Header().Get("Access-Control-Allow-Origin") != site {
		t.Fatalf("allowed POST: %d %v", rec.Code, rec.Header())
	}
	if rec := post(site, "Ticket "+tk.Sign(claims("t-cors"))); rec.Code != http.StatusUnauthorized || rec.Header().Get("Access-Control-Allow-Origin") != site {
		t.Fatalf("allowed POST, replayed ticket: %d %v, want 401 with CORS", rec.Code, rec.Header())
	}
	if rec := post("https://evil.example", "Ticket "+tk.Sign(claims("t-evil"))); rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("disallowed origin got %q", rec.Header().Get("Access-Control-Allow-Origin"))
	}
	// other routes never get CORS headers
	req := httptest.NewRequest(http.MethodGet, "/v1/jobs/x", nil)
	req.Header.Set("Origin", site)
	if rec := serve(h, req, "Bearer "+testToken); rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("GET job carries CORS headers")
	}

	// unset: no CORS anywhere; a preflight is just an unauthenticated request
	plain, _, _ := ticketServer(t, DefaultLimits())
	if rec := preflight(plain, site, http.MethodPost); rec.Code != http.StatusUnauthorized || rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("no origins configured: %d %v", rec.Code, rec.Header())
	}
}

func TestParseCORSOrigins(t *testing.T) {
	got, err := ParseCORSOrigins(" https://a.example.com, http://localhost:3000 ,")
	if err != nil || len(got) != 2 || got[0] != "https://a.example.com" || got[1] != "http://localhost:3000" {
		t.Fatalf("got %v, %v", got, err)
	}
	if got, err := ParseCORSOrigins(""); err != nil || len(got) != 0 {
		t.Fatalf("empty: %v, %v", got, err)
	}
	for _, bad := range []string{"*", "https://a.example.com/", "https://a.example.com/x", "a.example.com", "ftp://a.example.com", "https://*.example.com", "https://u@a.example.com"} {
		if _, err := ParseCORSOrigins(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestTicketReplayCacheIsBounded(t *testing.T) {
	tk, _ := NewTickets(testSecret)
	tk.maxUsed = 2
	now := time.Now()
	tk.now = func() time.Time { return now }
	use := func(jti string, exp time.Time) error {
		return tk.Use(TicketClaims{V: 1, JTI: jti, Exp: exp.Unix()})
	}
	if use("a", now.Add(-time.Hour)) != nil || use("b", now.Add(time.Minute)) != nil {
		t.Fatal("first uses failed")
	}
	// full: the expired "a" is dropped to make room
	if err := use("c", now.Add(time.Minute)); err != nil {
		t.Fatalf("after pruning: %v", err)
	}
	if err := use("d", now.Add(time.Minute)); err != errTicketCacheFull {
		t.Fatalf("full of live tickets: %v, want errTicketCacheFull", err)
	}
	tk.sweep()
	if len(tk.used) != 2 {
		t.Fatalf("sweep dropped live tickets: %v", tk.used)
	}
}

func TestMaxPagesLimit(t *testing.T) {
	if got := DefaultLimits().MaxPages; got != 1000 {
		t.Fatalf("default MaxPages = %d, want 1000", got)
	}
	for in, want := range map[string]int{"": 1000, "1": 1, " 250 ": 250, "10000": 10000} {
		if got, err := ParseMaxPages(in); err != nil || got != want {
			t.Errorf("ParseMaxPages(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, in := range []string{"0", "-1", "10001", "1.5", "abc", "100pages"} {
		if _, err := ParseMaxPages(in); err == nil {
			t.Errorf("ParseMaxPages(%q) accepted", in)
		}
	}

	// a custom limit fails a longer selection before rendering
	dir := t.TempDir()
	store, err := NewStore(dir, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	lim := DefaultLimits()
	lim.MaxPages = 1
	pool := NewPool(store, 1, 2, lim.MaxPages, lim.MaxQueueDepth, 30*time.Second)
	t.Cleanup(pool.Shutdown)
	h := NewServer(store, pool, lim, "").Handler()
	res := postJob(t, h, readFixture(t, "broken-xref.pdf"), map[string]string{"dpi": "72"})
	var created map[string]string
	_ = readJSON(res.Body, &created)
	if v := waitDone(t, h, created["id"]); v.Status != StatusFailed || !strings.Contains(v.Error, "limit is 1") || v.PagesDone != 0 {
		t.Fatalf("over -max-pages: status=%s err=%q done=%d", v.Status, v.Error, v.PagesDone)
	}
	// a per-job max_pages above the server limit does not raise it
	res = postJob(t, h, readFixture(t, "broken-xref.pdf"), map[string]string{"dpi": "72", "max_pages": "1000"})
	_ = readJSON(res.Body, &created)
	if v := waitDone(t, h, created["id"]); v.Status != StatusFailed || !strings.Contains(v.Error, "limit is 1") {
		t.Fatalf("max_pages above -max-pages: status=%s err=%q", v.Status, v.Error)
	}
	// within the limit renders
	res = postJob(t, h, fixturePDF(t), map[string]string{"dpi": "72", "pages": "1"})
	_ = readJSON(res.Body, &created)
	if v := waitDone(t, h, created["id"]); v.Status != StatusDone {
		t.Fatalf("one page under -max-pages 1: status=%s err=%q", v.Status, v.Error)
	}
}
