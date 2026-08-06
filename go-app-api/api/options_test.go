package api

import (
	"reflect"
	"testing"
)

func TestParsePageRanges(t *testing.T) {
	cases := []struct {
		sel  string
		n    int
		want []int
		err  bool
	}{
		{"", 3, []int{0, 1, 2}, false},
		{"1", 5, []int{0}, false},
		{"1-3", 5, []int{0, 1, 2}, false},
		{"1-3,7", 10, []int{0, 1, 2, 6}, false},
		{"9-", 12, []int{8, 9, 10, 11}, false},
		{"-2", 5, []int{0, 1}, false},
		{"3,1,3", 5, []int{0, 2}, false},       // dedup + sort
		{"2-100", 5, []int{1, 2, 3, 4}, false}, // clamped
		{"abc", 5, nil, true},
		{"1-x", 5, nil, true},
		{"99", 5, nil, true}, // matches nothing after clamping
	}
	for _, c := range cases {
		got, err := ParsePageRanges(c.sel, c.n)
		if c.err {
			if err == nil {
				t.Errorf("%q: expected error, got %v", c.sel, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: %v", c.sel, err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q: got %v, want %v", c.sel, got, c.want)
		}
	}
}

func TestParseJobOptions(t *testing.T) {
	lim := DefaultLimits()
	form := map[string]string{"dpi": "300", "gray": "true", "pages": "1-2"}
	opts, err := ParseJobOptions(func(k string) string { return form[k] }, lim)
	if err != nil {
		t.Fatal(err)
	}
	if opts.DPI != 300 || !opts.Gray || opts.Alpha || opts.Pages != "1-2" {
		t.Errorf("opts = %+v", opts)
	}

	form = map[string]string{"scale": "2"}
	opts, err = ParseJobOptions(func(k string) string { return form[k] }, lim)
	if err != nil || opts.DPI != 144 {
		t.Errorf("scale: opts=%+v err=%v", opts, err)
	}

	form = map[string]string{"dpi": "999999"}
	if _, err = ParseJobOptions(func(k string) string { return form[k] }, lim); err == nil {
		t.Error("out-of-range dpi accepted")
	}

	form = map[string]string{}
	opts, err = ParseJobOptions(func(k string) string { return form[k] }, lim)
	if err != nil || opts.Format != FormatPNG {
		t.Errorf("default format: opts=%+v err=%v", opts, err)
	}

	form = map[string]string{"format": "html"}
	opts, err = ParseJobOptions(func(k string) string { return form[k] }, lim)
	if err != nil || opts.Format != FormatHTML {
		t.Errorf("html format: opts=%+v err=%v", opts, err)
	}

	form = map[string]string{"format": "pdf"}
	if _, err = ParseJobOptions(func(k string) string { return form[k] }, lim); err == nil {
		t.Error("invalid format accepted")
	}
}
