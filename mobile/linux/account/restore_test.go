package account

import (
	"context"
	"errors"
	"fmt"
	"github.com/syntr0x79/qntw-client/mobile/linux/desktopmeta"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const restoreDeviceID = "11111111-2222-4333-8444-555555555555"
const restoreOtherID = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
const restoreInstallation = "12345678-1234-4234-8234-123456789abc"

func restoreFixtureConfig(t *testing.T, binding, installation string) string {
	t.Helper()
	cabinetTestConfig(t)
	root, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "quantway")
	if binding != "" || installation != "" {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if binding != "" {
		if err := os.WriteFile(filepath.Join(dir, "bindings"), []byte(binding), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if installation != "" {
		if err := os.WriteFile(filepath.Join(dir, "device-id"), []byte(installation), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func restoreServer(t *testing.T, kind, devices string, linkStatus int, extra func(http.ResponseWriter, *http.Request) bool) (*httptest.Server, *[]string) {
	t.Helper()
	requests := []string{}
	var origin string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		if r.Method != "GET" {
			t.Errorf("restore must never mutate the service: %s %s", r.Method, r.URL.Path)
			http.Error(w, "mutation forbidden", 405)
			return
		}
		if r.Header.Get("Cookie") != "qw_session=saved" || r.Header.Get("Origin") != origin || r.Header.Get("X-Quantway") != "1" {
			t.Error("restore session/CSRF headers missing")
		}
		if raw, _ := io.ReadAll(r.Body); len(raw) != 0 {
			t.Error("restore GET must not carry credentials in a body")
		}
		if extra != nil && extra(w, r) {
			return
		}
		switch r.URL.Path {
		case "/api/web/v1/me":
			fmt.Fprintf(w, `{"kind":%q}`, kind)
		case "/api/web/v1/account/me":
			fmt.Fprint(w, `{"subscription":{"days_left":17,"devices":3},"features":{"support":true}}`)
		case "/api/web/v1/account/devices":
			fmt.Fprint(w, devices)
		case "/api/web/v1/account/devices/" + restoreDeviceID + "/connect":
			if linkStatus != 0 && linkStatus != 200 {
				w.WriteHeader(linkStatus)
				fmt.Fprint(w, `{"error":{"code":"not_found","message":"Устройство недоступно"}}`)
				return
			}
			fmt.Fprint(w, `{"sub_url":"https://qntw.link/sub/abcdefghijklmnop"}`)
		default:
			t.Errorf("unexpected restore request %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	origin = server.URL
	return server, &requests
}

func TestRestoreSessionReusesExistingNameOrRenamedBindingWithoutWriting(t *testing.T) {
	for _, source := range []string{"name", "known-id"} {
		t.Run(source, func(t *testing.T) {
			binding, installation, name := "", restoreInstallation, desktopmeta.Label()+" "+strings.ReplaceAll(restoreInstallation, "-", "")
			if source == "known-id" {
				binding, installation, name = restoreDeviceID+"\n", "", "Переименованный компьютер"
			}
			dir := restoreFixtureConfig(t, binding, installation)
			server, requests := restoreServer(t, "retail", fmt.Sprintf(`{"devices":[{"id":%q,"name":%q,"platform":"linux"}]}`, restoreDeviceID, name), 200, nil)
			defer server.Close()
			session, err := (Client{HTTP: server.Client(), base: server.URL}).RestoreSession(context.Background(), "qw_session=saved")
			if err != nil || session.CurrentDevice != restoreDeviceID || session.DeviceLink == "" || session.Cookie != "qw_session=saved" || session.Identity["kind"] != "retail" || session.Account["subscription"] == nil {
				t.Fatalf("restore failed: %v", err)
			}
			want := []string{"GET /api/web/v1/me", "GET /api/web/v1/account/me", "GET /api/web/v1/account/devices", "GET /api/web/v1/account/devices/" + restoreDeviceID + "/connect"}
			if !reflect.DeepEqual(*requests, want) {
				t.Fatalf("unexpected restore reads: %v", *requests)
			}
			for file, want := range map[string]string{"bindings": binding, "device-id": installation} {
				raw, err := os.ReadFile(filepath.Join(dir, file))
				if want == "" {
					if !os.IsNotExist(err) {
						t.Fatalf("restore created %s", file)
					}
				} else if err != nil || string(raw) != want {
					t.Fatalf("restore changed %s: %v", file, err)
				}
			}
		})
	}
}

func TestRestoreSessionMissingOrDeletedDeviceRequiresUserAction(t *testing.T) {
	for _, kind := range []string{"no-local-identity", "deleted"} {
		t.Run(kind, func(t *testing.T) {
			binding := ""
			if kind == "deleted" {
				binding = restoreDeviceID + "\n"
			}
			dir := restoreFixtureConfig(t, binding, "")
			server, requests := restoreServer(t, "member", fmt.Sprintf(`{"devices":[{"id":%q,"name":"Чужое устройство в этом аккаунте"}]}`, restoreOtherID), 200, nil)
			defer server.Close()
			session, err := (Client{HTTP: server.Client(), base: server.URL}).RestoreSession(context.Background(), "qw_session=saved")
			if err != nil || session.DeviceError == "" || session.CurrentDevice != "" || session.DeviceLink != "" || session.NeedsReplacement || len(session.Devices) != 1 {
				t.Fatalf("missing binding must not allocate/select another device: %+v %v", session, err)
			}
			if len(*requests) != 3 {
				t.Fatalf("restore read an unrelated link: %v", *requests)
			}
			if kind == "no-local-identity" {
				if _, err := os.Stat(dir); !os.IsNotExist(err) {
					t.Fatal("restore created a local installation directory")
				}
			}
		})
	}
}

func TestRestoreSessionBlockedAndAmbiguousBindingsDoNotFetchLinks(t *testing.T) {
	for _, scenario := range []string{"blocked", "ambiguous"} {
		t.Run(scenario, func(t *testing.T) {
			binding := restoreDeviceID + "\n"
			devices := fmt.Sprintf(`{"devices":[{"id":%q,"name":"Компьютер","blocked":true}]}`, restoreDeviceID)
			if scenario == "ambiguous" {
				binding += restoreOtherID + "\n"
				devices = fmt.Sprintf(`{"devices":[{"id":%q},{"id":%q}]}`, restoreDeviceID, restoreOtherID)
			}
			restoreFixtureConfig(t, binding, "")
			server, requests := restoreServer(t, "retail", devices, 200, nil)
			defer server.Close()
			session, err := (Client{HTTP: server.Client(), base: server.URL}).RestoreSession(context.Background(), "qw_session=saved")
			if err != nil || session.DeviceError == "" || session.DeviceLink != "" || len(*requests) != 3 {
				t.Fatalf("unsafe binding selected: %+v %v", session, err)
			}
			if scenario == "blocked" && session.CurrentDevice != restoreDeviceID {
				t.Fatal("blocked computer identity was lost")
			}
			if scenario == "ambiguous" && session.CurrentDevice != "" {
				t.Fatal("ambiguous binding was selected silently")
			}
		})
	}
}

func TestRestoreSessionOwnerReadsNoPersonalDevicesOrIdentityFiles(t *testing.T) {
	dir := restoreFixtureConfig(t, "", "")
	server, requests := restoreServer(t, "owner", `{"devices":[]}`, 200, nil)
	defer server.Close()
	session, err := (Client{HTTP: server.Client(), base: server.URL}).RestoreSession(context.Background(), "qw_session=saved")
	if err != nil || session.Identity["kind"] != "owner" || session.CurrentDevice != "" || len(*requests) != 2 {
		t.Fatalf("owner restore failed: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("owner restore wrote installation files")
	}
}

func TestRestoreSessionPreservesAuthAndTransientErrorStatus(t *testing.T) {
	for _, tc := range []struct {
		stage  string
		status int
	}{{"me", 401}, {"account/me", 403}, {"account/devices", 503}, {"account/devices/" + restoreDeviceID + "/connect", 503}, {"account/devices/" + restoreDeviceID + "/connect", 401}} {
		t.Run(fmt.Sprintf("%s-%d", tc.stage, tc.status), func(t *testing.T) {
			restoreFixtureConfig(t, restoreDeviceID+"\n", "")
			server, _ := restoreServer(t, "retail", fmt.Sprintf(`{"devices":[{"id":%q}]}`, restoreDeviceID), 200, func(w http.ResponseWriter, r *http.Request) bool {
				if r.URL.Path != "/api/web/v1/"+tc.stage {
					return false
				}
				w.WriteHeader(tc.status)
				fmt.Fprintf(w, `{"error":{"code":"%s","message":"Сервис недоступен"}}`, map[bool]string{true: "unauthorized", false: "internal"}[tc.status < 500])
				return true
			})
			defer server.Close()
			_, err := (Client{HTTP: server.Client(), base: server.URL}).RestoreSession(context.Background(), "qw_session=saved")
			var denied *HTTPError
			if !errors.As(err, &denied) || denied.Status != tc.status || denied.Code == "" {
				t.Fatalf("restore error classification lost: %v", err)
			}
		})
	}
}

func TestRestoreSessionDeviceDeletedBetweenReadsDoesNotRecreateIt(t *testing.T) {
	restoreFixtureConfig(t, restoreDeviceID+"\n", "")
	server, requests := restoreServer(t, "retail", fmt.Sprintf(`{"devices":[{"id":%q}]}`, restoreDeviceID), 404, nil)
	defer server.Close()
	session, err := (Client{HTTP: server.Client(), base: server.URL}).RestoreSession(context.Background(), "qw_session=saved")
	if err != nil || session.DeviceError == "" || session.CurrentDevice != "" || session.DeviceLink != "" || len(session.Devices) != 0 || len(*requests) != 4 {
		t.Fatalf("deleted device was recreated or retained: %+v %v", session, err)
	}
}

type restoreOfflineTransport struct{}

func (restoreOfflineTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("offline")
}

func TestRestoreSessionOfflineIsNotAnAuthenticationFailure(t *testing.T) {
	_, err := (Client{HTTP: &http.Client{Transport: restoreOfflineTransport{}}}).RestoreSession(context.Background(), "qw_session=saved")
	var denied *HTTPError
	if err == nil || errors.As(err, &denied) {
		t.Fatalf("offline must not become unauthorized: %v", err)
	}
}

func TestRestoreSessionRejectsInvalidCookieBeforeNetwork(t *testing.T) {
	calls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; fmt.Fprint(w, `{}`) }))
	defer server.Close()
	for _, cookie := range []string{"", "qw_session=", "qw_session=x; other=y", "qw_session=x\r\nInjected: x"} {
		_, err := (Client{HTTP: server.Client(), base: server.URL}).RestoreSession(context.Background(), cookie)
		var denied *HTTPError
		if !errors.As(err, &denied) || denied.Status != 401 || denied.Code != "invalid_cookie" {
			t.Fatalf("invalid cookie classification lost: %v", err)
		}
	}
	if calls != 0 {
		t.Fatal("invalid cookie reached service")
	}
}
