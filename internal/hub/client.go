package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// hubTimeout is the ceiling on one hub call. A plugin's request usually comes from
// the console through the hub, so a hub that has stopped answering has to be given up
// on by the caller — nothing else is going to notice it hanging.
const hubTimeout = 10 * time.Second

// Transport is how the plugin reaches the hub's API: the same API the console uses,
// with whatever credential the caller's request arrived with.
//
// It is the plumbing rather than the contract — Client is the contract, and
// HTTPClient is the Client built on this transport. A nil Transport is a valid one —
// it means there is no hub to call, which is how a plugin started outside the
// cluster looks. Callers check for nil and skip the call; calling through a nil
// Transport is a bug, not a slow path.
type Transport struct {
	// BaseURL is the hub's address, from HUB_URL: http://hub:8080 inside
	// the cluster, where the plugin and the hub share a namespace. A cluster
	// still running the pre-rename core needs the old name, http://hub-backend:8080
	// (see the README's hub.url note).
	BaseURL string

	// HTTP is the underlying client. It carries a timeout, so a call cannot outlive
	// the patience of whoever is waiting on the plugin.
	HTTP *http.Client
}

// NewTransport builds the transport for a hub's address. An empty address means there
// is no hub — the plugin is on its own — and no transport comes back, which is how
// every caller learns to skip the call instead of failing it.
func NewTransport(baseURL string) *Transport {
	if baseURL == "" {
		return nil
	}
	return &Transport{
		BaseURL: baseURL,
		HTTP:    &http.Client{Timeout: hubTimeout},
	}
}

// StatusError is the hub answering with a status that is not a success. The code is
// the part worth having: "you may not see this" and "the hub is down" are different
// answers, and a plugin has to be able to tell them apart.
//
// Body is what the hub said, capped at hubErrorBodyLimit bytes: the hub's own message
// usually names the field it rejected, which is the difference between a log line an
// operator can act on and one that sends them into the source of two codebases.
type StatusError struct {
	Code int
	Body string
}

// hubErrorBodyLimit is how much of a failed response's body a StatusError keeps. A hub
// behind a proxy that answers 502 with an HTML page must not be able to balloon a log
// line, so the read is capped and a truncated body is marked as such.
const hubErrorBodyLimit = 512

// hubErrorBodyMarker marks a body that was cut at the cap.
const hubErrorBodyMarker = "…"

func (e *StatusError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("hub returned %d", e.Code)
	}
	return fmt.Sprintf("hub returned %d: %s", e.Code, e.Body)
}

// errorBody reads a failed response for the error string, capped and trimmed. The body
// has already been checked for being a body at all; a read that fails yields "", which
// leaves the error as the bare status it used to be.
func errorBody(r io.Reader) string {
	raw, err := io.ReadAll(io.LimitReader(r, hubErrorBodyLimit+1))
	if err != nil {
		return ""
	}
	truncated := len(raw) > hubErrorBodyLimit
	if truncated {
		raw = raw[:hubErrorBodyLimit]
	}
	// Cutting at a byte can cut a rune in half; the cut is dropped rather than left as
	// a replacement character in a log line.
	body := strings.TrimSpace(strings.ToValidUTF8(string(raw), ""))
	if truncated {
		body += hubErrorBodyMarker
	}
	return body
}

// GetJSON gets one path from the hub's API and decodes the JSON it answers with into
// out. The credential is the one that came with the request, sent as a bearer token,
// so the hub sees exactly what the caller behind it would have — no more. An empty
// credential sends no Authorization header, which is a call the hub will refuse.
//
// Anything the hub answers with that is not a success comes back as a *StatusError;
// a hub that cannot be reached, or that answers with something that is not the JSON it
// promised, comes back as a plain error. out may be nil to check reachability only.
func (c *Transport) GetJSON(ctx context.Context, credential, path string, query url.Values, out any) error {
	return c.do(ctx, http.MethodGet, credential, path, query, nil, out)
}

// PostJSON posts a body to one path of the hub's API and decodes the JSON the hub
// answers with into out. The body is sent as JSON; a nil body posts nothing, which is
// what the endpoints that take no body (the plugin's own credential mint) want.
//
// The same credential rules and the same error shapes apply as on GetJSON. out may be
// nil when the answer is only the status — a create whose result the caller does not
// need, for instance.
func (c *Transport) PostJSON(ctx context.Context, credential, path string, body, out any) error {
	return c.do(ctx, http.MethodPost, credential, path, nil, body, out)
}

// do is both verbs: build the request, send it with the credential, and turn the
// answer into either a decoded body or an error.
func (c *Transport) do(ctx context.Context, method, credential, path string, query url.Values, body, out any) error {
	base, err := url.Parse(c.BaseURL)
	if err != nil {
		return fmt.Errorf("hub base URL %q: %w", c.BaseURL, err)
	}
	// Joining rather than concatenating keeps a base URL that carries a path
	// prefix intact, and a path or query that needs escaping arrives as asked.
	target := base.JoinPath(path)
	target.RawQuery = query.Encode()

	// A posted body is marshalled here rather than by the caller: the hub speaks
	// JSON, and a caller that had to marshal itself would only be repeating this.
	var payload io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("hub request body for %s: %w", path, err)
		}
		payload = bytes.NewReader(raw)
	}

	req, err := http.NewRequestWithContext(ctx, method, target.String(), payload)
	if err != nil {
		return fmt.Errorf("hub request for %s: %w", path, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if credential != "" {
		req.Header.Set("Authorization", "Bearer "+credential)
	}

	res, err := c.HTTP.Do(req)
	if err != nil {
		// The text of a transport error names the URL, which is the hub's address
		// and the path — never the credential, which travels in a header.
		return fmt.Errorf("hub request for %s: %w", path, err)
	}
	defer res.Body.Close()

	// 2xx is the success band, not 200 alone: an API that answers 204 or 206 is
	// still answering.
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		// The body is read before it is dropped: it is the only place the hub says
		// why it refused, and a status alone has never been enough to find out.
		return &StatusError{Code: res.StatusCode, Body: errorBody(res.Body)}
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(res.Body).Decode(out); err != nil {
		return fmt.Errorf("hub response for %s: %w", path, err)
	}
	return nil
}
