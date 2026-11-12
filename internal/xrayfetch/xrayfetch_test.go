package xrayfetch

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"
	"time"

	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/proxy/freedom"
)

// Исходящее, которого этот бинарь не знает, — ErrInstance, а не ошибка
// маршрута: агент маяка пишет такой исход «не проверяемо».
func TestFetchThroughUnknownOutboundIsErrInstance(t *testing.T) {
	out := &core.OutboundHandlerConfig{
		Tag:           "probe",
		ProxySettings: &serial.TypedMessage{Type: "xray.proxy.nope.Config"},
	}
	_, err := FetchThrough(context.Background(), out, "http://127.0.0.1:1/", 0)
	if !errors.Is(err, ErrInstance) {
		t.Fatalf("err = %v, want ErrInstance", err)
	}
}

// Проба не оставляет после себя ничего живого. Агент на узле зовёт
// FetchThrough тысячи раз в час, и соединение, брошенное в пуле
// простаивающих, копилось до конца процесса: шесть горутин и десятки КБ на
// пробу, сотни МБ на узле за сутки.
func TestFetchThroughLeavesNoGoroutines(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("ok"))
	}))
	defer srv.Close()
	out := &core.OutboundHandlerConfig{Tag: "probe", ProxySettings: serial.ToTypedMessage(&freedom.Config{})}

	fetch := func() {
		if _, err := FetchThrough(context.Background(), out, srv.URL, 5*time.Second); err != nil {
			t.Fatal(err)
		}
	}
	fetch() // первый вызов поднимает разовое: пулы рантайма, httptest
	before := runtime.NumGoroutine()
	for range 50 {
		fetch()
	}
	// Закрытие соединения отрабатывает асинхронно: даём ему осесть.
	var after int
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if after = runtime.NumGoroutine(); after <= before+2 {
			return
		}
	}
	t.Fatalf("goroutines: %d before 50 fetches, %d after", before, after)
}
