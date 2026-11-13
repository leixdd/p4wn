package main

import (
	"testing"

	"p4wn/api"
)

func TestFlagOrEnv(t *testing.T) {
	env := func(m map[string]string) func(string) string {
		return func(k string) string { return m[k] }
	}
	cases := []struct {
		flag    string
		env     map[string]string
		want    int
		wantSrc string
		wantErr bool
	}{
		{"", nil, 4, "P4WN_PAGE_WORKERS", false},                                         // neither: default
		{"", map[string]string{"P4WN_PAGE_WORKERS": "2"}, 2, "P4WN_PAGE_WORKERS", false}, // env when flag absent
		{"6", map[string]string{"P4WN_PAGE_WORKERS": "2"}, 6, "-page-workers", false},    // flag wins
		{"6", map[string]string{"P4WN_PAGE_WORKERS": "x"}, 6, "-page-workers", false},    // bad env ignored under a flag
		{"", map[string]string{"P4WN_PAGE_WORKERS": "0"}, 0, "P4WN_PAGE_WORKERS", true},  // bad env rejected
		{"300", nil, 0, "-page-workers", true},                                           // bad flag rejected
	}
	for _, c := range cases {
		v, src := flagOrEnv(c.flag, "-page-workers", "P4WN_PAGE_WORKERS", env(c.env))
		got, err := api.ParseWorkers(v, api.DefaultPageWorkers)
		if src != c.wantSrc || (err != nil) != c.wantErr || (!c.wantErr && got != c.want) {
			t.Errorf("flag %q env %v: %d from %s, err %v; want %d from %s, err %t", c.flag, c.env, got, src, err, c.want, c.wantSrc, c.wantErr)
		}
	}

	// megapixels: env used when the flag is absent, flag wins
	v, _ := flagOrEnv("", "-max-megapixels", "P4WN_MAX_MEGAPIXELS", env(map[string]string{"P4WN_MAX_MEGAPIXELS": "20"}))
	if px, err := api.ParseMaxMegapixels(v); err != nil || px != 20_000_000 {
		t.Errorf("env megapixels: %d, %v", px, err)
	}
	v, _ = flagOrEnv("40", "-max-megapixels", "P4WN_MAX_MEGAPIXELS", env(map[string]string{"P4WN_MAX_MEGAPIXELS": "20"}))
	if px, err := api.ParseMaxMegapixels(v); err != nil || px != 40_000_000 {
		t.Errorf("flag megapixels: %d, %v", px, err)
	}
}
