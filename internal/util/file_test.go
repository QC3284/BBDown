package util

import (
	"os"
	"testing"
)

func TestGetValidFileName(t *testing.T) {
	cases := []struct {
		in       string
		repl     string
		slash    bool
		expected string
	}{
		{"a/b\\c:d*e?f\"g<h>i|j", "_", true, "a_b_c_d_e_f_g_h_i_j"},
		{"normal title.mp4", "_", true, "normal title.mp4"},
		{"CON", "_", true, "_CON"},
		{"con", "_", true, "_con"},
		{"LPT9", "_", true, "_LPT9"},
		{"COM1", "_", false, "_COM1"},
		{"CON.txt", "_", true, "_CON.txt"}, // reserved basename with extension
		{"", "_", true, ""},
		{"trailing.dot.", "_", true, "trailing.dot."},
	}
	for _, c := range cases {
		got := GetValidFileName(c.in, c.repl, c.slash)
		if got != c.expected {
			t.Errorf("GetValidFileName(%q, %q, %v) = %q, want %q", c.in, c.repl, c.slash, got, c.expected)
		}
	}
}

func TestFormatFileSize(t *testing.T) {
	cases := []struct {
		size float64
		want string
	}{
		{0, "0.00 B"},
		{1023, "1023.00 B"},
		{1024, "1.00 KB"},
		{1048576, "1.00 MB"},
		{1073741824, "1.00 GB"},
		{1099511627776, "1.00 TB"},
	}
	for _, c := range cases {
		if got := FormatFileSize(c.size); got != c.want {
			t.Errorf("FormatFileSize(%v) = %q, want %q", c.size, got, c.want)
		}
	}
}

func TestFormatTime(t *testing.T) {
	cases := []struct {
		sec      int
		absolute bool
		want     string
	}{
		{0, true, "00:00:00"},
		{61, true, "00:01:01"},
		{3661, true, "01:01:01"},
		{61, false, "01m01s"},
		{3661, false, "1h01m01s"},
	}
	for _, c := range cases {
		if got := FormatTime(c.sec, c.absolute); got != c.want {
			t.Errorf("FormatTime(%d, %v) = %q, want %q", c.sec, c.absolute, got, c.want)
		}
	}
}

func TestCombineMultipleFilesIntoSingleFile(t *testing.T) {
	dir := t.TempDir()
	f1 := dir + "/a.txt"
	f2 := dir + "/b.txt"
	out := dir + "/out.txt"
	if err := os.WriteFile(f1, []byte("hello "), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f2, []byte("world"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CombineMultipleFilesIntoSingleFile([]string{f1, f2}, out); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello world" {
		t.Fatalf("combined = %q", data)
	}
}
