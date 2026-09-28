package sfu

import "testing"

func TestChoose(t *testing.T) {
	all := []Layer{LayerQ, LayerH, LayerF}
	cases := []struct {
		name      string
		bwe       int
		preferred Layer
		available []Layer
		want      Layer
	}{
		{name: "preferred fits", bwe: 3_000_000, preferred: LayerH, available: all, want: LayerH},
		{name: "downgrade", bwe: 300_000, preferred: LayerF, available: all, want: LayerQ},
		{name: "mid", bwe: 900_000, preferred: LayerF, available: all, want: LayerH},
		{name: "only high", bwe: 100_000, preferred: LayerF, available: []Layer{LayerF}, want: LayerF},
		{name: "empty", bwe: 3_000_000, preferred: LayerF, available: nil, want: LayerNone},
		{name: "unknown preferred", bwe: 3_000_000, preferred: "x", available: all, want: LayerF},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Choose(tc.bwe, tc.preferred, tc.available); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}
