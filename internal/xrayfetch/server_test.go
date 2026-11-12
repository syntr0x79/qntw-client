package xrayfetch_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/proxy/freedom"

	"github.com/syntr0x79/qntw-client/internal/xrayfetch"
)

// Незнакомый протокол — ErrNoServer без слов о модеме: этот текст уходит и
// в голоса узлов парка.
func TestServerHostUnknownProtocol(t *testing.T) {
	out := &core.OutboundHandlerConfig{ProxySettings: serial.ToTypedMessage(&freedom.Config{})}
	_, err := xrayfetch.ServerHost(out)
	if !errors.Is(err, xrayfetch.ErrNoServer) || strings.Contains(err.Error(), "модем") {
		t.Fatalf("err = %v, ожидался ErrNoServer без слова «модем»", err)
	}
}
