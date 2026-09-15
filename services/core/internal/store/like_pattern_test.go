package store

import "testing"

func TestLikePattern(t *testing.T) {
	cases := map[string]string{
		"marble":     "%marble%",
		"100%":       `%100\%%`,
		"a_b":        `%a\_b%`,
		`back\slash`: `%back\\slash%`,
	}
	for in, want := range cases {
		if got := likePattern(in); got != want {
			t.Fatalf("likePattern(%q) = %q, want %q", in, got, want)
		}
	}
}
