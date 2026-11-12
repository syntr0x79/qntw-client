package xrayfetch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/xtls/xray-core/core"
	"google.golang.org/protobuf/proto"
)

// ChildArg — первый аргумент, с которым бинарь, зовущий FetchInChild,
// обязан уйти в RunChild вместо своей обычной работы.
const ChildArg = "probe-fetch"

// childGrace — сколько ребёнку дают сверх таймаута пробы на старт и выход.
const childGrace = 5 * time.Second

type childRequest struct {
	Outbound []byte        `json:"outbound"`
	URL      string        `json:"url"`
	Timeout  time.Duration `json:"timeout"`
}

type childReply struct {
	Body     []byte `json:"body,omitempty"`
	Err      string `json:"err,omitempty"`
	Instance bool   `json:"instance,omitempty"`
}

// FetchInChild — FetchThrough в отдельном процессе: bin запускается с
// ChildArg, делает одну пробу и выходит. Подпись та же, что у FetchThrough.
//
// Зачем процесс. xray держит состояние транспортов в глобальных переменных
// пакетов, и экземпляр при Close его не отдаёт: xhttp кладёт в
// splithttp.globalDialerMap запись на каждый новый экземпляр (ключ — указатель
// на его настройки потока) и никогда её не удаляет. Агент узла делает ~1500
// проб в час и копил так ~50 МБ в час, до OOM на узлах с 1 ГБ. Чистить чужие
// глобальные карты снаружи можно только хаками по внутренним именам ядра, а
// процесс забирает с собой всё, что ядро накопило: и эту утечку, и следующую
// такую же, и панику внутри ядра.
func FetchInChild(bin string) func(ctx context.Context, out *core.OutboundHandlerConfig, url string, timeout time.Duration) ([]byte, error) {
	return func(ctx context.Context, out *core.OutboundHandlerConfig, url string, timeout time.Duration) ([]byte, error) {
		if timeout <= 0 {
			timeout = 10 * time.Second
		}
		raw, err := proto.Marshal(out)
		if err != nil {
			return nil, fmt.Errorf("%w: marshal outbound: %w", ErrInstance, err)
		}
		req, err := json.Marshal(childRequest{Outbound: raw, URL: url, Timeout: timeout})
		if err != nil {
			return nil, fmt.Errorf("%w: marshal request: %w", ErrInstance, err)
		}

		ctx, cancel := context.WithTimeout(ctx, timeout+childGrace)
		defer cancel()
		// Ответ идёт отдельным дескриптором (fd 3): в stdout ребёнка пишет
		// журнал xray, и смешивать его с ответом нельзя.
		pr, pw, err := os.Pipe()
		if err != nil {
			return nil, fmt.Errorf("%w: pipe: %w", ErrInstance, err)
		}
		defer pr.Close()
		var logs tailBuffer
		cmd := exec.CommandContext(ctx, bin, ChildArg)
		cmd.Stdin = bytes.NewReader(req)
		cmd.Stdout, cmd.Stderr = &logs, &logs
		cmd.ExtraFiles = []*os.File{pw}
		if err := cmd.Start(); err != nil {
			pw.Close()
			return nil, fmt.Errorf("%w: start child: %w", ErrInstance, err)
		}
		pw.Close()
		reply, readErr := io.ReadAll(io.LimitReader(pr, 1<<20))
		waitErr := cmd.Wait()

		var r childReply
		if readErr != nil || json.Unmarshal(reply, &r) != nil {
			if ctx.Err() != nil {
				return nil, fmt.Errorf("probe child: %w", ctx.Err())
			}
			return nil, fmt.Errorf("%w: child gave no answer (%v): %s", ErrInstance, waitErr, logs.String())
		}
		if r.Err != "" {
			if r.Instance {
				return nil, fmt.Errorf("%w: %s", ErrInstance, strings.TrimPrefix(r.Err, ErrInstance.Error()+": "))
			}
			return nil, errors.New(r.Err)
		}
		return r.Body, nil
	}
}

// SelfExe — путь, по которому процесс запускает себя ребёнком проверки.
// /proc/self/exe указывает на inode, с которого процесс запущен, и переживает
// подмену файла (хост-агентом или самообновлением маяка): ребёнок всегда той
// же сборки, что родитель.
func SelfExe() string {
	if _, err := os.Stat("/proc/self/exe"); err == nil {
		return "/proc/self/exe"
	}
	if p, err := os.Executable(); err == nil {
		return p
	}
	return os.Args[0]
}

// RunChild — сторона ребёнка: запрос из stdin, ответ в fd 3. Ошибку
// возвращает, только когда ответить родителю нечем.
func RunChild(stdin io.Reader) error {
	out := os.NewFile(3, "probe-reply")
	if out == nil {
		return errors.New("probe child: no reply descriptor")
	}
	defer out.Close()
	reply := func(r childReply) error { return json.NewEncoder(out).Encode(r) }

	var req childRequest
	if err := json.NewDecoder(io.LimitReader(stdin, 1<<20)).Decode(&req); err != nil {
		return reply(childReply{Err: "read request: " + err.Error(), Instance: true})
	}
	ob := &core.OutboundHandlerConfig{}
	if err := proto.Unmarshal(req.Outbound, ob); err != nil {
		return reply(childReply{Err: "unmarshal outbound: " + err.Error(), Instance: true})
	}
	body, err := FetchThrough(context.Background(), ob, req.URL, req.Timeout)
	if err != nil {
		return reply(childReply{Err: err.Error(), Instance: errors.Is(err, ErrInstance)})
	}
	return reply(childReply{Body: body})
}

// tailBuffer хранит последние 2 КБ вывода ребёнка — для текста ошибки, если
// ребёнок умер, не ответив.
type tailBuffer struct{ b []byte }

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.b = append(t.b, p...)
	if len(t.b) > 2048 {
		t.b = t.b[len(t.b)-2048:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string { return strings.TrimSpace(string(t.b)) }
