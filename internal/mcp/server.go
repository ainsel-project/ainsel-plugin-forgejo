// Package mcp is the plugin's MCP endpoint: the tools the plugin offers to the
// agents that call it, over the MCP streamable-HTTP transport. internal/server
// mounts it at /mcp without a method pattern, so the transport's GET, POST and
// DELETE requests on that one path all reach it.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/api"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/hub"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/setup"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/work"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

// pluginName is the MCP server identity: the plugin id, spelled here rather than
// read from the manifest (only the version comes from there). It has to equal the
// manifest's id, which is also the prefix AInsel puts on tool names — rename the
// plugin, rename this.
const pluginName = "forgejo"

// NewHandler builds the plugin's MCP endpoint: its read-only work tools, the three
// operator-facing setup tools and the two operator-facing orphan-cleanup tools,
// over the streamable-HTTP transport, mounted
// as a plain http.Handler. StateLess mode keeps no session between requests,
// which is what the hub needs — it registers this server itself and re-syncs it
// by hitting /mcp directly every 30 s, from whichever replica the load balancer
// happens to reach.
//
// Every tool is registered for every caller, whatever kind they are: the hub
// lists a plugin's tools with its own background request and filters by audience
// itself, so a tool set that appeared only for some callers would look to the hub
// like a plugin with no tools at all. The setup tools' audience is "operator" in
// the manifest; what reaches an operator through AInsel is forgejo_setup_plan and
// the like, and what the plugin enforces at the call is the setup gate's own rule.
//
// version is the plugin's version, taken from the parsed manifest, so the
// endpoint reports the same version the manifest and the API do. w is the same
// read path the API routes serve — the tools ask it the same questions and answer
// with what the caller on the context may see. setup is the same setup service the
// wizard's routes serve from: the tools plan and apply an install through it, so
// both transports run one code path and refuse the same way.
func NewHandler(version string, w *api.WorkService, setupSvc *api.SetupService) http.Handler {
	s := mcpserver.NewMCPServer(pluginName, version, mcpserver.WithToolCapabilities(false))

	s.AddTool(listWorkTool(), listWorkHandler(w))
	s.AddTool(getWorkItemTool(), getWorkItemHandler(w))
	s.AddTool(setupOptionsTool(), setupOptionsHandler(setupSvc))
	s.AddTool(setupPlanTool(), setupPlanHandler(setupSvc))
	s.AddTool(setupApplyTool(), setupApplyHandler(setupSvc))
	s.AddTool(setupOrphansPlanTool(), setupOrphansPlanHandler(setupSvc))
	s.AddTool(setupOrphansRemoveTool(), setupOrphansRemoveHandler(setupSvc))

	return mcpserver.NewStreamableHTTPServer(s,
		mcpserver.WithStateLess(true),
		// The hub leaves its headers on the request it proxies, and the transport
		// builds a tool call's context from that request — so the caller's
		// credential and the invocation the call belongs to ride along to the
		// tool, where only a context is handed over.
		mcpserver.WithHTTPContextFunc(requestContext),
	)
}

// requestContext carries the hub's headers onto a tool call's context: the
// credential the hub minted for the caller behind the request, and the invocation
// the call belongs to. Reading the headers is all this does: a value that cannot be
// used leaves the context as it came, because a caller must not lose a tool call
// over a header the hub wrote. The credential is staged for the hub call the tool
// makes back; a tool call that arrives without one is refused by the tools
// themselves rather than answered from the plugin's own identity.
func requestContext(ctx context.Context, r *http.Request) context.Context {
	if caller, ok := hub.CallerFromRequest(r); ok {
		ctx = hub.WithCaller(ctx, caller)
	}
	if cred := hub.CredentialFromRequest(r); cred != "" {
		ctx = hub.WithCredential(ctx, cred)
	}
	// InvocationIDFromRequest has already matched the header against the shape the
	// hub sends, so a value that is really a blob never rides along to a log line
	// or a filename downstream.
	if id := hub.InvocationIDFromRequest(r); id != "" {
		ctx = hub.WithInvocationID(ctx, id)
	}
	return ctx
}

// -- the tools ---------------------------------------------------------------------

// listWorkTool is the tool an agent asks when it wants to know what AInsel has been
// working on. The description names no qualified tool name: an operator reaches this
// tool through AInsel as forgejo_list_work and an agent through the hub as plain
// list_work, and one description has to serve both.
func listWorkTool() mcp.Tool {
	return mcp.NewTool("list_work",
		mcp.WithDescription("List the work AInsel agents did on Forgejo: the issues and pull requests they ran on, each with its runs, cost, and page on the forge, newest activity first."),
		mcp.WithString("repo", mcp.Description(`Narrow to one repository, by full name ("owner/repo"). Omit it for every repository this plugin was installed on.`)),
		mcp.WithString("agent", mcp.Description("Narrow to one agent, by name. What is left is still whole threads, with the other agents' runs cut out of them.")),
		mcp.WithString("since", mcp.Description(`How far back to read: a whole number of days ("7d") or a moment in RFC 3339. Defaults to the last 30 days.`)),
		readOnlyHints(),
	)
}

// getWorkItemTool is the tool that reads one thread: what ran on it and what it cost.
func getWorkItemTool() mcp.Tool {
	return mcp.NewTool("get_work_item",
		mcp.WithDescription("Read one Forgejo issue or pull request: the runs that worked on it, what they cost, and its page on the forge."),
		mcp.WithString("repo", mcp.Required(), mcp.Description(`The repository's full name ("owner/repo").`)),
		// An integer rather than a string: a number typed as text is the common
		// mistake, and a schema that says integer lets a client catch it before
		// the call.
		mcp.WithInteger("number", mcp.Required(), mcp.Description("The issue or pull request number within the repository.")),
		readOnlyHints(),
	)
}

// readOnlyHints states the tool annotations rather than leaving them to the
// defaults, which a client reads as the worst case: an absent hint is destructive.
// Both tools read the hub's record of work and write nothing, and the same
// arguments give the same answer.
func readOnlyHints() mcp.ToolOption {
	return func(t *mcp.Tool) {
		mcp.WithReadOnlyHintAnnotation(true)(t)
		mcp.WithDestructiveHintAnnotation(false)(t)
		mcp.WithIdempotentHintAnnotation(true)(t)
		mcp.WithOpenWorldHintAnnotation(false)(t)
	}
}

// -- the setup tools -------------------------------------------------------------
//
// The five tools wrap the wizard's options, plan and apply — and the orphan
// sweep's plan and delete — over the same
// api.SetupService the console's routes serve from, and they carry the setup
// gate's own caller rule: an admin user with a credential, or the same refusal
// the route would answer with. Their audience is "operator" in the manifest, so
// the hub never merges them into an agent's tool set.

// setupOptionsTool is the tool an operator asks before planning: what can the
// install be filed under, and what may each role's tool checklist hold.
func setupOptionsTool() mcp.Tool {
	return mcp.NewTool("setup_options",
		mcp.WithDescription("List what the Forgejo setup can be built on: the AInsel groups an install can be filed under, the plugin groups, the configured Forgejo instances (name and address — the plan and apply tools accept only these), and each role's default and forbidden tools. Read it before the plan tool."),
		readOnlyHints(),
	)
}

// setupPlanTool previews an install. It writes nothing — the same preview the
// wizard's S6 shows, over the same Plan.
func setupPlanTool() mcp.Tool {
	opts := append([]mcp.ToolOption{
		mcp.WithDescription("Preview what the Forgejo setup would create for an organisation — Forgejo accounts and teams, AInsel agents, images, personas, a connector and triggers — without writing anything. The answer carries the verdict: a blocked plan is refused by the apply tool until it is fixed."),
	}, setupRequestArgs()...)
	opts = append(opts, readOnlyHints())
	return mcp.NewTool("setup_plan", opts...)
}

// setupApplyTool is the one setup tool that writes. Its description states what
// it creates and that the plan comes first, as the operator's guide to it.
func setupApplyTool() mcp.Tool {
	opts := append([]mcp.ToolOption{
		mcp.WithDescription("Create the Forgejo install for real: Forgejo accounts and teams and AInsel agents, images, personas, a connector and triggers. It rolls back on failure. Run setup_plan first — an apply whose plan is blocked is refused with the plan itself."),
	}, setupRequestArgs()...)
	opts = append(opts, destructiveHints())
	return mcp.NewTool("setup_apply", opts...)
}

// setupOrphansPlanTool previews the cleanup of an install's Forgejo leftovers.
// It writes nothing — the same plan the console's cleanup dialog shows before it
// offers the delete.
func setupOrphansPlanTool() mcp.Tool {
	opts := append([]mcp.ToolOption{
		mcp.WithDescription("List what a removed Forgejo install left behind in Forgejo — the bot accounts, the teams and the org webhook of a slug whose install row is gone — without deleting anything. A slug that still has a row is refused: it is an install, not an orphan. The names are the plugin's own convention for the slug, and anything another install's row still references is left out, so the list is exactly what setup_orphans_remove would delete. Run it first: the remove tool only makes sense after this plan has been read."),
	}, orphanRequestArgs()...)
	opts = append(opts, readOnlyHints())
	return mcp.NewTool("setup_orphans_plan", opts...)
}

// setupOrphansRemoveTool is the cleanup's second step. It is a separate tool from
// the plan on purpose: an operator cannot delete without the plan having been a
// visible, separate call.
func setupOrphansRemoveTool() mcp.Tool {
	opts := append([]mcp.ToolOption{
		mcp.WithDescription("Delete what setup_orphans_plan listed for a slug: the leftover bot accounts (purged, as the wizard's remove purges), teams and org webhook of a removed install. It re-derives the same convention-named set the plan showed and deletes only that, reading the install store again so a live install's accounts, teams and webhook are never touched. Run setup_orphans_plan first and read what it lists — a convention name can also be one a human chose by hand."),
	}, orphanRequestArgs()...)
	opts = append(opts, destructiveHints())
	return mcp.NewTool("setup_orphans_remove", opts...)
}

// orphanRequestArgs are the cleanup's typed arguments: the slug whose names to
// look for, the instance and org to look on (with no row there is nothing to
// resolve them from), and the admin token the sweep runs as — body-field style,
// never a URL (R4).
func orphanRequestArgs() []mcp.ToolOption {
	return []mcp.ToolOption{
		mcp.WithString("slug", mcp.Required(), mcp.Description("The slug whose leftovers to look for: the bot logins, team names and connector webhook of the install that was lost.")),
		mcp.WithString("baseUrl", mcp.Required(), mcp.Description("The Forgejo instance to clean up. It must be one of the configured instances (see setup_options) — an address outside that list is refused.")),
		mcp.WithString("org", mcp.Required(), mcp.Description("The Forgejo organisation the leftovers stand in.")),
		mcp.WithString("adminToken", mcp.Required(), mcp.Description("A Forgejo site-admin token: the sweep reads and deletes accounts, teams and webhooks. It is never repeated in an answer.")),
	}
}

// orphanRequestFromTool reads the cleanup arguments; all four are required, the
// way the route requires them in its body.
func orphanRequestFromTool(req mcp.CallToolRequest) (api.OrphanRequest, error) {
	var out api.OrphanRequest
	assign := func(name string, dst *string) error {
		v, err := req.RequireString(name)
		if err != nil {
			return err
		}
		*dst = v
		return nil
	}
	if err := assign("slug", &out.Slug); err != nil {
		return out, err
	}
	if err := assign("baseUrl", &out.BaseURL); err != nil {
		return out, err
	}
	if err := assign("org", &out.Org); err != nil {
		return out, err
	}
	if err := assign("adminToken", &out.AdminToken); err != nil {
		return out, err
	}
	return out, nil
}

// setupRequestArgs are the typed arguments plan and apply take: the request
// fields of the HTTP routes, field for field, so a caller types what the console
// sends and both answers are comparable. Free-form JSON is not offered: an
// argument that is not on the wizard's form is not the wizard's.
func setupRequestArgs() []mcp.ToolOption {
	return []mcp.ToolOption{
		mcp.WithString("slug", mcp.Required(), mcp.Description("Names the install and, through it, every resource it owns. Lowercase, and it cannot change afterwards.")),
		mcp.WithString("groupId", mcp.Required(), mcp.Description("The AInsel group every resource is filed under. The caller must be an owner or writer on it.")),
		mcp.WithString("baseUrl", mcp.Required(), mcp.Description("The Forgejo address the install points at, webhook and links included. It must be one of the instances configured on the plugin's Settings page (see setup_options) — an address outside that list is refused.")),
		mcp.WithString("org", mcp.Required(), mcp.Description("The Forgejo organisation the bots, the teams and the hook live in.")),
		// The token is an argument because the HTTP route takes it in the body: it is
		// the one secret the caller must type, and it never comes back out (R4).
		mcp.WithString("adminToken", mcp.Required(), mcp.Description("A Forgejo site-admin token, used to create the install's accounts, teams and webhook. It is never repeated in an answer.")),
		mcp.WithString("excludeLabel", mcp.Description("An issue carrying this label is left off the refiner. Empty means the shipped default.")),
		mcp.WithObject("roles", mcp.Description("Per-role choices, keyed refiner, developer and reviewer. Omit a role for the shipped defaults; an unknown role is refused."),
			mcp.Properties(map[string]any{
				"refiner":   roleInputSchema(),
				"developer": roleInputSchema(),
				"reviewer":  roleInputSchema(),
			}),
			mcp.AdditionalProperties(false),
		),
	}
}

// roleInputSchema is one role's choices as a JSON schema: the runtime image, the
// agent's display name, the role's tool set, and the hours it works — exactly the
// wizard's S4 form.
func roleInputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"runtime":   map[string]any{"type": "string", "description": "The role image's container image URL. Required for a new install."},
			"agentName": map[string]any{"type": "string", "description": "The agent's display name. Empty means the default, forgejo-<role>-<slug>."},
			"tools":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "The role's tool set. Omit it for the shipped defaults. A forbidden tool is refused by the plan."},
			"workingHours": map[string]any{
				"type":        "object",
				"description": "The hours this role's agent works. Omit it for any hour: on an edit that is also how a window the wizard wrote before is cleared, and it is what leaves a window set in the AInsel console untouched. An object of nothing at all is a different answer — every hour of every day.",
				"properties": map[string]any{
					"timezone": map[string]any{"type": "string", "description": "An IANA zone name such as Europe/Berlin. Empty means UTC."},
					"days":     map[string]any{"type": "array", "items": map[string]any{"type": "integer"}, "description": "Weekdays as Go numbers: 0 is Sunday, and 7 reads as Sunday too. Empty means every day."},
					"start":    map[string]any{"type": "string", "description": "When the window opens, HH:MM in the zone. Empty leaves the day open from its start."},
					"end":      map[string]any{"type": "string", "description": "When it closes, HH:MM in the zone. Earlier than start runs the window past midnight."},
				},
				"additionalProperties": false,
			},
			"patrol": map[string]any{
				"type":        "object",
				"description": "The developer's scheduled PR patrol: how often it runs, and whether it runs at all. The developer's card only — the plan ignores it on the other two. Omit it to say nothing about the patrol, which on an edit is what leaves a schedule or a prompt retuned on the AInsel console untouched. An object naming only an interval retunes the cadence and leaves the patrol as enabled as the install has it.",
				"properties": map[string]any{
					"enabled":  map[string]any{"type": "boolean", "description": "False switches the hub's row off rather than deleting it: it stays on the Schedules UI with its last and next run, and nothing here re-enables it. Omit it to leave the enablement where the install left it."},
					"interval": map[string]any{"type": "string", "description": "One of 1m, 2m, 3m, 4m, 5m, 6m, 10m, 12m, 15m, 20m, 30m, 1h, 2h, 3h, 4h, 6h, 8h, 12h. Anything else is refused by the plan: a step that divides neither 60 nor 24 is a cadence in name only."},
					"schedule": map[string]any{"type": "string", "description": "A cron expression, for a cadence the list above cannot offer. It wins over interval. The hub validates it; this plugin only checks it is shaped like one."},
				},
				"additionalProperties": false,
			},
		},
		"additionalProperties": false,
	}
}

// destructiveHints states apply's annotations rather than leaving them to the
// defaults: apply creates accounts and resources in two services and can take
// them away again, and a client should read it that way before calling it.
func destructiveHints() mcp.ToolOption {
	return func(t *mcp.Tool) {
		mcp.WithReadOnlyHintAnnotation(false)(t)
		mcp.WithDestructiveHintAnnotation(true)(t)
		mcp.WithIdempotentHintAnnotation(false)(t)
		mcp.WithOpenWorldHintAnnotation(true)(t)
	}
}

// setupCaller is the setup gate at the tool's door: a credential, and an admin
// user behind it — the same two checks the HTTP gate makes, in the same order,
// so an operator who would get a refusal from the console gets the same refusal
// from the tool. The service re-checks the caller on the way in; nothing about
// the setup is reachable by skipping this.
func setupCaller(ctx context.Context) (*hub.Caller, *mcp.CallToolResult) {
	if refused := requireCredential(ctx); refused != nil {
		return nil, refused
	}
	caller, ok := hub.CallerFromContext(ctx)
	if !ok || caller.Kind != "user" || !caller.IsAdmin {
		return nil, mcp.NewToolResultError("admin only")
	}
	return &caller, nil
}

// setupErrorText is the refusal a route would answer with, as tool text: the
// routes and the tools render a failure from one function, so "the same refusal
// as the HTTP route" is a fact rather than a hope.
func setupErrorText(err error) string {
	_, message, _ := api.RenderFailure(err)
	return message
}

// setupRequestFromTool reads the typed arguments into the request the service
// takes. The five required strings are refused by name when missing; roles is
// read next.
func setupRequestFromTool(req mcp.CallToolRequest) (api.SetupRequest, error) {
	var out api.SetupRequest
	assign := func(name string, dst *string) error {
		v, err := req.RequireString(name)
		if err != nil {
			return err
		}
		*dst = v
		return nil
	}
	if err := assign("slug", &out.Slug); err != nil {
		return out, err
	}
	if err := assign("groupId", &out.GroupID); err != nil {
		return out, err
	}
	if err := assign("baseUrl", &out.BaseURL); err != nil {
		return out, err
	}
	if err := assign("org", &out.Org); err != nil {
		return out, err
	}
	if err := assign("adminToken", &out.AdminToken); err != nil {
		return out, err
	}
	out.ExcludeLabel = strings.TrimSpace(req.GetString("excludeLabel", ""))

	roles, err := roleInputsFromTool(req)
	if err != nil {
		return out, err
	}
	out.Roles = roles
	return out, nil
}

// roleInputsFromTool reads the roles argument: an object keyed by the three
// roles, each the wizard's RoleInput. An omitted roles reads as nil, which the
// service reads as the shipped defaults — the console's default too. An unknown
// role key is refused rather than ignored: a caller that typed "devloper" has
// not configured the developer.
func roleInputsFromTool(req mcp.CallToolRequest) (map[setup.Role]setup.RoleInput, error) {
	raw, present := req.GetArguments()["roles"]
	if !present || raw == nil {
		return nil, nil
	}
	obj, ok := raw.(map[string]any)
	if !ok {
		return nil, errors.New(`argument "roles" must be an object keyed refiner, developer and reviewer`)
	}
	known := map[string]setup.Role{
		string(setup.RoleRefiner):   setup.RoleRefiner,
		string(setup.RoleDeveloper): setup.RoleDeveloper,
		string(setup.RoleReviewer):  setup.RoleReviewer,
	}
	out := make(map[setup.Role]setup.RoleInput, len(obj))
	for key, value := range obj {
		role, ok := known[key]
		if !ok {
			return nil, fmt.Errorf("unknown role %q: the roles are refiner, developer and reviewer", key)
		}
		fields, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("roles.%s must be an object", key)
		}
		var in setup.RoleInput
		if s, ok := fields["runtime"].(string); ok {
			in.Runtime = strings.TrimSpace(s)
		}
		if s, ok := fields["agentName"].(string); ok {
			in.AgentName = strings.TrimSpace(s)
		}
		switch tools := fields["tools"].(type) {
		case nil:
			// Omitted: the shipped defaults.
		case []any:
			for _, name := range tools {
				s, ok := name.(string)
				if !ok {
					return nil, fmt.Errorf("roles.%s.tools must be a list of tool names", key)
				}
				in.Tools = append(in.Tools, s)
			}
		default:
			return nil, fmt.Errorf("roles.%s.tools must be a list of tool names", key)
		}
		// The window is read as an object or not at all, and the two are different answers: absent
		// is "any hour", which on an edit is also the clear, while an empty object is a window over
		// every hour of every day. Nothing here decides whether either is a change — the plan
		// compares this against what the install row records, as it does for the console's form.
		switch hours := fields["workingHours"].(type) {
		case nil:
			// Omitted: no window.
		case map[string]any:
			w, err := workingHoursFromTool(hours, key)
			if err != nil {
				return nil, err
			}
			in.WorkingHours = w
		default:
			return nil, fmt.Errorf("roles.%s.workingHours must be an object", key)
		}
		// The patrol is read the same way: an object or nothing, and nothing is the answer
		// "say nothing about it" — which on an edit is what leaves a patrol retuned on the
		// console alone. Whether any of it is a change is the plan's to decide, against the
		// install row and not against the hub's live trigger.
		switch patrol := fields["patrol"].(type) {
		case nil:
			// Omitted: the patrol stays as the install has it.
		case map[string]any:
			p, err := patrolFromTool(patrol, key)
			if err != nil {
				return nil, err
			}
			in.Patrol = p
		default:
			return nil, fmt.Errorf("roles.%s.patrol must be an object", key)
		}
		out[role] = in
	}
	return out, nil
}

// patrolFromTool reads one install's patrol answer: the tick and the cadence. A field that is
// present but not the type it should be is refused here, where the caller can still fix the
// argument; a cadence the plugin does not offer is the plan's block to name, the same refusal
// the wizard's form gets. An absent enabled is left nil on purpose — it is the caller saying
// "keep the enablement", not saying "switch it off".
func patrolFromTool(fields map[string]any, role string) (*setup.PatrolInput, error) {
	out := &setup.PatrolInput{}
	if v, present := fields["enabled"]; present {
		b, ok := v.(bool)
		if !ok {
			return nil, fmt.Errorf("roles.%s.patrol.enabled must be true or false", role)
		}
		out.Enabled = &b
	}
	for _, cadence := range []struct {
		name string
		into *string
	}{{"interval", &out.Interval}, {"schedule", &out.Schedule}} {
		v, present := fields[cadence.name]
		if !present {
			continue
		}
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("roles.%s.patrol.%s must be a string", role, cadence.name)
		}
		*cadence.into = strings.TrimSpace(s)
	}
	return out, nil
}

// workingHoursFromTool reads one role's window: the zone, the days and the two bounds. A bound
// that is present but not a string is refused here, where the caller can still fix the argument;
// a bound that is a string the hub cannot use — "9am", a zone that does not exist — is the plan's
// block to name, the same refusal the console's form gets.
func workingHoursFromTool(fields map[string]any, role string) (*setup.WorkingHours, error) {
	out := &setup.WorkingHours{}
	if s, ok := fields["timezone"].(string); ok {
		out.Timezone = strings.TrimSpace(s)
	}
	for _, bound := range []struct {
		name string
		into *string
	}{{"start", &out.Start}, {"end", &out.End}} {
		v, present := fields[bound.name]
		if !present {
			continue
		}
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("roles.%s.%s must be an HH:MM time", role, bound.name)
		}
		*bound.into = strings.TrimSpace(s)
	}
	switch days := fields["days"].(type) {
	case nil:
		// Omitted: every day.
	case []any:
		for _, d := range days {
			n, ok := dayNumber(d)
			if !ok {
				return nil, fmt.Errorf("roles.%s.days must be a list of weekday numbers", role)
			}
			out.Days = append(out.Days, n)
		}
	default:
		return nil, fmt.Errorf("roles.%s.days must be a list of weekday numbers", role)
	}
	return out, nil
}

// dayNumber reads a weekday as the hub takes it. A JSON number arrives as a float64; a whole one
// is the day it names, and anything with a fraction is not a day. Whether the number is in range
// is the plan's to say, so the refusal reads the way the wizard's does.
func dayNumber(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		if n != float64(int(n)) {
			return 0, false
		}
		return int(n), true
	case int:
		return n, true
	}
	return 0, false
}

// setupOptionsHandler answers a tools/call for setup_options.
func setupOptionsHandler(svc *api.SetupService) mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		caller, refused := setupCaller(ctx)
		if refused != nil {
			return refused, nil
		}
		opts, err := svc.Options(ctx, caller)
		if err != nil {
			return mcp.NewToolResultError(setupErrorText(err)), nil
		}
		return jsonResult(opts)
	}
}

// setupPlanHandler answers a tools/call for setup_plan: the same preview the
// route writes with a 200, blocked verdict included.
func setupPlanHandler(svc *api.SetupService) mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		caller, refused := setupCaller(ctx)
		if refused != nil {
			return refused, nil
		}
		args, err := setupRequestFromTool(req)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		plan, err := svc.Plan(ctx, caller, args)
		if err != nil {
			return mcp.NewToolResultError(setupErrorText(err)), nil
		}
		return jsonResult(plan)
	}
}

// setupApplyHandler answers a tools/call for setup_apply. A blocked plan is
// refused with the plan JSON as the refusal — the same body the route answers the
// 409 with — because the block only reads against the thing it blocks.
func setupApplyHandler(svc *api.SetupService) mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		caller, refused := setupCaller(ctx)
		if refused != nil {
			return refused, nil
		}
		args, err := setupRequestFromTool(req)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		res, err := svc.Apply(ctx, caller, args)
		if err != nil {
			return mcp.NewToolResultError(setupErrorText(err)), nil
		}
		if res.Blocked != nil {
			body, err := json.Marshal(res.Blocked)
			if err != nil {
				return mcp.NewToolResultError("could not encode the answer"), nil
			}
			return mcp.NewToolResultError(string(body)), nil
		}
		// The same body the route writes with a 200.
		return jsonResult(struct {
			Install setup.Install     `json:"install"`
			Report  setup.ApplyReport `json:"report"`
		}{Install: res.Install, Report: res.Report})
	}
}

// setupOrphansPlanHandler answers a tools/call for setup_orphans_plan: the same
// plan the route writes with a 200.
func setupOrphansPlanHandler(svc *api.SetupService) mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		caller, refused := setupCaller(ctx)
		if refused != nil {
			return refused, nil
		}
		args, err := orphanRequestFromTool(req)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		plan, err := svc.PlanOrphans(ctx, caller, args)
		if err != nil {
			return mcp.NewToolResultError(setupErrorText(err)), nil
		}
		return jsonResult(plan)
	}
}

// setupOrphansRemoveHandler answers a tools/call for setup_orphans_remove: the
// same report the route writes with a 200.
func setupOrphansRemoveHandler(svc *api.SetupService) mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		caller, refused := setupCaller(ctx)
		if refused != nil {
			return refused, nil
		}
		args, err := orphanRequestFromTool(req)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		report, err := svc.DeleteOrphans(ctx, caller, args)
		if err != nil {
			return mcp.NewToolResultError(setupErrorText(err)), nil
		}
		return jsonResult(report)
	}
}

// listWorkHandler answers a tools/call for list_work.
func listWorkHandler(w *api.WorkService) mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if refused := requireCredential(ctx); refused != nil {
			return refused, nil
		}

		// The filters narrow the runs before work.Build groups them (the service's
		// doing), so what comes back is still threads rather than a run log with
		// holes in it.
		repo := strings.TrimSpace(req.GetString("repo", ""))
		agent := strings.TrimSpace(req.GetString("agent", ""))
		since, err := parseSince(req.GetString("since", ""))
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		groups, err := w.Work(ctx, api.Filter{Repo: repo, Agent: agent, Since: since})
		if err != nil {
			return mcp.NewToolResultError(toolError(err)), nil
		}

		// One flat list across the repositories, each item carrying its own
		// repository: the answer is read per thread, and a caller that asked for
		// everything gets the threads, not the grouping.
		items := make([]toolItem, 0, len(groups))
		for _, g := range groups {
			for _, it := range g.Items {
				items = append(items, compactItem(it))
			}
		}
		return jsonResult(map[string]any{"items": items})
	}
}

// getWorkItemHandler answers a tools/call for get_work_item: the one thread named by
// repository and number, with the runs that made it.
func getWorkItemHandler(w *api.WorkService) mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if refused := requireCredential(ctx); refused != nil {
			return refused, nil
		}

		// Both arguments are required, and each refusal names the one it is
		// missing: a caller that gave neither learns what to add.
		repo := strings.TrimSpace(req.GetString("repo", ""))
		if repo == "" {
			return mcp.NewToolResultError(`required argument "repo" is missing`), nil
		}
		number, err := req.RequireInt("number")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		item, err := w.Item(ctx, repo, number)
		if err != nil {
			return mcp.NewToolResultError(toolError(err)), nil
		}
		// Nothing was run under that number — or the plugin was never installed on
		// that repository. "no work here" is the honest answer, and an error is how
		// a caller learns to look somewhere else.
		if item == nil {
			return mcp.NewToolResultError("not found"), nil
		}
		return jsonResult(detailOf(*item))
	}
}

// -- the answers, in the shape the caller reads them ---------------------------------

// toolItem is one issue or pull request as list_work lists it: enough to point at
// the work and price it, with the pull requests of a thread nested under the issue
// they close.
type toolItem struct {
	Repo   string `json:"repo"`
	Number int    `json:"number"`
	Kind   string `json:"kind"`
	Title  string `json:"title"`
	URL    string `json:"url"`

	// CostUSD is null when any run of the item was never priced: an unknown cost
	// reads as unknown, not as a free week.
	CostUSD    *float64 `json:"costUsd"`
	DurationMs int64    `json:"durationMs"`
	Runs       int      `json:"runs"`

	LastActivity string     `json:"lastActivity"`
	PRs          []toolItem `json:"prs"`
}

// toolDetail is one thread as get_work_item reads it: the same identity as a
// toolItem, but with the runs themselves rather than a count of them.
type toolDetail struct {
	Repo   string `json:"repo"`
	Number int    `json:"number"`
	Kind   string `json:"kind"`
	Title  string `json:"title"`
	URL    string `json:"url"`

	CostUSD    *float64   `json:"costUsd"`
	DurationMs int64      `json:"durationMs"`
	Runs       []toolRun  `json:"runs"`
	PRs        []toolItem `json:"prs"`
}

// toolRun is one run of a thread: who ran it, in what role, how it ended, and what
// it cost — null when nobody measured.
type toolRun struct {
	ID     string  `json:"id"`
	Agent  string  `json:"agent"`
	Role   string  `json:"role"`
	Status string  `json:"status"`
	Start  string  `json:"start"`
	End    *string `json:"end"`

	CostUSD *float64 `json:"costUsd"`
}

// compactItem reduces a work item to the list shape, pulls and all: a thread's own
// runs counted, its finished time summed, its nested pull requests kept nested —
// the page a caller opens is per thread, and so is the work behind it.
func compactItem(it work.Item) toolItem {
	prs := make([]toolItem, 0, len(it.PRs))
	for _, pr := range it.PRs {
		prs = append(prs, compactItem(pr))
	}
	return toolItem{
		Repo:         it.Subject.Repo,
		Number:       it.Subject.Number,
		Kind:         string(it.Subject.Kind),
		Title:        it.Subject.Title,
		URL:          it.URL,
		CostUSD:      cost(it.CostUSD, it.CostKnown),
		DurationMs:   it.Duration.Milliseconds(),
		Runs:         len(it.Runs),
		LastActivity: formatTime(it.LastActivity),
		PRs:          prs,
	}
}

// detailOf expands a work item to the read-one shape: the runs spelled out one by
// one, the nested pull requests left compact.
func detailOf(it work.Item) toolDetail {
	runs := make([]toolRun, 0, len(it.Runs))
	for _, r := range it.Runs {
		runs = append(runs, toolRun{
			ID:      r.ID,
			Agent:   r.Agent,
			Role:    r.Role,
			Status:  r.Status,
			Start:   r.Start.Format(time.RFC3339),
			End:     formatTimePtr(r.End),
			CostUSD: cost(r.CostUSD, r.CostKnown),
		})
	}
	prs := make([]toolItem, 0, len(it.PRs))
	for _, pr := range it.PRs {
		prs = append(prs, compactItem(pr))
	}
	return toolDetail{
		Repo:       it.Subject.Repo,
		Number:     it.Subject.Number,
		Kind:       string(it.Subject.Kind),
		Title:      it.Subject.Title,
		URL:        it.URL,
		CostUSD:    cost(it.CostUSD, it.CostKnown),
		DurationMs: it.Duration.Milliseconds(),
		Runs:       runs,
		PRs:        prs,
	}
}

// cost hands back a measured cost as a pointer and an unmeasured one as nil, which
// marshals to a JSON null: the absence of a number is what says nobody priced this,
// where a 0 would say it was free.
func cost(usd float64, known bool) *float64 {
	if !known {
		return nil
	}
	return &usd
}

// formatTime writes a moment for a caller to read, and empty string for the zero
// time — a thread with no activity has no moment to name, and year 1 is not it.
func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

// formatTimePtr is formatTime for a run that may not have finished, whose end is
// already a pointer: nil stays nil, and a finished run's end is written out.
func formatTimePtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.Format(time.RFC3339)
	return &s
}

// -- what a tool says when it cannot answer -------------------------------------------

// requireCredential is ruling R21 at the tool's door: a call the hub forwarded with
// no credential is refused rather than answered from the plugin's own identity,
// which sees every group the chart grants it. nil means there is a credential and
// the tool may go on.
func requireCredential(ctx context.Context) *mcp.CallToolResult {
	if _, ok := hub.CredentialFromContext(ctx); ok {
		return nil
	}
	return mcp.NewToolResultError("missing credential")
}

// parseSince reads the list_work `since` argument the way the API reads its `since`
// parameter — the same grammar in both places, so one caller's week is never
// another caller's month: a whole number of days back ("7d"), a moment in
// RFC 3339, or nothing, which is the service's own default window.
//
// A value that is neither is refused with an error naming `since`, rather than
// read as the default: a caller that asks for six days and is handed a month has
// been told something it did not ask.
func parseSince(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, nil
	}
	if d, ok := daysBack(raw); ok {
		return time.Now().Add(-d), nil
	}
	at, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid since %q: give a window in days (\"7d\") or a moment in RFC 3339", raw)
	}
	return at, nil
}

// daysBack is the "<N>d" form: a whole number of days back from now. A negative
// count is not a window into the future, and a non-integer is not a window at all.
func daysBack(raw string) (time.Duration, bool) {
	digits, found := strings.CutSuffix(raw, "d")
	if !found {
		return 0, false
	}
	n, err := strconv.Atoi(digits)
	if err != nil || n < 0 {
		return 0, false
	}
	return time.Duration(n) * 24 * time.Hour, true
}

// toolError names what failed, in the one piece of wording a caller reads. It says
// which failure it is and nothing more — the hub's error text can name a resource
// the caller may not be able to see, and a tool result goes to whoever asked.
func toolError(err error) string {
	var se *hub.StatusError
	if errors.As(err, &se) && se.Code == http.StatusForbidden {
		// The caller's own credential was refused: this is theirs to act on.
		return "forbidden"
	}
	if errors.Is(err, api.ErrHubNotConfigured) {
		// Not the caller's fault and not the hub's: there is no hub to ask.
		return "hub not configured"
	}
	return "hub unavailable"
}

// jsonResult hands a tool's answer back as one text part holding JSON: the shape a
// calling agent parses, rather than prose it has to squint at.
func jsonResult(v any) (*mcp.CallToolResult, error) {
	body, err := json.Marshal(v)
	if err != nil {
		// Every value written here is marshable by construction; a failure is a
		// bug in this package, and says so as a tool error rather than a panic.
		return mcp.NewToolResultError("could not encode the answer"), nil
	}
	return mcp.NewToolResultText(string(body)), nil
}
