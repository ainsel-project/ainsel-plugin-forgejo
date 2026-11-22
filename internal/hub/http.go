package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/forge"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/setup"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/work"
)

var _ Client = (*HTTPClient)(nil)

// pluginID is this plugin's id: the one the manifest declares and the hub registered the
// Plugin CR under. It is spelled here rather than read from the manifest, because the
// call that needs it is the very first one a plugin makes and has to work before
// anything has parsed a manifest.
const pluginID = "forgejo"

// credentialsPath is where a plugin mints its own background credential: a POST with no
// body and no auth, which the hub answers only when the request comes from one of the
// plugin's verified pod IPs. It is not an /api/v1 route, so the route gate — and with it
// every permission — is out of the way; the address is the authentication.
const credentialsPath = "/api/internal/plugins/" + pluginID + "/credentials"

// managerStamp is the managedBy value the hub accepts from this plugin on create:
// plugincreds.ManagerStamp(pluginID) on the hub side, "plugin-" + the plugin id. A
// plugin-authenticated create carrying it makes the hub refuse every console write of
// the resource with 409 while leaving this plugin the one caller that may still write
// it — which is what stops a half-edit in the console from being fought by the next
// wizard apply. There is exactly one correct string: the hub 400s any other value from
// this caller and silently ignores the field from a non-plugin one, so the wizard's
// writes must also carry the plugin's own credential (see WithoutCredential) for the
// stamp to land and for later edits, rotates and removes to pass the named-manager rule.
const managerStamp = "plugin-" + pluginID

// listPageSize is AInsel's maximum page size. Reading a collection at its default of 50
// would be a round trip per fifty resources for a hub that lets the whole page be asked
// for at once.
const listPageSize = 200

// backgroundRenewBefore is how early a cached background credential is replaced. A call
// that starts a moment before the token expires would be refused for being a moment
// late, so the mint happens while there is still margin on the credential in hand.
const backgroundRenewBefore = 30 * time.Second

// maxConcurrentEventReads is how many event payloads Runs keeps in flight at once. One page
// is up to listPageSize invocations and the distinct events behind them are nearly that many:
// read one after another, each is a round trip to the hub, which is what made a page of the
// work list take seconds. Eight is a fan-out a hub on the same cluster answers without the
// reads queueing behind one another in its own connection pool.
const maxConcurrentEventReads = 8

// costRange is the window the token metrics are read over. AInsel accepts at most 7d on
// that endpoint (template issue #11), so a run whose event is older than a week has no
// cost to read and is reported as unknown — never as free.
const costRange = "7d"

// HTTPClient is the Client that calls a real hub over HTTP, on the transport the
// template's NewClient built. It is the caller's hands: every method sends the
// credential staged on the context (ruling R17), and the plugin's own background
// credential only on a context deliberately marked WithBackground — the work that has
// no user behind it: a webhook arriving, a poll, a sync. A call made on a context with
// neither is refused, so one that forgot its caller never acts as the plugin.
type HTTPClient struct {
	transport *Transport

	// now is the clock the credential cache reads. A field so a test can move time
	// instead of waiting for it.
	now func() time.Time

	// checkTimeout caps one CheckLLM call. NewHTTPClient sets it to llmCheckTimeout;
	// it is a field so a test can shorten the cap instead of waiting out ten seconds.
	checkTimeout time.Duration

	// mu guards the cached background credential, which any goroutine in the plugin
	// may need at any time. It is held across the mint so two goroutines that start
	// out with no token do not both go minting one.
	mu          sync.Mutex
	bgToken     string
	bgExpiresAt time.Time
}

// NewHTTPClient builds the hub client for a hub reached over t. No transport means no
// hub — NewTransport hands one back only for a non-empty HUB_URL — and then no client
// comes back either, which is how every caller here learns to skip the call rather than
// fail it.
func NewHTTPClient(t *Transport) *HTTPClient {
	if t == nil {
		return nil
	}
	return &HTTPClient{transport: t, now: time.Now, checkTimeout: llmCheckTimeout}
}

// get reads one path as the caller of ctx, post writes one and put replaces one. All
// three are the transport's verbs with the credential resolved, which is the only
// thing a call made through this client adds to a call made through the transport.
func (c *HTTPClient) get(ctx context.Context, path string, query url.Values, out any) error {
	cred, err := c.credential(ctx)
	if err != nil {
		return err
	}
	return c.transport.GetJSON(ctx, cred, path, query, out)
}

func (c *HTTPClient) post(ctx context.Context, path string, body, out any) error {
	cred, err := c.credential(ctx)
	if err != nil {
		return err
	}
	return c.transport.PostJSON(ctx, cred, path, body, out)
}

func (c *HTTPClient) put(ctx context.Context, path string, body, out any) error {
	cred, err := c.credential(ctx)
	if err != nil {
		return err
	}
	return c.transport.do(ctx, http.MethodPut, cred, path, nil, body, out)
}

// credential is the credential to call the hub with: the caller's own when their request
// is behind this call, so the hub answers with exactly what they may see; the plugin's
// own only on a context marked WithBackground, which is how an entry point says this
// work has no user behind it. A context with neither is a call that should not be made:
// it fails here, without asking the hub anything, rather than silently acting as the
// plugin (ruling R21).
func (c *HTTPClient) credential(ctx context.Context) (string, error) {
	if cred := credentialFromContext(ctx); cred != "" {
		return cred, nil
	}
	if backgroundFromContext(ctx) {
		return c.mintBackground(ctx)
	}
	return "", errors.New("hub: no credential for this call")
}

// CredentialError is the failure of the plugin's own background-credential mint: the
// hub refusing or not answering the credentials endpoint the plugin asks for its token
// at. It is typed because its status is not about a resource. A mint that answers 404
// (the endpoint not up yet, right after a rollout) unwraps to a *StatusError{404} that
// is otherwise indistinguishable from a delete answering that the resource is gone —
// and "the resource is gone" is a completed deletion while "the mint failed" is not.
// Anything that classifies a failure as "already gone" must therefore exclude a chain
// containing this error.
type CredentialError struct {
	Err error
}

func (e *CredentialError) Error() string { return "hub background credential: " + e.Err.Error() }

func (e *CredentialError) Unwrap() error { return e.Err }

// mintBackground returns the plugin's own credential, minted from a hub that authenticates
// the request by the pod's address: five minutes valid, cached, and re-minted before it
// expires rather than after. It acts only for the groups the chart lists under
// plugin.groupAccess, which is what the hub hands a plugin-kind caller.
//
// The token is never logged and never appears in an error: the text here names the
// failure, not the credential. Every failure is a *CredentialError, so a caller can tell
// "the hub would not give me a token" from "the hub refused the call I made with one".
func (c *HTTPClient) mintBackground(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.bgToken != "" && c.now().Before(c.bgExpiresAt.Add(-backgroundRenewBefore)) {
		return c.bgToken, nil
	}

	var res struct {
		Token     string `json:"token"`
		ExpiresAt string `json:"expiresAt"`
	}
	// No credential on the call that asks for one: this is the one request the plugin
	// makes as itself.
	if err := c.transport.PostJSON(ctx, "", credentialsPath, nil, &res); err != nil {
		return "", &CredentialError{Err: err}
	}
	if res.Token == "" {
		return "", &CredentialError{Err: errors.New("no token in the answer")}
	}
	expiresAt, err := time.Parse(time.RFC3339, res.ExpiresAt)
	if err != nil {
		return "", &CredentialError{Err: fmt.Errorf("expiry: %w", err)}
	}

	c.bgToken, c.bgExpiresAt = res.Token, expiresAt
	return c.bgToken, nil
}

// -- listing ------------------------------------------------------------------------

// listPage is the envelope every paginated hub list answers with. (Invocations name
// theirs differently — see invocationsSince.)
type listPage[T any] struct {
	Items      []T `json:"items"`
	Total      int `json:"total"`
	Page       int `json:"page"`
	PageSize   int `json:"pageSize"`
	TotalPages int `json:"totalPages"`
}

// listAll reads a hub collection to the end. One page of it is a part of the truth, and
// a wizard that lists only the first 200 triggers would tell an admin their install has
// fewer than it does, so the pages are followed until the answer says there are no more
// — an empty page stops too, in case a hub is ever behind on its own page count.
func listAll[T any](ctx context.Context, c *HTTPClient, path string, query url.Values) ([]T, error) {
	var out []T
	for page := 1; ; page++ {
		q := cloneQuery(query)
		q.Set("page", strconv.Itoa(page))
		q.Set("pageSize", strconv.Itoa(listPageSize))

		var resp listPage[T]
		if err := c.get(ctx, path, q, &resp); err != nil {
			return nil, err
		}
		out = append(out, resp.Items...)
		if len(resp.Items) == 0 || page >= resp.TotalPages {
			return out, nil
		}
	}
}

// cloneQuery copies a caller's filters so setting the page on them cannot write into a
// url.Values the caller still holds.
func cloneQuery(q url.Values) url.Values {
	out := url.Values{}
	for k, vs := range q {
		out[k] = append([]string(nil), vs...)
	}
	return out
}

// ListPersonas, ListAgents and ListConnectors all read a collection whose items name
// themselves with an id and a display name, which is what a Ref is.

func (c *HTTPClient) ListPersonas(ctx context.Context) ([]Ref, error) {
	return listAll[Ref](ctx, c, "/api/v1/personas", nil)
}

func (c *HTTPClient) CreatePersona(ctx context.Context, groupID, name, description, text string) (Ref, error) {
	var ref Ref
	body := struct {
		Name        string `json:"name"`
		GroupID     string `json:"groupId"`
		Description string `json:"description"`
		Text        string `json:"text"`
		ManagedBy   string `json:"managedBy"`
	}{Name: name, GroupID: groupID, Description: description, Text: text, ManagedBy: managerStamp}

	if err := c.post(ctx, "/api/v1/personas", body, &ref); err != nil {
		return Ref{}, err
	}
	return ref, nil
}

func (c *HTTPClient) ListAgents(ctx context.Context) ([]Ref, error) {
	// A trigger's agentRef is the agent's name, so Name is the half a caller wires;
	// ID is the half a caller updates.
	return listAll[Ref](ctx, c, "/api/v1/agents", nil)
}

func (c *HTTPClient) CreateAgent(ctx context.Context, spec AgentSpec) (Ref, error) {
	req := agentCreateRequest{
		Name:        spec.Name,
		GroupID:     spec.GroupID,
		Description: spec.Description,
		ImageRef:    agentImageRef{Name: spec.ImageRef},
		Env:         spec.Env,
		ManagedBy:   managerStamp,
	}
	// An agent image is named by its registry id, and an agent's persona by its id.
	if spec.PersonaID != "" {
		req.Persona = &agentPersonaRef{ID: spec.PersonaID}
	}
	if spec.LLM != nil {
		req.LLM = &agentLLM{Model: spec.LLM.Model, Provider: spec.LLM.Provider}
	}
	// An absent mcp means "inherit the image's", so an empty list is left out rather
	// than sent as an explicit none — which is what a wizard that means the image's
	// defaults wants.
	if spec.MCPServers != nil {
		req.MCP = &agentMCP{Servers: spec.MCPServers}
	}
	// Skills are the agent's own and inherit nothing: an absent or empty list is the
	// hub's "no skills", so the spec's slice goes on the wire as it stands.
	req.Skills = spec.Skills
	// The tool grant is the agent's own too, but an empty one is the opposite of what
	// skills mean: at the runtime an unset grant is every tool the image offers, so
	// the field carries omitempty and an empty spec stays off the wire rather than
	// widening the agent it was meant to restrict.
	req.EnabledTools = spec.EnabledTools
	// A window is written only when the spec names one: an absent key is the hub's
	// "any hour", which is what an install that never set hours wants.
	if spec.WorkingHours != nil {
		req.WorkingHours = spec.WorkingHours
	}
	// Each provider object is filled only when the spec names its value, so an unset
	// one stays absent rather than sent as an empty key.
	if spec.OllamaCloudKey != "" {
		req.OllamaCloud = &agentOllamaCloudInfo{APIKey: spec.OllamaCloudKey}
	}
	if spec.OpenCodeKey != "" {
		req.OpenCode = &agentOpenCodeInfo{APIKey: spec.OpenCodeKey}
	}
	if spec.AlibabaKey != "" {
		req.AlibabaCloud = &agentAlibabaCloudInfo{APIKey: spec.AlibabaKey}
	}
	if spec.OpenAIKey != "" || spec.OpenAIAuthMethod != "" {
		req.OpenAI = &agentOpenAIInfo{APIKey: spec.OpenAIKey, AuthMethod: spec.OpenAIAuthMethod}
	}
	if spec.AnthropicKey != "" {
		req.Anthropic = &agentAnthropicInfo{APIKey: spec.AnthropicKey}
	}
	if spec.GoogleKey != "" {
		req.Google = &agentGoogleInfo{APIKey: spec.GoogleKey}
	}
	if spec.CustomURL != "" || spec.CustomKey != "" {
		req.CustomProvider = &agentCustomProviderInfo{URL: spec.CustomURL, APIKey: spec.CustomKey}
	}

	var ref Ref
	if err := c.post(ctx, "/api/v1/agents", req, &ref); err != nil {
		return Ref{}, err
	}
	return ref, nil
}

// imageItem is one agent image as the hub lists it: an id to create on and a display
// name an admin recognises.
type imageItem struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
}

func (c *HTTPClient) ListAgentImages(ctx context.Context) ([]Ref, error) {
	items, err := listAll[imageItem](ctx, c, "/api/v1/agent-images", nil)
	if err != nil {
		return nil, err
	}
	out := make([]Ref, 0, len(items))
	for _, it := range items {
		// The display name is what a wizard shows and what an admin picks; the
		// registry id is what creating an agent on it takes.
		out = append(out, Ref{ID: it.ID, Name: it.DisplayName})
	}
	return out, nil
}

func (c *HTTPClient) ListConnectors(ctx context.Context) ([]Ref, error) {
	return listAll[Ref](ctx, c, "/api/v1/connectors", nil)
}

// connectorSignatureHeader is what marks a connector as this plugin's receiver: a
// connector has no type field, so a "Forgejo connector" is the generic webhook receiver
// that expects Forgejo's signature header (ruling R13).
const connectorSignatureHeader = "X-Forgejo-Signature"

func (c *HTTPClient) CreateConnector(ctx context.Context, groupID, name string) (Connector, error) {
	var resp struct {
		Ref

		WebhookEndpoint    string `json:"webhookEndpoint"`
		WebhookSecretValue string `json:"webhookSecretValue"`
	}
	body := struct {
		Name            string `json:"name"`
		GroupID         string `json:"groupId"`
		SignatureHeader string `json:"signatureHeader"`
		ManagedBy       string `json:"managedBy"`
	}{Name: name, GroupID: groupID, SignatureHeader: connectorSignatureHeader, ManagedBy: managerStamp}

	if err := c.post(ctx, "/api/v1/connectors", body, &resp); err != nil {
		return Connector{}, err
	}
	// The secret is the hub's plaintext, returned by this call and by a rotate and by
	// nothing else. Hand it to the admin; keep it nowhere.
	return Connector{
		Ref:                resp.Ref,
		WebhookEndpoint:    resp.WebhookEndpoint,
		WebhookSecretValue: resp.WebhookSecretValue,
	}, nil
}

// ListTriggers reads one connector's triggers — the read setup makes before it creates, so
// it can tell what an install already has from what it is missing. The list decodes
// straight into setup.Trigger: its JSON tags are already the API's field names (ruling R4),
// which is what makes setup.Missing's comparison read the same shape the hub answered.
func (c *HTTPClient) ListTriggers(ctx context.Context, connectorID string) ([]setup.Trigger, error) {
	query := url.Values{}
	if connectorID != "" {
		query.Set("connector", connectorID)
	}
	return listAll[setup.Trigger](ctx, c, "/api/v1/triggers", query)
}

func (c *HTTPClient) CreateTrigger(ctx context.Context, groupID string, t setup.Trigger) (Ref, error) {
	// setup.Trigger already carries the API's field names; the group is all that has
	// to be added to it (ruling R12).
	var ref Ref
	body := triggerCreateRequest{Trigger: t, GroupID: groupID}
	if err := c.post(ctx, "/api/v1/triggers", body, &ref); err != nil {
		return Ref{}, err
	}
	return ref, nil
}

// ListCronTriggers reads every cron trigger the caller may see — a cron trigger
// has no connector to scope a list by. The list decodes straight into
// setup.CronTrigger: its JSON tags are the hub cron-triggers API's field names.
// The install matches its own by name; the generated id a delete needs comes from
// this read.
func (c *HTTPClient) ListCronTriggers(ctx context.Context) ([]setup.CronTrigger, error) {
	return listAll[setup.CronTrigger](ctx, c, "/api/v1/cron-triggers", nil)
}

// CreateCronTrigger files one scheduled prompt under a group — the same groupId
// requirement every create carries (ruling R12). The hub validates the schedule and
// answers with the generated id.
func (c *HTTPClient) CreateCronTrigger(ctx context.Context, groupID string, ct setup.CronTrigger) (Ref, error) {
	var ref Ref
	body := cronTriggerCreateRequest{CronTrigger: ct, GroupID: groupID}
	if err := c.post(ctx, "/api/v1/cron-triggers", body, &ref); err != nil {
		return Ref{}, err
	}
	return ref, nil
}

// cronTriggerUpdateRequest is a cron trigger update. The hub requires the name, the
// agent and the prompt on every write, so all three are always sent; the schedule and
// the enablement are the two an install may mean to move, and an absent one is left
// where it is rather than cleared.
type cronTriggerUpdateRequest struct {
	Name     string  `json:"name"`
	AgentRef string  `json:"agentRef"`
	Prompt   string  `json:"prompt"`
	Schedule *string `json:"schedule,omitempty"`
	Enabled  *bool   `json:"enabled,omitempty"`
}

// UpdateCronTrigger writes one scheduled prompt by id — the generated id a list
// carried, since display names are not unique and are not lookups.
func (c *HTTPClient) UpdateCronTrigger(ctx context.Context, id string, u CronTriggerUpdate) error {
	// The two types are the same shape on purpose: the request is the update, and a
	// nil half of it is the caller saying "leave that where it is".
	return c.put(ctx, "/api/v1/cron-triggers/"+url.PathEscape(id), cronTriggerUpdateRequest(u), nil)
}

// mcpItem is one entry of AInsel's MCP server registry. The list is a bare JSON array
// rather than the paginated envelope every other collection uses.
type mcpItem struct {
	Name         string `json:"name"`
	DisplayName  string `json:"displayName"`
	URL          string `json:"url"`
	TokenFromEnv string `json:"tokenFromEnv,omitempty"`
}

func (c *HTTPClient) ListMCPServers(ctx context.Context) ([]MCPServer, error) {
	var items []mcpItem
	if err := c.get(ctx, "/api/v1/mcp-servers", nil, &items); err != nil {
		return nil, err
	}
	out := make([]MCPServer, 0, len(items))
	for _, it := range items {
		out = append(out, MCPServer{
			Name:         it.Name,
			DisplayName:  it.DisplayName,
			URL:          it.URL,
			TokenFromEnv: it.TokenFromEnv,
		})
	}
	return out, nil
}

func (c *HTTPClient) RegisterMCPServer(ctx context.Context, spec MCPServerSpec) (Ref, error) {
	var resp mcpItem
	body := struct {
		Name         string `json:"name"`
		GroupID      string `json:"groupId"`
		DisplayName  string `json:"displayName"`
		Description  string `json:"description,omitempty"`
		URL          string `json:"url"`
		TokenFromEnv string `json:"tokenFromEnv,omitempty"`
		ManagedBy    string `json:"managedBy"`
	}{
		Name:         spec.Name,
		GroupID:      spec.GroupID,
		DisplayName:  spec.DisplayName,
		Description:  spec.Description,
		URL:          spec.URL,
		TokenFromEnv: spec.TokenFromEnv,
		ManagedBy:    managerStamp,
	}

	if err := c.post(ctx, "/api/v1/mcp-servers", body, &resp); err != nil {
		return Ref{}, err
	}
	name := resp.Name
	if name == "" {
		name = spec.Name
	}
	return Ref{ID: name, Name: name}, nil
}

// -- images -------------------------------------------------------------------------

// CreateAgentImage posts an install's role image. The spec carries the hub's field
// names, so it goes out as it stands once the manager stamp is set on it; what comes
// back is the id every later call — refresh, tools, an agent's imageRef — acts on the
// image by, and the display name.
func (c *HTTPClient) CreateAgentImage(ctx context.Context, spec ImageSpec) (Ref, error) {
	spec.ManagedBy = managerStamp
	var resp struct {
		ID          string `json:"id"`
		DisplayName string `json:"displayName"`
	}
	if err := c.post(ctx, "/api/v1/agent-images", spec, &resp); err != nil {
		return Ref{}, err
	}
	name := resp.DisplayName
	if name == "" {
		name = spec.DisplayName
	}
	return Ref{ID: resp.ID, Name: name}, nil
}

// RefreshImageTools makes the hub list the tools of each of an image's MCP servers now
// and hands back the catalog that resulted. It takes no body: which servers to read is
// the image's own.
//
// A server the hub could not read comes back as a *ToolRefreshError naming it —
// alongside the other servers' tools rather than instead of them, because the
// plugin's own MCP server can never be listed anonymously (spec §3.5) and a caller
// that lost the forge server's tools over that one failure would have nothing to
// profile. A call that failed outright — the image has no MCP servers, the hub refused
// — carries no tools, only the hub's own error.
func (c *HTTPClient) RefreshImageTools(ctx context.Context, id string) ([]ImageTool, error) {
	var resp struct {
		Image struct {
			Tools []ImageTool `json:"tools"`
		} `json:"image"`
		Warnings []string `json:"warnings"`
	}
	if err := c.post(ctx, "/api/v1/agent-images/"+url.PathEscape(id)+"/refresh-mcp", nil, &resp); err != nil {
		return nil, err
	}
	if len(resp.Warnings) > 0 {
		return resp.Image.Tools, &ToolRefreshError{Servers: warningServers(resp.Warnings)}
	}
	return resp.Image.Tools, nil
}

// warningServers names the failing server behind each refresh warning. The hub
// prefixes a per-server failure with the server's registry name ("<name>: <err>"); a
// warning that carries no such prefix is kept whole, so a caller that ignores the one
// server it expects to fail cannot silently ignore an error it cannot attribute.
func warningServers(warnings []string) []string {
	out := make([]string, 0, len(warnings))
	for _, w := range warnings {
		if server, _, ok := strings.Cut(w, ": "); ok && server != "" {
			out = append(out, server)
			continue
		}
		out = append(out, w)
	}
	return out
}

// GetAgentImage reads one image back as the managed state the plugin compares an
// install against. The env and the MCP servers are read for their names only: a value
// the hub stores as a secret never comes back, and a server's URL and token env var
// are the registry's, which the name is the handle to.
func (c *HTTPClient) GetAgentImage(ctx context.Context, id string) (Image, error) {
	var resp struct {
		ID          string      `json:"id"`
		DisplayName string      `json:"displayName"`
		ImageURL    string      `json:"imageURL"`
		Tools       []ImageTool `json:"tools"`
		Env         []EnvVar    `json:"env"`
		MCPServers  []struct {
			Name string `json:"name"`
		} `json:"mcpServers"`
	}
	if err := c.get(ctx, "/api/v1/agent-images/"+url.PathEscape(id), nil, &resp); err != nil {
		return Image{}, err
	}

	img := Image{
		ID:          resp.ID,
		DisplayName: resp.DisplayName,
		ImageURL:    resp.ImageURL,
		Tools:       resp.Tools,
	}
	for _, e := range resp.Env {
		img.Env = append(img.Env, e.Name)
	}
	for _, m := range resp.MCPServers {
		img.MCPServers = append(img.MCPServers, m.Name)
	}
	return img, nil
}

// SetImageTools restates the whole catalog with only the role's tools enabled. The
// hub replaces the image's tools with what arrives, which is why the caller hands back
// the catalog as RefreshImageTools read it: dropping a tool out of the list would
// drop it off the image.
func (c *HTTPClient) SetImageTools(ctx context.Context, id string, tools []ImageTool) error {
	body := struct {
		Tools []ImageTool `json:"tools"`
	}{Tools: tools}
	return c.put(ctx, "/api/v1/agent-images/"+url.PathEscape(id), body, nil)
}

// UpdateAgentImage changes what an install's image runs on and what it runs with.
// The pointers are the hub's keep-versus-set rule: a field left nil is absent from the
// body and the hub keeps what it holds, a non-nil empty slice arrives as [] and
// clears — the difference an edit-install with blank secrets depends on.
func (c *HTTPClient) UpdateAgentImage(ctx context.Context, id string, u ImageUpdate) error {
	req := agentImageUpdateRequest{
		ImageURL:   u.ImageURL,
		MCPServers: u.MCPServers,
	}
	if u.Env != nil {
		items := make([]agentImageEnvUpdate, 0, len(*u.Env))
		for _, e := range *u.Env {
			item := agentImageEnvUpdate{Name: e.Name, Value: e.Value}
			// A name this plugin sends as a secret says so; one it does not leaves
			// the flag alone rather than writing secret:false — which is the only
			// way an update that did not mean to could declassify a stored token.
			if e.Secret {
				secret := true
				item.Secret = &secret
			}
			items = append(items, item)
		}
		req.Env = &items
	}
	return c.put(ctx, "/api/v1/agent-images/"+url.PathEscape(id), req, nil)
}

// -- agents -------------------------------------------------------------------------

// GetAgent reads one agent back as the managed state it is.
func (c *HTTPClient) GetAgent(ctx context.Context, id string) (Agent, error) {
	var resp struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Description string `json:"description,omitempty"`
		ImageRef    struct {
			Name string `json:"name"`
		} `json:"imageRef"`
		Persona *struct {
			ID string `json:"id"`
		} `json:"persona,omitempty"`
		LLM            *agentLLM                `json:"llm,omitempty"`
		Env            []EnvVar                 `json:"env,omitempty"`
		MCP            *agentMCPInfo            `json:"mcp,omitempty"`
		Skills         []string                 `json:"skills,omitempty"`
		EnabledTools   []string                 `json:"enabledTools,omitempty"`
		WorkingHours   *WorkingHours            `json:"workingHours,omitempty"`
		HoursOpen      bool                     `json:"hoursOpen,omitempty"`
		HoursClosesAt  string                   `json:"hoursClosesAt,omitempty"`
		HoursNextOpen  string                   `json:"hoursNextOpenAt,omitempty"`
		OllamaCloud    *agentOllamaCloudInfo    `json:"ollamaCloud,omitempty"`
		OpenCode       *agentOpenCodeInfo       `json:"openCode,omitempty"`
		AlibabaCloud   *agentAlibabaCloudInfo   `json:"alibabaCloud,omitempty"`
		OpenAI         *agentOpenAIInfo         `json:"openAI,omitempty"`
		Anthropic      *agentAnthropicInfo      `json:"anthropic,omitempty"`
		Google         *agentGoogleInfo         `json:"google,omitempty"`
		CustomProvider *agentCustomProviderInfo `json:"customProvider,omitempty"`
	}
	if err := c.get(ctx, "/api/v1/agents/"+url.PathEscape(id), nil, &resp); err != nil {
		return Agent{}, err
	}

	agent := Agent{
		ID:          resp.ID,
		Name:        resp.Name,
		Description: resp.Description,
		ImageRef:    resp.ImageRef.Name,
		Env:         resp.Env,
	}
	if resp.Persona != nil {
		agent.PersonaID = resp.Persona.ID
	}
	if resp.LLM != nil {
		agent.LLM = &LLMSpec{Model: resp.LLM.Model, Provider: resp.LLM.Provider}
	}
	if resp.MCP != nil {
		for _, s := range resp.MCP.Servers {
			agent.MCPServers = append(agent.MCPServers, s.Name)
		}
	}
	agent.Skills = resp.Skills
	agent.EnabledTools = resp.EnabledTools
	agent.WorkingHours = resp.WorkingHours
	agent.HoursOpen = resp.HoursOpen
	agent.HoursClosesAt = resp.HoursClosesAt
	agent.HoursNextOpenAt = resp.HoursNextOpen
	if resp.OllamaCloud != nil {
		agent.OllamaCloudKey = resp.OllamaCloud.APIKey
	}
	if resp.OpenCode != nil {
		agent.OpenCodeKey = resp.OpenCode.APIKey
	}
	if resp.AlibabaCloud != nil {
		agent.AlibabaKey = resp.AlibabaCloud.APIKey
	}
	if resp.OpenAI != nil {
		agent.OpenAIKey = resp.OpenAI.APIKey
		// The hub answers the method an agent holds and defaults it to the API key
		// when the agent names none, so a block that exists always says which of
		// the two this agent is. A block that does not exist says nothing at all.
		agent.OpenAIAuthMethod = resp.OpenAI.AuthMethod
		if agent.OpenAIAuthMethod == "" {
			agent.OpenAIAuthMethod = setup.LLMAuthMethodAPIKey
		}
	}
	if resp.Anthropic != nil {
		agent.AnthropicKey = resp.Anthropic.APIKey
	}
	if resp.Google != nil {
		agent.GoogleKey = resp.Google.APIKey
	}
	if resp.CustomProvider != nil {
		agent.CustomURL = resp.CustomProvider.URL
		agent.CustomKey = resp.CustomProvider.APIKey
	}
	return agent, nil
}

// UpdateAgent changes an agent's properties. A field left nil is left alone.
func (c *HTTPClient) UpdateAgent(ctx context.Context, id string, u AgentUpdate) error {
	req := agentUpdateRequest{}
	if u.Name != nil {
		req.Name = u.Name
	}
	if u.ImageRef != nil {
		req.ImageRef = &agentImageRef{Name: *u.ImageRef}
	}
	if u.PersonaID != nil {
		if *u.PersonaID == "" {
			req.Persona = &agentPersonaRef{ID: ""}
		} else {
			req.Persona = &agentPersonaRef{ID: *u.PersonaID}
		}
	}
	if u.LLM != nil {
		req.LLM = &agentLLM{Model: u.LLM.Model, Provider: u.LLM.Provider}
	}
	if u.MCPServers != nil {
		req.MCP = &agentMCP{Servers: *u.MCPServers}
	}
	if u.Skills != nil {
		req.Skills = u.Skills
	}
	// The grant follows the same pointer rule as skills, but a non-nil empty slice is
	// dropped rather than sent: the runtime reads an unset grant as "everything", so
	// clearing a grant is a widening, which no caller of this client means.
	if u.EnabledTools != nil && len(*u.EnabledTools) > 0 {
		req.EnabledTools = u.EnabledTools
	}
	// The window: a clear is the hub's empty object, and it wins over a set because
	// writing a window nobody asked for is the worse of the two caller errors. Neither
	// leaves the key out on its own account — an absent key is "keep the schedule".
	if u.ClearWorkingHours {
		req.WorkingHours = &WorkingHours{}
	} else if u.WorkingHours != nil {
		req.WorkingHours = u.WorkingHours
	}
	if u.Env != nil {
		items := make([]agentImageEnvUpdate, 0, len(*u.Env))
		for _, e := range *u.Env {
			item := agentImageEnvUpdate{Name: e.Name, Value: e.Value}
			if e.Secret {
				secret := true
				item.Secret = &secret
			}
			items = append(items, item)
		}
		req.Env = &items
	}
	if u.OllamaCloudKey != nil {
		req.OllamaCloud = &agentOllamaCloudInfo{APIKey: *u.OllamaCloudKey}
	}
	if u.OpenCodeKey != nil {
		req.OpenCode = &agentOpenCodeInfo{APIKey: *u.OpenCodeKey}
	}
	if u.AlibabaKey != nil {
		req.AlibabaCloud = &agentAlibabaCloudInfo{APIKey: *u.AlibabaKey}
	}
	if u.OpenAIKey != nil || u.OpenAIAuthMethod != nil {
		req.OpenAI = &agentOpenAIInfo{}
		if u.OpenAIKey != nil {
			req.OpenAI.APIKey = *u.OpenAIKey
		}
		if u.OpenAIAuthMethod != nil {
			req.OpenAI.AuthMethod = *u.OpenAIAuthMethod
		}
	}
	if u.AnthropicKey != nil {
		req.Anthropic = &agentAnthropicInfo{APIKey: *u.AnthropicKey}
	}
	if u.GoogleKey != nil {
		req.Google = &agentGoogleInfo{APIKey: *u.GoogleKey}
	}
	if u.CustomURL != nil || u.CustomKey != nil {
		req.CustomProvider = &agentCustomProviderInfo{}
		if u.CustomURL != nil {
			req.CustomProvider.URL = *u.CustomURL
		}
		if u.CustomKey != nil {
			req.CustomProvider.APIKey = *u.CustomKey
		}
	}
	return c.put(ctx, "/api/v1/agents/"+url.PathEscape(id), req, nil)
}

// deleteTolerant is a hub delete that reads "not there" as done. A removal that
// is retried — the row survives while anything is outstanding — re-attempts what
// an earlier pass already deleted; if deleting a gone resource errored, the row
// could never clear and the removal could never converge. Anything else the hub
// answers with a non-success for stays the error it is.
func (c *HTTPClient) deleteTolerant(ctx context.Context, path string) error {
	cred, err := c.credential(ctx)
	if err != nil {
		return err
	}
	err = c.transport.do(ctx, http.MethodDelete, cred, path, nil, nil, nil)
	var se *StatusError
	if errors.As(err, &se) && se.Code == http.StatusNotFound {
		return nil
	}
	return err
}

// DeleteAgent deletes an agent by id. Already gone is deleted, not an error.
func (c *HTTPClient) DeleteAgent(ctx context.Context, id string) error {
	return c.deleteTolerant(ctx, "/api/v1/agents/"+url.PathEscape(id))
}

// DeleteAgentImage deletes an agent image by id. Already gone is deleted, not an error.
func (c *HTTPClient) DeleteAgentImage(ctx context.Context, id string) error {
	return c.deleteTolerant(ctx, "/api/v1/agent-images/"+url.PathEscape(id))
}

// DeletePersona deletes a persona by id. Already gone is deleted, not an error.
func (c *HTTPClient) DeletePersona(ctx context.Context, id string) error {
	return c.deleteTolerant(ctx, "/api/v1/personas/"+url.PathEscape(id))
}

// GetPersona reads one persona back with its text. The list hands out id and
// name only; the text — the whole of what a persona says — is what the
// reconcile pass has to read to know whether the hub already holds what the
// settings would have it hold.
func (c *HTTPClient) GetPersona(ctx context.Context, id string) (Persona, error) {
	var resp struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Text string `json:"text"`
	}
	if err := c.get(ctx, "/api/v1/personas/"+url.PathEscape(id), nil, &resp); err != nil {
		return Persona{}, err
	}
	out := Persona{ID: resp.ID, Name: resp.Name, Text: resp.Text}
	if out.ID == "" {
		out.ID = id
	}
	return out, nil
}

// UpdatePersona rewrites a persona's text. Only the text is sent: the hub keeps the name
// and description it has for every field a request leaves out.
func (c *HTTPClient) UpdatePersona(ctx context.Context, id, text string) error {
	body := struct {
		Text string `json:"text"`
	}{Text: text}
	return c.put(ctx, "/api/v1/personas/"+url.PathEscape(id), body, nil)
}

// DeleteConnector deletes a connector by id. Already gone is deleted, not an error.
func (c *HTTPClient) DeleteConnector(ctx context.Context, id string) error {
	return c.deleteTolerant(ctx, "/api/v1/connectors/"+url.PathEscape(id))
}

// DeleteMCPServer deletes an MCP server by id. Already gone is deleted, not an error.
func (c *HTTPClient) DeleteMCPServer(ctx context.Context, id string) error {
	return c.deleteTolerant(ctx, "/api/v1/mcp-servers/"+url.PathEscape(id))
}

// DeleteTrigger deletes a trigger by id. Already gone is deleted, not an error.
func (c *HTTPClient) DeleteTrigger(ctx context.Context, id string) error {
	return c.deleteTolerant(ctx, "/api/v1/triggers/"+url.PathEscape(id))
}

// DeleteCronTrigger deletes a cron trigger by id — the generated id a list
// carried, since display names are not unique and are not lookups. Already gone
// is deleted, not an error.
func (c *HTTPClient) DeleteCronTrigger(ctx context.Context, id string) error {
	return c.deleteTolerant(ctx, "/api/v1/cron-triggers/"+url.PathEscape(id))
}

// RotateConnectorSecret rotates a connector's webhook secret.
func (c *HTTPClient) RotateConnectorSecret(ctx context.Context, id string) (Connector, error) {
	var resp struct {
		Ref
		WebhookEndpoint    string `json:"webhookEndpoint"`
		WebhookSecretValue string `json:"webhookSecretValue"`
	}
	if err := c.post(ctx, "/api/v1/connectors/"+url.PathEscape(id)+"/rotate-secret", nil, &resp); err != nil {
		return Connector{}, err
	}
	return Connector{
		Ref:                resp.Ref,
		WebhookEndpoint:    resp.WebhookEndpoint,
		WebhookSecretValue: resp.WebhookSecretValue,
	}, nil
}

// Events reads the events the hub recorded for one connector since the given moment.
func (c *HTTPClient) Events(ctx context.Context, connectorID string, since time.Time) ([]Event, error) {
	var out []Event
	for page := 1; ; page++ {
		q := url.Values{
			"page":     {strconv.Itoa(page)},
			"pageSize": {strconv.Itoa(listPageSize)},
		}
		if connectorID != "" {
			q.Set("connector", connectorID)
		}
		if !since.IsZero() {
			q.Set("since", since.UTC().Format(time.RFC3339))
		}

		var resp struct {
			Events []struct {
				ID        string `json:"id"`
				Type      string `json:"type"`
				Timestamp string `json:"timestamp"`
			} `json:"events"`
			Total int `json:"total"`
		}
		if err := c.get(ctx, "/api/v1/events", q, &resp); err != nil {
			return nil, err
		}
		for _, e := range resp.Events {
			at, _ := time.Parse(time.RFC3339, e.Timestamp)
			out = append(out, Event{ID: e.ID, Type: e.Type, At: at})
		}
		if len(resp.Events) == 0 || len(out) >= resp.Total {
			break
		}
	}
	return out, nil
}

// PluginGroups returns the group ids the plugin has access to. It uses the background
// credential even if the context carries a user credential.
func (c *HTTPClient) PluginGroups(ctx context.Context) ([]string, error) {
	// Clear the user credential but keep the caller's deadline and cancellation by
	// wrapping the context. Then mark it as background work so the plugin's own
	// credential is minted.
	bgCtx := WithBackground(WithoutCredential(ctx))
	cred, err := c.credential(bgCtx)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Groups []struct {
			ID string `json:"id"`
		} `json:"groups"`
	}
	if err := c.transport.GetJSON(bgCtx, cred, "/api/v1/plugin-context", nil, &resp); err != nil {
		return nil, err
	}

	// Empty, not nil: a plugin with no group access yet is the normal state of a fresh
	// install, and the setup options answer this list to the UI as JSON — nil would be
	// null there, not [].
	out := make([]string, 0, len(resp.Groups))
	for _, g := range resp.Groups {
		out = append(out, g.ID)
	}
	return out, nil
}

// -- llm reachability ---------------------------------------------------------------

// llmCheckPath is the hub's "can this provider serve this model?" endpoint: a POST
// of {provider, model} that answers 200 when the provider resolves and the model can
// serve, and a status plus a sanitized failure sentence otherwise. The route is a hub
// addition: a hub without it answers 404, which is "cannot check" here, never ok.
const llmCheckPath = "/api/v1/llm/check"

// llmCheckTimeout caps one pair's check. The question goes out to a third-party LLM
// provider that may be down or slow, and "Check now" owes the browser an answer: a
// pair that has not replied by the cap is reported as unknown — the check could not
// be completed — rather than holding the whole request open.
const llmCheckTimeout = 10 * time.Second

// CheckLLM asks the hub whether one (provider, model) pair can serve. The mapping
// from the hub's answer to the three states:
//
//   - 2xx → ok: the hub asked, and the pair answered.
//   - 502/503/504 → unreachable, carrying the hub's own capped failure sentence:
//     these are the statuses that mean "the hub asked, the pair could not serve".
//   - 401/403 → unknown, with a reason that reads as an instruction: the route is there
//     but the hub will not accept the plugin's credential for it, which in practice
//     means the manifest's `llm:check` is not approved on the Plugin CR. The question
//     was still never asked, so the pair is not down — but an admin has to be told
//     what to approve, not shown a status code.
//   - any other status → unknown: the request failed, not the pair. A route that is
//     not there (404/405), a body the hub would not accept (400 — e.g. a custom
//     provider, which cannot be checked from a pair), the hub rate-limiting the
//     plugin (429), or a hub-side fault (500) all say nothing about whether the
//     provider can serve, and must not be reported as if they did.
//   - no answer at all (transport failure, the cap expiring) → unknown: a hub that
//     cannot be asked says nothing about the provider.
//
// Only a cancelled context comes back as an error: the caller is going away, and
// there is no answer to report.
func (c *HTTPClient) CheckLLM(ctx context.Context, provider, model string) (LLMCheckResult, error) {
	limit := c.checkTimeout
	if limit == 0 {
		limit = llmCheckTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()

	body := struct {
		Provider string `json:"provider"`
		Model    string `json:"model"`
	}{Provider: provider, Model: model}

	// No body is decoded on success: the status is the whole answer. On failure the
	// transport has already read the hub's body into the StatusError, capped — the
	// hub's sanitized sentence lives in its "error" field, nothing more.
	err := c.post(ctx, llmCheckPath, body, nil)
	if err == nil {
		return LLMCheckResult{State: LLMCheckOK}, nil
	}
	if errors.Is(err, context.Canceled) {
		return LLMCheckResult{}, err
	}

	var se *StatusError
	if errors.As(err, &se) {
		switch se.Code {
		case http.StatusBadGateway, http.StatusServiceUnavailable,
			http.StatusGatewayTimeout:
			// The hub answered the question on the provider's behalf: the pair
			// cannot serve. Carry the hub's capped sentence — nothing else.
			reason := hubFailureMessage(se.Body)
			if reason == "" {
				reason = fmt.Sprintf("the hub answered %d", se.Code)
			}
			return LLMCheckResult{State: LLMCheckUnreachable, Reason: reason}, nil
		case http.StatusUnauthorized, http.StatusForbidden:
			// The route exists; the hub will not let this plugin's credential through it.
			// For a route the manifest declares, the only thing that produces this is an
			// unapproved permission — and the hub can only ever approve names the
			// manifest declared, so the actionable answer names the permission to
			// approve. Still unknown, never unreachable: a refused question is not a
			// provider that failed one, and the settings summary prints this sentence
			// to an admin.
			detail := hubFailureMessage(se.Body)
			if detail == "" {
				detail = "permission not approved"
			}
			return LLMCheckResult{
				State: LLMCheckUnknown,
				Reason: fmt.Sprintf(
					"cannot check: the hub refused the check (%s) — approve the llm:check permission for this plugin in the AInsel console",
					detail),
			}, nil
		default:
			// The request failed, not the pair: no route, a body the hub would not
			// accept, the hub rate-limiting the plugin, or a hub-side fault. None of
			// these is evidence about the provider, so none may read as unreachable —
			// the question was never answered.
			return LLMCheckResult{
				State:  LLMCheckUnknown,
				Reason: fmt.Sprintf("cannot check: the hub would not answer the check (%d)", se.Code),
			}, nil
		}
	}

	// The hub itself did not answer — unreachable, or the cap expired on the way
	// there. Either way the question was not asked, and the provider's state is
	// unknown, not bad.
	return LLMCheckResult{
		State:  LLMCheckUnknown,
		Reason: "cannot check: the hub did not answer",
	}, nil
}

// hubFailureMessage reads the hub's own explanation off a failure body: its "error"
// field when the body is the JSON the hub promises, the whole (already capped) body
// when it is not — a proxy's HTML page is still the only explanation there is. The
// text is the hub's, sanitized and capped hub-side; this only extracts it.
func hubFailureMessage(body string) string {
	if body == "" {
		return ""
	}
	var parsed struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &parsed); err == nil && parsed.Error != "" {
		return parsed.Error
	}
	return body
}

// -- runs ---------------------------------------------------------------------------

// invocationItem is one invocation as the hub lists it, reduced to what a run of the
// plugin's work list is made of.
type invocationItem struct {
	ID        string          `json:"id"`
	AgentName string          `json:"agentName"`
	Status    string          `json:"status"`
	Connector string          `json:"connector"`
	EventID   string          `json:"eventId"`
	StartTime time.Time       `json:"startTime"`
	EndTime   *time.Time      `json:"endTime"`
	Task      *invocationTask `json:"task"`
}

// invocationTask is the task an invocation was one attempt at: while the hub still retries
// it, the invocation carries the latest attempt and the task says the work is not over.
type invocationTask struct {
	Status   string `json:"status"`
	RunState string `json:"runState"`
}

// Runs reads the runs the hub recorded as the query asks and assembles them into the work
// list's shape: the invocation for the identity and the timing, the event behind it for the
// issue or pull request it was about, and — when the query asks for costs — the token metrics
// for what it cost.
//
// Both halves of the scope are the plugin's own rather than the hub's whole story. The
// connector list is what makes this the work of one forge rather than of every install:
// nothing here shows a run the plugin's own connectors did not receive. The agent list is what
// makes it this install's agents' work: the hub is asked for each named agent's invocations,
// so a plugin with three agents reads three filtered pages instead of every invocation the hub
// holds, most of which it would throw away. A query naming no connector or no agent names
// nothing to read and asks the hub nothing.
//
// The events behind the runs are read in parallel, and the cost metrics alongside them: one
// page is 200 runs, and a page that waits for each read in turn is a page that takes as long
// as the hub has round trips.
func (c *HTTPClient) Runs(ctx context.Context, q RunsQuery) ([]work.Run, error) {
	want := make(map[string]bool, len(q.Connectors))
	for _, id := range q.Connectors {
		want[id] = true
	}
	agents := uniqueIDs(q.Agents)
	if len(want) == 0 || len(agents) == 0 {
		return []work.Run{}, nil
	}

	invocations, err := c.invocationsForAgents(ctx, agents, q.Since)
	if err != nil {
		return nil, err
	}

	// Keep this forge's runs, and collect the distinct events behind them: one event can
	// start several runs — one trigger matching several agents, or the same event retried —
	// and its payload is the same read every time, so it is read once for all of them.
	kept := make([]invocationItem, 0, len(invocations))
	eventIDs := make([]string, 0, len(invocations))
	seenEvent := make(map[string]bool, len(invocations))
	for _, inv := range invocations {
		if !want[inv.Connector] {
			continue
		}
		kept = append(kept, inv)
		if inv.EventID != "" && !seenEvent[inv.EventID] {
			seenEvent[inv.EventID] = true
			eventIDs = append(eventIDs, inv.EventID)
		}
	}

	// The subject cache is asked before the hub is: an event the plugin has already read
	// has its answer at home, and the ~20 KB payload behind it is not worth fetching a
	// second time. A cache that cannot be read is logged and skipped — the hub then
	// answers for every event, which is exactly the old behaviour, never a failed read.
	subjects := make(map[string]forge.Subject, len(eventIDs))
	toFetch := eventIDs
	if q.Cache != nil && len(eventIDs) > 0 {
		cached, err := q.Cache.Subjects(ctx, eventIDs)
		if err != nil {
			log.Printf("hub: subject cache read: %v", err)
		} else {
			for id, subject := range cached {
				subjects[id] = subject
			}
			toFetch = make([]string, 0, len(eventIDs))
			for _, id := range eventIDs {
				if _, ok := cached[id]; !ok {
					toFetch = append(toFetch, id)
				}
			}
		}
	}
	// newSubjects holds only what this call read from the hub — including the zero
	// subjects of 404s, whose answer is final too — and is what the cache is offered.
	newSubjects := make(map[string]forge.Subject, len(toFetch))

	// The event payloads and, when the caller's page shows costs, the token metrics are read
	// together rather than in turn. The metrics answer for the whole window in one call, so a
	// page that read them after its events paid for a round trip it could have had for free —
	// and a page that shows no cost never asks for them at all.
	readCtx, cancelReads := context.WithCancel(ctx)
	defer cancelReads()

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		costs   map[string]tokenRow
		readErr error
	)

	if q.Costs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// A hub that cannot answer for its metrics is costsByEvent's to swallow: a work
			// list without costs is a shorter truth than no work list at all.
			costs = c.costsByEvent(readCtx)
		}()
	}

	// A semaphore rather than a goroutine per event: a page of distinct events is up to a
	// page of runs, and a hub is not obliged to answer a hundred reads at once.
	sem := make(chan struct{}, maxConcurrentEventReads)
	for _, eventID := range toFetch {
		wg.Add(1)
		go func(eventID string) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-readCtx.Done():
				return
			}

			subject, err := c.subjectForEvent(readCtx, eventID)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if readErr == nil {
					readErr = err
				}
				// One event the hub would not answer fails the read — there is no honest
				// page to show without it — so the reads still waiting stop instead of
				// asking a hub that is already in trouble for more.
				cancelReads()
				return
			}
			subjects[eventID] = subject
			newSubjects[eventID] = subject
		}(eventID)
	}
	wg.Wait()
	if readErr != nil {
		return nil, readErr
	}

	// The read answered, so what it saw of the hub is worth keeping: the next page
	// asks the cache before it asks the hub. A cache that cannot be written is
	// logged and let go — the runs are the answer either way.
	if q.Cache != nil && len(newSubjects) > 0 {
		if err := q.Cache.PutSubjects(ctx, newSubjects, time.Now()); err != nil {
			log.Printf("hub: subject cache write: %v", err)
		}
	}

	runs := make([]work.Run, 0, len(kept))
	for _, inv := range kept {
		run := work.Run{
			ID:        inv.ID,
			Agent:     inv.AgentName,
			Status:    inv.Status,
			Connector: inv.Connector,
			Start:     inv.StartTime,
			End:       inv.EndTime,
		}
		// A failed attempt is not a finished run while the hub still retries the task
		// behind it: the runState names what the task is doing, and anything else the hub
		// reports for a pending or claimed task — claimed is a worker on it right now — is
		// still work in progress.
		if inv.Task != nil && (inv.Task.Status == "pending" || inv.Task.Status == "claimed") {
			run.Status = work.StatusRunning
			if inv.Task.RunState == work.StatusQueued || inv.Task.RunState == work.StatusRunning {
				run.Status = inv.Task.RunState
			}
		}
		if inv.EventID != "" {
			// The read either answered or failed the call above; an event the hub no longer
			// holds is the zero subject its 404 left in this map.
			run.Subject = subjects[inv.EventID]
			if row, ok := costs[inv.EventID]; ok {
				run.CostUSD = row.CostUSD
				run.CostKnown = row.CostReported
			}
		}
		runs = append(runs, run)
	}
	return runs, nil
}

// uniqueIDs is a list of ids with the repeats and the empty ones taken out, in the order the
// caller named them. A caller that named the same agent twice would otherwise read that
// agent's invocations twice and learn nothing new from the second read.
func uniqueIDs(in []string) []string {
	var out []string
	seen := make(map[string]bool, len(in))
	for _, id := range in {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// invocationsForAgents reads each named agent's invocations and merges them into one list in
// the order the agents were named. The hub filters by agent, so an invocation belongs to one
// read; a hub that hands the same one back under two of them is answered by the invocation id,
// which is the run's identity — a run counted twice would be work counted twice.
func (c *HTTPClient) invocationsForAgents(ctx context.Context, agents []string, since time.Time) ([]invocationItem, error) {
	var out []invocationItem
	seen := make(map[string]bool)
	for _, agent := range agents {
		page, err := c.invocationsSince(ctx, agent, since)
		if err != nil {
			return nil, err
		}
		for _, inv := range page {
			if seen[inv.ID] {
				continue
			}
			seen[inv.ID] = true
			out = append(out, inv)
		}
	}
	return out, nil
}

// invocationsSince reads one agent's invocation pages since a moment, with the hub's own
// `agent=` filter so a page of work reads a page of the agents it is about rather than every
// invocation the hub holds. An empty agent reads every agent's, which is what a caller that
// names none gets by not calling at all.
//
// The envelope names its list "invocations" rather than "items" — the one collection that
// does — which is why this is its own loop rather than another use of listAll.
func (c *HTTPClient) invocationsSince(ctx context.Context, agent string, since time.Time) ([]invocationItem, error) {
	var out []invocationItem
	for page := 1; ; page++ {
		q := url.Values{
			"since":    {since.UTC().Format(time.RFC3339)},
			"page":     {strconv.Itoa(page)},
			"pageSize": {strconv.Itoa(listPageSize)},
		}
		if agent != "" {
			q.Set("agent", agent)
		}
		var resp struct {
			Invocations []invocationItem `json:"invocations"`
			TotalPages  int              `json:"totalPages"`
		}
		if err := c.get(ctx, "/api/v1/invocations", q, &resp); err != nil {
			return nil, err
		}
		out = append(out, resp.Invocations...)
		if len(resp.Invocations) == 0 || page >= resp.TotalPages {
			return out, nil
		}
	}
}

// subjectForEvent reads what one event was about. Runs reads the events behind its page in
// parallel and keeps each answer once, so this says nothing about caching: one read, one event.
//
// An event past retention answers 404: the hub no longer has the payload, so nothing here can
// say what the run was about. With no subject to link to, work.Build drops the run from the
// work view — a reader could not open anything a zero subject names.
//
// blocked: AInsel addition 2 (durable run subject) — once the hub keeps a run's subject past
// the event's retention, a run whose event is gone keeps its place in the work view instead of
// dropping out of it.
func (c *HTTPClient) subjectForEvent(ctx context.Context, eventID string) (forge.Subject, error) {
	var ev struct {
		Payload json.RawMessage `json:"payload"`
	}
	err := c.get(ctx, "/api/v1/events/"+url.PathEscape(eventID), nil, &ev)
	var se *StatusError
	if errors.As(err, &se) && se.Code == http.StatusNotFound {
		return forge.Subject{}, nil
	}
	if err != nil {
		return forge.Subject{}, err
	}

	subject, _ := forge.SubjectFromEvent(eventTypeFromPayload(ev.Payload), ev.Payload)
	return subject, nil
}

// blocked: AInsel addition 2 (durable run subject) — the hub stores an event's type in
// its webhook headers and serves neither the header nor a subject, so until it serves
// one the type has to be inferred from the payload's shape (ruling R10).
//
// eventTypeFromPayload is that inference: which of Forgejo's keys the payload carries
// says which event it came from. It is a guess with a known blur — a comment on a pull
// request carries pull_request too, and reads here as a pull request rather than as the
// comment on it — which is exactly what a durable subject would settle.
func eventTypeFromPayload(payload []byte) string {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		return ""
	}
	_, hasIssue := fields["issue"]
	_, hasComment := fields["comment"]
	_, hasPull := fields["pull_request"]

	switch {
	case hasPull:
		return "pull_request"
	case hasIssue && hasComment:
		return "issue_comment"
	case hasIssue:
		return "issues"
	default:
		// A push, a package event, anything else: forge.SubjectFromEvent has no
		// reading for it and says so.
		return ""
	}
}

// tokenRow is one event's measured tokens, as the metrics endpoint reports them.
type tokenRow struct {
	Event        string  `json:"event"`
	CostUSD      float64 `json:"costUsd"`
	CostReported bool    `json:"costReported"`
}

// costsByEvent reads the last seven days of token metrics and indexes them by event id.
//
// A hub that cannot answer this — no log store yet, an observability outage — is answered
// with no costs rather than with a failed run list: a run's cost is unknown for anything
// older than this window anyway, and a work list without costs is a shorter truth than
// no work list at all.
func (c *HTTPClient) costsByEvent(ctx context.Context) map[string]tokenRow {
	var resp struct {
		Rows []tokenRow `json:"rows"`
	}
	if err := c.get(ctx, "/api/v1/observability/metrics/tokens/by-event",
		url.Values{"range": {costRange}}, &resp); err != nil {
		return nil
	}
	rows := make(map[string]tokenRow, len(resp.Rows))
	for _, row := range resp.Rows {
		rows[row.Event] = row
	}
	return rows
}

// -- request and response shapes -----------------------------------------------------

// agentCreateRequest is POST /api/v1/agents's body: AInsel's nested shapes, built from
// an AgentSpec. The pointers are the hub's own absent-versus-empty distinction — an
// absent mcp means "inherit the image's", while skills belong to the agent and inherit
// nothing, so an absent or empty skills list means no skills.
type agentCreateRequest struct {
	Name        string           `json:"name"`
	GroupID     string           `json:"groupId"`
	Description string           `json:"description,omitempty"`
	ImageRef    agentImageRef    `json:"imageRef"`
	Persona     *agentPersonaRef `json:"persona,omitempty"`
	LLM         *agentLLM        `json:"llm,omitempty"`
	MCP         *agentMCP        `json:"mcp,omitempty"`
	Skills      []string         `json:"skills,omitempty"`
	// EnabledTools is the agent's tool grant, in the image catalog's spellings. It
	// carries omitempty so an empty one stays absent: at the runtime an unset grant is
	// every tool the image offers.
	EnabledTools []string `json:"enabledTools,omitempty"`
	Env          []EnvVar `json:"env,omitempty"`

	// ManagedBy is the manager stamp: the hub's create-time ownership field, always
	// this plugin's stamp (managerStamp). It is set by the client, never by a caller —
	// a wizard resource is the plugin's, and the hub refuses console writes of it.
	ManagedBy string `json:"managedBy,omitempty"`

	// WorkingHours is the window the agent is created with. An absent one is the hub's
	// "any hour"; the create takes no clear, because there is nothing to clear.
	WorkingHours *WorkingHours `json:"workingHours,omitempty"`

	// Provider keys for the LLM providers the plugin delivers a credential for,
	// sent as the same objects the update request takes: an agent created without
	// its key has none and a custom-provider agent without its URL falls back to
	// the OpenAI default endpoint.
	OllamaCloud    *agentOllamaCloudInfo    `json:"ollamaCloud,omitempty"`
	OpenCode       *agentOpenCodeInfo       `json:"openCode,omitempty"`
	AlibabaCloud   *agentAlibabaCloudInfo   `json:"alibabaCloud,omitempty"`
	OpenAI         *agentOpenAIInfo         `json:"openAI,omitempty"`
	Anthropic      *agentAnthropicInfo      `json:"anthropic,omitempty"`
	Google         *agentGoogleInfo         `json:"google,omitempty"`
	CustomProvider *agentCustomProviderInfo `json:"customProvider,omitempty"`
}

// agentImageRef names the agent-image registry entry an agent runs on.
type agentImageRef struct {
	Name string `json:"name"`
}

// agentPersonaRef names the persona an agent answers from, by its id.
type agentPersonaRef struct {
	ID string `json:"id"`
}

// agentLLM is the model an agent runs on. Provider is optional: an empty one leaves the
// choice to the image.
type agentLLM struct {
	Model    string `json:"model"`
	Provider string `json:"provider,omitempty"`
}

// agentMCP selects MCP servers by registry name; the hub resolves each to its URL and
// token env var when it writes the agent.
type agentMCP struct {
	Servers []string `json:"servers"`
}

// triggerCreateRequest is the trigger the hub's POST takes: setup.Trigger's fields,
// inline, plus the group it is filed under.
type triggerCreateRequest struct {
	setup.Trigger
	GroupID string `json:"groupId"`
}

// cronTriggerCreateRequest is the cron trigger the hub's POST takes: setup.CronTrigger's
// fields, inline, plus the group it is filed under. The trigger's own id is empty on a
// create and carries omitempty, so it never goes out.
type cronTriggerCreateRequest struct {
	setup.CronTrigger
	GroupID string `json:"groupId"`
}

// agentImageUpdateRequest is PUT /api/v1/agent-images/{name}'s body: AInsel's update
// request, where a nil field means "do not modify" and a non-nil empty slice means
// "clear". Display name and description are not on it: an install's image keeps the
// ones it was created with.
type agentImageUpdateRequest struct {
	ImageURL   *string                `json:"imageURL,omitempty"`
	Env        *[]agentImageEnvUpdate `json:"env,omitempty"`
	MCPServers *[]string              `json:"mcpServers,omitempty"`
}

// agentImageEnvUpdate is one env entry of an image update. Secret is a pointer the way
// the hub defines it: omitted, it keeps the flag stored for that name, and only an
// explicit false declassifies — which this client never sends.
type agentImageEnvUpdate struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Secret *bool  `json:"secret,omitempty"`
}

// agentMCPInfo is the API representation of the agent-scoped MCP selection.
type agentMCPInfo struct {
	Servers []agentMCPServer `json:"servers"`
}

// agentMCPServer is one agent-scoped MCP server with resolved details.
type agentMCPServer struct {
	Name         string `json:"name"`
	URL          string `json:"url"`
	TokenFromEnv string `json:"tokenFromEnv,omitempty"`
}

// agentOllamaCloudInfo contains Ollama Cloud provider details.
type agentOllamaCloudInfo struct {
	APIKey string `json:"apiKey,omitempty"`
}

// agentOpenCodeInfo contains OpenCode provider details.
type agentOpenCodeInfo struct {
	APIKey string `json:"apiKey,omitempty"`
}

// agentAlibabaCloudInfo contains Alibaba Cloud provider details.
type agentAlibabaCloudInfo struct {
	APIKey string `json:"apiKey,omitempty"`
}

// agentOpenAIInfo contains OpenAI provider details: the hub's openAI block.
// AuthMethod is the hub's authMethod — "APIKey" (its default, and what APIKey
// feeds) or "ChatGPTSignIn", which arrives with no key at all. Both are omitted
// when empty, so an API-key write goes out exactly as it did before the
// sign-in method existed.
type agentOpenAIInfo struct {
	APIKey     string `json:"apiKey,omitempty"`
	AuthMethod string `json:"authMethod,omitempty"`
}

// agentAnthropicInfo contains Anthropic provider details.
type agentAnthropicInfo struct {
	APIKey string `json:"apiKey,omitempty"`
}

// agentGoogleInfo contains Google Gemini provider details.
type agentGoogleInfo struct {
	APIKey string `json:"apiKey,omitempty"`
}

// agentCustomProviderInfo contains custom provider details.
type agentCustomProviderInfo struct {
	URL    string `json:"url"`
	APIKey string `json:"apiKey,omitempty"`
}

// agentUpdateRequest is PUT /api/v1/agents/{id}'s body.
type agentUpdateRequest struct {
	// Name is the agent's display name. The hub keeps the agent's id when it is absent,
	// which is the same absent-versus-empty rule every other field here follows.
	Name         *string                `json:"name,omitempty"`
	ImageRef     *agentImageRef         `json:"imageRef,omitempty"`
	Persona      *agentPersonaRef       `json:"persona,omitempty"`
	LLM          *agentLLM              `json:"llm,omitempty"`
	Env          *[]agentImageEnvUpdate `json:"env,omitempty"`
	MCP          *agentMCP              `json:"mcp,omitempty"`
	Skills       *[]string              `json:"skills,omitempty"`
	EnabledTools *[]string              `json:"enabledTools,omitempty"`
	// WorkingHours is the hub's wrapper convention: an absent key leaves the agent's
	// schedule alone, an empty object removes it. ClearWorkingHours on AgentUpdate is
	// what asks for that empty object.
	WorkingHours   *WorkingHours            `json:"workingHours,omitempty"`
	OllamaCloud    *agentOllamaCloudInfo    `json:"ollamaCloud,omitempty"`
	OpenCode       *agentOpenCodeInfo       `json:"openCode,omitempty"`
	AlibabaCloud   *agentAlibabaCloudInfo   `json:"alibabaCloud,omitempty"`
	OpenAI         *agentOpenAIInfo         `json:"openAI,omitempty"`
	Anthropic      *agentAnthropicInfo      `json:"anthropic,omitempty"`
	Google         *agentGoogleInfo         `json:"google,omitempty"`
	CustomProvider *agentCustomProviderInfo `json:"customProvider,omitempty"`
}
