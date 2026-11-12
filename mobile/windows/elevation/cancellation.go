package elevation

import "context"

// IsOnlyCancellation distinguishes an expected Stop from a joined cleanup
// failure. errors.Is(err, context.Canceled) alone discards the latter.
func IsOnlyCancellation(err error) bool { return onlyCancellation(err, 0) }
func onlyCancellation(err error, depth int) bool {
	if err == nil || depth > 64 {
		return false
	}
	if err == context.Canceled {
		return true
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		children := joined.Unwrap()
		if len(children) == 0 {
			return false
		}
		for _, child := range children {
			if !onlyCancellation(child, depth+1) {
				return false
			}
		}
		return true
	}
	if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		return onlyCancellation(wrapped.Unwrap(), depth+1)
	}
	return false
}
