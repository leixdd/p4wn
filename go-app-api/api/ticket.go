package api

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Upload tickets let a trusted backend authorise one POST /v1/jobs by someone
// it does not want to hand the static token to (typically a browser uploading
// straight to this server). The backend signs the ticket with a secret it
// shares with this server:
//
//	ticket = base64url(payload JSON) "." base64url(HMAC-SHA256(secret, base64url(payload JSON)))
//
// base64url without padding (RFC 4648 §5); the MAC is over the first part as
// sent. The payload is TicketClaims. A ticket is presented as
// "Authorization: Ticket <ticket>", is valid for POST /v1/jobs only, and is
// accepted once.

const (
	// TicketVersion is the only payload "v" accepted.
	TicketVersion = 1
	// MinTicketSecretLen is the shortest P4WN_TICKET_SECRET accepted, in bytes.
	MinTicketSecretLen = 32
	// MaxTicketLifetime bounds exp − now: longer-lived tickets are refused, which
	// also bounds how long a used ticket ID must be remembered.
	MaxTicketLifetime = time.Hour
	// TicketClockSkew is the tolerance on exp for clocks that differ between
	// the issuer and this server.
	TicketClockSkew = 30 * time.Second
	// maxTicketLen bounds the header value before any decoding.
	maxTicketLen = 4096
	// maxUsedTickets bounds the replay cache (at most an hour of used ticket
	// IDs). When it is full of unexpired IDs new tickets get 503.
	maxUsedTickets = 100_000
)

// TicketClaims is a ticket's payload. Job options set here are the job's
// options; a ticket request may not carry any of its own.
type TicketClaims struct {
	V   int    `json:"v"`   // TicketVersion
	Exp int64  `json:"exp"` // unix seconds; required
	JTI string `json:"jti"` // unique ID, [A-Za-z0-9._:-]{1,128}; required

	// MaxBytes lowers the server's upload limit for this request (0 = unset).
	MaxBytes int64 `json:"max_bytes,omitempty"`
	// MaxPages fails the job when the document has more pages (0 = unset).
	MaxPages int `json:"max_pages,omitempty"`
	// KeyPrefix is the job's S3 key prefix (empty = the job ID).
	KeyPrefix string `json:"key_prefix,omitempty"`

	DPI    float64 `json:"dpi,omitempty"`    // 0 = server default
	Pages  string  `json:"pages,omitempty"`  // empty = all pages
	Gray   bool    `json:"gray,omitempty"`   //
	Alpha  bool    `json:"alpha,omitempty"`  //
	Format string  `json:"format,omitempty"` // png (default) or html
}

var jtiRe = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

// ticketError is a refused ticket; the message is safe to return and log.
type ticketError struct{ msg string }

func (e *ticketError) Error() string { return e.msg }

func ticketErr(format string, args ...any) error {
	return &ticketError{msg: fmt.Sprintf(format, args...)}
}

var errTicketCacheFull = errors.New("too many unexpired tickets in use, retry later")

// Tickets verifies upload tickets and remembers used ticket IDs until they
// expire. The memory is per process: after a restart a ticket used before it
// is accepted once more until its exp.
type Tickets struct {
	secret []byte
	now    func() time.Time

	mu      sync.Mutex
	used    map[string]int64 // jti → exp
	maxUsed int
}

// NewTickets enables tickets signed with secret (at least MinTicketSecretLen bytes).
func NewTickets(secret string) (*Tickets, error) {
	if len(secret) < MinTicketSecretLen {
		return nil, fmt.Errorf("ticket secret must be at least %d bytes (e.g. openssl rand -hex 32)", MinTicketSecretLen)
	}
	return &Tickets{secret: []byte(secret), now: time.Now, used: make(map[string]int64), maxUsed: maxUsedTickets}, nil
}

// Sign mints a ticket for c. The server only verifies tickets; Sign is the
// reference for issuers and is used by the tests.
func (t *Tickets) Sign(c TicketClaims) string {
	return SignTicket(t.secret, c)
}

// SignTicket encodes and signs c with secret.
func SignTicket(secret []byte, c TicketClaims) string {
	payload, _ := json.Marshal(c)
	p := base64.RawURLEncoding.EncodeToString(payload)
	return p + "." + base64.RawURLEncoding.EncodeToString(ticketMAC(secret, p))
}

func ticketMAC(secret []byte, payloadPart string) []byte {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(payloadPart))
	return m.Sum(nil)
}

// Verify checks a ticket's signature, version, expiry and fields. It does not
// mark it used (see Use).
func (t *Tickets) Verify(ticket string) (TicketClaims, error) {
	var c TicketClaims
	if len(ticket) > maxTicketLen {
		return c, ticketErr("malformed ticket")
	}
	payloadPart, sigPart, ok := strings.Cut(ticket, ".")
	if !ok || payloadPart == "" || strings.Contains(sigPart, ".") {
		return c, ticketErr("malformed ticket")
	}
	sig, err := base64.RawURLEncoding.DecodeString(sigPart)
	if err != nil {
		return c, ticketErr("malformed ticket")
	}
	// Nothing in the payload is looked at before the signature holds.
	if !hmac.Equal(sig, ticketMAC(t.secret, payloadPart)) {
		return c, ticketErr("bad ticket signature")
	}
	payload, err := base64.RawURLEncoding.DecodeString(payloadPart)
	if err != nil {
		return c, ticketErr("malformed ticket")
	}
	dec := json.NewDecoder(bytes.NewReader(payload))
	// An unknown field may be a constraint this server cannot enforce: refuse.
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil || dec.More() {
		return c, ticketErr("malformed ticket payload")
	}
	if c.V != TicketVersion {
		return c, ticketErr("unsupported ticket version %d (want %d)", c.V, TicketVersion)
	}
	if !jtiRe.MatchString(c.JTI) {
		return c, ticketErr("ticket jti missing or invalid")
	}
	now := t.now()
	switch {
	case c.Exp <= 0:
		return c, ticketErr("ticket exp missing")
	case now.After(time.Unix(c.Exp, 0).Add(TicketClockSkew)):
		return c, ticketErr("ticket expired")
	case time.Unix(c.Exp, 0).After(now.Add(MaxTicketLifetime + TicketClockSkew)):
		return c, ticketErr("ticket lifetime exceeds %s", MaxTicketLifetime)
	case c.MaxBytes < 0, c.MaxPages < 0, c.DPI < 0:
		return c, ticketErr("ticket has a negative limit")
	}
	return c, nil
}

// Use marks a verified ticket used. A second Use of the same jti before it
// expires fails.
func (t *Tickets) Use(c TicketClaims) error {
	now := t.now().Unix()
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, seen := t.used[c.JTI]; seen {
		return ticketErr("ticket already used")
	}
	if len(t.used) >= t.maxUsed {
		cutoff := now - int64(TicketClockSkew/time.Second)
		for id, exp := range t.used {
			if exp < cutoff {
				delete(t.used, id)
			}
		}
		if len(t.used) >= t.maxUsed {
			return errTicketCacheFull
		}
	}
	t.used[c.JTI] = c.Exp
	return nil
}

// sweep forgets used tickets that can no longer verify anyway.
func (t *Tickets) sweep() {
	cutoff := t.now().Unix() - int64(TicketClockSkew/time.Second)
	t.mu.Lock()
	defer t.mu.Unlock()
	for id, exp := range t.used {
		if exp < cutoff {
			delete(t.used, id)
		}
	}
}

// StartSweeper forgets expired ticket IDs every interval until stop is closed.
func (t *Tickets) StartSweeper(interval time.Duration, stop <-chan struct{}) {
	go func() {
		tk := time.NewTicker(interval)
		defer tk.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tk.C:
				t.sweep()
			}
		}
	}()
}

// jobOptionKeys are the request parameters ParseJobOptions reads; a ticket
// request may send none of them.
var jobOptionKeys = []string{"dpi", "scale", "pages", "gray", "alpha", "format", "key_prefix", "max_pages"}

// optionGetter exposes the ticket's job options in ParseJobOptions' terms, so
// they are validated exactly like request parameters.
func (c TicketClaims) optionGetter() func(string) string {
	return func(key string) string {
		switch key {
		case "dpi":
			if c.DPI > 0 {
				return strconv.FormatFloat(c.DPI, 'g', -1, 64)
			}
		case "pages":
			return c.Pages
		case "gray":
			if c.Gray {
				return "true"
			}
		case "alpha":
			if c.Alpha {
				return "true"
			}
		case "format":
			return c.Format
		case "key_prefix":
			return c.KeyPrefix
		case "max_pages":
			if c.MaxPages > 0 {
				return strconv.Itoa(c.MaxPages)
			}
		}
		return ""
	}
}

type ticketCtxKey struct{}

func withTicket(ctx context.Context, c TicketClaims) context.Context {
	return context.WithValue(ctx, ticketCtxKey{}, c)
}

func ticketFrom(ctx context.Context) (TicketClaims, bool) {
	c, ok := ctx.Value(ticketCtxKey{}).(TicketClaims)
	return c, ok
}

// ParseCORSOrigins parses P4WN_CORS_ORIGINS: comma-separated exact origins
// such as https://app.example.com or http://localhost:3000. Empty = none.
func ParseCORSOrigins(s string) ([]string, error) {
	var out []string
	for _, part := range strings.Split(s, ",") {
		o := strings.TrimSpace(part)
		if o == "" {
			continue
		}
		u, err := url.Parse(o)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" ||
			u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil || strings.Contains(u.Host, "*") ||
			o != u.Scheme+"://"+u.Host {
			return nil, fmt.Errorf("%q is not an origin (want scheme://host[:port], no path or wildcard)", o)
		}
		out = append(out, o)
	}
	return out, nil
}
