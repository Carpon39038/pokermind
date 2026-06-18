package engine

import "testing"

func TestStreetString(t *testing.T) {
	cases := []struct {
		s    Street
		want string
	}{
		{Preflop, "preflop"},
		{Flop, "flop"},
		{Turn, "turn"},
		{River, "river"},
		{Showdown, "showdown"},
	}
	for _, tc := range cases {
		if got := tc.s.String(); got != tc.want {
			t.Errorf("%v.String() = %q, want %q", tc.s, got, tc.want)
		}
	}
}

func TestActionTypeString(t *testing.T) {
	cases := []struct {
		a    ActionType
		want string
	}{
		{Fold, "fold"},
		{Call, "call"},
		{Raise, "raise"},
	}
	for _, tc := range cases {
		if got := tc.a.String(); got != tc.want {
			t.Errorf("%v.String() = %q, want %q", tc.a, got, tc.want)
		}
	}
}
