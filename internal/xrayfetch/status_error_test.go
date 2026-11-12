package xrayfetch

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestHTTPStatusErrorPreservesBoundedStatus(t *testing.T) {
	err := fmt.Errorf("control fetch: %w", &HTTPStatusError{StatusCode: 503})
	var status interface{ HTTPStatusCode() int }
	if !errors.As(err, &status) || status.HTTPStatusCode() != 503 {
		t.Fatalf("HTTP status unavailable through wrapped error: %v", err)
	}
	if !strings.Contains(err.Error(), "503") || strings.Contains(err.Error(), "https://") {
		t.Fatalf("unexpected public error: %v", err)
	}
}
