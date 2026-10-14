// Package procname turns the process a connection came from into the name
// of the application users know, so that "Google Chrome Helper (Renderer)"
// counts towards Google Chrome.
package procname

import (
	"regexp"
	"strings"
)

var versionLike = regexp.MustCompile(`^v?\d+(\.\d+)+([-+._][0-9A-Za-z.]+)?$`)

// skipDirs are directories that name no application when walking up from
// an executable whose own name says nothing, such as a version number.
var skipDirs = map[string]bool{
	"bin": true, "sbin": true, "libexec": true, "lib": true, "versions": true, "version": true,
	"current": true, "contents": true, "macos": true, "resources": true, "app": true,
	"usr": true, "local": true, "opt": true, "share": true, ".local": true, "application": true,
}

// App returns the application a process belongs to, from its name and its
// executable's path; "" when both are empty.
func App(process, path string) string {
	if path != "" {
		if app := bundleName(path); app != "" {
			return app
		}
	}
	name := process
	if name == "" && path != "" {
		name = base(path)
	}
	name = strings.TrimSuffix(strings.TrimSuffix(name, ".exe"), ".EXE")
	if versionLike.MatchString(name) && path != "" {
		if n := ownerDir(path); n != "" {
			return n
		}
	}
	return name
}

// bundleName returns the outermost macOS app bundle of a path:
// /Applications/Google Chrome.app/Contents/Frameworks/…/Google Chrome
// Helper.app/… is Google Chrome.
func bundleName(path string) string {
	i := strings.Index(path, ".app/")
	if i < 0 {
		if !strings.HasSuffix(path, ".app") {
			return ""
		}
		i = len(path) - len(".app")
	}
	start := strings.LastIndex(path[:i], "/") + 1
	return path[start:i]
}

func split(path string) []string {
	return strings.FieldsFunc(path, func(r rune) bool { return r == '/' || r == '\\' })
}

func base(path string) string {
	parts := split(path)
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}

// ownerDir names the program of a versioned executable, such as
// ~/.local/share/claude/versions/2.1.296, which is claude.
func ownerDir(path string) string {
	parts := split(path)
	for i := len(parts) - 2; i >= 0; i-- {
		p := parts[i]
		if skipDirs[strings.ToLower(p)] || versionLike.MatchString(p) || strings.HasSuffix(p, ":") {
			continue
		}
		return strings.TrimPrefix(p, ".")
	}
	return ""
}
