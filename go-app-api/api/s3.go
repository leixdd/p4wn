package api

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)

// S3 uploads rendered pages to a bucket with SigV4-signed, path-style PUTs.
// ponytail: stdlib single-PUT client; move to aws-sdk-go-v2 if multipart
// uploads, IAM-role credentials or virtual-host addressing are ever needed.
type S3 struct {
	Endpoint  string // scheme://host[:port], no trailing slash
	Region    string
	Bucket    string
	AccessKey string
	SecretKey string
	Client    *http.Client
}

var (
	bucketRe    = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)
	keyPrefixRe = regexp.MustCompile(`^[A-Za-z0-9_-]+(/[A-Za-z0-9_-]+)*$`)
)

// S3FromEnv builds the uploader from P4WN_S3_BUCKET, P4WN_S3_REGION
// (default us-east-1), P4WN_S3_ENDPOINT (default AWS regional endpoint),
// P4WN_S3_ACCESS_KEY and P4WN_S3_SECRET_KEY. It returns nil, nil when
// P4WN_S3_BUCKET is unset (uploads disabled).
func S3FromEnv() (*S3, error) {
	s := &S3{
		Bucket:    os.Getenv("P4WN_S3_BUCKET"),
		Region:    os.Getenv("P4WN_S3_REGION"),
		Endpoint:  strings.TrimRight(os.Getenv("P4WN_S3_ENDPOINT"), "/"),
		AccessKey: os.Getenv("P4WN_S3_ACCESS_KEY"),
		SecretKey: os.Getenv("P4WN_S3_SECRET_KEY"),
		Client:    &http.Client{Timeout: 30 * time.Second},
	}
	if s.Bucket == "" {
		return nil, nil
	}
	if !bucketRe.MatchString(s.Bucket) {
		return nil, fmt.Errorf("P4WN_S3_BUCKET %q is not a valid bucket name", s.Bucket)
	}
	if s.AccessKey == "" || s.SecretKey == "" {
		return nil, fmt.Errorf("P4WN_S3_ACCESS_KEY and P4WN_S3_SECRET_KEY are required with P4WN_S3_BUCKET")
	}
	if s.Region == "" {
		s.Region = "us-east-1"
	}
	if s.Endpoint == "" {
		s.Endpoint = "https://s3." + s.Region + ".amazonaws.com"
	}
	return s, nil
}

// Put stores body under key, retrying transport errors and 5xx responses.
// key must need no URL escaping (page keys are built from keyPrefixRe).
func (s *S3) Put(ctx context.Context, key, contentType string, body []byte) error {
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return err
			case <-time.After(time.Duration(attempt) * 500 * time.Millisecond):
			}
		}
		var retry bool
		if retry, err = s.put(ctx, key, contentType, body); !retry {
			return err
		}
	}
	return err
}

func (s *S3) put(ctx context.Context, key, contentType string, body []byte) (retry bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, s.Endpoint+"/"+s.Bucket+"/"+key, bytes.NewReader(body))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", contentType)
	s.sign(req, body, time.Now())
	res, err := s.Client.Do(req)
	if err != nil {
		return true, fmt.Errorf("s3 put %s: %w", key, err)
	}
	defer res.Body.Close()
	msg, _ := io.ReadAll(io.LimitReader(res.Body, 512))
	if res.StatusCode/100 != 2 {
		return res.StatusCode >= 500, fmt.Errorf("s3 put %s: %s: %s", key, res.Status, msg)
	}
	return false, nil
}

// sign adds SigV4 headers, signing host, x-amz-content-sha256 and x-amz-date.
// The path and query are used as written, so they must already be canonical.
func (s *S3) sign(req *http.Request, body []byte, now time.Time) {
	amzDate := now.UTC().Format("20060102T150405Z")
	date := amzDate[:8]
	sum := sha256.Sum256(body)
	payload := hex.EncodeToString(sum[:])
	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("X-Amz-Content-Sha256", payload)

	const signed = "host;x-amz-content-sha256;x-amz-date"
	canonical := strings.Join([]string{
		req.Method, req.URL.EscapedPath(), req.URL.RawQuery,
		"host:" + req.URL.Host, "x-amz-content-sha256:" + payload, "x-amz-date:" + amzDate, "",
		signed, payload,
	}, "\n")
	scope := date + "/" + s.Region + "/s3/aws4_request"
	csum := sha256.Sum256([]byte(canonical))
	toSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + hex.EncodeToString(csum[:])

	key := []byte("AWS4" + s.SecretKey)
	for _, part := range []string{date, s.Region, "s3", "aws4_request", toSign} {
		h := hmac.New(sha256.New, key)
		h.Write([]byte(part))
		key = h.Sum(nil)
	}
	req.Header.Set("Authorization", fmt.Sprintf("AWS4-HMAC-SHA256 Credential=%s/%s,SignedHeaders=%s,Signature=%s",
		s.AccessKey, scope, signed, hex.EncodeToString(key)))
}
