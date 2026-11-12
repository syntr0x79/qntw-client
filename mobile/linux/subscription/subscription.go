// Package subscription loads Quantway and third-party subscriptions without
// persisting their credentials. It shares the protocol parser with Android.
package subscription

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/syntr0x79/qntw-client/mobile/linux/desktopmeta"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/syntr0x79/qntw-client/mobile/engine"
)

var tokenPath = regexp.MustCompile(`^/sub/([A-Za-z0-9_-]{16,256})/?$`)

type Profile struct {
	JobID     string `json:"job_id"`
	Label     string `json:"label"`
	Engine    string `json:"engine"`
	Protocol  string `json:"protocol"`
	Transport string `json:"transport"`
}
type response struct {
	Version  int       `json:"version"`
	Profiles []Profile `json:"profiles"`
}

type Client struct {
	HTTP     *http.Client
	testHost string
}

// Load never logs subscription URLs, tokens, raw configuration or HTTP bodies.
func (c Client) Load(ctx context.Context, text string) ([]Profile, error) {
	u, err := url.Parse(strings.TrimSpace(text))
	if err != nil || !strings.EqualFold(u.Scheme, "https") || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
		return nil, errors.New("a complete HTTPS subscription link is required")
	}
	client := http.Client{Timeout: 20 * time.Second}
	if c.HTTP != nil {
		client = *c.HTTP
	}
	// Credentials in headers or query strings must not follow a redirect.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	var token []string
	if IsQuantwayHost(u.Hostname()) || (c.testHost != "" && u.Host == c.testHost) {
		token = tokenPath.FindStringSubmatch(u.EscapedPath())
	}
	requestURL := u.String()
	if token != nil {
		requestURL = (&url.URL{Scheme: "https", Host: u.Host, Path: "/api/mobile/v1/profiles"}).String()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, errors.New("invalid subscription link")
	}
	req.Header.Set("User-Agent", desktopmeta.UserAgent())
	if token != nil {
		req.Header.Set("Authorization", "Bearer "+token[1])
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, errors.New("could not reach subscription service")
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("subscription service returned HTTP %d", res.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return nil, errors.New("invalid or oversized subscription response")
	}
	if token == nil {
		parsed, e := engine.ImportSubscription(string(data))
		if e != nil {
			return nil, errors.New("subscription has no supported profiles")
		}
		data = []byte(parsed)
	}
	var decoded response
	if err := json.Unmarshal(data, &decoded); err != nil || decoded.Version != 1 {
		return nil, errors.New("invalid subscription response")
	}
	return decoded.Profiles, nil
}

// IsQuantwayHost distinguishes our device API from third-party /sub links.
func IsQuantwayHost(host string) bool {
	return strings.EqualFold(host, "qntw.link") || strings.EqualFold(host, "quantway.link")
}
