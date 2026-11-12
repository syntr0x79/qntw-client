package awgprobe

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestProbeLive — зонд против настоящего входа. AWG_LIVE_CONF — путь к
// .conf (как отдаёт BuildAWGConf) или сам текст; AWG_LIVE_URL — ControlURL,
// по умолчанию generate_204 Google.
//
//	AWG_LIVE_CONF=./dev.conf go test -run Live -v ./internal/awgprobe/
func TestProbeLive(t *testing.T) {
	conf := os.Getenv("AWG_LIVE_CONF")
	if conf == "" {
		t.Skip("AWG_LIVE_CONF не задан")
	}
	if b, err := os.ReadFile(conf); err == nil {
		conf = string(b)
	}
	url := os.Getenv("AWG_LIVE_URL")
	if url == "" {
		url = "http://www.gstatic.com/generate_204"
	}
	start := time.Now()
	if err := Probe(context.Background(), conf, Options{ControlURL: url}); err != nil {
		t.Fatalf("Probe: %v", err)
	}
	t.Logf("ok за %s", time.Since(start))
}
