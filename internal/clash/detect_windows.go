package clash

import (
	"strings"

	"golang.org/x/sys/windows"
)

// Service mode can override the pipe named in Verge's configuration. Enumerate
// live pipes directly: a normal user cannot read a service core's command line.
func fromRunningPipes() []Endpoint {
	pattern := windows.StringToUTF16Ptr(`\\.\pipe\*`)
	var data windows.Win32finddata
	handle, err := windows.FindFirstFile(pattern, &data)
	if err != nil {
		return nil
	}
	defer windows.FindClose(handle)

	var eps []Endpoint
	for {
		name := windows.UTF16ToString(data.FileName[:])
		lower := strings.ToLower(name)
		if lower == "verge-mihomo" || strings.HasPrefix(lower, "verge-mihomo-") {
			eps = append(eps, Endpoint{
				Kind: KindPipe, Address: `\\.\pipe\` + name, Source: "the running core",
			})
		}
		if err := windows.FindNextFile(handle, &data); err != nil {
			break
		}
	}
	return eps
}
