package procname

import "testing"

func TestApp(t *testing.T) {
	cases := []struct{ process, path, want string }{
		{"Google Chrome Helper", "/Applications/Google Chrome.app/Contents/Frameworks/Google Chrome Framework.framework/Versions/130/Helpers/Google Chrome Helper.app/Contents/MacOS/Google Chrome Helper", "Google Chrome"},
		{"codex", "/Applications/ChatGPT.app/Contents/Resources/codex-cli/CodexCLI.app/Contents/MacOS/codex", "ChatGPT"},
		{"UURemote", "/Applications/UURemote.app/Contents/MacOS/UURemote", "UURemote"},
		{"2.1.296", "/Users/ada/.local/share/claude/versions/2.1.296", "claude"},
		{"chrome.exe", `C:\Program Files\Google\Chrome\Application\chrome.exe`, "chrome"},
		{"curl", "/usr/bin/curl", "curl"},
		{"node", "", "node"},
		{"", "", ""},
		{"", "/opt/homebrew/bin/wget", "wget"},
	}
	for _, c := range cases {
		if got := App(c.process, c.path); got != c.want {
			t.Errorf("App(%q, %q) = %q, want %q", c.process, c.path, got, c.want)
		}
	}
}
