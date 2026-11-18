package hub

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// TestNewTransport checks the two states the hub can be in for a plugin: there is
// one to talk to, or there is not. An empty HUB_URL — a plugin run on its own,
// outside the cluster — gives no transport at all, which is how every caller learns
// to skip the call instead of failing.
func TestNewTransport(t *testing.T) {
	if c := NewTransport(""); c != nil {
		t.Errorf("NewTransport(\"\") = %+v, want nil (hub calls disabled)", c)
	}

	c := NewTransport("http://hub-backend:8080")
	if c == nil {
		t.Fatal("NewTransport with a base URL returned nil")
	}
	if c.BaseURL != "http://hub-backend:8080" {
		t.Errorf("BaseURL = %q, want %q", c.BaseURL, "http://hub-backend:8080")
	}
	if c.HTTP == nil {
		t.Fatal("HTTP is nil, want a client")
	}
	// A plugin's request is on the hub's hot path: a hung hub has to give up on
	// its own, because nothing else is going to cancel the call.
	if want := 10 * time.Second; c.HTTP.Timeout != want {
		t.Errorf("HTTP.Timeout = %s, want %s", c.HTTP.Timeout, want)
	}
}

// recordedRequest is what the fake hub saw, so the test can assert on the call
// rather than only on the answer.
type recordedRequest struct {
	auth   string
	method string
	path   string
	query  url.Values
	ctype  string
	body   string
}

// hubStub answers with the body and status the case asked for and records the
// request it answered. It stands in for the hub on both verbs: GET and POST.
func hubStub(t *testing.T, status int, body string) (*Transport, *recordedRequest) {
	t.Helper()

	rec := &recordedRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.auth = r.Header.Get("Authorization")
		rec.method = r.Method
		rec.path = r.URL.Path
		rec.query = r.URL.Query()
		rec.ctype = r.Header.Get("Content-Type")
		if raw, err := io.ReadAll(r.Body); err == nil {
			rec.body = string(raw)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	return NewTransport(srv.URL), rec
}

// TestGetJSON checks the call the plugin makes back to the hub: the credential
// goes out as a bearer token, the path and query arrive as given, and a 2xx body
// is decoded into the caller's type.
func TestGetJSON(t *testing.T) {
	c, rec := hubStub(t, http.StatusOK, `{"value":"hi"}`)

	var out struct {
		Value string `json:"value"`
	}
	err := c.GetJSON(context.Background(), "ainselp_token", "/api/v1/invocations",
		url.Values{"agent": {"ops agent"}, "limit": {"1"}}, &out)
	if err != nil {
		t.Fatalf("GetJSON: %v", err)
	}

	if out.Value != "hi" {
		t.Errorf("decoded value = %q, want %q", out.Value, "hi")
	}
	if want := "Bearer ainselp_token"; rec.auth != want {
		t.Errorf("Authorization = %q, want %q", rec.auth, want)
	}
	if want := "/api/v1/invocations"; rec.path != want {
		t.Errorf("path = %q, want %q", rec.path, want)
	}
	if rec.method != http.MethodGet {
		t.Errorf("method = %q, want %q", rec.method, http.MethodGet)
	}
	if got := rec.query.Get("agent"); got != "ops agent" {
		t.Errorf("query agent = %q, want %q", got, "ops agent")
	}
	if got := rec.query.Get("limit"); got != "1" {
		t.Errorf("query limit = %q, want %q", got, "1")
	}
}

// TestGetJSONNoCredential checks the case a plugin hits when it is called without
// a user behind it: there is no credential to send, so no Authorization header
// goes out rather than a "Bearer " with nothing after it.
func TestGetJSONNoCredential(t *testing.T) {
	c, rec := hubStub(t, http.StatusOK, `{}`)

	if err := c.GetJSON(context.Background(), "", "/api/v1/invocations", nil, new(struct{})); err != nil {
		t.Fatalf("GetJSON: %v", err)
	}
	if rec.auth != "" {
		t.Errorf("Authorization = %q, want no header", rec.auth)
	}
}

// TestGetJSONWithoutOut checks that a caller who only wants to know whether the
// hub answers can pass no out at all and still get the status checked.
func TestGetJSONWithoutOut(t *testing.T) {
	c, _ := hubStub(t, http.StatusOK, `{"value":"hi"}`)

	if err := c.GetJSON(context.Background(), "ainselp_token", "/api/v1/invocations", nil, nil); err != nil {
		t.Fatalf("GetJSON: %v", err)
	}
}

// TestGetJSONStatusError checks that a hub which answers with a failure is
// reported as one, and as which one: a plugin has to tell "you may not see this"
// from "the hub is down", and the status code is the only thing that says.
func TestGetJSONStatusError(t *testing.T) {
	tests := []struct {
		name   string
		status int
	}{
		{name: "forbidden", status: http.StatusForbidden},
		{name: "not found", status: http.StatusNotFound},
		{name: "unauthorized", status: http.StatusUnauthorized},
		{name: "server error", status: http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := hubStub(t, tt.status, `{"error":"no"}`)

			err := c.GetJSON(context.Background(), "ainselp_token", "/api/v1/invocations", nil, new(struct{}))
			if err == nil {
				t.Fatal("GetJSON returned no error for a non-2xx answer")
			}
			var se *StatusError
			if !errors.As(err, &se) {
				t.Fatalf("error = %v, want a *StatusError", err)
			}
			if se.Code != tt.status {
				t.Errorf("Code = %d, want %d", se.Code, tt.status)
			}
			// The credential is a token. It has no business being in a string
			// that a handler may well print or return.
			if strings.Contains(err.Error(), "ainselp_token") {
				t.Errorf("error %q leaks the credential", err)
			}
		})
	}
}

// TestGetJSONBadBody checks a hub that answers 200 with something that is not
// the JSON it promised: an error, and not a *StatusError, because the status
// said the request was fine.
func TestGetJSONBadBody(t *testing.T) {
	c, _ := hubStub(t, http.StatusOK, `<html>not json</html>`)

	err := c.GetJSON(context.Background(), "ainselp_token", "/api/v1/invocations", nil, new(struct{}))
	if err == nil {
		t.Fatal("GetJSON returned no error for a body that is not JSON")
	}
	var se *StatusError
	if errors.As(err, &se) {
		t.Errorf("error = %v, want something other than a *StatusError", err)
	}
}

// TestGetJSONUnreachable checks a hub that is not there at all — the hub scaled
// to zero, the Service name is wrong — as an error rather than a panic or a
// blocked caller.
func TestGetJSONUnreachable(t *testing.T) {
	c := NewTransport("http://127.0.0.1:1")

	err := c.GetJSON(context.Background(), "ainselp_token", "/api/v1/invocations", nil, new(struct{}))
	if err == nil {
		t.Fatal("GetJSON returned no error against an unreachable hub")
	}
	var se *StatusError
	if errors.As(err, &se) {
		t.Errorf("error = %v, want a transport error", err)
	}
}

// TestGetJSONCancelled checks that the caller's context is honoured: a request
// the client gives up on stops the hub call instead of running on behind it.
func TestGetJSONCancelled(t *testing.T) {
	c, _ := hubStub(t, http.StatusOK, `{}`)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := c.GetJSON(ctx, "ainselp_token", "/api/v1/invocations", nil, new(struct{})); err == nil {
		t.Fatal("GetJSON returned no error against a cancelled context")
	}
}

// TestStatusErrorMessage checks the text a caller sees when it does not dig the
// code out of the error: it names the status the hub answered with, and nothing
// else — in particular not the credential.
func TestStatusErrorMessage(t *testing.T) {
	err := error(&StatusError{Code: http.StatusForbidden})
	if msg := err.Error(); !strings.Contains(msg, "403") {
		t.Errorf("Error() = %q, want it to name status 403", msg)
	}
	if msg := err.Error(); strings.Contains(msg, "ainselp") || strings.Contains(msg, "Bearer") {
		t.Errorf("Error() = %q, want no credential in it", msg)
	}
}

// TestStatusErrorCarriesTheHubBody is the reason this error says more than the status: the
// hub's own message names the field it rejected and the type it wanted, on one line. A 400
// that reads "hub returned 400" sent its reader to both codebases to find out why.
func TestStatusErrorCarriesTheHubBody(t *testing.T) {
	const hubBody = `{"error":"invalid JSON: json: cannot unmarshal object into Go struct field SimpleAgentCreateRequest.skills of type []string"}`
	c, _ := hubStub(t, http.StatusBadRequest, hubBody)

	err := c.PostJSON(context.Background(), "ainselp_token", "/api/v1/agents", map[string]any{}, nil)
	if err == nil {
		t.Fatal("PostJSON returned no error for a 400")
	}
	var se *StatusError
	if !errors.As(err, &se) {
		t.Fatalf("error = %v, want a *StatusError", err)
	}
	if se.Code != http.StatusBadRequest {
		t.Errorf("Code = %d, want %d", se.Code, http.StatusBadRequest)
	}
	if !strings.Contains(err.Error(), "SimpleAgentCreateRequest.skills") {
		t.Errorf("Error() = %q, want the hub's own message in it", err)
	}
	if !strings.Contains(err.Error(), "400") {
		t.Errorf("Error() = %q, want it to name status 400 as well", err)
	}
	if strings.Contains(err.Error(), "ainselp_token") {
		t.Errorf("Error() = %q, leaks the credential", err)
	}
}

// TestStatusErrorTruncatesALargeBody caps what a failed body can put in a log line: a hub
// behind a proxy that answers 502 with an HTML page is still a hub that answered 502.
func TestStatusErrorTruncatesALargeBody(t *testing.T) {
	c, _ := hubStub(t, http.StatusBadGateway, strings.Repeat("<html>", 300))

	err := c.GetJSON(context.Background(), "ainselp_token", "/api/v1/invocations", nil, new(struct{}))
	var se *StatusError
	if !errors.As(err, &se) {
		t.Fatalf("error = %v, want a *StatusError", err)
	}
	if len(se.Body) > hubErrorBodyLimit+len(hubErrorBodyMarker) {
		t.Errorf("Body is %d bytes, want at most %d", len(se.Body), hubErrorBodyLimit+len(hubErrorBodyMarker))
	}
	if !strings.HasSuffix(err.Error(), hubErrorBodyMarker) {
		t.Errorf("Error() = %q, want it to end in the truncation marker", err)
	}
}

// TestStatusErrorWithoutABodyIsTheBareStatus keeps the old text for a hub that refuses
// without saying anything, so the error of a hub that has nothing to add reads as it used to.
func TestStatusErrorWithoutABodyIsTheBareStatus(t *testing.T) {
	c, _ := hubStub(t, http.StatusForbidden, "   ")

	err := c.GetJSON(context.Background(), "ainselp_token", "/api/v1/invocations", nil, new(struct{}))
	var se *StatusError
	if !errors.As(err, &se) {
		t.Fatalf("error = %v, want a *StatusError", err)
	}
	if got, want := err.Error(), "hub returned 403"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

// TestPostJSON checks the write half of the transport: the body arrives as the JSON
// the hub's API expects, the credential rides along as a bearer token, and the
// created object in the answer is decoded.
func TestPostJSON(t *testing.T) {
	c, rec := hubStub(t, http.StatusCreated, `{"id":"c-1","name":"forgejo"}`)

	var out struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	err := c.PostJSON(context.Background(), "ainselp_token", "/api/v1/connectors",
		struct {
			Name string `json:"name"`
		}{Name: "forgejo"}, &out)
	if err != nil {
		t.Fatalf("PostJSON: %v", err)
	}

	if out.ID != "c-1" || out.Name != "forgejo" {
		t.Errorf("decoded = %+v, want {c-1 forgejo}", out)
	}
	if rec.method != http.MethodPost {
		t.Errorf("method = %q, want %q", rec.method, http.MethodPost)
	}
	if want := "/api/v1/connectors"; rec.path != want {
		t.Errorf("path = %q, want %q", rec.path, want)
	}
	if want := "application/json"; rec.ctype != want {
		t.Errorf("Content-Type = %q, want %q", rec.ctype, want)
	}
	if want := `{"name":"forgejo"}`; rec.body != want {
		t.Errorf("body = %s, want %s", rec.body, want)
	}
	if want := "Bearer ainselp_token"; rec.auth != want {
		t.Errorf("Authorization = %q, want %q", rec.auth, want)
	}
}

// TestPostJSONNilBody checks the one post that carries nothing: the plugin's own
// credential mint, which the hub answers on the strength of the pod's address. A nil
// body has to go out as an empty request rather than the string "null", which is what
// marshalling a nil would have written.
func TestPostJSONNilBody(t *testing.T) {
	c, rec := hubStub(t, http.StatusOK, `{"token":"ainselp_bg"}`)

	var out struct {
		Token string `json:"token"`
	}
	if err := c.PostJSON(context.Background(), "", "/api/internal/plugins/forgejo/credentials", nil, &out); err != nil {
		t.Fatalf("PostJSON: %v", err)
	}

	if out.Token != "ainselp_bg" {
		t.Errorf("token = %q, want %q", out.Token, "ainselp_bg")
	}
	if rec.body != "" {
		t.Errorf("body = %q, want no body", rec.body)
	}
	if rec.ctype != "" {
		t.Errorf("Content-Type = %q, want none for a body that has no content", rec.ctype)
	}
	if rec.auth != "" {
		t.Errorf("Authorization = %q, want no header", rec.auth)
	}
}

// TestPostJSONWithoutOut checks a caller that only wants to know whether the write
// took: no out still checks the status, and still does not read the body as JSON.
func TestPostJSONWithoutOut(t *testing.T) {
	c, _ := hubStub(t, http.StatusCreated, `{"id":"t-1"}`)

	if err := c.PostJSON(context.Background(), "ainselp_token", "/api/v1/triggers", map[string]string{"name": "t"}, nil); err != nil {
		t.Fatalf("PostJSON: %v", err)
	}
}

// TestPostJSONStatusError checks a write the hub refused is reported as which refusal,
// so a wizard can tell "you may not create this" from "the hub is down" — and without
// the credential in the text.
func TestPostJSONStatusError(t *testing.T) {
	c, _ := hubStub(t, http.StatusForbidden, `{"error":"forbidden"}`)

	err := c.PostJSON(context.Background(), "ainselp_token", "/api/v1/agents", map[string]string{"name": "a"}, new(struct{}))
	if err == nil {
		t.Fatal("PostJSON returned no error for a non-2xx answer")
	}
	var se *StatusError
	if !errors.As(err, &se) {
		t.Fatalf("error = %v, want a *StatusError", err)
	}
	if se.Code != http.StatusForbidden {
		t.Errorf("Code = %d, want %d", se.Code, http.StatusForbidden)
	}
	if strings.Contains(err.Error(), "ainselp_token") {
		t.Errorf("error %q leaks the credential", err)
	}
}
