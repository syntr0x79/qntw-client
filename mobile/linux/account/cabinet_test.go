package account

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/syntr0x79/qntw-client/mobile/linux/desktopmeta"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

const cabinetTestID = "11111111-2222-4333-8444-555555555555"

func cabinetTestConfig(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("AppData", dir) // os.UserConfigDir on Windows
}

func TestCabinetForwardsAllowlistedCustomerActions(t *testing.T) {
	cabinetTestConfig(t)
	cases := []struct{ method, path, body, remote string }{
		{"GET", "me", "", "account/me"},
		{"GET", "devices", "", "account/devices"},
		{"POST", "devices", `{"name":"Телевизор","platform":"tv"}`, "account/devices"},
		{"POST", "devices", `{}`, "account/devices"},
		{"PATCH", "devices/" + cabinetTestID, `{"name":"Дом"}`, "account/devices/" + cabinetTestID},
		{"DELETE", "devices/" + cabinetTestID, "", "account/devices/" + cabinetTestID},
		{"POST", "devices/" + cabinetTestID + "/block", "", "account/devices/" + cabinetTestID + "/block"},
		{"POST", "devices/" + cabinetTestID + "/unblock", "", "account/devices/" + cabinetTestID + "/unblock"},
		{"POST", "devices/" + cabinetTestID + "/reissue", "", "account/devices/" + cabinetTestID + "/reissue"},
		{"GET", "devices/" + cabinetTestID + "/connect", "", "account/devices/" + cabinetTestID + "/connect"},
		{"GET", "platforms", "", "account/platforms"},
		{"GET", "plans", "", "account/plans"},
		{"POST", "invoices", `{"plan":"device","method":"sbp","devices":2}`, "account/invoices"},
		{"GET", "invoices?before=" + cabinetTestID, "", "account/invoices?before=" + cabinetTestID},
		{"GET", "invoices/" + cabinetTestID + "?poll=1", "", "account/invoices/" + cabinetTestID + "?poll=1"},
		{"GET", "history?before=12345", "", "account/history?before=12345"},
		{"POST", "subscription/pause", "", "account/subscription/pause"},
		{"POST", "subscription/resume", "", "account/subscription/resume"},
		{"GET", "promo", "", "account/promo"},
		{"POST", "promo", `{"code":"GIFT"}`, "account/promo"},
		{"DELETE", "promo", "", "account/promo"},
		{"GET", "referral", "", "account/referral"},
		{"GET", "support", "", "account/support"},
		{"GET", "support/tickets/42", "", "account/support/tickets/42"},
		{"POST", "support/messages", `{"body":"Не подключается"}`, "account/support/messages"},
		{"GET", "tg-proxy?peek=1", "", "account/tg-proxy?peek=1"},
		{"POST", "tg-proxy/rotate", "", "account/tg-proxy/rotate"},
		{"POST", "telegram-link", "", "account/telegram-link"},
		{"POST", "purchases/attach", `{"code":"purchase-code"}`, "account/purchases/attach"},
		{"GET", "purchases/abcdefghijklmnop", "", "account/purchases/abcdefghijklmnop"},
		{"POST", "token", "", "token"},
		{"POST", "logout", "", "logout"},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			calls := 0
			var base string
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != tc.method || r.URL.RequestURI() != "/api/web/v1/"+tc.remote {
					t.Errorf("unexpected action %s %s", r.Method, r.URL.RequestURI())
				}
				if r.Header.Get("Cookie") != "qw_session=test" || r.Header.Get("Origin") != base || r.Header.Get("X-Quantway") != "1" {
					t.Error("cabinet authorization/CSRF headers missing")
				}
				raw, _ := io.ReadAll(r.Body)
				if tc.body != "" {
					var want, got map[string]any
					_ = json.Unmarshal([]byte(tc.body), &want)
					if json.Unmarshal(raw, &got) != nil || !reflect.DeepEqual(got, want) {
						t.Errorf("unexpected request body %s", raw)
					}
				} else if len(raw) != 0 {
					t.Error("unexpected mutation body")
				}
				if tc.path == "logout" {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				fmt.Fprint(w, `{"message":"Готово","devices":[],"in_use":2,"paid":4}`)
			}))
			defer server.Close()
			base = server.URL
			data, err := (Client{HTTP: server.Client(), base: base}).Cabinet(context.Background(), CabinetRequest{
				Cookie: "qw_session=test", Method: tc.method, Path: tc.path, Body: json.RawMessage(tc.body),
			})
			if err != nil || calls != 1 {
				t.Fatalf("request failed/retried: %v calls=%d", err, calls)
			}
			if tc.path == "logout" {
				if data == nil || len(data) != 0 {
					t.Fatalf("204 must be an empty data object: %v", data)
				}
			} else if data["paid"] != float64(4) {
				t.Fatal("full API metadata was not preserved")
			}
		})
	}
}

func TestCabinetRejectsUnsafeRoutesBodiesAndCredentialsBeforeNetwork(t *testing.T) {
	cases := []CabinetRequest{
		{Method: "GET", Path: "https://other.invalid/api"},
		{Method: "GET", Path: "//other.invalid/devices"},
		{Method: "GET", Path: "/devices"},
		{Method: "GET", Path: "../org/settings"},
		{Method: "GET", Path: "devices%2f..%2fme"},
		{Method: "GET", Path: "devices\\..\\me"},
		{Method: "GET", Path: "org/people"},
		{Method: "POST", Path: "devices/buy"},
		{Method: "PUT", Path: "devices/" + cabinetTestID},
		{Method: "POST", Path: "logout?next=https://other.invalid"},
		{Method: "GET", Path: "devices/not-a-uuid/connect"},
		{Method: "DELETE", Path: "devices/11111111222243338444555555555555"},
		{Method: "GET", Path: "support/tickets/-1"},
		{Method: "GET", Path: "support/tickets/9223372036854775808"},
		{Method: "GET", Path: "invoices?before=not-a-uuid"},
		{Method: "GET", Path: "history?before=0"},
		{Method: "GET", Path: "invoices?before=" + cabinetTestID + "&before=" + cabinetTestID},
		{Method: "GET", Path: "devices?user_id=" + cabinetTestID},
		{Method: "GET", Path: "tg-proxy?peek=0"},
		{Method: "GET", Path: "devices", Body: json.RawMessage(`{"name":"injected"}`)},
		{Method: "POST", Path: "devices", Body: json.RawMessage(`{"name":"x","user_id":"foreign"}`)},
		{Method: "POST", Path: "devices", Body: json.RawMessage(`{"platform":"../../org"}`)},
		{Method: "PATCH", Path: "devices/" + cabinetTestID, Body: json.RawMessage(`{"name":" "}`)},
		{Method: "PATCH", Path: "devices/" + cabinetTestID, Body: json.RawMessage(`{"name":` + strconvQuote(strings.Repeat("я", 41)) + `}`)},
		{Method: "POST", Path: "promo", Body: json.RawMessage(`{"code":" "}`)},
		{Method: "POST", Path: "invoices", Body: json.RawMessage(`{"plan":"p","method":"m","devices":-1}`)},
		{Method: "POST", Path: "invoices", Body: json.RawMessage(`{"plan":"p","method":"m","devices":1.5}`)},
		{Method: "POST", Path: "support/messages", Body: json.RawMessage(`{"body":" "}`)},
		{Method: "POST", Path: "support/messages", Body: json.RawMessage(`{"body":` + strconvQuote(strings.Repeat("a", 4001)) + `}`)},
		{Method: "POST", Path: "devices", Body: json.RawMessage(`[]`)},
		{Method: "POST", Path: "devices", Body: json.RawMessage(`null`)},
		{Method: "POST", Path: "devices", Body: json.RawMessage(`{} {}`)},
	}
	for _, cookie := range []string{"", "qw_session=", "qw_session=x; other=y", "qw_session=x\r\nInjected: y", "qw_session=x y", "qw_session=\"quoted\""} {
		cases = append(cases, CabinetRequest{Cookie: cookie, Method: "GET", Path: "devices"})
	}
	calls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; fmt.Fprint(w, `{}`) }))
	defer server.Close()
	client := Client{HTTP: server.Client(), base: server.URL}
	for i, req := range cases {
		if i < len(cases)-6 {
			req.Cookie = "qw_session=test"
		}
		if _, err := client.Cabinet(context.Background(), req); err == nil {
			t.Errorf("unsafe request accepted: %d %s %s", i, req.Method, req.Path)
		}
	}
	if calls != 0 {
		t.Fatalf("rejected requests reached the network: %d", calls)
	}
}

func strconvQuote(value string) string { raw, _ := json.Marshal(value); return string(raw) }

func TestCabinetDoesNotTurnNullableDeviceCountIntoZero(t *testing.T) {
	route, err := resolveCabinet("POST", "invoices")
	if err != nil {
		t.Fatal(err)
	}
	body, err := validateCabinetBody(route, json.RawMessage(`{"plan":"p","method":"m","devices":null}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := body["devices"]; exists {
		t.Fatal("null must preserve the server's current allowance")
	}
}

func TestCabinetPreservesServerPermissionAndMutationFailuresWithoutRetry(t *testing.T) {
	for _, status := range []int{401, 403, 404, 409, 429, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			calls := 0
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.WriteHeader(status)
				fmt.Fprint(w, `{"error":{"code":"org_managed","message":"Этим аккаунтом управляет организация."}}`)
			}))
			defer server.Close()
			_, err := (Client{HTTP: server.Client(), base: server.URL}).Cabinet(context.Background(), CabinetRequest{Cookie: "qw_session=test", Method: "DELETE", Path: "devices/" + cabinetTestID})
			var apiError *HTTPError
			if !errors.As(err, &apiError) || apiError.Status != status || apiError.Code != "org_managed" || apiError.Message != "Этим аккаунтом управляет организация." || calls != 1 {
				t.Fatalf("error metadata lost/retried: %+v calls=%d", err, calls)
			}
		})
	}
}

func TestCabinetNeverFollowsRedirectsWithSessionCookie(t *testing.T) {
	calls := 0
	other := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; fmt.Fprint(w, `{}`) }))
	defer other.Close()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	_, err := (Client{HTTP: server.Client(), base: server.URL}).Cabinet(context.Background(), CabinetRequest{Cookie: "qw_session=test", Method: "GET", Path: "devices"})
	var apiError *HTTPError
	if !errors.As(err, &apiError) || apiError.Status != 307 || calls != 0 {
		t.Fatal("authenticated redirect was followed")
	}
}

func TestCabinetReissuePreservesKnownComputerAfterRename(t *testing.T) {
	cabinetTestConfig(t)
	const newID = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
	if err := rememberBinding(cabinetTestID); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/web/v1/account/devices/"+cabinetTestID+"/reissue" {
			t.Errorf("unexpected mutation: %s %s", r.Method, r.URL.Path)
		}
		fmt.Fprintf(w, `{"device":{"id":%q,"name":"My renamed PC"}}`, newID)
	}))
	defer server.Close()
	_, err := (Client{HTTP: server.Client(), base: server.URL}).Cabinet(context.Background(), CabinetRequest{Cookie: "qw_session=test", Method: "POST", Path: "devices/" + cabinetTestID + "/reissue"})
	if err != nil {
		t.Fatal(err)
	}
	known, err := knownBindings()
	if err != nil || !known[newID] {
		t.Fatal("new computer identity was not saved")
	}
}

func TestBlockedCurrentComputerKeepsItsIdentityWithoutCreatingAnother(t *testing.T) {
	cabinetTestConfig(t)
	if err := rememberBinding(cabinetTestID); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/api/web/v1/account/devices" {
			t.Errorf("blocked computer must not be created or connected: %s %s", r.Method, r.URL.Path)
		}
		fmt.Fprintf(w, `{"devices":[{"id":%q,"name":"Renamed PC","blocked":true}]}`, cabinetTestID)
	}))
	defer server.Close()
	id, link, err := (Client{HTTP: server.Client(), base: server.URL}).BindDevice(context.Background(), "qw_session=test", desktopmeta.Label()+" 11111111222243338444555555555555")
	if id != cabinetTestID || link != "" || err == nil {
		t.Fatalf("blocked computer identity lost: %s %v", id, err)
	}
}

func TestOwnerLoginCanContinueWithoutPersonalDevicePermission(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/web/v1/login" {
			http.SetCookie(w, &http.Cookie{Name: "qw_session", Value: "owner-session", Path: "/"})
			fmt.Fprint(w, `{}`)
			return
		}
		if r.URL.Path != "/api/web/v1/account/devices" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"error":{"code":"org_managed","message":"Этим аккаунтом управляет организация."}}`)
	}))
	defer server.Close()
	session, err := (Client{HTTP: server.Client(), base: server.URL}).Login(context.Background(), "owner-code")
	if err != nil || session.Cookie != "qw_session=owner-session" || len(session.Devices) != 0 {
		t.Fatalf("owner cannot proceed to identity snapshot: %v", err)
	}
}

func TestCabinetCodeRotationReturnsOnlyItsFreshSessionCookie(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "qw_session=before" {
			t.Error("old session was not used for rotation")
		}
		http.SetCookie(w, &http.Cookie{Name: "qw_session", Value: "after", HttpOnly: true, Secure: true, Path: "/"})
		http.SetCookie(w, &http.Cookie{Name: "unrelated", Value: "ignored", Path: "/"})
		if r.URL.Path == "/api/web/v1/token" {
			fmt.Fprint(w, `{"code":"new-code"}`)
		} else {
			fmt.Fprint(w, `{"subscription":{}}`)
		}
	}))
	defer server.Close()
	client := Client{HTTP: server.Client(), base: server.URL}
	data, err := client.Cabinet(context.Background(), CabinetRequest{Cookie: "qw_session=before", Method: "POST", Path: "token"})
	if err != nil || data["session_cookie"] != "qw_session=after" || data["code"] != "new-code" {
		t.Fatalf("session rotation metadata lost: %v", err)
	}
	data, err = client.Cabinet(context.Background(), CabinetRequest{Cookie: "qw_session=before", Method: "GET", Path: "me"})
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := data["session_cookie"]; exists {
		t.Fatal("cookies may only be returned for explicit token rotation")
	}
}

func TestCabinetSupportImageAndAttachmentAdapters(t *testing.T) {
	image, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/l1sAAAAASUVORK5CYII=")
	calls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Cookie") != "qw_session=test" || r.Header.Get("X-Quantway") != "1" {
			t.Error("image authorization missing")
		}
		if r.Method == "GET" {
			if r.URL.Path != "/api/web/v1/account/support/attachments/42" {
				t.Error("wrong attachment path")
			}
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(image)
			return
		}
		if r.URL.Path != "/api/web/v1/account/support/images" {
			t.Error("wrong image path")
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatal(err)
		}
		file, _, err := r.FormFile("image")
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		raw, _ := io.ReadAll(file)
		if !bytes.Equal(raw, image) || r.FormValue("caption") != "Скриншот" {
			t.Error("multipart image/caption mismatch")
		}
		fmt.Fprint(w, `{"ticket_id":42}`)
	}))
	defer server.Close()
	client := Client{HTTP: server.Client(), base: server.URL}
	body, _ := json.Marshal(map[string]string{"image_base64": base64.StdEncoding.EncodeToString(image), "caption": "Скриншот"})
	data, err := client.Cabinet(context.Background(), CabinetRequest{Cookie: "qw_session=test", Method: "POST", Path: "support/images", Body: body})
	if err != nil || data["ticket_id"] != float64(42) {
		t.Fatalf("image upload: %v", err)
	}
	data, err = client.Cabinet(context.Background(), CabinetRequest{Cookie: "qw_session=test", Method: "GET", Path: "support/attachments/42"})
	if err != nil || data["mime"] != "image/png" || data["image_base64"] != base64.StdEncoding.EncodeToString(image) {
		t.Fatalf("attachment download: %v", err)
	}
	for _, encoded := range []string{"not base64", base64.StdEncoding.EncodeToString([]byte("<svg></svg>")), strings.Repeat("A", base64.StdEncoding.EncodedLen(maxCabinetImage)+4)} {
		body, _ = json.Marshal(map[string]string{"image_base64": encoded})
		if _, err := client.Cabinet(context.Background(), CabinetRequest{Cookie: "qw_session=test", Method: "POST", Path: "support/images", Body: body}); err == nil {
			t.Fatal("invalid/oversized image accepted")
		}
	}
	if calls != 2 {
		t.Fatal("invalid images reached server")
	}
}
