package cli

import (
	"reflect"
	"testing"
)

func TestNormalizeCliArgs(t *testing.T) {
	cases := []struct {
		in   []string
		want []string
	}{
		{[]string{"-help"}, []string{"--help"}},
		{[]string{"-?"}, []string{"--help"}},
		{[]string{"-version"}, []string{"--version"}},
		{[]string{"-I", "av170001"}, []string{"-I", "av170001"}},
		{[]string{"--help"}, []string{"--help"}},
	}
	for _, c := range cases {
		got := normalizeCliArgs(c.in)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("normalizeCliArgs(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}
