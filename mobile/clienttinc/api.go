package clienttinc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	wire "github.com/syntr0x79/qntw-client/internal/clienttincwire"
)

type API struct {
	credentials Credentials
	client      *http.Client
}

func NewAPI(c Credentials, transport http.RoundTripper) *API {
	return &API{credentials: c, client: &http.Client{Transport: transport, Timeout: 12 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

type APIError struct {
	Status     int
	Code       string
	RetryAfter int // seconds; zero means no valid server hint
}

func responseError(res *http.Response) APIError {
	e := APIError{Status: res.StatusCode}
	if seconds, err := strconv.Atoi(res.Header.Get("Retry-After")); err == nil && seconds > 0 {
		if seconds > 60 {
			seconds = 60
		}
		e.RetryAfter = seconds
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, 4097))
	var body struct {
		Code string `json:"code"`
	}
	if err == nil && len(data) <= 4096 && json.Unmarshal(data, &body) == nil {
		// Never retain arbitrary response text, even in a machine-code field.
		switch body.Code {
		case "capacity_exhausted", "access_denied", "subscription_inactive", "invalid_device_token", "device_revoked":
			e.Code = body.Code
		}
	}
	return e
}

func (e APIError) Error() string { return fmt.Sprintf("client tinc API status %d", e.Status) }

func (a *API) call(ctx context.Context, method, path string, input, output any) error {
	var body io.Reader
	if input != nil {
		b, e := json.Marshal(input)
		if e != nil {
			return e
		}
		body = bytes.NewReader(b)
	}
	r, e := http.NewRequestWithContext(ctx, method, a.credentials.Origin+"/api/client-tinc/v1/"+path, body)
	if e != nil {
		return e
	}
	r.Header.Set("Authorization", "Bearer "+a.credentials.Token)
	r.Header.Set("Content-Type", "application/json")
	res, e := a.client.Do(r)
	if e != nil {
		return fmt.Errorf("client tinc API unreachable")
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return responseError(res)
	}
	if output == nil {
		io.Copy(io.Discard, io.LimitReader(res.Body, 4096))
		return nil
	}
	b, e := io.ReadAll(io.LimitReader(res.Body, (1<<20)+1))
	if e != nil || len(b) > 1<<20 {
		return fmt.Errorf("invalid API response")
	}
	if json.Unmarshal(b, output) != nil {
		return fmt.Errorf("invalid API response")
	}
	return nil
}
func (a *API) Identity(ctx context.Context, key string) error {
	return a.call(ctx, "POST", "identity", wire.IdentityRequest{PublicKey: key, Kind: "desktop"}, nil)
}
func (a *API) Country(ctx context.Context, country string) error {
	if !safeCountry.MatchString(country) {
		return fmt.Errorf("country must be two uppercase letters")
	}
	return a.call(ctx, "POST", "country", wire.CountryRequest{Country: country}, nil)
}
func (a *API) Config(ctx context.Context) (wire.ClientConfig, error) {
	var c wire.ClientConfig
	e := a.call(ctx, "GET", "config", nil, &c)
	return c, e
}
