package api

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ainsel-project/ainsel-plugin-forgejo/content"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/forgejo"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/hub"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/settings"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/setup"
)

// SetupConfig holds settings the handler needs from the environment or chart.
type SetupConfig struct {
	ForgejoBaseURL string

	// HubBaseURL is the hub's own address (HUB_URL). The orphan sweep uses it to
	// recognise a leftover org webhook: the hook of an install points at the hub's
	// connector webhook space, and with the install row gone nothing else names it.
	HubBaseURL string
}

// storeOps is the interface for store operations needed by the setup API.
type storeOps interface {
	Save(context.Context, setup.Install) error
	List(context.Context) ([]setup.Install, error)
	Delete(context.Context, string) error
}

// settingsReader is what the setup routes need of the settings store: the
// effective values the wizard no longer collects, and the configured Forgejo
// instances the wizard picks from. Writing them belongs to the settings routes,
// not here.
type settingsReader interface {
	All(context.Context) ([]settings.Setting, error)
	ListInstances(context.Context) ([]settings.Instance, error)
}

// SetupRequest is what a plan or an apply is asked for: the per-install facts the
// wizard collects. The HTTP route decodes it from the request body and the MCP
// tool reads it from its typed arguments — the same fields, under the same names,
// so the two transports can be compared answer for answer.
//
// It carries secrets (AdminToken), which are pass-through: they reach the plan and
// the apply and never come back out (R4).
type SetupRequest struct {
	Slug         string                         `json:"slug"`
	GroupID      string                         `json:"groupId"`
	BaseURL      string                         `json:"baseUrl"`
	Org          string                         `json:"org"`
	AdminToken   string                         `json:"adminToken"`
	ExcludeLabel string                         `json:"excludeLabel"`
	Roles        map[setup.Role]setup.RoleInput `json:"roles"`
}

// SetupOptions is what the options call answers: the groups an install can be
// filed under, the plugin groups, the Forgejo address the console pre-fills, the
// configured Forgejo instances the Instance step selects from, and the tool
// checklist's rails. It is the shape the options route has always answered with,
// named so the MCP tool answers with it too.
type SetupOptions struct {
	Groups         []CallerGroup       `json:"groups"`
	PluginGroups   []string            `json:"pluginGroups"`
	ForgejoBaseURL string              `json:"forgejoBaseURL"`
	Instances      []settings.Instance `json:"instances"`
	Tools          SetupRoleTools      `json:"tools"`
}

// SetupRoleTools is what the tool checklist is drawn from: the set each role
// starts with, and the tools the R7 rails say that role may never have. The admin
// adds to the defaults and takes from them; the rails are not a suggestion
// (spec §3.3, §5).
type SetupRoleTools struct {
	Defaults  map[string][]string `json:"defaults"`
	Forbidden map[string][]string `json:"forbidden"`
}

// SetupApply is what an apply came to. Blocked is the plan when the apply refused
// to write anything: a non-nil Blocked is the whole answer — the HTTP route
// renders it as a 409 whose body is the plan, and the MCP tool refuses with the
// same JSON — and nothing was created (R2). On the writing path Blocked is nil
// and Install and Report say what was made.
type SetupApply struct {
	Blocked *setup.Result
	Install setup.Install
	Report  setup.ApplyReport
}

// SetupService is the wizard's three operator operations — options, plan, apply —
// behind one callable surface, so the HTTP routes and the MCP tools run the same
// code: the same caller checks, the same settings reading, the same Plan and the
// same Apply.Run. The routes became thin wrappers over it; their answers did not
// change.
//
// Every method takes the caller and enforces the setup gate's caller rule on it
// (an admin user, and for plan and apply a writer on the named group), so a
// transport cannot reach an operation by skipping the gate.
type SetupService struct {
	Hub      hub.Client
	Forgejo  *forgejo.Client
	Store    storeOps
	Settings settingsReader
	Config   SetupConfig
}

// NewSetupService builds the service the routes and the tools share. Its
// dependencies are exactly the ones NewSetup used to hold.
func NewSetupService(h hub.Client, fg *forgejo.Client, s storeOps, st settingsReader, cfg SetupConfig) *SetupService {
	return &SetupService{Hub: h, Forgejo: fg, Store: s, Settings: st, Config: cfg}
}

// setupCallerAllowed is the gate's caller rule as an error: the setup is for an
// admin user, on either transport. The HTTP gate answers it before the service is
// reached; the MCP tools leave it to the service, so both refuse with the same
// "admin only" the console reads.
func setupCallerAllowed(caller *hub.Caller) error {
	if caller == nil || caller.Kind != "user" || !caller.IsAdmin {
		return &answerError{status: http.StatusForbidden, message: "admin only"}
	}
	return nil
}

// Options answers what the admin can pick from: the groups to file the install
// under and the tool checklist's rails, and nothing else. The old pick lists —
// hub agents, personas, images, MCP servers — are gone: an install creates all of
// those itself, so a running instance's resources are not sources the wizard can
// pick from. Taken names are still read, but for collision detection only, when
// the plan is built.
func (svc *SetupService) Options(ctx context.Context, caller *hub.Caller) (SetupOptions, error) {
	if err := setupCallerAllowed(caller); err != nil {
		return SetupOptions{}, err
	}

	pluginGroups, err := svc.Hub.PluginGroups(ctx)
	if err != nil {
		return SetupOptions{}, err
	}

	// The configured instances: the wizard's Instance step selects over them.
	// A store that cannot answer is the same errSettings the plan path answers
	// with — an options answer with no list would render an empty select and
	// send the admin to Settings for a store failure.
	instances, err := svc.Settings.ListInstances(ctx)
	if err != nil {
		log.Printf("api: setup/options: settings.ListInstances: %v", err)
		return SetupOptions{}, errSettings
	}

	tools := SetupRoleTools{
		Defaults:  map[string][]string{},
		Forbidden: map[string][]string{},
	}
	for _, r := range []setup.Role{setup.RoleRefiner, setup.RoleDeveloper, setup.RoleReviewer} {
		tools.Defaults[string(r)] = content.DefaultTools(r)
		tools.Forbidden[string(r)] = content.ForbiddenTools(r)
	}

	return SetupOptions{
		Groups:         callerWritableGroups(caller),
		PluginGroups:   pluginGroups,
		ForgejoBaseURL: svc.Config.ForgejoBaseURL,
		Instances:      instances,
		Tools:          tools,
	}, nil
}

// toInput is the request as the setup package's Input, cleaned by the one rule every
// entry point shares (see setup.Normalize). The routes resolve the install, the
// configured-instance check and the Forgejo client off the result, so all three read
// the address the plan and the apply go on to store — a baseUrl typed with a stray
// space around it is not mistaken for an address nobody configured.
func (r SetupRequest) toInput() setup.Input {
	return setup.Normalize(setup.Input{
		Slug:         r.Slug,
		GroupID:      r.GroupID,
		BaseURL:      r.BaseURL,
		Org:          r.Org,
		AdminToken:   r.AdminToken,
		ExcludeLabel: r.ExcludeLabel,
		Roles:        r.Roles,
	})
}

// checkBaseURLConfigured refuses a baseUrl that is not one of the configured
// Forgejo instances — the check that turns "a typo produces an install that
// points at the wrong host" into a refused plan that names the address.
//
// An empty baseUrl is legal only on an edit, where it means "keep the install's
// own address", which is what plan.go's wanted-install rule depends on — and it
// is what the wizard submits for its "Keep <url>" state, the edit path's one way
// to leave an address that is not on the list alone. On a create (existing ==
// nil) a blank is refused whenever the list is non-empty: there is no row to
// keep, and letting it through would store an install with no address at all and
// silently drop it onto the FORGEJO_BASE_URL fallback — possibly a host the admin
// removed from the list. Empty stays legal when the list is empty, which is the
// env-only path a deployment starts on before any instance is configured.
// A non-empty address outside the list is refused here either way, so no plan can
// land an install on an address nobody configured: the admin adds the instance on
// Settings or moves the install onto a listed one.
func (svc *SetupService) checkBaseURLConfigured(ctx context.Context, baseURL string, existing *setup.Install) error {
	base := settings.NormalizeBaseURL(baseURL)
	instances, err := svc.Settings.ListInstances(ctx)
	if err != nil {
		log.Printf("api: setup: settings.ListInstances: %v", err)
		return errSettings
	}
	if base == "" {
		if existing == nil && len(instances) > 0 {
			return badRequest("baseUrl is required: pick a configured Forgejo instance on the plugin's Settings page or in the wizard")
		}
		return nil
	}
	for _, in := range instances {
		if settings.NormalizeBaseURL(in.BaseURL) == base {
			return nil
		}
	}
	if len(instances) == 0 {
		return badRequest(fmt.Sprintf("baseUrl %q is refused: no Forgejo instance is configured — add one on the plugin's Settings page", base))
	}
	return badRequest(fmt.Sprintf("baseUrl %q is not a configured Forgejo instance — configure it on the plugin's Settings page or pick a listed instance", base))
}

// forgejoClientFor resolves the Forgejo client a request works against: the
// address the request names, or the address the existing install carries when
// the request leaves baseUrl empty (an edit that keeps it), or — only while no
// instance list exists to say otherwise — the FORGEJO_BASE_URL the plugin was
// started with. Reads and writes of one install therefore go to that install's
// own Forgejo, not to whichever host the environment happened to name.
func (svc *SetupService) forgejoClientFor(baseURL string, existing *setup.Install) *forgejo.Client {
	base := settings.NormalizeBaseURL(baseURL)
	if base == "" && existing != nil {
		base = settings.NormalizeBaseURL(existing.BaseURL)
	}
	if base == "" {
		base = settings.NormalizeBaseURL(svc.Config.ForgejoBaseURL)
	}
	if base == settings.NormalizeBaseURL(svc.Config.ForgejoBaseURL) {
		// The env-var client is already built (and may carry an injected http
		// client a test set up); reuse it rather than building an equal one.
		return svc.Forgejo
	}
	return forgejo.New(base, nil)
}

// Plan previews what an install would do, and writes nothing (R2). A caller
// without write access to the named group is refused the way the route has always
// refused it — a bad request naming groupId.
func (svc *SetupService) Plan(ctx context.Context, caller *hub.Caller, req SetupRequest) (setup.Result, error) {
	if err := setupCallerAllowed(caller); err != nil {
		return setup.Result{}, err
	}
	if !callerCanWrite(caller, req.GroupID) {
		return setup.Result{}, badRequest("groupId")
	}

	in := req.toInput()

	existing, err := getInstallBySlug(ctx, svc.Store, in.Slug)
	if err != nil {
		log.Printf("api: setup/plan: getInstallBySlug: %v", err)
		return setup.Result{}, errStore
	}

	// A plan is only drawn against a configured instance: an address nobody
	// configured is refused here, before any Forgejo is contacted, rather than
	// previewing an install pointed at the wrong host.
	if err := svc.checkBaseURLConfigured(ctx, in.BaseURL, existing); err != nil {
		return setup.Result{}, err
	}

	// The effective role settings: what the install's agents are created on
	// and what its forge MCP server is pointed at.
	vals, err := wizardSettings(ctx, svc.Settings)
	if err != nil {
		return setup.Result{}, err
	}
	applyWizardKeys(vals, in)

	snap, err := buildSnapshot(ctx, svc.Hub, svc.forgejoClientFor(in.BaseURL, existing), svc.Store, caller, in.AdminToken, in.Org, in.Slug, existing)
	if err != nil {
		return setup.Result{}, err
	}
	snap.Settings = vals

	return setup.Plan(in, snap), nil
}

// Apply writes what the plan previews, in dependency order, rolling back on
// failure. It re-plans first and refuses a blocked plan without writing: the
// answer is the plan itself, the way the route has always answered a blocked
// apply.
func (svc *SetupService) Apply(ctx context.Context, caller *hub.Caller, req SetupRequest) (SetupApply, error) {
	if err := setupCallerAllowed(caller); err != nil {
		return SetupApply{}, err
	}
	if !callerCanWrite(caller, req.GroupID) {
		return SetupApply{}, badRequest("groupId")
	}

	in := req.toInput()

	existing, err := getInstallBySlug(ctx, svc.Store, in.Slug)
	if err != nil {
		log.Printf("api: setup/apply: getInstallBySlug: %v", err)
		return SetupApply{}, errStore
	}

	// The same refusal the plan route draws: an apply cannot land an install
	// on an address that is not a configured instance.
	if err := svc.checkBaseURLConfigured(ctx, in.BaseURL, existing); err != nil {
		return SetupApply{}, err
	}

	vals, err := wizardSettings(ctx, svc.Settings)
	if err != nil {
		return SetupApply{}, err
	}
	applyWizardKeys(vals, in)

	fg := svc.forgejoClientFor(in.BaseURL, existing)
	snap, err := buildSnapshot(ctx, svc.Hub, fg, svc.Store, caller, in.AdminToken, in.Org, in.Slug, existing)
	if err != nil {
		return SetupApply{}, err
	}
	snap.Settings = vals

	// Re-plan first.
	plan := setup.Plan(in, snap)

	// A blocked apply answers with the plan itself — the same shape the preview
	// returns, verdict included. The admin has just been shown what the install is
	// about to create; when one of those turns out to be blocked, the answer that lets
	// the wizard show the block against the thing it blocks is the plan, not a list of
	// messages on its own. Nothing is written on the way to either answer (R2).
	if len(plan.Verdict.Blocking) > 0 {
		return SetupApply{Blocked: &plan}, nil
	}

	applyObj := setup.Apply{
		Hub:          &hubAdapter{svc.Hub},
		Forgejo:      &forgejoAdapter{fg, fg.Admin(in.AdminToken), in.Org},
		Now:          time.Now,
		Rand:         rand.Reader,
		PersonaFor:   personaForFunc,
		DefaultTools: content.DefaultTools,
		Settings:     vals,
	}

	// The wizard is the sole author of what it creates: every create carries this
	// plugin's manager stamp, and the hub lets only the named manager write a stamped
	// resource. So every write goes out as the plugin itself — the caller's credential
	// is dropped (the stamp and the named-manager check are derived from the
	// authenticated caller, so a write made as the console admin would either be
	// silently unstamped or 409 against a stamped resource), while the caller's
	// deadline and cancellation are kept. The reads above — buildSnapshot, the store —
	// stay on the caller's credential, so console visibility rules still apply to
	// what a preview shows.
	writeCtx := hub.WithBackground(hub.WithoutCredential(ctx))

	install, report, err := applyObj.Run(writeCtx, in, plan, existing)
	// Ensure report slices are never nil, on both answers.
	if report.Created == nil {
		report.Created = []setup.Op{}
	}
	if report.RolledBack == nil {
		report.RolledBack = []setup.Op{}
	}
	if report.Leftovers == nil {
		report.Leftovers = []setup.Op{}
	}
	if report.Notes == nil {
		report.Notes = []string{}
	}
	if err != nil {
		log.Printf("api: setup/apply: Apply.Run: %v", err)
		// Extract the service and step from the error message
		errMsg := extractServiceError(err)
		return SetupApply{}, &ApplyError{message: errMsg, report: report}
	}

	if err := svc.Store.Save(ctx, install); err != nil {
		log.Printf("api: setup/apply: store.Save: %v", err)
		return SetupApply{}, errStore
	}

	return SetupApply{Install: install, Report: report}, nil
}

// -- the orphan sweep ---------------------------------------------------------------
//
// An install whose row is lost — a remove that ran without the admin token and
// dropped what Forgejo kept, or the fresh-start cutover — leaves its bot users,
// its teams and its org webhook in Forgejo with nothing in the plugin naming
// them. The sweep finds them again the only way left: the plugin's own naming
// convention (the same helpers the plan names resources with), plus the one hook
// tell that survives a lost row — a URL pointing at the hub. Because a
// convention can also match a resource a human made by hand, the sweep is two
// steps: a plan that lists every candidate, and a delete that re-derives the
// same set and removes it. Nothing is ever deleted that the plan could not show.

// OrphanRequest names the leftovers to sweep: the slug whose names to look for,
// the Forgejo instance to look on and its org — with no row there is nothing to
// resolve either from, so the request carries both, and the instance is checked
// against the configured list the way a plan's is (S1). The admin token is the
// request's only secret: it goes in the body, never a URL (R4), and never comes
// back out.
type OrphanRequest struct {
	Slug       string `json:"slug"`
	BaseURL    string `json:"baseUrl"`
	Org        string `json:"org"`
	AdminToken string `json:"adminToken"`
}

// OrphanItem is one Forgejo resource the sweep would delete, named so an admin
// can recognise it before anything is removed: the kind, the name Forgejo shows,
// the id or login a delete needs, and a line saying what deleting it takes with
// it. The detail never carries the admin token or any other secret.
type OrphanItem struct {
	Kind   string `json:"kind"` // "user", "team" or "hook"
	Name   string `json:"name"`
	ID     string `json:"id"`
	Detail string `json:"detail,omitempty"`
}

// OrphanPlan is the answer to the plan step: what was found, and where it was
// looked. Delete re-derives this same set server-side — the names are
// convention-fixed, so the plan is a rendering of what delete will do, not a
// ticket that scopes it.
type OrphanPlan struct {
	Slug    string       `json:"slug"`
	BaseURL string       `json:"baseUrl"`
	Org     string       `json:"org"`
	Items   []OrphanItem `json:"items"`
}

// OrphanDeleteReport says what the sweep removed and what it could not. A
// failure here is not kept anywhere — there is no row to keep — so the answer
// names it plainly; the admin runs the sweep again to try the rest.
type OrphanDeleteReport struct {
	Deleted []string `json:"deleted"`
	Failed  []string `json:"failed"`
}

// checkOrphanRequest is what both sweep steps refuse before asking Forgejo
// anything: a slug to derive names from, an org to look in, and the admin token
// the sweep cannot run without — plan included, because reading the candidates
// is already admin work. The instance is checked the way plan and apply check
// theirs: an address outside the configured list is refused (S1).
func (svc *SetupService) checkOrphanRequest(ctx context.Context, req *OrphanRequest) error {
	if req == nil {
		return badRequest("slug is required: the leftovers are named from it")
	}
	// Normalized once, here, so the names the sweep derives, the names the plan
	// echoes back and the org a hook delete is addressed to are all the same slug
	// and org — not one trimmed and one not.
	req.Slug = strings.TrimSpace(req.Slug)
	req.Org = strings.TrimSpace(req.Org)
	if req.Slug == "" {
		return badRequest("slug is required: the leftovers are named from it")
	}
	if req.Org == "" {
		return badRequest("org is required")
	}
	if req.AdminToken == "" {
		return badRequest("adminToken is required: the sweep reads and deletes Forgejo resources as a site administrator")
	}
	return svc.checkBaseURLConfigured(ctx, req.BaseURL, nil)
}

// orphanLiveRefs is what the install store still claims on one Forgejo instance: the
// bot logins, team ids and hook ids a row references. The sweep consults it before
// it names a single candidate, because a resource a row still points at belongs to
// an install that is still installed — the sweep's whole premise is that the row is
// gone, and that is checkable.
//
// The refs are read per instance (the address in its comparison form), because a
// login, a team and a hook are Forgejo objects and only mean something on the
// instance they live on. Within an instance a login is protected everywhere —
// accounts are global to the Forgejo — while a team id and a hook id are protected
// in the org that holds them.
type orphanLiveRefs struct {
	base    string
	org     string
	logins  map[string]string         // bot login -> the slug whose row references it
	teamIDs map[int64]orphanLiveOwner // team id -> the row that references it
	hookIDs map[int64]orphanLiveOwner // hook id -> the row that references it
}

type orphanLiveOwner struct {
	slug string
	org  string
}

// orphanLiveReads the store for the sweep. It refuses the whole sweep when the slug
// is a live install on that instance and org — a typo or a misremembered slug is
// exactly the case that would otherwise purge a working install's accounts — and
// otherwise returns what every other row on that instance still references.
func (svc *SetupService) orphanLiveRefs(ctx context.Context, req OrphanRequest) (*orphanLiveRefs, error) {
	installs, err := svc.Store.List(ctx)
	if err != nil {
		return nil, err
	}
	base := settings.NormalizeBaseURL(req.BaseURL)
	refs := &orphanLiveRefs{
		base:    base,
		org:     req.Org,
		logins:  map[string]string{},
		teamIDs: map[int64]orphanLiveOwner{},
		hookIDs: map[int64]orphanLiveOwner{},
	}
	for _, in := range installs {
		sameInstance := settings.NormalizeBaseURL(in.BaseURL) == base
		sameOrg := strings.EqualFold(strings.TrimSpace(in.Org), req.Org)
		if sameInstance && sameOrg && strings.EqualFold(strings.TrimSpace(in.Slug), req.Slug) {
			// Not an orphan: the row is there. Refuse before Forgejo is asked for
			// anything — a mistyped slug must not purge a working install.
			return nil, badRequest(in.Slug + " is still installed: the sweep is for a slug whose row is gone — remove the install instead")
		}
		if !sameInstance {
			continue
		}
		owner := orphanLiveOwner{slug: in.Slug, org: strings.TrimSpace(in.Org)}
		for _, st := range in.Roles {
			if st.BotLogin != "" {
				refs.logins[st.BotLogin] = in.Slug
			}
			if st.TeamID != 0 {
				refs.teamIDs[st.TeamID] = owner
			}
		}
		if in.HookID != 0 {
			refs.hookIDs[in.HookID] = owner
		}
	}
	return refs, nil
}

// skip names the reason a candidate is left alone, or "" when nothing protects it.
func (r *orphanLiveRefs) skip(kind, login string, id int64) string {
	switch kind {
	case "user":
		if slug, ok := r.logins[login]; ok {
			return "still referenced by install " + slug
		}
	case "team":
		if owner, ok := r.teamIDs[id]; ok && strings.EqualFold(owner.org, r.org) {
			return "still referenced by install " + owner.slug
		}
	case "hook":
		if owner, ok := r.hookIDs[id]; ok && strings.EqualFold(owner.org, r.org) {
			return "still referenced by install " + owner.slug
		}
	}
	return ""
}

// orphanAdmin resolves the instance and checks the token is what it claims to
// be: a site administrator on that Forgejo. A token that is not gets a refusal
// naming it, rather than a 403 from whichever call happens to touch an admin
// route first.
func (svc *SetupService) orphanAdmin(ctx context.Context, req OrphanRequest) (*forgejo.Admin, error) {
	admin := svc.forgejoClientFor(req.BaseURL, nil).Admin(req.AdminToken)
	_, isAdmin, err := admin.WhoAmI(ctx)
	if err != nil {
		return nil, err
	}
	if !isAdmin {
		return nil, badRequest("the admin token is not a site administrator on that Forgejo")
	}
	return admin, nil
}

// orphanItems derives the candidates for a slug: the three bot logins, the three
// team names (with the ids a delete needs) and the org hooks whose URL points at
// the hub. Every name comes from the same helpers the wizard creates with — the
// sweep looks for exactly what an install would have made, and nothing else.
func (svc *SetupService) orphanItems(ctx context.Context, admin *forgejo.Admin, org, slug string, live *orphanLiveRefs) ([]OrphanItem, error) {
	items := []OrphanItem{}

	for _, role := range []setup.Role{setup.RoleRefiner, setup.RoleDeveloper, setup.RoleReviewer} {
		login := setup.BotLogin(role, slug)
		exists, err := admin.UserExists(ctx, login)
		if err != nil {
			return nil, err
		}
		if exists {
			if why := live.skip("user", login, 0); why != "" {
				log.Printf("api: setup/orphans: keeping user %s: %s", login, why)
			} else {
				items = append(items, OrphanItem{
					Kind: "user", Name: login, ID: login,
					Detail: "deleting the account purges the content it owns, as the wizard's remove does",
				})
			}
		}

		name := setup.TeamName(role, slug)
		id, found, err := admin.FindTeam(ctx, org, name)
		if err != nil {
			return nil, err
		}
		if found {
			if why := live.skip("team", "", id); why != "" {
				log.Printf("api: setup/orphans: keeping team %s: %s", name, why)
			} else {
				items = append(items, OrphanItem{
					Kind: "team", Name: name, ID: strconv.FormatInt(id, 10),
					Detail: "deleting the team removes its memberships and repo access, as the wizard's remove does",
				})
			}
		}
	}

	// The hook of a lost install: the connector it delivered to is gone, so the
	// exact endpoint the create returned cannot be reconstructed. What survives is
	// the hub's own address — the plugin always knows it (it calls the hub REST API
	// there) — so "a hook under the hub's address" is the match. It can catch a
	// still-live install's hook too: that one is plugin-made as well, and the plan
	// shows every candidate with its URL for the admin to confirm before anything
	// goes.
	hooks, err := admin.ListOrgHooks(ctx, org)
	if err != nil {
		return nil, err
	}
	for _, hook := range hooks {
		if !svc.hookPointsAtHub(hook.URL) {
			continue
		}
		if why := live.skip("hook", "", hook.ID); why != "" {
			log.Printf("api: setup/orphans: keeping hook %d (%s): %s", hook.ID, hook.URL, why)
			continue
		}
		items = append(items, OrphanItem{
			Kind: "hook", Name: hook.URL, ID: strconv.FormatInt(hook.ID, 10),
			Detail: "a webhook still delivering to the hub",
		})
	}

	return items, nil
}

// hookPointsAtHub reports whether a webhook URL points at the hub — the one tell
// that a leftover org hook carries with it. Without the hub's address configured
// there is nothing to match against, and no hook is claimed: a sweep that
// guessed would be a sweep that deleted the wrong webhook.
func (svc *SetupService) hookPointsAtHub(hookURL string) bool {
	base := strings.TrimRight(strings.TrimSpace(svc.Config.HubBaseURL), "/")
	if base == "" {
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(hookURL), base+"/")
}

// PlanOrphans lists what a slug left in Forgejo and deletes nothing. It is the
// step that makes the convention match safe: whatever it shows is exactly what
// DeleteOrphans will remove — and nothing a row still points at is ever in it, so
// the plan can tell a live install's resources from an orphan's.
func (svc *SetupService) PlanOrphans(ctx context.Context, caller *hub.Caller, req OrphanRequest) (OrphanPlan, error) {
	if err := setupCallerAllowed(caller); err != nil {
		return OrphanPlan{}, err
	}
	if err := svc.checkOrphanRequest(ctx, &req); err != nil {
		return OrphanPlan{}, err
	}
	live, err := svc.orphanLiveRefs(ctx, req)
	if err != nil {
		return OrphanPlan{}, err
	}
	admin, err := svc.orphanAdmin(ctx, req)
	if err != nil {
		return OrphanPlan{}, err
	}
	items, err := svc.orphanItems(ctx, admin, req.Org, req.Slug, live)
	if err != nil {
		return OrphanPlan{}, err
	}
	return OrphanPlan{
		Slug:    req.Slug,
		BaseURL: settings.NormalizeBaseURL(req.BaseURL),
		Org:     req.Org,
		Items:   items,
	}, nil
}

// DeleteOrphans removes what the plan step shows. It re-derives the candidate
// set itself rather than trusting a caller to send the plan back: the names are
// convention-fixed, so the set can only ever be this slug's own, and a caller
// cannot widen the blast radius by editing a list. Deletes are 404-tolerant, so
// running the sweep twice converges: the second pass finds what the first one's
// failures left, and calls what has since gone deleted.
func (svc *SetupService) DeleteOrphans(ctx context.Context, caller *hub.Caller, req OrphanRequest) (OrphanDeleteReport, error) {
	report := OrphanDeleteReport{Deleted: []string{}, Failed: []string{}}
	if err := setupCallerAllowed(caller); err != nil {
		return report, err
	}
	if err := svc.checkOrphanRequest(ctx, &req); err != nil {
		return report, err
	}
	live, err := svc.orphanLiveRefs(ctx, req)
	if err != nil {
		return report, err
	}
	admin, err := svc.orphanAdmin(ctx, req)
	if err != nil {
		return report, err
	}
	items, err := svc.orphanItems(ctx, admin, req.Org, req.Slug, live)
	if err != nil {
		return report, err
	}

	for _, item := range items {
		var delErr error
		switch item.Kind {
		case "user":
			delErr = admin.DeleteUser(ctx, item.Name)
		case "team":
			id, perr := strconv.ParseInt(item.ID, 10, 64)
			if perr != nil {
				delErr = perr
			} else {
				delErr = admin.DeleteTeam(ctx, id)
			}
		case "hook":
			id, perr := strconv.ParseInt(item.ID, 10, 64)
			if perr != nil {
				delErr = perr
			} else {
				delErr = admin.DeleteOrgHook(ctx, req.Org, id)
			}
		}
		if delErr != nil {
			log.Printf("api: setup/orphans: delete %s:%s: %v", item.Kind, item.Name, delErr)
			report.Failed = append(report.Failed, item.Kind+":"+item.Name)
			continue
		}
		log.Printf("api: setup/orphans: deleted %s:%s (slug %s, org %s, %s)", item.Kind, item.Name, req.Slug, req.Org, live.base)
		report.Deleted = append(report.Deleted, item.Kind+":"+item.Name)
	}
	log.Printf("api: setup/orphans: sweep of slug %s on %s org %s deleted %d (users %d, teams %d, hooks %d), failed %d",
		req.Slug, live.base, req.Org, len(report.Deleted),
		countKind(report.Deleted, "user"), countKind(report.Deleted, "team"), countKind(report.Deleted, "hook"),
		len(report.Failed))
	return report, nil
}

// countKind counts the "kind:name" entries of one kind in a delete report.
func countKind(entries []string, kind string) int {
	n := 0
	for _, e := range entries {
		if strings.HasPrefix(e, kind+":") {
			n++
		}
	}
	return n
}

// NewSetup serves the setup wizard routes: list installs, show options, plan and apply,
// deliver webhook, repair connection, and delete.
//
// The options route lists no pickable sources any more: a created-only install
// creates its own agents, images, personas and MCP server, so there is nothing to
// offer to pick. What the admin used to type for those — the role llms and the
// forge MCP wiring — is read from the settings store on every plan and apply.
//
// The options, plan and apply routes are thin: they decode, gate and render; the
// answers come from the same SetupService the MCP tools call.
func NewSetup(h hub.Client, fg *forgejo.Client, s storeOps, st settingsReader, cfg SetupConfig) http.Handler {
	return NewSetupService(h, fg, s, st, cfg).Handler()
}

// Handler serves the routes over the service. The aliases keep the route bodies
// reading the dependencies by the short names they have always used.
func (svc *SetupService) Handler() http.Handler {
	h := svc.Hub
	s := svc.Store
	mux := http.NewServeMux()

	// GET /api/setup/installs — list installs with connection status.
	// A retiring row is not an install: its resources are mid-teardown and the
	// wizard has nothing to offer for it. The filter lives here, on the route, and
	// not in store.List — the cutover, the plan's collision check and the remove
	// route all read the unfiltered list, and each of them still needs to see a row
	// that is being cleaned up.
	mux.HandleFunc("GET /api/setup/installs", func(w http.ResponseWriter, r *http.Request) {
		gate(w, r, h, func(ctx context.Context, caller *hub.Caller) error {
			installs, err := s.List(ctx)
			if err != nil {
				log.Printf("api: setup/installs: store.List: %v", err)
				return errStore
			}
			visible := []setup.Install{}
			for _, in := range installs {
				if in.Retiring {
					continue
				}
				visible = append(visible, in)
			}
			writeJSON(w, http.StatusOK, struct {
				Installs []setup.Install `json:"installs"`
			}{Installs: visible})
			return nil
		})
	})

	// GET /api/setup/options — show what the admin can pick from: the groups to
	// file the install under and the tool checklist's rails, and nothing else.
	// The old pick lists — hub agents, personas, images, MCP servers — are gone:
	// an install creates all of those itself, so a running instance's resources
	// are not sources the wizard can pick from. Taken names are still read, but
	// for collision detection only, when the plan is built.
	mux.HandleFunc("GET /api/setup/options", func(w http.ResponseWriter, r *http.Request) {
		gate(w, r, h, func(ctx context.Context, caller *hub.Caller) error {
			opts, err := svc.Options(ctx, caller)
			if err != nil {
				return err
			}
			writeJSON(w, http.StatusOK, opts)
			return nil
		})
	})

	// POST /api/setup/plan — preview what would be created. The request carries
	// only the per-install facts; a body that still sends the old picked ids or
	// typed llm fields decodes past them — unknown JSON fields are ignored, so
	// an old console cannot fail a plan, it just stops being able to set what
	// the settings store owns now.
	mux.HandleFunc("POST /api/setup/plan", func(w http.ResponseWriter, r *http.Request) {
		gate(w, r, h, func(ctx context.Context, caller *hub.Caller) error {
			var req SetupRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				return badRequest("invalid json")
			}

			result, err := svc.Plan(ctx, caller, req)
			if err != nil {
				return err
			}

			writeJSON(w, http.StatusOK, result)
			return nil
		})
	})

	// POST /api/setup/apply — apply the plan. Like the plan, the request names
	// only the per-install facts; the settings supply everything else.
	mux.HandleFunc("POST /api/setup/apply", func(w http.ResponseWriter, r *http.Request) {
		gate(w, r, h, func(ctx context.Context, caller *hub.Caller) error {
			var req SetupRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				return badRequest("invalid json")
			}

			res, err := svc.Apply(ctx, caller, req)
			if err != nil {
				return err
			}

			if res.Blocked != nil {
				writeJSON(w, http.StatusConflict, res.Blocked)
				return nil
			}

			writeJSON(w, http.StatusOK, struct {
				Install setup.Install     `json:"install"`
				Report  setup.ApplyReport `json:"report"`
			}{
				Install: res.Install,
				Report:  res.Report,
			})
			return nil
		})
	})

	// GET /api/setup/installs/{slug}/deliveries?since= — check webhook delivery
	mux.HandleFunc("GET /api/setup/installs/{slug}/deliveries", func(w http.ResponseWriter, r *http.Request) {
		gate(w, r, h, func(ctx context.Context, caller *hub.Caller) error {
			slug := r.PathValue("slug")
			sinceStr := r.URL.Query().Get("since")

			// Get install by slug
			inst, err := getInstallBySlug(ctx, s, slug)
			if err != nil {
				log.Printf("api: setup deliveries: getInstallBySlug: %v", err)
				return errStore
			}
			if inst == nil {
				return notFound("install not found")
			}

			// Parse since timestamp
			var since time.Time
			if sinceStr != "" {
				if t, err := time.Parse(time.RFC3339Nano, sinceStr); err == nil {
					since = t
				}
			}

			// Get events from hub
			events, err := h.Events(ctx, inst.ConnectorID, since)
			if err != nil {
				return err
			}

			// Mark webhook verified on first event
			verified := inst.WebhookVerifiedAt != nil
			if len(events) > 0 && !verified {
				now := time.Now()
				inst.WebhookVerifiedAt = &now
				if err := s.Save(ctx, *inst); err != nil {
					log.Printf("api: setup deliveries: store.Save: %v", err)
					return errStore
				}
				verified = true
			}

			writeJSON(w, http.StatusOK, struct {
				Verified bool       `json:"verified"`
				At       *time.Time `json:"at,omitempty"`
			}{
				Verified: verified,
				At:       inst.WebhookVerifiedAt,
			})
			return nil
		})
	})

	// POST /api/setup/installs/{slug}/repair-connection — rotate webhook secret
	mux.HandleFunc("POST /api/setup/installs/{slug}/repair-connection", func(w http.ResponseWriter, r *http.Request) {
		gate(w, r, h, func(ctx context.Context, caller *hub.Caller) error {
			slug := r.PathValue("slug")

			var req struct {
				AdminToken string `json:"adminToken"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				return badRequest("invalid json")
			}

			// Get install by slug
			inst, err := getInstallBySlug(ctx, s, slug)
			if err != nil {
				log.Printf("api: setup repair: getInstallBySlug: %v", err)
				return errStore
			}
			if inst == nil {
				return notFound("install not found")
			}

			// Rotate connector secret in hub. The connector is stamped as the plugin's,
			// so the rotate goes as the plugin — the named manager is the only caller
			// the hub lets write a stamped resource (the caller's deadline is kept).
			conn, err := h.RotateConnectorSecret(hub.WithBackground(hub.WithoutCredential(ctx)), inst.ConnectorID)
			if err != nil {
				return err
			}

			// Update org hook with new secret. The hook lives on the install's own
			// Forgejo, so the rotate reaches that address, not the env var's.
			admin := svc.forgejoClientFor("", inst).Admin(req.AdminToken)
			hook := forgejo.Hook{
				URL:    conn.WebhookEndpoint,
				Secret: conn.WebhookSecretValue,
				Events: setup.HookEvents(),
			}
			if err := admin.UpdateOrgHook(ctx, inst.Org, inst.HookID, hook); err != nil {
				return err
			}

			writeJSON(w, http.StatusOK, struct{}{})
			return nil
		})
	})

	// POST /api/setup/installs/{slug}/remove — remove install.
	//
	// POST rather than DELETE because the removal takes the admin token, and the only
	// client that calls it — the console's plugin HttpClient — sends a body with POST,
	// PUT and PATCH. A DELETE route that quietly ignores the token is worse than a POST
	// that reads it: the removal would then always leave the bots and the team behind and
	// call that a success. Same shape as repair-connection above, which also takes its
	// secret in the body (R4: never in a URL, where access logs keep it).
	mux.HandleFunc("POST /api/setup/installs/{slug}/remove", func(w http.ResponseWriter, r *http.Request) {
		gate(w, r, h, func(ctx context.Context, caller *hub.Caller) error {
			slug := r.PathValue("slug")

			var req struct {
				AdminToken string `json:"adminToken"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				// Token is optional
			}

			// Get install by slug
			inst, err := getInstallBySlug(ctx, s, slug)
			if err != nil {
				log.Printf("api: setup delete: getInstallBySlug: %v", err)
				return errStore
			}
			if inst == nil {
				return notFound("install not found")
			}

			deleted := []string{}
			checklist := []string{}

			// Everything the hub kept of this install is the plugin's own — the
			// stamped resources answer every non-manager write with 409 — so the whole
			// removal goes out as the plugin. The caller's deadline is kept; the store
			// and the Forgejo side are untouched by this.
			writeCtx := hub.WithBackground(hub.WithoutCredential(ctx))

			// A hub-side failure keeps the row. The row is the only record of which
			// resources belong to this install (R5): drop it and whatever survived has no
			// owner left to name it, and no later removal can find it — the checklist
			// cannot help, because nothing recorded what to look for. A Forgejo leftover
			// is the same problem wearing another address: the row names the hook, the
			// teams and the bots just as exactly as it names the hub resources, so keeping
			// it is just as good a way back — and dropping it is just as good a way to
			// make an orphan. Both flags therefore keep the row, and the next remove —
			// with the admin token this one lacked, or against the service that has come
			// back — re-derives the same targets and converges, because a delete of what
			// is already gone counts as deleted.
			hubFailed := false
			forgejoFailed := false
			hubDelete := func(kind, name string, del func() error) {
				if err := del(); err != nil {
					checklist = append(checklist, kind+":"+name)
					hubFailed = true
					return
				}
				deleted = append(deleted, kind+":"+name)
			}
			forgejoDelete := func(kind, name string, del func() error) {
				if err := del(); err != nil {
					checklist = append(checklist, kind+":"+name)
					forgejoFailed = true
					return
				}
				deleted = append(deleted, kind+":"+name)
			}

			// Delete in reverse dependency order (spec §3.7):
			// 1. Org hook first (Forgejo, with admin token) — on the install's own
			// instance, resolved from the row rather than from the env var.
			fgInst := svc.forgejoClientFor("", inst)
			if req.AdminToken != "" {
				admin := fgInst.Admin(req.AdminToken)
				if inst.HookID != 0 {
					id := inst.HookID
					forgejoDelete("hook", strconv.FormatInt(id, 10), func() error {
						return admin.DeleteOrgHook(ctx, inst.Org, id)
					})
				}
			} else {
				// Without admin token the hook cannot be reached: it goes to the
				// checklist, and the row stays so a later remove with a token can
				// still find it.
				if inst.HookID != 0 {
					checklist = append(checklist, "hook:"+strconv.FormatInt(inst.HookID, 10))
					forgejoFailed = true
				}
			}

			// 2. Triggers, on the install's own connector. A trigger's id is not in the row,
			// but every name the install gave one carries its slug and no other install's.
			if inst.ConnectorID != "" {
				triggers, err := h.ListTriggers(writeCtx, inst.ConnectorID)
				if err != nil {
					// Nothing can be removed without knowing what is there. The row stays so
					// that removing the install can be tried again.
					log.Printf("api: setup delete: ListTriggers: %v", err)
					hubFailed = true
				}
				for _, trigger := range triggers {
					if !strings.HasPrefix(trigger.Name, "forgejo-") || !strings.HasSuffix(trigger.Name, "-"+slug) {
						continue
					}
					id := trigger.ID
					hubDelete("trigger", id, func() error { return h.DeleteTrigger(writeCtx, id) })
				}
			}

			// 2b. The cron trigger — the developer's PR patrol. It has no connector, so
			// the list is the hub's whole set; the install's own is the one name it gave
			// it, derived from the slug, and the delete takes the id the list carried.
			if cronTriggers, err := h.ListCronTriggers(writeCtx); err != nil {
				log.Printf("api: setup delete: ListCronTriggers: %v", err)
				hubFailed = true
			} else {
				for _, cronTrigger := range cronTriggers {
					if cronTrigger.Name != setup.CronPatrolName(slug) {
						continue
					}
					id := cronTrigger.ID
					hubDelete("cronTrigger", id, func() error { return h.DeleteCronTrigger(writeCtx, id) })
				}
			}

			// 3. Connector
			if inst.ConnectorID != "" {
				id := inst.ConnectorID
				hubDelete("connector", id, func() error { return h.DeleteConnector(writeCtx, id) })
			}

			// 4. Agents
			for _, role := range []setup.Role{setup.RoleRefiner, setup.RoleDeveloper, setup.RoleReviewer} {
				if id := inst.Roles[role].AgentID; id != "" {
					hubDelete("agent", id, func() error { return h.DeleteAgent(writeCtx, id) })
				}
			}

			// 5. Images
			for _, role := range []setup.Role{setup.RoleRefiner, setup.RoleDeveloper, setup.RoleReviewer} {
				if id := inst.Roles[role].ImageID; id != "" {
					hubDelete("image", id, func() error { return h.DeleteAgentImage(writeCtx, id) })
				}
			}

			// 6. Personas
			for _, role := range []setup.Role{setup.RoleRefiner, setup.RoleDeveloper, setup.RoleReviewer} {
				if id := inst.Roles[role].PersonaID; id != "" {
					hubDelete("persona", id, func() error { return h.DeletePersona(writeCtx, id) })
				}
			}

			// 7. MCP server
			if inst.MCPServer != "" {
				name := inst.MCPServer
				hubDelete("mcp", name, func() error { return h.DeleteMCPServer(writeCtx, name) })
			}

			// 8. Teams and users (Forgejo, admin token)
			if req.AdminToken != "" {
				admin := fgInst.Admin(req.AdminToken)

				// Delete teams
				for _, role := range []setup.Role{setup.RoleRefiner, setup.RoleDeveloper, setup.RoleReviewer} {
					st := inst.Roles[role]
					if st.TeamID != 0 {
						id := st.TeamID
						forgejoDelete("team", strconv.FormatInt(id, 10), func() error {
							return admin.DeleteTeam(ctx, id)
						})
					}
				}

				// Delete users
				for _, role := range []setup.Role{setup.RoleRefiner, setup.RoleDeveloper, setup.RoleReviewer} {
					st := inst.Roles[role]
					if st.BotLogin != "" {
						login := st.BotLogin
						forgejoDelete("user", login, func() error {
							return admin.DeleteUser(ctx, login)
						})
					}
				}
			} else {
				// Without admin token, teams and users go to checklist — and the row
				// stays with them, because the row is what names them for the next
				// remove.
				for _, role := range []setup.Role{setup.RoleRefiner, setup.RoleDeveloper, setup.RoleReviewer} {
					st := inst.Roles[role]
					if st.TeamID != 0 {
						checklist = append(checklist, "team:"+strconv.FormatInt(st.TeamID, 10))
						forgejoFailed = true
					}
					if st.BotLogin != "" {
						checklist = append(checklist, "user:"+st.BotLogin)
						forgejoFailed = true
					}
				}
			}

			// The row goes only when nothing was kept — on either side. While it is
			// there the admin can remove the install again once whatever failed has
			// cleared (with the Forgejo admin token, if what is left is Forgejo's); once
			// it is gone, the survivors are unnamed and unreachable.
			kept := hubFailed || forgejoFailed
			if !kept {
				if err := s.Delete(ctx, slug); err != nil {
					log.Printf("api: setup delete: store.Delete: %v", err)
					return errStore
				}
			}

			writeJSON(w, http.StatusOK, struct {
				Deleted   []string `json:"deleted"`
				Checklist []string `json:"checklist"`
				// Kept says the install is still installed: something — on the hub or on
				// Forgejo — could not be removed, so the row was left to make it findable
				// again. Remove it again (with the Forgejo admin token, if the checklist
				// names Forgejo items) and the removal converges.
				Kept bool `json:"kept"`
			}{
				Deleted:   deleted,
				Checklist: checklist,
				Kept:      kept,
			})
			return nil
		})
	})

	// POST /api/setup/orphans/{slug}/plan — list what a slug left in Forgejo.
	// Two steps by design: nothing is deleted by this route, and the delete route
	// below shows the same list before it acts on it. The admin token is in the
	// body, like remove's (R4).
	mux.HandleFunc("POST /api/setup/orphans/{slug}/plan", func(w http.ResponseWriter, r *http.Request) {
		gate(w, r, h, func(ctx context.Context, caller *hub.Caller) error {
			var req OrphanRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				return badRequest("invalid json")
			}
			req.Slug = r.PathValue("slug")

			plan, err := svc.PlanOrphans(ctx, caller, req)
			if err != nil {
				return err
			}
			writeJSON(w, http.StatusOK, plan)
			return nil
		})
	})

	// POST /api/setup/orphans/{slug}/delete — remove what the plan step lists.
	// The caller has seen the plan first: the console runs the plan before it
	// offers this call, and so do the MCP tools, which name the two steps as two
	// tools for exactly that reason.
	mux.HandleFunc("POST /api/setup/orphans/{slug}/delete", func(w http.ResponseWriter, r *http.Request) {
		gate(w, r, h, func(ctx context.Context, caller *hub.Caller) error {
			var req OrphanRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				return badRequest("invalid json")
			}
			req.Slug = r.PathValue("slug")

			report, err := svc.DeleteOrphans(ctx, caller, req)
			if err != nil {
				return err
			}
			writeJSON(w, http.StatusOK, report)
			return nil
		})
	})

	return mux
}

// wizardSettings reads the settings store once and folds the answer into the
// shape the setup flow reads: each role's effective llm, and the forge MCP
// server's wiring. The MCP pair is read off the first role — the registry keeps
// a value per role because every settings field lives per role, but an install
// has one forge MCP server, so one reading names it; the settings page is where
// the roles' values are kept in step.
func wizardSettings(ctx context.Context, st settingsReader) (setup.Settings, error) {
	all, err := st.All(ctx)
	if err != nil {
		log.Printf("api: setup: settings.All: %v", err)
		return setup.Settings{}, errSettings
	}

	vals := setup.Settings{LLM: map[setup.Role]setup.RoleLLM{}}
	for _, s := range all {
		role := setup.Role(s.Role)
		switch s.Field {
		case settings.FieldLLMProvider, settings.FieldLLMModel, settings.FieldLLMCustomUrl,
			settings.FieldLLMCustomKey, settings.FieldLLMOpenAIKey,
			settings.FieldLLMAnthropicKey, settings.FieldLLMGoogleKey,
			settings.FieldLLMOpenAIAuthMethod:
			llm := vals.LLM[role]
			switch s.Field {
			case settings.FieldLLMProvider:
				llm.Provider = s.Value
			case settings.FieldLLMModel:
				llm.Model = s.Value
			case settings.FieldLLMCustomUrl:
				llm.CustomURL = s.Value
			case settings.FieldLLMOpenAIAuthMethod:
				llm.OpenAIAuthMethod = s.Value
			default:
				// One of the four key rows. The store's raw value: the masking
				// that keeps a key out of a console answer happens on the way out
				// of the settings routes (settings.MaskSecrets), not here — the
				// create needs the key.
				switch s.Field {
				case settings.FieldLLMCustomKey:
					llm.CustomKey = s.Value
				case settings.FieldLLMOpenAIKey:
					llm.OpenAIKey = s.Value
				case settings.FieldLLMAnthropicKey:
					llm.AnthropicKey = s.Value
				case settings.FieldLLMGoogleKey:
					llm.GoogleKey = s.Value
				}
			}
			vals.LLM[role] = llm
		case settings.FieldMCPUrl:
			if role == settings.Roles[0] {
				vals.MCPURL = s.Value
			}
		case settings.FieldMCPTokenFromEnv:
			if role == settings.Roles[0] {
				vals.MCPTokenFromEnv = s.Value
			}
		}
	}
	return vals, nil
}

// applyWizardKeys folds the wizard's optional per-role provider key into the
// settings reading the plan and the apply are built on. The key is a
// pass-through: it goes into the role's slot for the provider the role runs on
// and marks the reading as wizard-sourced, so the plan previews the write and
// the apply sends it as the agent's own hub secret. Nothing here stores it —
// neither the settings table nor the install row ever sees it (R4).
//
// A key for a provider the role does not run on is dropped rather than stored
// for later: the wizard names one key per role, and a key for a provider the
// agent will not use has no agent to hold it. So does a key beside the ChatGPT
// sign-in — the hub refuses the pair, and the sign-in is the method the role
// asked for.
func applyWizardKeys(vals setup.Settings, in setup.Input) {
	for role, ri := range in.Roles {
		key := strings.TrimSpace(ri.LLMKey)
		if key == "" || vals.LLM == nil {
			continue
		}
		llm := vals.LLM[role]
		switch llm.Provider {
		case setup.LLMProviderCustom:
			llm.CustomKey = key
		case setup.LLMProviderOpenAI:
			if setup.IsChatGPTSignIn(llm.OpenAIAuthMethod) {
				continue
			}
			llm.OpenAIKey = key
		case setup.LLMProviderAnthropic:
			llm.AnthropicKey = key
		case setup.LLMProviderGoogle:
			llm.GoogleKey = key
		default:
			// A provider the plugin holds no key for (ollama-cloud, opencode,
			// alibaba-cloud): nothing to fill, and nothing to say.
			continue
		}
		llm.KeyFromWizard = true
		vals.LLM[role] = llm
	}
}

// buildSnapshot constructs the snapshot needed for planning.
func buildSnapshot(ctx context.Context, h hub.Client, fg *forgejo.Client, s storeOps, caller *hub.Caller, adminToken, org, slug string, existing *setup.Install) (setup.Snapshot, error) {
	snap := setup.Snapshot{
		Existing:     existing,
		CallerGroups: callerWritableGroupIDs(caller),
		DefaultTools: content.DefaultTools,
		CheckTools:   content.CheckTools,
	}

	// Get plugin groups
	pluginGroups, err := h.PluginGroups(ctx)
	if err != nil {
		return snap, err
	}
	snap.PluginGroups = pluginGroups

	// Get taken names
	snap.TakenNames = make(map[string]bool)

	// Check against hub resources
	if personas, err := h.ListPersonas(ctx); err == nil {
		for _, p := range personas {
			snap.TakenNames["persona:"+p.Name] = true
			snap.TakenNames["persona:"+p.ID] = true
		}
	}
	if agents, err := h.ListAgents(ctx); err == nil {
		for _, ag := range agents {
			snap.TakenNames["agent:"+ag.Name] = true
			snap.TakenNames["agent:"+ag.ID] = true
		}
	}
	if connectors, err := h.ListConnectors(ctx); err == nil {
		for _, c := range connectors {
			snap.TakenNames["connector:"+c.Name] = true
			snap.TakenNames["connector:"+c.ID] = true
		}
	}
	if mcpServers, err := h.ListMCPServers(ctx); err == nil {
		for _, m := range mcpServers {
			// The registry keys a server by its name alone, so the name is the whole of
			// the name a create would have to claim.
			snap.TakenNames["mcp:"+m.Name] = true
		}
	}
	if images, err := h.ListAgentImages(ctx); err == nil {
		for _, img := range images {
			// Both halves: a create claims the display name, and the id is what a second
			// install of the same name would collide with.
			snap.TakenNames["image:"+img.ID] = true
			snap.TakenNames["image:"+img.Name] = true
		}
	}

	// Populate Prior from existing install's resources (for edits)
	if existing != nil {
		snap.Prior = make(map[setup.Role]setup.PriorState)
		for _, r := range []setup.Role{setup.RoleRefiner, setup.RoleDeveloper, setup.RoleReviewer} {
			st := existing.Roles[r]
			priorState := setup.PriorState{}

			// Read image to get runtime
			if st.ImageID != "" {
				if img, err := h.GetAgentImage(ctx, st.ImageID); err == nil {
					priorState.Runtime = img.ImageURL
				}
			}

			// Read agent to get name, provider, model and the custom endpoint's
			// URL. The URL is diffed when the role runs on the custom provider; a
			// key is not read — it can never be read back (R4). The row's
			// fingerprints stand in for them: the key drift the hub cannot show,
			// the row can.
			priorState.CustomKeyHash = st.LLMCustomKeyHash
			priorState.OpenAIKeyHash = st.OpenAIKeyHash
			priorState.AnthropicKeyHash = st.AnthropicKeyHash
			priorState.GoogleKeyHash = st.GoogleKeyHash
			if st.AgentID != "" {
				if ag, err := h.GetAgent(ctx, st.AgentID); err == nil {
					priorState.AgentName = ag.Name
					priorState.CustomURL = ag.CustomURL
					// The auth method is the one thing about a role's credential the
					// hub can answer, so it is diffed by value: a role moved to the
					// sign-in (or off it) previews as the write that moves it.
					priorState.OpenAIAuthMethod = ag.OpenAIAuthMethod
					if ag.LLM != nil {
						priorState.Provider = ag.LLM.Provider
						priorState.Model = ag.LLM.Model
					}
				}
			}

			snap.Prior[r] = priorState
		}
	}

	// Check Forgejo resources if admin token provided
	if adminToken != "" {
		admin := fg.Admin(adminToken)

		// Check admin status
		_, isAdmin, err := admin.WhoAmI(ctx)
		if err == nil {
			snap.AdminChecked = true
			snap.AdminIsAdmin = isAdmin
		}

		// Check org existence
		orgExists, _ := admin.OrgExists(ctx, org)
		snap.OrgExists = orgExists

		// Check Forgejo logins and teams if admin. The names are the ones the install would
		// create, from the same helpers the plan names resources with: a check against a
		// name the install does not use says nothing about the one it does.
		for _, r := range []setup.Role{setup.RoleRefiner, setup.RoleDeveloper, setup.RoleReviewer} {
			login := setup.BotLogin(r, slug)
			if exists, _ := admin.UserExists(ctx, login); exists {
				snap.TakenNames["login:"+login] = true
			}
			teamName := setup.TeamName(r, slug)
			if exists, _ := admin.TeamExists(ctx, org, teamName); exists {
				snap.TakenNames["team:"+teamName] = true
			}
		}
	}

	// Add trigger names to taken names
	if existing != nil && existing.ConnectorID != "" {
		if triggers, err := h.ListTriggers(ctx, existing.ConnectorID); err == nil {
			for _, trigger := range triggers {
				snap.TakenNames["trigger:"+trigger.Name] = true
			}
		}
	}

	// Add cron trigger names to taken names. The hub's cron list is global — a cron
	// trigger belongs to no connector — so a new install's patrol name is taken by
	// whoever holds it anywhere.
	if cronTriggers, err := h.ListCronTriggers(ctx); err == nil {
		for _, ct := range cronTriggers {
			snap.TakenNames["cronTrigger:"+ct.Name] = true
		}
	}

	return snap, nil
}

func callerWritableGroupIDs(caller *hub.Caller) []string {
	var ids []string
	for _, g := range caller.Groups {
		if g.Role == "owner" || g.Role == "writer" {
			ids = append(ids, g.ID)
		}
	}
	return ids
}

func callerWritableGroups(caller *hub.Caller) []CallerGroup {
	groups := make([]CallerGroup, 0)
	for _, g := range caller.Groups {
		if g.Role == "owner" || g.Role == "writer" {
			groups = append(groups, CallerGroup{ID: g.ID, Name: g.Name, Role: g.Role})
		}
	}
	return groups
}

func callerCanWrite(caller *hub.Caller, groupID string) bool {
	for _, g := range caller.Groups {
		if g.ID == groupID && (g.Role == "owner" || g.Role == "writer") {
			return true
		}
	}
	return false
}

// CallerGroup is one group the caller may file an install under: writable, and
// named so the options answer can name it.
type CallerGroup struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Role string `json:"role"`
}

// gate checks the request has a valid caller and credential, then runs the handler.
func gate(w http.ResponseWriter, r *http.Request, h hub.Client, handler func(context.Context, *hub.Caller) error) {
	credential := hub.CredentialFromRequest(r)
	if credential == "" {
		writeError(w, http.StatusUnauthorized, "missing credential")
		return
	}

	caller, ok := hub.CallerFromRequest(r)
	if !ok || caller.Kind != "user" || !caller.IsAdmin {
		writeError(w, http.StatusForbidden, "admin only")
		return
	}

	ctx := hub.WithCredential(r.Context(), credential)
	if err := handler(ctx, &caller); err != nil {
		writeFailure(w, err)
	}
}

func personaForFunc(role setup.Role, bots map[setup.Role]string, baseURL string) string {
	return content.PersonaFor(role, bots, baseURL)
}

// getInstallBySlug finds an install in the store by slug.
func getInstallBySlug(ctx context.Context, s storeOps, slug string) (*setup.Install, error) {
	installs, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	for i := range installs {
		if installs[i].Slug == slug {
			return &installs[i], nil
		}
	}
	return nil, nil
}

// extractServiceError extracts the step name and service error message from an Apply.Run error.
// The error format is typically "StepName: <underlying error>". The underlying error may be
// a forgejo.StatusError or hub.StatusError which contain the service's message.
func extractServiceError(err error) string {
	// Get the error string, which has format "StepName: message"
	errStr := err.Error()

	// Try to find the step name by looking for ": " separator
	parts := strings.SplitN(errStr, ": ", 2)
	var stepName string
	var restErr string
	if len(parts) == 2 {
		stepName = parts[0]
		restErr = parts[1]
	} else {
		// Fallback if no separator found
		return errStr
	}

	// Check if the underlying error is a forgejo.StatusError
	// and extract its message
	var fErr *forgejo.StatusError
	if errors.As(err, &fErr) {
		if fErr.Message != "" {
			return stepName + ": " + fErr.Message
		}
		return stepName + ": forgejo returned " + strconv.Itoa(fErr.Code)
	}

	// A failed background mint is the hub refusing the plugin its own identity.
	// Every write of an apply now goes as the plugin — the stamp and the
	// named-manager rule are derived from the caller — so no step can be written
	// without the token. Name that failure rather than the per-step noise.
	var cErr *hub.CredentialError
	if errors.As(err, &cErr) {
		return stepName + ": the hub would not give the plugin a credential: " + cErr.Err.Error()
	}

	// A hub.StatusError has no message field of its own — it carries the hub's response
	// body in its Error() text, which the wrapper above already put into restErr, so the
	// rest of the message is what to show.
	// No specific error type found; use the rest of the message
	return stepName + ": " + restErr
}
