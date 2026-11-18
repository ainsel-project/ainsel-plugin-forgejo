package hub

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

// encodeContext renders a context header the way the hub does it: the JSON body
// in base64url without padding.
func encodeContext(t *testing.T, body string) string {
	t.Helper()

	if !json.Valid([]byte(body)) {
		t.Fatalf("test context %q is not valid JSON", body)
	}
	return base64.RawURLEncoding.EncodeToString([]byte(body))
}

// TestCallerFromRequest walks what the hub forwards on an authenticated api or
// mcp request: the context header is there when the caller is a user and absent
// when they are not, and a plugin must survive both — and survive a header it
// cannot read, rather than answering 500 over a header it does not control.
func TestCallerFromRequest(t *testing.T) {
	tests := []struct {
		name   string
		header string // X-Ainsel-Context, "" leaves the header unset
		// setEmptyHeader distinguishes "header present but empty" from "no
		// header" in the case whose header is "".
		setEmptyHeader bool
		wantOK         bool
		want           Caller
	}{
		{
			name:   "no header means no caller",
			wantOK: false,
		},
		{
			name:           "empty header means no caller",
			header:         "",
			setEmptyHeader: true,
			wantOK:         false,
		},
		{
			name:   "full context",
			header: encodeContext(t, `{"pluginId":"forgejo","kind":"user","sub":"00u1","username":"alice","isAdmin":true,"permissions":["agents:read"],"groups":[{"id":"g1","name":"Platform","role":"member"}],"invocationId":"inv-1"}`),
			wantOK: true,
			want: Caller{
				PluginID:     "forgejo",
				Kind:         "user",
				Sub:          "00u1",
				Username:     "alice",
				IsAdmin:      true,
				Permissions:  []string{"agents:read"},
				Groups:       []Group{{ID: "g1", Name: "Platform", Role: "member"}},
				InvocationID: "inv-1",
			},
		},
		{
			name:   "context with only what the hub always sets",
			header: encodeContext(t, `{"pluginId":"forgejo","kind":"user","sub":"00u1","username":"alice","isAdmin":false,"permissions":[],"groups":[],"invocationId":""}`),
			wantOK: true,
			want: Caller{
				PluginID:    "forgejo",
				Kind:        "user",
				Sub:         "00u1",
				Username:    "alice",
				Permissions: []string{},
				Groups:      []Group{},
			},
		},
		{
			// A header that is not base64url at all — a truncated value, a proxy
			// that re-encoded it — is no caller, not a failure.
			name:   "bad base64",
			header: "not base64 at all!",
			wantOK: false,
		},
		{
			// Padded base64 is not what the hub sends (RawURLEncoding), and a
			// value that decodes to nothing readable is no caller either.
			name:   "base64 with padding",
			header: encodeContext(t, `{"username":"alice"}`) + "==",
			wantOK: false,
		},
		{
			name:   "base64 of something that is not JSON",
			header: base64.RawURLEncoding.EncodeToString([]byte("hello")),
			wantOK: false,
		},
		{
			name:   "json that is not an object",
			header: encodeContext(t, `[1,2,3]`),
			wantOK: false,
		},
		{
			// A field the plugin has never heard of is not a reason to lose the
			// caller: the platform adds fields, the plugin does not have to
			// learn about each one to keep working.
			name:   "unknown field is ignored",
			header: encodeContext(t, `{"username":"alice","futureField":{"a":1}}`),
			wantOK: true,
			want:   Caller{Username: "alice"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/api/work", nil)
			// Setting the header to "" is not the same as not setting it, and the
			// two cases above cover both: a proxy that forwards an empty context
			// header must read as no caller too.
			if tt.header != "" || tt.setEmptyHeader {
				req.Header.Set("X-Ainsel-Context", tt.header)
			}

			got, ok := CallerFromRequest(req)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v (header %q)", ok, tt.wantOK, tt.header)
			}
			if !ok {
				// The zero value is what a caller that is not there looks like,
				// so a half-decoded caller cannot slip through as "present".
				if !callersEqual(got, Caller{}) {
					t.Errorf("caller = %+v, want the zero value when absent", got)
				}
				return
			}
			if !callersEqual(got, tt.want) {
				t.Errorf("caller = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// callersEqual compares two callers by their encoded form, because a nil slice
// and an empty one read the same to every consumer and should not fail a test
// that only cares about the values.
func callersEqual(got, want Caller) bool {
	g, _ := json.Marshal(got)
	w, _ := json.Marshal(want)
	return string(g) == string(w)
}

// TestCredentialFromRequest checks the scoped credential the hub leaves for the
// plugin to call back with: it is the header when the hub minted one and an
// empty string when it did not.
func TestCredentialFromRequest(t *testing.T) {
	tests := []struct {
		name   string
		header string
		set    bool
		want   string
	}{
		{name: "credential present", header: "ainselp_abc", set: true, want: "ainselp_abc"},
		{name: "credential empty", header: "", set: true, want: ""},
		{name: "credential absent", set: false, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/api/work", nil)
			if tt.set {
				req.Header.Set("X-Ainsel-Credential", tt.header)
			}
			if got := CredentialFromRequest(req); got != tt.want {
				t.Errorf("credential = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestCredentialFromContext round-trips a credential through the context the way a request
// does: a handler stages the credential the hub forwarded and the hub client reads it back off
// the context that reaches the call, so a credential staged on a request has to survive into
// the call made from it — and a context nobody staged has to read as having none.
func TestCredentialFromContext(t *testing.T) {
	if got, ok := CredentialFromContext(t.Context()); ok || got != "" {
		t.Errorf("credential = (%q, %v) on a bare context, want (\"\", false)", got, ok)
	}

	ctx := WithCredential(t.Context(), "ainselp_abc")
	if got, ok := CredentialFromContext(ctx); !ok || got != "ainselp_abc" {
		t.Errorf("credential = (%q, %v), want (\"ainselp_abc\", true)", got, ok)
	}

	// The credential rides down to derived contexts the way every context value does:
	// a handler that puts a timeout on the request's context still calls the hub as
	// the caller who made the request.
	derived := context.WithValue(ctx, struct{ k string }{"x"}, 1)
	if got, ok := CredentialFromContext(derived); !ok || got != "ainselp_abc" {
		t.Errorf("credential on a derived context = (%q, %v), want the staged one", got, ok)
	}

	// Staging a second time replaces the first, so a credential cannot survive from one
	// request into the next.
	if got, _ := CredentialFromContext(WithCredential(ctx, "ainselp_def")); got != "ainselp_def" {
		t.Errorf("credential = %q, want the one staged last", got)
	}

	// An empty staging is not a credential: the hub minted nothing, and a reader should
	// see the absence rather than a token whose value is "".
	if got, ok := CredentialFromContext(WithCredential(t.Context(), "")); ok || got != "" {
		t.Errorf("empty staging = (%q, %v), want (\"\", false)", got, ok)
	}
}

// TestBackgroundContext checks the marker that says a context's hub calls have no
// caller behind them: a bare context is not marked, a marked one is, and the marking
// rides down to derived contexts the way every context value does.
func TestBackgroundContext(t *testing.T) {
	if backgroundFromContext(t.Context()) {
		t.Error("a bare context reads as background-marked")
	}

	ctx := WithBackground(t.Context())
	if !backgroundFromContext(ctx) {
		t.Error("WithBackground did not mark the context")
	}
	// A handler that derives a timeout context from the marked one is still doing
	// background work.
	if !backgroundFromContext(context.WithValue(ctx, struct{ k string }{"x"}, 1)) {
		t.Error("a derived context lost the background marker")
	}
}

// TestInvocationIDFromRequest checks the invocation id the hub leaves on an MCP call it made
// for an agent: the header is the hub's promise about its own value, and anything that does
// not keep that promise is no id at all rather than an id to hand back or key something on.
func TestInvocationIDFromRequest(t *testing.T) {
	tests := []struct {
		name   string
		header string // X-Ainsel-Invocation-Id, "" leaves the header unset
		// setEmptyHeader distinguishes "header present but empty" from "no
		// header" in the case whose header is "".
		setEmptyHeader bool
		want           string
	}{
		{name: "no header means no invocation", want: ""},
		{name: "empty header means no invocation", header: "", setEmptyHeader: true, want: ""},
		{
			// The shape an agent runtime sends: a prefix and a short random tail.
			name:   "invocation id present",
			header: "inv-1234abcd",
			want:   "inv-1234abcd",
		},
		{
			// Every character the shape allows is kept, so a runtime that spells its
			// ids differently is not silently ignored.
			name:   "id of every allowed character",
			header: "aZ9._:-x",
			want:   "aZ9._:-x",
		},
		{
			// One character below the cap is an id; the shape counts 128.
			name:   "id at the length cap",
			header: strings.Repeat("i", 128),
			want:   strings.Repeat("i", 128),
		},
		{
			name:   "id past the length cap",
			header: strings.Repeat("i", 129),
			want:   "",
		},
		{
			// A path separator, a space and a control character are none of them
			// characters the hub sends, so none of them arrive as an id.
			name:   "id with a slash",
			header: "inv/../1",
			want:   "",
		},
		{name: "id with a space", header: "inv 1", want: ""},
		{name: "id with a newline", header: "inv-1\n", want: ""},
		{name: "id with a non-ASCII character", header: "inv-\u00e4l", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/mcp", nil)
			if tt.header != "" || tt.setEmptyHeader {
				req.Header.Set("X-Ainsel-Invocation-Id", tt.header)
			}
			if got := InvocationIDFromRequest(req); got != tt.want {
				t.Errorf("invocation id = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestInvocationIDContext round-trips an invocation id through the request context, the way
// the caller does: a tool handler is handed a context, not the request that carried the
// header. An empty id reads as no id, whoever staged it.
func TestInvocationIDContext(t *testing.T) {
	if got := InvocationIDFromContext(t.Context()); got != "" {
		t.Errorf("invocation id = %q on a bare context, want %q", got, "")
	}

	ctx := WithInvocationID(t.Context(), "inv-1234abcd")
	if got := InvocationIDFromContext(ctx); got != "inv-1234abcd" {
		t.Errorf("invocation id = %q, want %q", got, "inv-1234abcd")
	}

	// The id rides down to derived contexts the way every context value does: a handler
	// that puts a timeout on the request's context still names the invocation.
	derived := context.WithValue(ctx, struct{ k string }{"x"}, 1)
	if got := InvocationIDFromContext(derived); got != "inv-1234abcd" {
		t.Errorf("invocation id on a derived context = %q, want the staged one", got)
	}

	// Storing a second time replaces the first rather than stacking, so a handler cannot
	// read an invocation from a previous request.
	if got := InvocationIDFromContext(WithInvocationID(ctx, "inv-9999ffff")); got != "inv-9999ffff" {
		t.Errorf("invocation id = %q, want the one stored last", got)
	}

	// An empty id is the same answer as no id, whoever stored it.
	if got := InvocationIDFromContext(WithInvocationID(t.Context(), "")); got != "" {
		t.Errorf("invocation id = %q after storing the empty string, want %q", got, "")
	}

	// A value that is not a string cannot be read as an id: the key is unexported, but a
	// context carrying something else under a same-typed key from another package still
	// has to read as "no id".
	type otherKey struct{}
	if got := InvocationIDFromContext(context.WithValue(t.Context(), otherKey{}, "inv-1")); got != "" {
		t.Errorf("invocation id = %q from an unrelated key, want %q", got, "")
	}
}

// TestCallerContext round-trips a caller through the request context, which is
// how a handler reached over MCP gets at it: the MCP transport builds the tool
// context from the HTTP request, so the caller has to ride along on it.
func TestCallerContext(t *testing.T) {
	if _, ok := CallerFromContext(t.Context()); ok {
		t.Error("CallerFromContext on a bare context says there is a caller")
	}

	want := Caller{Username: "alice", Groups: []Group{{ID: "g1", Name: "Platform"}}}
	ctx := WithCaller(t.Context(), want)

	got, ok := CallerFromContext(ctx)
	if !ok {
		t.Fatal("CallerFromContext lost the caller it was given")
	}
	if !callersEqual(got, want) {
		t.Errorf("caller = %+v, want %+v", got, want)
	}

	// Storing a second time replaces the first rather than stacking, so a
	// handler cannot read a caller from a previous request.
	ctx = WithCaller(ctx, Caller{Username: "bob"})
	if got, _ := CallerFromContext(ctx); got.Username != "bob" {
		t.Errorf("caller = %q, want the one stored last", got.Username)
	}
}
