package neverskip

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	baseURL   = "https://nskapi.neverskip.com"
	userAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 " +
		"(KHTML, like Gecko) Chrome/148.0.0.0 Safari/537.36"
)

// Client talks to the parent-portal API at nskapi.neverskip.com.
//
// Auth is a single header named "token" whose value is the same value as the
// "token" cookie set on parent.neverskip.com after OTP login. The Angular SPA
// reads that cookie via JS and resends it as a header because cross-domain
// cookies don't auto-attach. We do the same — minus the browser.
type Client struct {
	token string
	http  *http.Client
}

func New(token string) *Client {
	return &Client{
		token: token,
		http: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (c *Client) Lounge(ctx context.Context) (*LoungeResp, error) {
	var out LoungeResp
	if err := c.postJSON(ctx, "/parentweb/connect/fetchloungeinfo", listingBody(), &out); err != nil {
		return nil, err
	}
	if !out.S {
		return nil, ErrUnauthenticated
	}
	return &out, nil
}

func (c *Client) DailyNotice(ctx context.Context) (*DailyNoticeResp, error) {
	var out DailyNoticeResp
	if err := c.postJSON(ctx, "/parentweb/connect/fetchdailynoticeinfo", listingBody(), &out); err != nil {
		return nil, err
	}
	if !out.S {
		return nil, ErrUnauthenticated
	}
	return &out, nil
}

func (c *Client) HasAuth(ctx context.Context) error {
	var probe struct {
		S bool `json:"S"`
	}
	if err := c.postJSON(ctx, "/parentweb/auth/hasauth", map[string]any{}, &probe); err != nil {
		return err
	}
	if !probe.S {
		return ErrUnauthenticated
	}
	return nil
}

// ErrUnauthenticated indicates the token has expired or been rejected.
// The operator needs to re-pair (re-capture the token from a fresh browser
// login). The poll loop pushes a notification on this error.
var ErrUnauthenticated = errUnauthenticated{}

type errUnauthenticated struct{}

func (errUnauthenticated) Error() string { return "neverskip: token rejected (re-auth required)" }

func listingBody() map[string]any {
	return map[string]any{"values": "", "page": "", "filter_date": "0"}
}

func (c *Client) postJSON(ctx context.Context, path string, body any, out any) error {
	buf, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal body: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+path, bytes.NewReader(buf))
	if err != nil {
		return err
	}
	req.Header.Set("token", c.token)
	req.Header.Set("content-type", "application/json")
	req.Header.Set("accept", "application/json, text/plain, */*")
	req.Header.Set("origin", "https://parent.neverskip.com")
	req.Header.Set("referer", "https://parent.neverskip.com/")
	req.Header.Set("user-agent", userAgent)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("POST %s: %w", path, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return ErrUnauthenticated
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("POST %s: status %d", path, resp.StatusCode)
	}
	if err := json.Unmarshal(respBody, out); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}
