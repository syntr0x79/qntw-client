// Package account implements the existing Quantway cabinet protocol.
package account

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/syntr0x79/qntw-client/mobile/linux/desktopmeta"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	"strings"
	"time"
)

const origin = "https://qntw.link"

type Device struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Platform string `json:"platform"`
	Blocked  bool   `json:"blocked"`
}
type Session struct {
	Cookie           string         `json:"cookie"`
	Devices          []Device       `json:"devices"`
	Identity         map[string]any `json:"identity,omitempty"`
	Account          map[string]any `json:"account,omitempty"`
	CurrentDevice    string         `json:"current_device,omitempty"`
	DeviceLink       string         `json:"device_link,omitempty"`
	DeviceError      string         `json:"device_error,omitempty"`
	NeedsReplacement bool           `json:"needs_replacement,omitempty"`
}
type HTTPError struct {
	Status  int
	Code    string
	Message string
}

func (e *HTTPError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return fmt.Sprintf("service returned HTTP %d", e.Status)
}

type Client struct {
	HTTP *http.Client
	base string
}

func (c Client) request(ctx context.Context, method, path, cookie string, body any) ([]byte, *http.Response, error) {
	base := c.base
	if base == "" {
		base = origin
	}
	var reader io.Reader
	if body != nil {
		b, e := json.Marshal(body)
		if e != nil {
			return nil, nil, e
		}
		reader = bytes.NewReader(b)
	}
	req, e := http.NewRequestWithContext(ctx, method, base+"/api/web/v1/"+path, reader)
	if e != nil {
		return nil, nil, errors.New("invalid request")
	}
	req.Header.Set("Origin", base)
	req.Header.Set("X-Quantway", "1")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", desktopmeta.UserAgent())
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	client := http.Client{Timeout: 20 * time.Second}
	if c.HTTP != nil {
		client = *c.HTTP
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, e := client.Do(req)
	if e != nil {
		return nil, nil, errors.New("service unavailable")
	}
	defer res.Body.Close()
	data, e := io.ReadAll(io.LimitReader(res.Body, (2<<20)+1))
	if e != nil || len(data) > 2<<20 {
		return nil, nil, errors.New("invalid response")
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, nil, responseError(res.StatusCode, data)
	}
	return data, res, nil
}
func (c Client) Login(ctx context.Context, code string) (Session, error) {
	code = strings.TrimSpace(code)
	if code == "" || len(code) > 512 {
		return Session{}, errors.New("invalid login code")
	}
	_, res, e := c.request(ctx, "POST", "login", "", map[string]string{"code": code})
	if e != nil {
		return Session{}, e
	}
	cookie := ""
	for _, v := range res.Cookies() {
		if v.Name == "qw_session" && v.Value != "" && v.MaxAge >= 0 {
			cookie = (&http.Cookie{Name: v.Name, Value: v.Value}).String()
			break
		}
	}
	if !validCookie(cookie) {
		return Session{}, errors.New("missing session")
	}
	data, _, e := c.request(ctx, "GET", "account/devices", cookie, nil)
	if e != nil {
		// Organization owners can sign in and use support/invoices but do not
		// have personal devices. The caller reads the authoritative identity
		// before attempting installation binding.
		var denied *HTTPError
		if errors.As(e, &denied) && denied.Status == http.StatusForbidden {
			return Session{Cookie: cookie, Devices: []Device{}}, nil
		}
		return Session{}, e
	}
	var result struct {
		Devices []Device `json:"devices"`
	}
	if json.Unmarshal(data, &result) != nil {
		return Session{}, errors.New("invalid response")
	}
	return Session{Cookie: cookie, Devices: result.Devices}, nil
}
func (c Client) DeviceLink(ctx context.Context, cookie, id string) (string, error) {
	if !canonicalUUID(id) || !validCookie(cookie) {
		return "", errors.New("invalid session or device")
	}
	data, _, e := c.request(ctx, "GET", "account/devices/"+id+"/connect", cookie, nil)
	if e != nil {
		return "", e
	}
	var result struct {
		Link string `json:"sub_url"`
	}
	if json.Unmarshal(data, &result) != nil {
		return "", errors.New("invalid response")
	}
	u, e := url.Parse(result.Link)
	if e != nil || u.Scheme != "https" || u.Host != "qntw.link" || u.User != nil || u.Fragment != "" {
		return "", errors.New("invalid device link")
	}
	return result.Link, nil
}

// Snapshot uses the same cabinet endpoints as Android. No account secrets are persisted.
func (c Client) Snapshot(ctx context.Context, cookie string) (map[string]any, map[string]any, error) {
	if !validCookie(cookie) {
		return nil, nil, errors.New("invalid session")
	}
	var identity, account map[string]any
	raw, _, err := c.request(ctx, "GET", "me", cookie, nil)
	if err != nil {
		return nil, nil, err
	}
	if json.Unmarshal(raw, &identity) != nil {
		return nil, nil, errors.New("invalid identity")
	}
	raw, _, err = c.request(ctx, "GET", "account/me", cookie, nil)
	if err != nil {
		return nil, nil, err
	}
	if json.Unmarshal(raw, &account) != nil {
		return nil, nil, errors.New("invalid account")
	}
	return identity, account, nil
}
func validCookie(cookie string) bool {
	if !strings.HasPrefix(cookie, "qw_session=") || len(cookie) <= len("qw_session=") || len(cookie) > 4096 {
		return false
	}
	for _, b := range []byte(strings.TrimPrefix(cookie, "qw_session=")) {
		if b < 0x21 || b > 0x7e || strings.ContainsRune("\";,\\", rune(b)) {
			return false
		}
	}
	return true
}

// DeviceName is installation-specific, stable across restarts, and non-secret.
// Atomic exclusive creation avoids creating multiple identities on concurrent starts.
func DeviceName() (string, error) {
	root, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(root, "quantway")
	if err = os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "device-id")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err == nil {
		_, err = f.WriteString(uuid.NewString())
		closeErr := f.Close()
		if err != nil {
			return "", err
		}
		if closeErr != nil {
			return "", closeErr
		}
	} else if !os.IsExist(err) {
		return "", err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(string(b))
	if _, err = uuid.Parse(id); err != nil {
		return "", errors.New("invalid device identity")
	}
	return desktopmeta.Label() + " " + strings.ReplaceAll(id, "-", ""), nil
}

// Local bindings contain only server-side device IDs, never subscription tokens.
func bindingPath() (string, error) {
	root, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(root, "quantway")
	if err = os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	return filepath.Join(dir, "bindings"), nil
}
func knownBindings() (map[string]bool, error) {
	path, err := bindingPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]bool{}, nil
	}
	if err != nil {
		return nil, err
	}
	if len(data) > 64<<10 {
		return nil, errors.New("invalid bindings")
	}
	ids := map[string]bool{}
	for _, id := range strings.Fields(string(data)) {
		if canonicalUUID(id) {
			ids[id] = true
		}
	}
	return ids, nil
}
func rememberBinding(id string) error {
	if !canonicalUUID(id) {
		return errors.New("invalid device")
	}
	known, err := knownBindings()
	if err != nil {
		return err
	}
	if known[id] {
		return nil
	}
	path, err := bindingPath()
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	_, writeErr := fmt.Fprintln(f, id)
	return errors.Join(writeErr, f.Close())
}

// RefreshBinding retries automatic binding after a slot was freed elsewhere and
// returns a fresh replacement list when the subscription remains full.
func (c Client) RefreshBinding(ctx context.Context, cookie string) (Session, error) {
	if !validCookie(cookie) {
		return Session{}, errors.New("invalid session")
	}
	raw, _, err := c.request(ctx, "GET", "account/devices", cookie, nil)
	if err != nil {
		return Session{}, err
	}
	var session Session
	if json.Unmarshal(raw, &session) != nil {
		return Session{}, errors.New("invalid device list")
	}
	name, err := DeviceName()
	if err == nil {
		session.CurrentDevice, session.DeviceLink, err = c.BindDevice(ctx, cookie, name)
	}
	if err != nil {
		var apiError *HTTPError
		if session.CurrentDevice == "" && errors.As(err, &apiError) && apiError.Status == http.StatusConflict {
			session.NeedsReplacement = true
			session.DeviceError = "Нет свободных мест. Выберите устройство для замены."
		} else {
			session.DeviceError = "Не удалось подключить этот компьютер. Проверьте подписку и попробуйте снова."
		}
		return session, nil
	}
	found := false
	for _, device := range session.Devices {
		found = found || device.ID == session.CurrentDevice
	}
	if !found {
		session.Devices = append(session.Devices, Device{ID: session.CurrentDevice, Name: "Этот компьютер", Platform: desktopmeta.Platform()})
	}
	return session, nil
}

// BindDevice reuses only a device belonging to this installation.
func (c Client) BindDevice(ctx context.Context, cookie, name string) (string, string, error) {
	if !validCookie(cookie) || !strings.HasPrefix(name, desktopmeta.Label()+" ") {
		return "", "", errors.New("invalid request")
	}
	if _, err := uuid.Parse(strings.TrimPrefix(name, desktopmeta.Label()+" ")); err != nil {
		return "", "", errors.New("invalid device identity")
	}
	raw, _, err := c.request(ctx, "GET", "account/devices", cookie, nil)
	if err != nil {
		return "", "", err
	}
	var list struct {
		Devices   []Device `json:"devices"`
		LinkWorks bool     `json:"link_works"`
	}
	if json.Unmarshal(raw, &list) != nil {
		return "", "", errors.New("invalid device list")
	}
	known, err := knownBindings()
	if err != nil {
		return "", "", err
	}
	id := ""
	for _, d := range list.Devices {
		if d.Name == name || known[d.ID] {
			if d.Blocked {
				return d.ID, "", errors.New("device blocked")
			}
			if id != "" {
				return "", "", errors.New("ambiguous device identity")
			}
			id = d.ID
		}
	}
	if id == "" {
		raw, _, err = c.request(ctx, "POST", "account/devices", cookie, map[string]string{"name": name, "platform": desktopmeta.Platform()})
		if err != nil {
			return "", "", err
		}
		var created struct {
			Device Device `json:"device"`
		}
		if json.Unmarshal(raw, &created) != nil || !canonicalUUID(created.Device.ID) {
			return "", "", errors.New("invalid created device")
		}
		id = created.Device.ID
	}
	if err = rememberBinding(id); err != nil {
		return id, "", err
	}
	link, err := c.DeviceLink(ctx, cookie, id)
	if err != nil {
		return id, "", err
	}
	return id, link, nil
}

// ReplaceDevice atomically revokes the chosen device and reissues its slot.
func (c Client) ReplaceDevice(ctx context.Context, cookie, id, name string) (string, string, error) {
	if !validCookie(cookie) || !canonicalUUID(id) || !strings.HasPrefix(name, desktopmeta.Label()+" ") {
		return "", "", errors.New("invalid request")
	}
	raw, _, err := c.request(ctx, "POST", "account/devices/"+id+"/reissue", cookie, map[string]any{})
	if err != nil {
		return "", "", err
	}
	var created struct {
		Device Device `json:"device"`
	}
	if json.Unmarshal(raw, &created) != nil || !canonicalUUID(created.Device.ID) {
		return "", "", errors.New("invalid replacement")
	}
	newID := created.Device.ID
	if err = rememberBinding(newID); err != nil {
		return newID, "", err
	}
	// The atomic replacement retains the previous name. Rename for clarity; the
	// stored device ID still allows recovery if this separate call fails.
	_, _, _ = c.request(ctx, "PATCH", "account/devices/"+newID, cookie, map[string]string{"name": name})
	link, err := c.DeviceLink(ctx, cookie, newID)
	return newID, link, err
}

func (c Client) Plans(ctx context.Context, cookie string) (map[string]any, error) {
	if !validCookie(cookie) {
		return nil, errors.New("invalid session")
	}
	raw, _, err := c.request(ctx, "GET", "account/plans", cookie, nil)
	if err != nil {
		return nil, err
	}
	var data map[string]any
	if json.Unmarshal(raw, &data) != nil {
		return nil, errors.New("invalid plans")
	}
	return data, nil
}
func (c Client) Invoice(ctx context.Context, cookie, plan, method string, devices *int) (map[string]any, error) {
	if !validCookie(cookie) || len(plan) == 0 || len(plan) > 128 || len(method) == 0 || len(method) > 128 {
		return nil, errors.New("invalid invoice")
	}
	body := map[string]any{"plan": plan, "method": method}
	if devices != nil {
		if *devices < 0 || *devices > 10000 {
			return nil, errors.New("invalid device count")
		}
		body["devices"] = *devices
	}
	raw, _, err := c.request(ctx, "POST", "account/invoices", cookie, body)
	if err != nil {
		return nil, err
	}
	var data map[string]any
	if json.Unmarshal(raw, &data) != nil {
		return nil, errors.New("invalid invoice")
	}
	return data, nil
}

func (c Client) InvoiceStatus(ctx context.Context, cookie, id string) (map[string]any, error) {
	if !validCookie(cookie) || !canonicalUUID(id) {
		return nil, errors.New("invalid invoice request")
	}
	raw, _, err := c.request(ctx, "GET", "account/invoices/"+id, cookie, nil)
	if err != nil {
		return nil, err
	}
	var data map[string]any
	if json.Unmarshal(raw, &data) != nil {
		return nil, errors.New("invalid invoice")
	}
	return data, nil
}
