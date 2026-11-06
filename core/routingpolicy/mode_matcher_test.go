package routingpolicy

import "testing"

func TestNormalizeMode(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  Mode
		bad   bool
	}{
		{"", Global, false}, {"global", Global, false}, {"bypass_cn", BypassCN, false},
		{"direct", "", true}, {"GLOBAL", "", true}, {" bypass_cn", "", true}, {" ", "", true},
	} {
		t.Run(tc.input, func(t *testing.T) {
			got, err := Normalize(tc.input)
			if (err != nil) != tc.bad || got != tc.want {
				t.Fatalf("Normalize(%q) = %q, %v; want %q, error=%v", tc.input, got, err, tc.want, tc.bad)
			}
		})
	}
}
