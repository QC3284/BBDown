package download

import (
	"reflect"
	"testing"
)

// TestSplitArgsRespectsQuotes: strings.Fields broke a quoted value into several
// tokens, so an option like --user-agent="Mozilla/5.0 (X11)" reached aria2c as
// three separate arguments.
func TestSplitArgsRespectsQuotes(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"-x16 -s16", []string{"-x16", "-s16"}},
		{`--user-agent="Mozilla/5.0 (X11)" -x16`, []string{"--user-agent=Mozilla/5.0 (X11)", "-x16"}},
		{`--header='Referer: https://x' -j16`, []string{"--header=Referer: https://x", "-j16"}},
		{"  -x16   -s16  ", []string{"-x16", "-s16"}},
	}

	for _, c := range cases {
		if got := splitArgs(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("splitArgs(%q) = %#v, want %#v", c.in, got, c.want)
		}
	}
}

// TestSplitArgsKeepsUnclosedQuoteTail: an unclosed quote must not swallow the
// rest of the configuration.
func TestSplitArgsKeepsUnclosedQuoteTail(t *testing.T) {
	got := splitArgs(`--user-agent="Mozilla/5.0 -x16`)
	if len(got) != 1 || got[0] != "--user-agent=Mozilla/5.0 -x16" {
		t.Errorf("splitArgs with an unclosed quote = %#v, want the tail kept as one argument", got)
	}
}
