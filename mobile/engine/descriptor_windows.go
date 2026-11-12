package engine

import "errors"

// Windows desktop uses the native Wintun backend, not the Android/Linux
// inherited packet descriptor entry point. Probes still share this package.
func duplicateTunnelDescriptor(int) (int, error) {
	return -1, errors.New("inherited TUN descriptors are unavailable on Windows; use the Windows desktop backend")
}
func closeTunnelDescriptor(int) error { return nil }
