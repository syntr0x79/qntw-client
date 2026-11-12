package elevation

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestStopNeverHidesJoinedCleanupFailure(t *testing.T) {
	for _, err := range []error{context.Canceled, fmt.Errorf("stop: %w", context.Canceled), errors.Join(context.Canceled, nil), errors.Join(context.Canceled, fmt.Errorf("wrapped: %w", context.Canceled))} {
		if !IsOnlyCancellation(err) {
			t.Fatalf("normal cancellation rejected: %v", err)
		}
	}
	cleanup := errors.New("owned route cleanup failed")
	for _, err := range []error{nil, cleanup, context.DeadlineExceeded, errors.Join(context.Canceled, cleanup), fmt.Errorf("cleanup: %w", errors.Join(context.Canceled, cleanup))} {
		if IsOnlyCancellation(err) {
			t.Fatalf("cleanup failure hidden: %v", err)
		}
	}
}
