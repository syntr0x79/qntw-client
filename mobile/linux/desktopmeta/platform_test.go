package desktopmeta

import "testing"

func TestDesktopPlatformIdentity(t *testing.T) {
	for _, test := range []struct{ goos, platform, label string }{
		{"windows", "windows", "Windows"},
		{"linux", "linux", "Linux"},
		{"darwin", "macos", "Mac"},
	} {
		platform, label := platformFor(test.goos)
		if platform != test.platform || label != test.label {
			t.Fatalf("%s: %s %s", test.goos, platform, label)
		}
	}
}
