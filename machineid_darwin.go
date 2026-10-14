package main

import (
	"os/exec"
	"regexp"
)

var platformUUID = regexp.MustCompile(`"IOPlatformUUID"\s*=\s*"([^"]+)"`)

func machineID() string {
	out, err := exec.Command("ioreg", "-rd1", "-c", "IOPlatformExpertDevice").Output()
	if err != nil {
		return ""
	}
	if m := platformUUID.FindSubmatch(out); m != nil {
		return string(m[1])
	}
	return ""
}
