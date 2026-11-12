package clienttinc

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDeviceTokenIsSentOnlyToOriginalAPIOrigin(t *testing.T) {
	const token = "private-subscription-token-for-test"
	var leaked bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked = true; w.WriteHeader(200) }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			t.Error("device authorization absent")
		}
		if r.URL.Path != "/api/client-tinc/v1/config" {
			t.Error("unexpected API path")
		}
		w.Header().Set("Location", target.URL)
		w.WriteHeader(307)
	}))
	defer server.Close()
	a := NewAPI(Credentials{Origin: server.URL, Token: token}, nil)
	_, e := a.Config(context.Background())
	if e == nil {
		t.Fatal("redirect accepted")
	}
	if leaked {
		t.Fatal("authorization followed a redirect")
	}
	if strings.Contains(e.Error(), token) {
		t.Fatal("secret exposed")
	}
}

func TestPublicCapacityErrorIsBoundedAndDoesNotEchoBody(t *testing.T) {
	for _, item := range []struct {
		body, retry, code string
		seconds           int
	}{
		{`{"code":"capacity_exhausted","error":"private-secret"}`, "3", "capacity_exhausted", 3},
		{`{"code":"capacity_exhausted"}`, "9999", "capacity_exhausted", 60},
		{`{"code":"private_secret"}`, "-1", "", 0},
		{`broken private-secret`, "bad", "", 0},
		{`{"code":"capacity_exhausted","error":"` + strings.Repeat("s", 5000) + `"}`, "2", "", 2},
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Retry-After", item.retry)
			w.WriteHeader(503)
			io.WriteString(w, item.body)
		}))
		_, err := NewAPI(Credentials{Origin: server.URL, Token: "private-secret"}, nil).Config(context.Background())
		server.Close()
		var apiErr APIError
		if !errors.As(err, &apiErr) || apiErr.Status != 503 || apiErr.Code != item.code || apiErr.RetryAfter != item.seconds || strings.Contains(err.Error(), "private") {
			t.Fatalf("unsafe error metadata: %#v %v", apiErr, err)
		}
	}
}

func TestServerErrorBodyCannotExposeDeviceSecrets(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		io.WriteString(w, "private-bearer-value")
	}))
	defer server.Close()
	a := NewAPI(Credentials{Origin: server.URL, Token: "private-bearer-value"}, nil)
	_, e := a.Config(context.Background())
	if e == nil || strings.Contains(e.Error(), "private-bearer-value") {
		t.Fatal("API error includes secret response")
	}
}
