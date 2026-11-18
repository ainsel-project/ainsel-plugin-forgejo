// Package hub is the plugin's side of the AInsel hub: the caller the hub forwards
// with every request it proxies, the invocation an agent's call belongs to, and the
// client the plugin calls the hub back with.
//
// All of it is optional at runtime. A plugin started on its own has no hub behind it
// and no hub headers in front of it: there is no caller to read, no invocation to name
// a call by, and NewClient hands back no client to call with. Nothing in here treats
// that as an error — the plugin answers the same questions either way, with less to
// say.
package hub

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"regexp"
)

// The headers the hub leaves on a request it proxies to a plugin. The hub strips
// X-Ainsel-* from whatever the caller sent, so a header that arrives here came from
// the hub and not from whoever is on the other side of it.
const (
	// credentialHeader carries a short-lived token scoped to this plugin and this
	// caller, for calling the hub back with. It is a credential: it never belongs
	// in a log line or an error message.
	credentialHeader = "X-Ainsel-Credential"

	// contextHeader describes the caller whose request this is, as JSON in
	// base64url without padding. The hub only mints it for api and mcp requests
	// made by an authenticated user, so its absence is normal.
	contextHeader = "X-Ainsel-Context"

	// invocationHeader names the agent invocation an MCP call belongs to. The hub
	// sets it on the calls the agent runtime makes and sends none otherwise, so a
	// request without one is normal — for a user's call and for a plugin on its own.
	invocationHeader = "X-Ainsel-Invocation-Id"
)

// invocationIDPattern is the shape the hub promises for an invocation id: the
// characters its own ids are made of, and a length cap, so a header that is really a
// blob cannot be carried into a log line or a filename downstream.
var invocationIDPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

// Group is one of the caller's groups, the way the hub resolved it. A group id the
// hub does not know gets an empty Name.
type Group struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Role string `json:"role"`
}

// Caller is who the hub says is asking. It is the body the hub puts in
// X-Ainsel-Context, decoded: the field names and their meaning are the hub's, and a
// plugin reads them rather than assuming its own.
type Caller struct {
	// PluginID is the id this plugin is registered under, as the hub saw it when
	// it proxied the request.
	PluginID string `json:"pluginId"`

	// Kind is what sort of caller this is: "user", "agent" or "plugin". What the
	// hub proxies for a caller in front of a browser is always "user" today.
	Kind string `json:"kind"`

	// Sub is the caller's stable subject id — the identity to key own-work on,
	// unlike Username, which a caller can change.
	Sub string `json:"sub"`

	// Username is the caller's display name: what to greet them by, not what to
	// authorise them with.
	Username string `json:"username"`

	// IsAdmin says the caller is an admin, and sees everything.
	IsAdmin bool `json:"isAdmin"`

	// Permissions are the ones the plugin was approved for, which is the ceiling
	// on what this caller may get through it.
	Permissions []string `json:"permissions"`

	// Groups are the caller's, with the role they hold in each.
	Groups []Group `json:"groups"`

	// InvocationID identifies the invocation this request belongs to, and is
	// empty for a request the hub did not start as part of an invocation.
	InvocationID string `json:"invocationId"`
}

// CallerFromRequest reads the caller the hub forwarded, and reports whether there
// was one to read.
//
// A header that is missing, or that cannot be decoded, is simply no caller: the hub
// owns that header, and a plugin that answered 500 over a value it does not control
// would be blaming the caller for something upstream of them.
func CallerFromRequest(r *http.Request) (Caller, bool) {
	raw := r.Header.Get(contextHeader)
	if raw == "" {
		return Caller{}, false
	}

	// RawURLEncoding is what the hub encodes with: base64url, no padding. A value
	// that is not that is not a context, whatever it is.
	body, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return Caller{}, false
	}

	// An unknown field is not a reason to lose the caller: the platform adds to
	// this body and the plugin keeps working without learning about each one.
	var caller Caller
	if err := json.Unmarshal(body, &caller); err != nil {
		return Caller{}, false
	}
	return caller, true
}

// CredentialFromRequest returns the scoped credential the hub minted for this
// request, or "" when it minted none — a request that reached the plugin without a
// user behind it has nothing to call the hub back with.
//
// The value is a token. Stage it on the request's context with WithCredential and
// call the hub from there; do not log it.
func CredentialFromRequest(r *http.Request) string {
	return r.Header.Get(credentialHeader)
}

// InvocationIDFromRequest returns the id of the agent invocation this request
// belongs to, or "" when there is none to read: the hub sends the header only on the
// MCP calls it makes for an agent, and a value that is not the shape the hub sends
// counts as none as well — it is not an id to hand back or to key anything on.
func InvocationIDFromRequest(r *http.Request) string {
	id := r.Header.Get(invocationHeader)
	if !invocationIDPattern.MatchString(id) {
		return ""
	}
	return id
}

// callerKey is the key a caller is staged under on a context. It is unexported so
// no other package can overwrite the value or read it by guessing a string.
type callerKey struct{}

// WithCaller stages a caller on a context, for a handler that is handed a context
// rather than the request — an MCP tool call, whose context the transport builds
// from the request that carried it.
func WithCaller(ctx context.Context, caller Caller) context.Context {
	return context.WithValue(ctx, callerKey{}, caller)
}

// CallerFromContext returns the caller staged on a context by WithCaller, and
// reports whether one was there.
func CallerFromContext(ctx context.Context) (Caller, bool) {
	caller, ok := ctx.Value(callerKey{}).(Caller)
	return caller, ok
}

// invocationKey is the key an invocation id is staged under on a context, as
// unexported as callerKey is for the same reason.
type invocationKey struct{}

// WithInvocationID stages an invocation id on a context, for the tool call that is
// handed a context rather than the request the header arrived on — the same hop
// WithCaller is for a caller.
func WithInvocationID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, invocationKey{}, id)
}

// InvocationIDFromContext returns the invocation id staged on a context by
// WithInvocationID, or "" when none was staged. An empty id is no id, whoever put it
// there.
func InvocationIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(invocationKey{}).(string)
	return id
}

// credentialKey is the key a credential is staged under on a context, unexported for
// the same reason callerKey is.
type credentialKey struct{}

// WithCredential stages the credential that came with a request on a context, so a
// hub call made further down from that request's handler can be made as the caller who
// made it — the hub then answers with exactly what they may see.
//
// A context with no credential staged makes no background call either: only a context
// marked with WithBackground gets the plugin's own credential. A context with neither
// fails its hub calls.
func WithCredential(ctx context.Context, cred string) context.Context {
	return context.WithValue(ctx, credentialKey{}, cred)
}

// CredentialFromContext returns the credential staged on a context by WithCredential, and
// reports whether there was one to read. A context that was never staged, or that was staged
// with the empty string the hub uses for "I minted none", reads as no credential.
//
// This is the reader a caller outside the package uses to prove a hub call carries the caller
// it was handed — a test of a route, say, checking the credential it put on the context is the
// credential the hub was called with. Nothing here logs the value.
func CredentialFromContext(ctx context.Context) (string, bool) {
	cred, ok := ctx.Value(credentialKey{}).(string)
	if !ok || cred == "" {
		return "", false
	}
	return cred, true
}

// credentialFromContext returns the credential staged by WithCredential, or "" when none was
// staged. It is the client's own read of CredentialFromContext, so where a credential can come
// from stays decided in one place.
func credentialFromContext(ctx context.Context) string {
	cred, _ := CredentialFromContext(ctx)
	return cred
}

// WithoutCredential returns a context with the same deadline and cancellation as the caller's,
// but with any user credential removed. Use this before WithBackground to keep the caller's
// deadline while switching to the plugin's own credential.
//
// It is exported because the wizard's write path needs it: the hub derives a create's
// managedBy stamp from the authenticated caller, so a wizard write that wants the stamp
// has to arrive as the plugin, not as the console caller whose request started the work.
func WithoutCredential(ctx context.Context) context.Context {
	return &credentialFilteringContext{ctx}
}

// credentialFilteringContext wraps a context but intercepts lookups for the credential key,
// returning nil instead of any user credential value. This allows the caller's deadline and
// cancellation to be preserved while the credential is cleared.
type credentialFilteringContext struct {
	context.Context
}

// Value returns the value associated with the key, except for the credential key, which
// returns nil to prevent inheriting a user's credential from the wrapped context.
func (c *credentialFilteringContext) Value(key interface{}) interface{} {
	// Filter out the credential key so we don't inherit a user credential
	if _, ok := key.(credentialKey); ok {
		return nil
	}
	return c.Context.Value(key)
}

// backgroundKey marks a context as doing the plugin's own work. It is unexported for
// the same reason credentialKey is.
type backgroundKey struct{}

// WithBackground marks a context as work with no user behind it — a webhook arriving,
// a poll, a sync — whose hub calls the plugin makes as itself, under the identity the
// chart's plugin.groupAccess authorises.
//
// Background is a decision an entry point makes, not a fallback a missing credential
// falls into: a call on a context that was neither given a caller's credential nor
// marked here is a bug in the calling code, and the client refuses it rather than
// silently acting as the plugin where the caller may not have been able to.
func WithBackground(ctx context.Context) context.Context {
	return context.WithValue(ctx, backgroundKey{}, true)
}

// backgroundFromContext reports whether WithBackground marked this context.
func backgroundFromContext(ctx context.Context) bool {
	marked, _ := ctx.Value(backgroundKey{}).(bool)
	return marked
}
