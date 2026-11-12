package account

import (
	"context"
	"fmt"
	"github.com/syntr0x79/qntw-client/mobile/linux/desktopmeta"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLoginAndDeviceLink(t *testing.T) {
	const id = "11111111-2222-4333-8444-555555555555"
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/web/v1/login":
			http.SetCookie(w, &http.Cookie{Name: "qw_session", Value: "secret", Path: "/", HttpOnly: true})
			fmt.Fprint(w, `{}`)
		case "/api/web/v1/account/devices":
			if r.Header.Get("Cookie") != "qw_session=secret" {
				t.Error("session cookie missing")
			}
			fmt.Fprintf(w, `{"devices":[{"id":%q,"name":"Desktop"}]}`, id)
		case "/api/web/v1/account/devices/" + id + "/connect":
			if r.Header.Get("Cookie") != "qw_session=secret" {
				t.Error("session cookie missing")
			}
			fmt.Fprint(w, `{"sub_url":"https://qntw.link/sub/abcdefghijklmnop"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := Client{HTTP: server.Client(), base: server.URL}
	session, err := client.Login(context.Background(), "secret-code")
	if err != nil || len(session.Devices) != 1 {
		t.Fatalf("login: %v", err)
	}
	link, err := client.DeviceLink(context.Background(), session.Cookie, id)
	if err != nil || link != "https://qntw.link/sub/abcdefghijklmnop" {
		t.Fatalf("device: %v", err)
	}
}
func TestDeviceRejectsUnsafeValues(t *testing.T) {
	c := Client{}
	for _, pair := range [][2]string{{"qw_session=x", "../login"}, {"qw_session=x; other=y", "11111111-2222-4333-8444-555555555555"}, {"qw_session=x\r\nInjected: x", "11111111-2222-4333-8444-555555555555"}} {
		if _, err := c.DeviceLink(context.Background(), pair[0], pair[1]); err == nil || strings.Contains(err.Error(), pair[0]) {
			t.Fatal("unsafe input accepted or exposed")
		}
	}
}

func TestBindDeviceReusesInstallationAndDoesNotCreateTwice(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_CONFIG_HOME", root)
	const id = "11111111-2222-4333-8444-555555555555"
	name := desktopmeta.Label() + " 11111111222243338444555555555555"
	created := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/web/v1/account/devices" && r.Method == "GET":
			if created == 0 {
				fmt.Fprint(w, `{"devices":[],"link_works":true}`)
			} else {
				fmt.Fprintf(w, `{"devices":[{"id":%q,"name":%q}],"link_works":true}`, id, name)
			}
		case r.URL.Path == "/api/web/v1/account/devices" && r.Method == "POST":
			created++
			fmt.Fprintf(w, `{"device":{"id":%q,"name":%q}}`, id, name)
		case r.URL.Path == "/api/web/v1/account/devices/"+id+"/connect":
			fmt.Fprint(w, `{"sub_url":"https://qntw.link/sub/abcdefghijklmnop"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	c := Client{HTTP: server.Client(), base: server.URL}
	for i := 0; i < 2; i++ {
		got, link, err := c.BindDevice(context.Background(), "qw_session=secret", name)
		if err != nil || got != id || link == "" {
			t.Fatalf("bind %d: %s %v", i, got, err)
		}
	}
	if created != 1 {
		t.Fatalf("created %d devices", created)
	}
}

func TestDeviceNamePersists(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_CONFIG_HOME", root)
	first, err := DeviceName()
	if err != nil {
		t.Fatal(err)
	}
	second, err := DeviceName()
	if err != nil || first != second || len(first) > 40 {
		t.Fatalf("device identity unstable or too long: %q %v", second, err)
	}
}

func TestRefreshBindingRecoversAfterSlotIsFreed(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_CONFIG_HOME", root)
	const oldID = "11111111-2222-4333-8444-555555555555"
	const currentID = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
	free, created := false, false
	deviceName := "Старый телефон"
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/web/v1/account/devices":
			if created {
				name, err := DeviceName()
				if err != nil {
					t.Error(err)
				}
				fmt.Fprintf(w, `{"devices":[{"id":%q,"name":%q}]}`, currentID, name)
			} else {
				fmt.Fprintf(w, `{"devices":[{"id":%q,"name":%q}]}`, oldID, deviceName)
			}
		case r.Method == "POST" && r.URL.Path == "/api/web/v1/account/devices":
			if !free {
				w.WriteHeader(http.StatusConflict)
				fmt.Fprint(w, `{}`)
				return
			}
			created = true
			fmt.Fprintf(w, `{"device":{"id":%q}}`, currentID)
		case r.Method == "GET" && r.URL.Path == "/api/web/v1/account/devices/"+currentID+"/connect":
			fmt.Fprint(w, `{"sub_url":"https://qntw.link/sub/abcdefghijklmnop"}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := Client{HTTP: server.Client(), base: server.URL}
	full, err := client.RefreshBinding(context.Background(), "qw_session=secret")
	if err != nil || !full.NeedsReplacement || full.CurrentDevice != "" || len(full.Devices) != 1 {
		t.Fatalf("full subscription: %+v %v", full, err)
	}
	deviceName = "Переименованный телефон"
	full, err = client.RefreshBinding(context.Background(), "qw_session=secret")
	if err != nil || !full.NeedsReplacement || full.Devices[0].Name != deviceName {
		t.Fatalf("replacement list was not refreshed: %+v %v", full, err)
	}
	free = true
	bound, err := client.RefreshBinding(context.Background(), "qw_session=secret")
	if err != nil || bound.NeedsReplacement || bound.CurrentDevice != currentID || bound.DeviceLink == "" || bound.DeviceError != "" {
		t.Fatalf("freed slot was not reused: %+v %v", bound, err)
	}
}

func TestReplacementReusesAtomicReissueAndRecognisesRenamedDevice(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_CONFIG_HOME", root)
	const oldID = "11111111-2222-4333-8444-555555555555"
	const newID = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
	name := desktopmeta.Label() + " 11111111222243338444555555555555"
	calls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/api/web/v1/account/devices/"+oldID+"/reissue":
			calls++
			fmt.Fprintf(w, `{"device":{"id":%q,"name":"Old"}}`, newID)
		case r.Method == "PATCH" && r.URL.Path == "/api/web/v1/account/devices/"+newID:
			fmt.Fprint(w, `{}`)
		case r.Method == "GET" && r.URL.Path == "/api/web/v1/account/devices":
			fmt.Fprintf(w, `{"devices":[{"id":%q,"name":"Renamed"}]}`, newID)
		case r.Method == "GET" && r.URL.Path == "/api/web/v1/account/devices/"+newID+"/connect":
			fmt.Fprint(w, `{"sub_url":"https://qntw.link/sub/abcdefghijklmnop"}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	c := Client{HTTP: server.Client(), base: server.URL}
	id, link, err := c.ReplaceDevice(context.Background(), "qw_session=secret", oldID, name)
	if err != nil || id != newID || link == "" {
		t.Fatalf("replace: %v", err)
	}
	id, _, err = c.BindDevice(context.Background(), "qw_session=secret", name)
	if err != nil || id != newID || calls != 1 {
		t.Fatalf("rebind: %v", err)
	}
}
