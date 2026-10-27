package engine

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/syntr0x79/qntw-backend/internal/awgprobe"
	"github.com/syntr0x79/qntw-backend/internal/external"
	"github.com/syntr0x79/qntw-backend/internal/mobileprobe/wire"
	"github.com/syntr0x79/qntw-backend/internal/topology"
	"google.golang.org/protobuf/proto"
	"strings"
)

// ImportSubscription reuses the backend's strict link parsers. Unsupported
// protocols are counted; raw URIs and parser errors never enter diagnostics.
func ImportSubscription(body string) (string, error) {
	if len(body) > 1<<20 {
		return "", errImport()
	}
	text := strings.TrimSpace(body)
	if !strings.Contains(text, "://") && !strings.HasPrefix(text, "[Interface]") {
		for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
			if b, e := encoding.DecodeString(strings.Join(strings.Fields(text), "")); e == nil {
				text = string(b)
				break
			}
		}
	}
	lines := strings.Split(text, "\n")
	if strings.HasPrefix(text, "[Interface]") {
		lines = []string{text}
	}
	jobs := []wire.Job{}
	seen := map[string]bool{}
	skipped := 0
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if len(jobs) >= 128 {
			skipped++
			continue
		}
		sum := sha256.Sum256([]byte(line))
		id := "external/" + hex.EncodeToString(sum[:16])
		if seen[id] {
			continue
		}
		seen[id] = true
		j := wire.Job{ID: id, Fingerprint: hex.EncodeToString(sum[:])}
		if strings.HasPrefix(line, "awg://") || strings.HasPrefix(line, "[Interface]") {
			conf, label, e := topology.ParseAWGLink(line)
			if strings.HasPrefix(line, "[Interface]") {
				conf = line
				label = "AmneziaWG"
				e = nil
			}
			if e != nil || awgprobe.ValidateConfig(conf) != nil {
				skipped++
				continue
			}
			j.Engine = "amneziawg"
			j.Protocol = "awg"
			j.Label = label
			j.AWGConf = conf
		} else {
			p, e := external.ParseLink(line)
			if e != nil {
				skipped++
				continue
			}
			out, e := external.Outbound(line)
			if e != nil {
				skipped++
				continue
			}
			j.Outbound, e = proto.Marshal(out)
			if e != nil {
				skipped++
				continue
			}
			j.Engine = "xray"
			j.Protocol = string(p.Protocol)
			j.Label = p.UpstreamName
			if j.Label == "" {
				j.Label = p.Host
			}
			if in, _, _, e := topology.ParseVLESSLink(line); e == nil {
				j.Transport = in.Transport.Kind
				j.Security = in.Transport.Sec
			}
		}
		if j.Label == "" {
			j.Label = "AmneziaWG"
		}
		if chars := []rune(j.Label); len(chars) > 160 {
			j.Label = string(chars[:160])
		}
		jobs = append(jobs, j)
	}
	if len(jobs) == 0 {
		return "", errImport()
	}
	b, e := json.Marshal(map[string]any{"version": 1, "profiles": jobs, "skipped": skipped, "control_url": "https://example.com/"})
	return string(b), e
}

func errImport() error { return errors.New("no supported subscription profiles") }
