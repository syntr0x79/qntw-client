package subscription

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestQuantwayProfilesAndAuthentication(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/mobile/v1/profiles" || r.Header.Get("Authorization") != "Bearer abcdefghijklmnop" {
			t.Errorf("unexpected request")
		}
		fmt.Fprint(w, `{"version":1,"profiles":[{"job_id":"a","label":"Germany","engine":"xray"}]}`)
	}))
	defer server.Close()
	profiles, err := (Client{HTTP: server.Client(), testHost: server.Listener.Addr().String()}).Load(context.Background(), server.URL+"/sub/abcdefghijklmnop")
	if err != nil || len(profiles) != 1 || profiles[0].Label != "Germany" {
		t.Fatalf("profiles=%v err=%v", profiles, err)
	}
}
func TestExternalSubscriptionUsesSharedParser(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "vless://11111111-2222-4333-8444-555555555555@127.0.0.1:443?encryption=none&security=none&type=tcp#Local")
	}))
	defer server.Close()
	profiles, err := (Client{HTTP: server.Client()}).Load(context.Background(), server.URL+"/feed")
	if err != nil || len(profiles) != 1 || profiles[0].Engine != "xray" {
		t.Fatalf("profiles=%v err=%v", profiles, err)
	}
}
func TestRejectsInsecureOrRedirectedSubscription(t *testing.T) {
	if _, err := (Client{}).Load(context.Background(), "http://example.com/sub/abcdefghijklmnop"); err == nil {
		t.Fatal("cleartext accepted")
	}
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("followed redirect") }))
	defer target.Close()
	source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
	defer source.Close()
	_, err := (Client{HTTP: source.Client(), testHost: source.Listener.Addr().String()}).Load(context.Background(), source.URL+"/sub/abcdefghijklmnop")
	if err == nil || strings.Contains(err.Error(), "abcdefghijklmnop") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExternalSubPathIsNotTreatedAsQuantwayAPI(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sub/abcdefghijklmnop" || r.Header.Get("Authorization") != "" {
			t.Errorf("unexpected request: %s", r.URL.Path)
		}
		fmt.Fprint(w, "vless://11111111-2222-4333-8444-555555555555@127.0.0.1:443?encryption=none&security=none&type=tcp#Local")
	}))
	defer server.Close()
	profiles, err := (Client{HTTP: server.Client()}).Load(context.Background(), server.URL+"/sub/abcdefghijklmnop")
	if err != nil || len(profiles) != 1 {
		t.Fatalf("external subscription: %v", err)
	}
}
