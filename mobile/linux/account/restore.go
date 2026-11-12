package account

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/syntr0x79/qntw-client/mobile/linux/desktopmeta"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

// RestoreSession verifies an existing cookie and reuses only an installation
// already present in the account. Launching the app never allocates a device,
// rotates a link, or creates local installation identity files.
func (c Client) RestoreSession(ctx context.Context, cookie string) (Session, error) {
	if !validCookie(cookie) {
		return Session{}, &HTTPError{Status: http.StatusUnauthorized, Code: "invalid_cookie", Message: "Сохранённый вход недействителен. Войдите заново."}
	}
	identity, account, err := c.Snapshot(ctx, cookie)
	if err != nil {
		return Session{}, err
	}
	if identity == nil || account == nil {
		return Session{}, errors.New("Не удалось прочитать кабинет. Попробуйте ещё раз.")
	}
	session := Session{Cookie: cookie, Identity: identity, Account: account, Devices: []Device{}}
	if identity["kind"] == "owner" {
		return session, nil
	}
	raw, _, err := c.request(ctx, http.MethodGet, "account/devices", cookie, nil)
	if err != nil {
		return Session{}, err
	}
	var list struct {
		Devices []Device `json:"devices"`
	}
	if json.Unmarshal(raw, &list) != nil || list.Devices == nil {
		return Session{}, errors.New("Не удалось прочитать устройства. Попробуйте ещё раз.")
	}
	session.Devices = list.Devices
	name, known, err := existingInstallationBinding()
	if err != nil {
		session.DeviceError = "Не удалось определить привязку этого компьютера. Подключите его в кабинете."
		return session, nil
	}
	var matches []Device
	for _, device := range list.Devices {
		if (name != "" && device.Name == name) || known[strings.ToLower(device.ID)] {
			matches = append(matches, device)
		}
	}
	if len(matches) == 0 {
		session.DeviceError = "Этот компьютер не подключён к аккаунту. Подключите его в кабинете."
		return session, nil
	}
	if len(matches) != 1 || !canonicalUUID(matches[0].ID) {
		session.DeviceError = "Не удалось однозначно определить этот компьютер. Проверьте устройства в кабинете."
		return session, nil
	}
	device := matches[0]
	session.CurrentDevice = device.ID
	if device.Blocked {
		session.DeviceError = "Этот компьютер заблокирован. Разблокируйте его в списке устройств."
		return session, nil
	}
	session.DeviceLink, err = c.DeviceLink(ctx, cookie, device.ID)
	if err == nil {
		return session, nil
	}
	var apiError *HTTPError
	if errors.As(err, &apiError) {
		switch apiError.Status {
		case http.StatusNotFound:
			// The device may have been deleted between the list and link reads.
			session.CurrentDevice = ""
			session.DeviceError = "Этот компьютер удалён из аккаунта. Подключите его в кабинете."
			for index, item := range session.Devices {
				if item.ID == device.ID {
					session.Devices = append(session.Devices[:index], session.Devices[index+1:]...)
					break
				}
			}
			return session, nil
		case http.StatusConflict:
			session.DeviceError = apiError.Message
			if session.DeviceError == "" {
				session.DeviceError = "Подключение сейчас недоступно. Проверьте подписку и устройства в кабинете."
			}
			if apiError.Code == "blocked" {
				for index := range session.Devices {
					if session.Devices[index].ID == device.ID {
						session.Devices[index].Blocked = true
					}
				}
			}
			return session, nil
		}
	}
	// Auth errors and temporary service/network errors remain structured; the
	// GUI may clear an expired credential but must retain it while offline.
	return Session{}, err
}

func readExistingBindingFile(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(raw)) > limit {
		return nil, errors.New("invalid installation binding")
	}
	return raw, nil
}

// Unlike DeviceName/knownBindings, this path is strictly read-only, including
// when the configuration directory or either file does not exist yet.
func existingInstallationBinding() (string, map[string]bool, error) {
	root, err := os.UserConfigDir()
	if err != nil {
		return "", nil, err
	}
	dir := filepath.Join(root, "quantway")
	bindings, err := readExistingBindingFile(filepath.Join(dir, "bindings"), 64<<10)
	if err != nil {
		return "", nil, err
	}
	known := map[string]bool{}
	for _, id := range strings.Fields(string(bindings)) {
		if canonicalUUID(id) {
			known[strings.ToLower(id)] = true
		}
	}
	rawName, err := readExistingBindingFile(filepath.Join(dir, "device-id"), 512)
	if err != nil {
		return "", nil, err
	}
	name := ""
	if len(rawName) > 0 {
		id := strings.TrimSpace(string(rawName))
		if _, err = uuid.Parse(id); err != nil {
			return "", nil, errors.New("invalid device identity")
		}
		name = desktopmeta.Label() + " " + strings.ReplaceAll(id, "-", "")
	}
	return name, known, nil
}
