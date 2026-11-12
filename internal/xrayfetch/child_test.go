package xrayfetch

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/proxy/freedom"
)

// Бинарь тестов сам служит ребёнком: FetchInChild(os.Args[0]) запускает его
// с ChildArg, как агент узла запускает себя.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == ChildArg {
		if err := RunChild(os.Stdin); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestFetchInChildReturnsBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("203.0.113.7"))
	}))
	defer srv.Close()
	out := &core.OutboundHandlerConfig{Tag: "probe", ProxySettings: serial.ToTypedMessage(&freedom.Config{})}
	body, err := FetchInChild(os.Args[0])(context.Background(), out, srv.URL, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "203.0.113.7" {
		t.Fatalf("body = %q", body)
	}
}

// Ошибка экземпляра в ребёнке доходит до родителя как ErrInstance: агент
// пишет такой исход «не проверяемо», а не «мёртв».
func TestFetchInChildKeepsErrInstance(t *testing.T) {
	out := &core.OutboundHandlerConfig{
		Tag:           "probe",
		ProxySettings: &serial.TypedMessage{Type: "xray.proxy.nope.Config"},
	}
	_, err := FetchInChild(os.Args[0])(context.Background(), out, "http://127.0.0.1:1/", time.Second)
	if !errors.Is(err, ErrInstance) {
		t.Fatalf("err = %v, want ErrInstance", err)
	}
}

// Неудача маршрута — обычная ошибка, не ErrInstance.
func TestFetchInChildRouteFailureIsPlainError(t *testing.T) {
	out := &core.OutboundHandlerConfig{Tag: "probe", ProxySettings: serial.ToTypedMessage(&freedom.Config{})}
	_, err := FetchInChild(os.Args[0])(context.Background(), out, "http://127.0.0.1:1/", 2*time.Second)
	if err == nil || errors.Is(err, ErrInstance) {
		t.Fatalf("err = %v, want a plain fetch error", err)
	}
}

// Ребёнок, который не ответил, — ErrInstance с хвостом его вывода.
func TestFetchInChildMissingBinary(t *testing.T) {
	out := &core.OutboundHandlerConfig{Tag: "probe", ProxySettings: serial.ToTypedMessage(&freedom.Config{})}
	_, err := FetchInChild("/nonexistent/agent")(context.Background(), out, "http://127.0.0.1:1/", time.Second)
	if !errors.Is(err, ErrInstance) {
		t.Fatalf("err = %v, want ErrInstance", err)
	}
}
