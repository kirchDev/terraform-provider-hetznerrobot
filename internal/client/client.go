// Package client is a minimal client for the Hetzner Robot webservice.
//
// Shape per the official documentation (robot.hetzner.com/doc/webservice):
//   - Base URL: https://robot-ws.your-server.de ; HTTP Basic auth with a
//     webservice user (Robot → Settings → Web service and app settings).
//   - Requests are application/x-www-form-urlencoded; arrays travel as
//     key[]=a&key[]=b and the firewall rules as rules[input][0][name]=….
//   - Responses are JSON with the entity wrapped in its name: a single read is
//     {"server": {…}}, a collection [{"server": {…}}, …]. The vSwitch endpoints
//     are the exception and answer with bare objects.
//   - Errors are {"error": {"status", "code", "message"}}; INVALID_INPUT adds
//     "missing" and "invalid", RATE_LIMIT_EXCEEDED (a 403) "max_request" and
//     "interval".
//   - A collection without entries is a 404 (NOT_FOUND, "No … found"), not an
//     empty array — GetList turns that into an empty slice.
//   - Three failed logins block the calling IP for ten minutes, so a 401 is
//     never retried.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultEndpoint is the base URL of the Robot webservice.
const DefaultEndpoint = "https://robot-ws.your-server.de"

// maxAttempts bounds the retry loop for transient failures.
const maxAttempts = 5

// storageBoxWriteInterval is the documented limit for every Storage Box write:
// "1 request per 5 seconds".
const storageBoxWriteInterval = 5 * time.Second

// Client talks to the Robot webservice.
type Client struct {
	httpClient *http.Client
	endpoint   string
	username   string
	password   string
	userAgent  string
	// backoff is the base delay between retries; tests shrink it.
	backoff time.Duration
	// sbInterval spaces Storage Box writes; tests shrink it.
	sbInterval time.Duration

	mu          sync.Mutex
	lastSBWrite time.Time
}

// New constructs a Client. An empty endpoint falls back to DefaultEndpoint.
func New(endpoint, username, password, userAgent string) *Client {
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	return &Client{
		httpClient: &http.Client{Timeout: 120 * time.Second},
		endpoint:   strings.TrimRight(endpoint, "/"),
		username:   username,
		password:   password,
		userAgent:  userAgent,
		backoff:    2 * time.Second,
		sbInterval: storageBoxWriteInterval,
	}
}

// SetPacing overrides the retry backoff and the Storage Box write spacing.
// Only the tests need it.
func (c *Client) SetPacing(backoff, storageBoxInterval time.Duration) {
	c.backoff = backoff
	c.sbInterval = storageBoxInterval
}

// Get fetches path with an optional query and decodes the JSON body into out.
func (c *Client) Get(ctx context.Context, path string, query url.Values, out any) error {
	raw, err := c.Do(ctx, http.MethodGet, path, query, nil)
	if err != nil {
		return err
	}
	return decode(raw, out, http.MethodGet, path)
}

// Send issues a write with form as the body and decodes the response into out
// (when non-nil and the response is not empty).
func (c *Client) Send(ctx context.Context, method, path string, form url.Values, out any) error {
	raw, err := c.Do(ctx, method, path, nil, form)
	if err != nil {
		return err
	}
	return decode(raw, out, method, path)
}

func decode(raw json.RawMessage, out any, method, path string) error {
	if out == nil || len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decoding %s %s response: %w", method, path, err)
	}
	return nil
}

// GetObject reads a single entity wrapped as {"<key>": {…}}.
func GetObject[T any](ctx context.Context, c *Client, path, key string, query url.Values) (T, error) {
	var zero T
	raw, err := c.Do(ctx, http.MethodGet, path, query, nil)
	if err != nil {
		return zero, err
	}
	return Unwrap[T](raw, key)
}

// SendObject issues a write and unwraps the {"<key>": {…}} answer.
func SendObject[T any](ctx context.Context, c *Client, method, path, key string, form url.Values) (T, error) {
	var zero T
	raw, err := c.Do(ctx, method, path, nil, form)
	if err != nil {
		return zero, err
	}
	return Unwrap[T](raw, key)
}

// GetList reads a collection wrapped as [{"<key>": {…}}, …]. The API answers an
// empty collection with a 404, which comes back as an empty slice. A top-level
// collection (/server, /storagebox) uses its own code for that ("No server
// found" is SERVER_NOT_FOUND); below an entity only NOT_FOUND means empty —
// STORAGEBOX_NOT_FOUND on /storagebox/1/snapshot is a missing parent and stays
// an error.
func GetList[T any](ctx context.Context, c *Client, path, key string, query url.Values) ([]T, error) {
	raw, err := c.Do(ctx, http.MethodGet, path, query, nil)
	if NotFound(err) && (strings.Count(path, "/") == 1 || HasCode(err, "NOT_FOUND")) {
		return []T{}, nil
	}
	if err != nil {
		return nil, err
	}
	return UnwrapList[T](raw, key)
}

// Unwrap decodes {"<key>": {…}} into T. An object without the key is decoded
// as T directly, so a bare answer works too.
func Unwrap[T any](raw json.RawMessage, key string) (T, error) {
	var out T
	var env map[string]json.RawMessage
	if err := json.Unmarshal(raw, &env); err != nil {
		return out, fmt.Errorf("decoding %q response: %w", key, err)
	}
	inner, ok := env[key]
	if !ok {
		inner = raw
	}
	if err := json.Unmarshal(inner, &out); err != nil {
		return out, fmt.Errorf("decoding %q response: %w", key, err)
	}
	return out, nil
}

// UnwrapList decodes [{"<key>": {…}}, …] into []T. A null body is an empty list.
func UnwrapList[T any](raw json.RawMessage, key string) ([]T, error) {
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("decoding %q list: %w", key, err)
	}
	out := make([]T, 0, len(items))
	for _, item := range items {
		v, err := Unwrap[T](item, key)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// APIError is a non-2xx answer of the webservice.
type APIError struct {
	StatusCode int
	Method     string
	Path       string
	// Code is the API's error code, e.g. SERVER_NOT_FOUND or INVALID_INPUT.
	Code    string
	Message string
	Missing []string
	Invalid []string
	// MaxRequest and Interval describe the limit of a RATE_LIMIT_EXCEEDED.
	MaxRequest int
	Interval   int
}

func (e *APIError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "hetzner robot API %s %s: status %d", e.Method, e.Path, e.StatusCode)
	if e.Code != "" {
		b.WriteString(" " + e.Code)
	}
	if e.Message != "" {
		b.WriteString(": " + e.Message)
	}
	if len(e.Missing) > 0 {
		b.WriteString(" (missing: " + strings.Join(e.Missing, ", ") + ")")
	}
	if len(e.Invalid) > 0 {
		b.WriteString(" (invalid: " + strings.Join(e.Invalid, ", ") + ")")
	}
	switch {
	case e.Code == "RATE_LIMIT_EXCEEDED" && e.MaxRequest > 0:
		fmt.Fprintf(&b, " — the limit is %d requests per %d seconds", e.MaxRequest, e.Interval)
	case e.StatusCode == http.StatusUnauthorized:
		b.WriteString(" — check the webservice user and password; Robot blocks the calling IP for ten minutes after three failed logins")
	}
	return b.String()
}

// NotFound reports whether err is a 404 from the API.
func NotFound(err error) bool {
	var e *APIError
	return errors.As(err, &e) && e.StatusCode == http.StatusNotFound
}

// HasCode reports whether err is an API error carrying one of codes.
func HasCode(err error, codes ...string) bool {
	var e *APIError
	if !errors.As(err, &e) {
		return false
	}
	for _, c := range codes {
		if e.Code == c {
			return true
		}
	}
	return false
}

// Do performs an authenticated request and returns the raw 2xx body. query is
// appended to the URL, form (when non-nil) becomes the url-encoded body.
//
// Retries: a transport error or 5xx on a GET, and a 503 on other writes — the
// documentation reserves 503 for maintenance, when nothing was processed. An
// order is the exception: a 503 from a proxy after the order landed would
// otherwise buy twice. A write that failed otherwise may have landed and is
// never repeated. 401 and
// the rate limit (403 RATE_LIMIT_EXCEEDED, counted per hour) are not retried
// either.
func (c *Client) Do(ctx context.Context, method, path string, query, form url.Values) (json.RawMessage, error) {
	if method != http.MethodGet && strings.HasPrefix(path, "/storagebox") {
		if err := c.paceStorageBox(ctx); err != nil {
			return nil, err
		}
	}
	for attempt := 1; ; attempt++ {
		raw, retry, err := c.once(ctx, method, path, query, form)
		if err == nil || !retry || attempt == maxAttempts {
			return raw, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(c.backoff * time.Duration(1<<(attempt-1))):
		}
	}
}

// paceStorageBox waits until the documented spacing between two Storage Box
// writes has passed.
func (c *Client) paceStorageBox(ctx context.Context) error {
	c.mu.Lock()
	wait := time.Until(c.lastSBWrite.Add(c.sbInterval))
	if wait < 0 {
		wait = 0
	}
	c.lastSBWrite = time.Now().Add(wait)
	c.mu.Unlock()
	if wait == 0 {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(wait):
		return nil
	}
}

func (c *Client) once(ctx context.Context, method, path string, query, form url.Values) (json.RawMessage, bool, error) {
	u := c.endpoint + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, false, err
	}
	req.SetBasicAuth(c.username, c.password)
	req.Header.Set("Accept", "application/json")
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if c.userAgent != "" {
		req.Header.Set("User-Agent", c.userAgent)
	}

	res, err := c.httpClient.Do(req)
	if err != nil {
		// A transport error on a read is safe to retry; on a write it may have
		// landed, and repeating an order would duplicate it.
		return nil, method == http.MethodGet, err
	}
	defer func() { _ = res.Body.Close() }()

	data, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, false, err
	}
	if res.StatusCode >= 200 && res.StatusCode < 300 {
		return data, false, nil
	}

	apiErr := parseError(data, res.StatusCode, method, path)
	retry := (res.StatusCode == http.StatusServiceUnavailable && !strings.HasPrefix(path, "/order/")) ||
		(res.StatusCode >= 500 && method == http.MethodGet)
	return nil, retry, apiErr
}

func parseError(body []byte, status int, method, path string) *APIError {
	e := &APIError{StatusCode: status, Method: method, Path: path}
	var env struct {
		Error struct {
			Code       string          `json:"code"`
			Message    string          `json:"message"`
			Missing    json.RawMessage `json:"missing"`
			Invalid    json.RawMessage `json:"invalid"`
			MaxRequest FlexInt         `json:"max_request"`
			Interval   FlexInt         `json:"interval"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &env) == nil && (env.Error.Code != "" || env.Error.Message != "") {
		e.Code = env.Error.Code
		e.Message = env.Error.Message
		e.Missing = fieldList(env.Error.Missing)
		e.Invalid = fieldList(env.Error.Invalid)
		e.MaxRequest = int(env.Error.MaxRequest.Value)
		e.Interval = int(env.Error.Interval.Value)
		return e
	}
	e.Message = strings.TrimSpace(string(body))
	if e.Message == "" {
		e.Message = http.StatusText(status)
	}
	return e
}

// fieldList reads the "missing"/"invalid" members, which are an array of names
// or null.
func fieldList(raw json.RawMessage) []string {
	var out []string
	if json.Unmarshal(raw, &out) == nil {
		return out
	}
	return nil
}

// Bool renders a boolean the way the webservice expects it in a form.
func Bool(b bool) string { return strconv.FormatBool(b) }
