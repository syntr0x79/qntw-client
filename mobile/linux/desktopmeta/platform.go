// Package desktopmeta keeps desktop account identities consistent across OSes.
package desktopmeta

import "runtime"

func platformFor(goos string) (string, string) {
	if goos == "darwin" {
		return "macos", "Mac"
	}
	if goos == "windows" {
		return "windows", "Windows"
	}
	// Existing Linux installations and their identity format stay unchanged.
	return "linux", "Linux"
}

func Platform() string  { platform, _ := platformFor(runtime.GOOS); return platform }
func Label() string     { _, label := platformFor(runtime.GOOS); return label }
func UserAgent() string { return "Quantway" + Label() + "/0.1" }
