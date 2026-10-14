package store

import "testing"

func TestMatches(t *testing.T) {
	for _, tc := range []struct {
		name, search string
		also         []string
		want         bool
	}{
		{"Google Chrome", "chrome", nil, true},
		{"未识别进程", "unidentified", nil, false},
		{"未识别进程", "unidentified", []string{"未识别进程"}, true},
		{"局域网 192.168.1.5", "lan 192.168", []string{"局域网 192.168"}, true},
		{"Steam", "unidentified", []string{"未识别进程"}, false},
		{"Steam", "x", []string{""}, false},
	} {
		if got := Matches(tc.name, tc.search, tc.also); got != tc.want {
			t.Errorf("Matches(%q, %q, %q) = %v", tc.name, tc.search, tc.also, got)
		}
	}
}
