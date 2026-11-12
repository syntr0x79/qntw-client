package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/syntr0x79/qntw-client/mobile/linux/desktopmeta"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/syntr0x79/qntw-client/mobile/linux/account"
	"github.com/syntr0x79/qntw-client/mobile/linux/probe"
	"github.com/syntr0x79/qntw-client/mobile/linux/subscription"
)

type request struct {
	AcceptanceDiagnostics bool            `json:"acceptance_diagnostics,omitempty"`
	Link                  string          `json:"link"`
	Code                  string          `json:"code"`
	Cookie                string          `json:"cookie"`
	DeviceID              string          `json:"device_id"`
	Job                   json.RawMessage `json:"job"`
	ControlURL            string          `json:"control_url"`
	Plan                  string          `json:"plan"`
	Method                string          `json:"method"`
	Devices               *int            `json:"devices"`
	BindDevice            bool            `json:"bind_device"`
	Path                  string          `json:"path"`
	Body                  json.RawMessage `json:"body"`
	Country               string          `json:"country"`
}

type result struct {
	Results  []probe.Result         `json:"results,omitempty"`
	Session  *account.Session       `json:"session,omitempty"`
	Link     string                 `json:"link,omitempty"`
	Profiles []subscription.Profile `json:"profiles,omitempty"`
	Error    string                 `json:"error,omitempty"`
	Data     map[string]any         `json:"data,omitempty"`
	Status   int                    `json:"status,omitempty"`
	Code     string                 `json:"code,omitempty"`
}

var responseOut io.Writer = os.Stdout

func main() {
	action := flag.String("action", "profiles", "operation")
	jsonMode := flag.Bool("json", false, "read a subscription link from stdin and write JSON")
	progressMode := flag.Bool("progress", false, "emit probe progress as JSON lines")
	flag.Parse()
	if *jsonMode && configureJSONOutput() != nil {
		os.Exit(1)
	}
	if !*jsonMode {
		fmt.Fprintln(os.Stderr, "Quantway для "+desktopmeta.Label()+": загрузка маршрутов.")
		fmt.Fprint(os.Stderr, "Вставьте HTTPS-ссылку на подписку и нажмите Enter: ")
	}
	reader := bufio.NewReader(os.Stdin)
	maxInput := 1 << 20
	if *jsonMode && *action == "cabinet" {
		// Base64 for a support screenshot (10 MiB), plus its caption. Other
		// cabinet bodies have their own 4 KiB allowlist validation.
		maxInput = 15 << 20
	}
	line, err := readRequestLine(reader, maxInput)
	if len(line) > maxInput || (err != nil && strings.TrimSpace(line) == "") {
		fail(*jsonMode, "Не удалось прочитать ссылку.")
	}
	if *jsonMode && *action != "profiles" {
		var req request
		if json.Unmarshal([]byte(line), &req) != nil {
			fail(true, "Некорректный запрос.")
		}
		switch *action {
		case "tinc-catalog":
			tincCatalog(req)
			return
		case "tinc-connect":
			tincConnect(req, reader)
			return
		case "restore-session":
			session, err := (account.Client{}).RestoreSession(context.Background(), req.Cookie)
			if err != nil {
				_ = json.NewEncoder(responseOut).Encode(cabinetFailure(err))
				os.Exit(1)
			}
			_ = json.NewEncoder(responseOut).Encode(result{Session: &session})
			return
		case "cabinet":
			data, err := (account.Client{}).Cabinet(context.Background(), account.CabinetRequest{
				Cookie: req.Cookie, Method: req.Method, Path: req.Path, Body: req.Body,
			})
			if err != nil {
				_ = json.NewEncoder(responseOut).Encode(cabinetFailure(err))
				os.Exit(1)
			}
			_ = json.NewEncoder(responseOut).Encode(struct {
				Data map[string]any `json:"data"`
			}{Data: data})
			return
		case "connect":
			connect(req, reader)
			return
		case "login":
			session, e := (account.Client{}).Login(context.Background(), req.Code)
			if e != nil {
				_ = json.NewEncoder(responseOut).Encode(cabinetFailure(e))
				return
			}
			identity, accountData, e := (account.Client{}).Snapshot(context.Background(), session.Cookie)
			if e != nil {
				fail(true, "Не удалось загрузить кабинет. Попробуйте войти снова.")
			}
			session.Identity, session.Account = identity, accountData
			if identity["kind"] != "owner" {
				name, e := account.DeviceName()
				if e == nil {
					session.CurrentDevice, session.DeviceLink, e = (account.Client{}).BindDevice(context.Background(), session.Cookie, name)
				}
				if e == nil && session.CurrentDevice != "" {
					found := false
					for _, d := range session.Devices {
						if d.ID == session.CurrentDevice {
							found = true
							break
						}
					}
					if !found {
						session.Devices = append(session.Devices, account.Device{ID: session.CurrentDevice, Name: "Этот компьютер", Platform: desktopmeta.Platform()})
					}
				}
				if e != nil {
					var apiError *account.HTTPError
					if session.CurrentDevice == "" && errors.As(e, &apiError) && apiError.Status == http.StatusConflict {
						session.NeedsReplacement = true
						session.DeviceError = "Нет свободных мест. Выберите устройство для замены."
					} else {
						session.DeviceError = "Не удалось подключить этот компьютер. Проверьте подписку и попробуйте снова."
					}
				}
			}
			_ = json.NewEncoder(responseOut).Encode(result{Session: &session})
			return
		case "replace-device":
			name, e := account.DeviceName()
			if e != nil {
				fail(true, "Не удалось определить этот компьютер.")
			}
			id, link, e := (account.Client{}).ReplaceDevice(context.Background(), req.Cookie, req.DeviceID, name)
			if e != nil {
				fail(true, "Не удалось завершить замену. Проверьте список устройств и попробуйте снова.")
			}
			_ = json.NewEncoder(responseOut).Encode(result{Data: map[string]any{"device_id": id, "link": link}})
			return
		case "account-refresh":
			identity, accountData, e := (account.Client{}).Snapshot(context.Background(), req.Cookie)
			if e != nil {
				fail(true, "Не удалось обновить кабинет.")
			}
			data := map[string]any{"identity": identity, "account": accountData}
			if req.BindDevice && identity["kind"] != "owner" {
				binding, err := (account.Client{}).RefreshBinding(context.Background(), req.Cookie)
				if err != nil {
					fail(true, "Не удалось обновить устройства. Попробуйте ещё раз.")
				}
				data["binding"] = binding
			}
			_ = json.NewEncoder(responseOut).Encode(result{Data: data})
			return
		case "plans":
			data, e := (account.Client{}).Plans(context.Background(), req.Cookie)
			if e != nil {
				fail(true, "Не удалось загрузить тарифы.")
			}
			_ = json.NewEncoder(responseOut).Encode(result{Data: data})
			return
		case "invoice-status":
			data, e := (account.Client{}).InvoiceStatus(context.Background(), req.Cookie, req.DeviceID)
			if e != nil {
				fail(true, "Не удалось проверить счёт.")
			}
			_ = json.NewEncoder(responseOut).Encode(result{Data: data})
			return
		case "invoice":
			data, e := (account.Client{}).Invoice(context.Background(), req.Cookie, req.Plan, req.Method, req.Devices)
			if e != nil {
				fail(true, "Не удалось создать счёт. Попробуйте ещё раз.")
			}
			_ = json.NewEncoder(responseOut).Encode(result{Data: data})
			return
		case "probe":
			var progress func(int, int)
			if *progressMode {
				progress = func(done, total int) {
					_ = json.NewEncoder(responseOut).Encode(map[string]any{"type": "progress", "done": done, "total": total})
				}
			}
			results, e := (probe.Client{}).RunWithProgress(context.Background(), req.Link, progress)
			if e != nil {
				fail(true, "Не удалось проверить маршруты. Проверьте сеть и попробуйте позже.")
			}
			_ = json.NewEncoder(responseOut).Encode(result{Results: results})
			return
		case "device":
			link, e := (account.Client{}).DeviceLink(context.Background(), req.Cookie, req.DeviceID)
			if e != nil {
				fail(true, "Не удалось получить подписку устройства.")
			}
			_ = json.NewEncoder(responseOut).Encode(result{Link: link})
			return
		default:
			fail(true, "Неизвестная операция.")
		}
	}
	profiles, err := (subscription.Client{}).Load(context.Background(), line)
	if err != nil {
		fail(*jsonMode, "Не удалось загрузить подписку. Проверьте ссылку и подключение к сети.")
	}
	if *jsonMode {
		_ = json.NewEncoder(responseOut).Encode(result{Profiles: profiles})
		return
	}
	fmt.Printf("Маршрутов: %d\n", len(profiles))
	for _, p := range profiles {
		fmt.Printf("%s · %s\n", p.Label, p.Protocol)
	}
}

// Read from the original buffered reader so a pipelined stop command remains
// available to the connection lifecycle after its initial JSON request.
func readRequestLine(reader *bufio.Reader, limit int) (string, error) {
	var line []byte
	for {
		part, err := reader.ReadSlice('\n')
		if len(line)+len(part) > limit {
			return "", errors.New("request too large")
		}
		line = append(line, part...)
		if err != bufio.ErrBufferFull {
			return string(line), err
		}
	}
}

func cabinetFailure(err error) result {
	out := result{Error: err.Error(), Code: "request_failed"}
	var apiError *account.HTTPError
	if errors.As(err, &apiError) {
		out.Status = apiError.Status
		if apiError.Code != "" {
			out.Code = apiError.Code
		} else {
			switch {
			case apiError.Status == http.StatusUnauthorized:
				out.Code = "unauthorized"
			case apiError.Status == http.StatusForbidden:
				out.Code = "forbidden"
			case apiError.Status >= 500:
				out.Code = "service_unavailable"
			default:
				out.Code = "http_error"
			}
		}
	}
	return out
}

func fail(jsonMode bool, message string) {
	if jsonMode {
		_ = json.NewEncoder(responseOut).Encode(result{Error: message})
	} else {
		fmt.Fprintln(os.Stderr, message)
	}
	os.Exit(1)
}
