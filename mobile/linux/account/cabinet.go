package account

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/syntr0x79/qntw-client/mobile/linux/desktopmeta"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// CabinetRequest has no origin or arbitrary URL: paths are an allowlist of the
// existing customer cabinet APIs. Session credentials arrive only on stdin.
type CabinetRequest struct {
	Cookie string          `json:"cookie"`
	Method string          `json:"method"`
	Path   string          `json:"path"`
	Body   json.RawMessage `json:"body,omitempty"`
}

const maxCabinetImage = 10 << 20

var apiCode = regexp.MustCompile(`^[a-z0-9_]{1,64}$`)

func responseError(status int, raw []byte) *HTTPError {
	err := &HTTPError{Status: status}
	var data struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &data) == nil {
		if apiCode.MatchString(data.Error.Code) {
			err.Code = data.Error.Code
		}
		if utf8.ValidString(data.Error.Message) && utf8.RuneCountInString(data.Error.Message) <= 2000 {
			err.Message = data.Error.Message
		}
	}
	return err
}

func canonicalUUID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil && strings.EqualFold(value, id.String())
}

func positiveID(value string) bool {
	n, err := strconv.ParseInt(value, 10, 64)
	return err == nil && n > 0 && strconv.FormatInt(n, 10) == value
}

type cabinetRoute struct {
	path    string
	fields  map[string]string
	query   map[string]string
	binary  bool
	image   bool
	reissue string
}

// Resolve only paths already mounted by internal/api.routes and webapi.New.
// Role and ownership authorization remain enforced by the existing server.
func resolveCabinet(method, rawPath string) (cabinetRoute, error) {
	invalid := errors.New("Недопустимое действие кабинета.")
	if len(rawPath) == 0 || len(rawPath) > 1024 || strings.ContainsAny(rawPath, "\\#%\r\n\t ") {
		return cabinetRoute{}, invalid
	}
	u, err := url.Parse(rawPath)
	if err != nil || u.IsAbs() || u.Host != "" || strings.HasPrefix(u.Path, "/") || strings.Contains(u.Path, "..") {
		return cabinetRoute{}, invalid
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return cabinetRoute{}, invalid
	}
	route := cabinetRoute{path: "account/" + u.Path}
	allow := func(fields map[string]string, queries map[string]string) (cabinetRoute, error) {
		route.fields, route.query = fields, queries
		for key, values := range query {
			if len(values) != 1 || values[0] == "" {
				return cabinetRoute{}, invalid
			}
			switch queries[key] {
			case "uuid":
				if !canonicalUUID(values[0]) {
					return cabinetRoute{}, invalid
				}
			case "id":
				if !positiveID(values[0]) {
					return cabinetRoute{}, invalid
				}
			case "one":
				if values[0] != "1" {
					return cabinetRoute{}, invalid
				}
			default:
				return cabinetRoute{}, invalid
			}
		}
		if encoded := query.Encode(); encoded != "" {
			route.path += "?" + encoded
		}
		return route, nil
	}
	if (u.Path == "logout" || u.Path == "token") && method == "POST" {
		route.path = u.Path
		return allow(nil, nil)
	}
	switch method + " " + u.Path {
	case "GET me", "GET devices", "GET platforms", "GET plans", "GET promo", "GET referral", "GET support":
		return allow(nil, nil)
	case "GET invoices":
		return allow(nil, map[string]string{"before": "uuid"})
	case "GET history":
		return allow(nil, map[string]string{"before": "id"})
	case "GET tg-proxy":
		return allow(nil, map[string]string{"peek": "one"})
	case "POST subscription/pause", "POST subscription/resume", "DELETE promo", "POST telegram-link", "POST tg-proxy/rotate":
		return allow(nil, nil)
	case "POST devices":
		return allow(map[string]string{"name": "name?", "platform": "platform?"}, nil)
	case "POST promo":
		return allow(map[string]string{"code": "code"}, nil)
	case "POST invoices":
		return allow(map[string]string{"plan": "key", "method": "key", "devices": "count?"}, nil)
	case "POST support/messages":
		return allow(map[string]string{"body": "message"}, nil)
	case "POST support/images":
		route.image = true
		return allow(map[string]string{"image_base64": "image", "caption": "caption?"}, nil)
	case "POST purchases/attach":
		return allow(map[string]string{"code": "code"}, nil)
	}
	parts := strings.Split(u.Path, "/")
	if len(parts) >= 2 && parts[0] == "devices" && canonicalUUID(parts[1]) {
		if len(parts) == 2 {
			switch method {
			case "PATCH":
				return allow(map[string]string{"name": "name"}, nil)
			case "DELETE":
				return allow(nil, nil)
			}
		}
		if len(parts) == 3 {
			if parts[2] == "connect" && method == "GET" {
				return allow(nil, nil)
			}
			if method == "POST" && (parts[2] == "block" || parts[2] == "unblock" || parts[2] == "reissue") {
				if parts[2] == "reissue" {
					route.reissue = parts[1]
				}
				return allow(nil, nil)
			}
		}
	}
	if len(parts) == 2 && parts[0] == "invoices" && canonicalUUID(parts[1]) && method == "GET" {
		return allow(nil, map[string]string{"poll": "one"})
	}
	if len(parts) == 3 && parts[0] == "support" && positiveID(parts[2]) && method == "GET" {
		if parts[1] == "tickets" {
			return allow(nil, nil)
		}
		if parts[1] == "attachments" {
			route.binary = true
			return allow(nil, nil)
		}
	}
	if len(parts) == 2 && parts[0] == "purchases" && method == "GET" && len(parts[1]) >= 16 && len(parts[1]) <= 256 {
		for _, ch := range parts[1] {
			if !(ch >= 'A' && ch <= 'Z' || ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || ch == '-' || ch == '_') {
				return cabinetRoute{}, invalid
			}
		}
		return allow(nil, nil)
	}
	return cabinetRoute{}, invalid
}

func validateCabinetBody(route cabinetRoute, raw json.RawMessage) (map[string]any, error) {
	invalid := errors.New("Проверьте поля запроса кабинета.")
	if !route.image && len(raw) > 4<<10 {
		return nil, invalid
	}
	fields := map[string]json.RawMessage{}
	if len(raw) > 0 && (raw[0] != '{' || json.Unmarshal(raw, &fields) != nil || fields == nil) {
		return nil, invalid
	}
	body := map[string]any{}
	for name := range fields {
		if _, ok := route.fields[name]; !ok {
			return nil, invalid
		}
	}
	for name, rule := range route.fields {
		value, exists := fields[name]
		optional := strings.HasSuffix(rule, "?")
		rule = strings.TrimSuffix(rule, "?")
		if !exists {
			if optional {
				continue
			}
			return nil, invalid
		}
		if optional && bytes.Equal(value, []byte("null")) {
			continue
		}
		if rule == "count" {
			var count int
			if json.Unmarshal(value, &count) != nil || count < 0 || count > 10000 {
				return nil, invalid
			}
			body[name] = count
			continue
		}
		var text string
		if json.Unmarshal(value, &text) != nil || !utf8.ValidString(text) || strings.ContainsRune(text, 0) {
			return nil, invalid
		}
		switch rule {
		case "name":
			if utf8.RuneCountInString(text) > 40 || (!optional && strings.TrimSpace(text) == "") {
				return nil, errors.New("Название должно содержать от 1 до 40 символов.")
			}
		case "platform":
			if text != "" && text != "android" && text != "ios" && text != "macos" && text != "linux" && text != "windows" && text != "tv" {
				return nil, invalid
			}
		case "key":
			if strings.TrimSpace(text) == "" || len(text) > 128 {
				return nil, invalid
			}
		case "code":
			if strings.TrimSpace(text) == "" || len(text) > 512 {
				return nil, invalid
			}
		case "message", "caption":
			if utf8.RuneCountInString(text) > 4000 || (rule == "message" && strings.TrimSpace(text) == "") {
				return nil, invalid
			}
		case "image":
			if len(text) == 0 || len(text) > base64.StdEncoding.EncodedLen(maxCabinetImage) {
				return nil, invalid
			}
		}
		body[name] = text
	}
	return body, nil
}

// Cabinet preserves the original JSON API object; no customer identity, role,
// ownership, or permissions are inferred from fields supplied by the UI.
func (c Client) Cabinet(ctx context.Context, req CabinetRequest) (map[string]any, error) {
	if !validCookie(req.Cookie) {
		return nil, &HTTPError{Status: http.StatusUnauthorized, Code: "unauthorized", Message: "Войдите заново."}
	}
	route, err := resolveCabinet(req.Method, req.Path)
	if err != nil {
		return nil, err
	}
	body, err := validateCabinetBody(route, req.Body)
	if err != nil {
		return nil, err
	}
	if route.image {
		return c.cabinetImage(ctx, req.Cookie, route.path, body)
	}
	if route.binary {
		return c.cabinetAttachment(ctx, req.Cookie, route.path)
	}
	ours := false
	if route.reissue != "" {
		known, err := knownBindings()
		if err != nil {
			return nil, errors.New("Не удалось прочитать привязку этого компьютера.")
		}
		ours = known[route.reissue]
	}
	var payload any
	if req.Method != "GET" && (len(body) > 0 || route.fields != nil) {
		payload = body
	}
	raw, response, err := c.request(ctx, req.Method, route.path, req.Cookie, payload)
	if err != nil {
		return nil, err
	}
	data := map[string]any{}
	if len(raw) > 0 && (json.Unmarshal(raw, &data) != nil || data == nil) {
		return nil, errors.New("Не удалось прочитать ответ кабинета.")
	}
	if route.path == "token" {
		// The code rotation revokes the previous session prefix and issues a
		// fresh cookie for this client, just as it does for the current web tab.
		for _, cookie := range response.Cookies() {
			if cookie.Name == "qw_session" && cookie.Value != "" && cookie.MaxAge >= 0 {
				value := (&http.Cookie{Name: cookie.Name, Value: cookie.Value}).String()
				if validCookie(value) {
					data["session_cookie"] = value
				}
			}
		}
	}
	if ours {
		device, ok := data["device"].(map[string]any)
		id, valid := device["id"].(string)
		if !ok || !valid || !canonicalUUID(id) {
			return nil, errors.New("Не удалось прочитать новое устройство.")
		}
		if rememberBinding(id) != nil {
			data["binding_warning"] = "Новая ссылка выпущена. Не удалось сохранить привязку этого компьютера; сохраните новую ссылку перед выходом."
		}
	}
	return data, nil
}

func (c Client) cabinetBinaryRequest(ctx context.Context, method, path, cookie, contentType string, body io.Reader) ([]byte, string, error) {
	base := c.base
	if base == "" {
		base = origin
	}
	req, err := http.NewRequestWithContext(ctx, method, base+"/api/web/v1/"+path, body)
	if err != nil {
		return nil, "", errors.New("Некорректный запрос.")
	}
	req.Header.Set("Origin", base)
	req.Header.Set("X-Quantway", "1")
	req.Header.Set("Cookie", cookie)
	req.Header.Set("User-Agent", desktopmeta.UserAgent())
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	client := http.Client{Timeout: 20 * time.Second}
	if c.HTTP != nil {
		client = *c.HTTP
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(req)
	if err != nil {
		return nil, "", errors.New("Не удалось связаться с сервисом.")
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxCabinetImage+1))
	if err != nil || len(raw) > maxCabinetImage {
		return nil, "", errors.New("Слишком большой ответ сервиса.")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, "", responseError(response.StatusCode, raw)
	}
	return raw, response.Header.Get("Content-Type"), nil
}

func (c Client) cabinetImage(ctx context.Context, cookie, path string, body map[string]any) (map[string]any, error) {
	image, err := base64.StdEncoding.Strict().DecodeString(body["image_base64"].(string))
	if err != nil || len(image) == 0 || len(image) > maxCabinetImage {
		return nil, errors.New("Изображение должно быть PNG или JPEG не больше 10 МБ.")
	}
	mime := http.DetectContentType(image)
	if mime != "image/png" && mime != "image/jpeg" {
		return nil, errors.New("Выберите скриншот в PNG или JPEG.")
	}
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	part, err := writer.CreateFormFile("image", "screenshot")
	if err != nil {
		return nil, err
	}
	_, _ = part.Write(image)
	if caption, ok := body["caption"].(string); ok {
		_ = writer.WriteField("caption", caption)
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	raw, _, err := c.cabinetBinaryRequest(ctx, "POST", path, cookie, writer.FormDataContentType(), &buffer)
	if err != nil {
		return nil, err
	}
	var data map[string]any
	if json.Unmarshal(raw, &data) != nil || data == nil {
		return nil, errors.New("Не удалось прочитать ответ поддержки.")
	}
	return data, nil
}

func (c Client) cabinetAttachment(ctx context.Context, cookie, path string) (map[string]any, error) {
	raw, mime, err := c.cabinetBinaryRequest(ctx, "GET", path, cookie, "", nil)
	if err != nil {
		return nil, err
	}
	detected := http.DetectContentType(raw)
	if detected != "image/png" && detected != "image/jpeg" {
		return nil, errors.New("Неподдерживаемый формат вложения.")
	}
	if mime != detected {
		mime = detected
	}
	return map[string]any{"mime": mime, "image_base64": base64.StdEncoding.EncodeToString(raw)}, nil
}
