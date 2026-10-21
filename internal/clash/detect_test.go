package clash

import "testing"

func TestFlagValue(t *testing.T) {
	for _, tc := range []struct {
		line, name, want string
	}{
		{"/bin/verge-mihomo -d /cfg -ext-ctl-unix /tmp/verge.sock -secret abc", "-ext-ctl-unix", "/tmp/verge.sock"},
		{"/bin/verge-mihomo -d /cfg -ext-ctl-unix /tmp/verge.sock -secret abc", "-ext-ctl", ""},
		{"/bin/verge-mihomo -d /cfg -ext-ctl-unix /tmp/verge.sock -secret abc", "-secret", "abc"},
		{"/bin/mihomo -ext-ctl=127.0.0.1:9090 -secret=abc", "-ext-ctl", "127.0.0.1:9090"},
		{"/bin/mihomo -ext-ctl=127.0.0.1:9090 -secret=abc", "-secret", "abc"},
		{`/bin/mihomo -ext-ctl-unix="/tmp/a b.sock"`, "-ext-ctl-unix", "/tmp/a b.sock"},
		{"/bin/mihomo -ext-ctl-unix=/tmp/a.sock", "-ext-ctl", ""},
		{"/bin/mihomo -d /cfg", "-ext-ctl", ""},
	} {
		if got := flagValue(tc.line, tc.name); got != tc.want {
			t.Errorf("flagValue(%q, %q) = %q, want %q", tc.line, tc.name, got, tc.want)
		}
	}
}
