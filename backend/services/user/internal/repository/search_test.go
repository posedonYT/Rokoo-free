package repository

import "testing"

func TestEscapeLike(t *testing.T) {
	cases := map[string]string{
		"abc":   "abc",
		"a%b":   `a\%b`,
		"a_b":   `a\_b`,
		`a\b`:   `a\\b`,
		`\%_`:   `\\\%\_`,
		"alice": "alice",
	}
	for in, want := range cases {
		if got := escapeLike(in); got != want {
			t.Errorf("escapeLike(%q)=%q want %q", in, got, want)
		}
	}
}

func TestParseTag(t *testing.T) {
	if _, _, ok := parseTag("alice"); ok {
		t.Fatal("no tag expected")
	}
	n, d, ok := parseTag("alice#0042")
	if !ok || n != "alice" || d != "0042" {
		t.Fatalf("got %q %q %v", n, d, ok)
	}
}
