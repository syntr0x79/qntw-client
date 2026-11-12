//go:build (!linux && !darwin) || android || ios

package engine

func tunName(int) (string, error) { return "quantway", nil }
