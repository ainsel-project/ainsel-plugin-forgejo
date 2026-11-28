// API types for the Forgejo plugin's backend routes.
// The paths are relative to the plugin's own mount: the console's api client
// is already bound to /api/v1/plugins/forgejo/api.

import type { HttpClient } from "@ainsel/plugin-sdk";

// The console's HTTP client rejects with an Error that also carries the HTTP
// `status` and, when the response body parsed as JSON, that body (the parsed
// apply report rides along on `report` for the routes that attach it there).
// A catch parameter is `unknown` in strict TypeScript, so every handler that
// reads one of these goes through this accessor instead of annotating the
// parameter `any` — the shape the client actually answers, in one place.
export interface ApiError {
  status?: number;
  body?: Record<string, unknown>;
  report?: unknown;
  message?: string;
}

// Reads a thrown value as the shape above. Never throws: a value that is not
// an Error still answers, with only the fields it happens to carry.
export function asApiError(err: unknown): ApiError {
  return (err ?? {}) as ApiError;
}

// Group is one of the caller's groups as the hub resolved it.
export interface Group {
  id: string;
  name: string;
  role: string;
}

// Subject is an issue or pull request.
export interface Subject {
  repo: string;
  number: number;
  kind: string; // "issues" or "pulls"
  title?: string;
  branch?: string;
  body?: string;
}

// Run is one AInsel run.
export interface Run {
  id: string;
  // agent is the hub's id for the agent; agentName the display name the plugin
  // resolved for it (the id again when the hub would not name it) — what a page shows.
  agent: string;
  agentName?: string;
  role: string;
  status: string;
  connector: string;
  start: string; // ISO8601
  end?: string; // ISO8601
  durationMs?: number;
  costUsd?: number | null;
  costKnown: boolean;
  subject: Subject;
  url: string;
  issueUrl?: string;
}

// Item is an issue or pull request the agents worked on.
export interface Item {
  subject: Subject;
  url: string;
  runs: Run[];
  durationMs: number;
  costUsd?: number | null;
  costKnown: boolean;
  lastActivity: string; // ISO8601
  prs: Item[];
}

// Repo is a repository's work.
export interface Repo {
  repo: string;
  url: string;
  items: Item[];
  costUsd?: number | null;
  costKnown: boolean;
}

// WorkResponse is what /api/work returns.
export interface WorkResponse {
  repos: Repo[];
}

// NowResponse is what /api/now returns.
export interface NowResponse {
  runs: Run[];
}

// ItemResponse is what /api/work/{owner}/{repo}/{number} returns.
export interface ItemResponse {
  item: Item;
}

// InvocationResponse is what /api/invocations/{id}/work returns.
export interface InvocationResponse {
  item: Item;
  siblingsCostUsd?: number | null;
  siblingsCostKnown: boolean;
}

// WorkApi is the slice of the SDK's HttpClient the UI uses for work endpoints.
export interface WorkApi {
  get(
    path: string,
    init?: { query?: Record<string, string> },
  ): Promise<WorkResponse | NowResponse | ItemResponse | InvocationResponse>;
}

// Setup API types

export interface Group {
  id: string;
  name: string;
  role: string;
}

// RoleToolSets is what the tool checklist is drawn from: the set each role starts with
// (spec §3.3) and the tools the R7 rails say that role may never have. Both come from the
// same code that applies them, so the wizard cannot offer what the apply would refuse.
export interface RoleToolSets {
  defaults: Record<string, string[]>;
  forbidden: Record<string, string[]>;
}

// ForgejoInstance is one configured Forgejo: the name the admin gave it and its
// address. The wizard's Instance step selects over these instead of typing a
// free-text URL, and the plan refuses an address that is not one of them.
export interface ForgejoInstance {
  name: string;
  baseUrl: string;
}

// SetupOptions is what /setup/options answers after the created-only cutover:
// the pick lists (hub agents, personas, images, MCP servers) are gone, because
// an install creates all of those itself and nothing running is a source to
// pick from. What is left is what the wizard still picks from or obeys —
// including the configured instances, which the Instance step selects over.
export interface SetupOptions {
  groups: Group[];
  pluginGroups: string[];
  forgejoBaseURL: string;
  instances: ForgejoInstance[];
  tools: RoleToolSets;
}

export interface SetupInstalls {
  installs: Install[];
}

// WorkingHours is the window an agent works in, in the shape the AInsel hub's agent spec
// takes it: days as Go's weekday numbers (0 is Sunday, and 7 reads as Sunday too), the bounds
// as HH:MM in the zone, and a start later than the end meaning the window runs past midnight.
// An absent window is "any hour" — which is not the same thing as an empty one, and the whole
// of a re-apply turns on the difference.
export interface WorkingHours {
  timezone: string;
  days: number[];
  start: string;
  end: string;
}

export interface RoleState {
  agentId: string;
  imageId: string;
  personaId: string;
  botLogin: string;
  teamId: number;
  tools: string[];
  // The window the wizard last wrote for this role — not what the agent holds now. The row is
  // the wizard's baseline, and it is why an edit that changes nothing writes nothing. A window
  // an admin set in the AInsel console is not read into here, so it is never compared and
  // never overwritten. Absent means the wizard never set one.
  workingHours?: WorkingHours;
  // The TURN_TIMEOUT_MS the role image was last written with, in milliseconds
  // (issue #61). Empty on rows written before the option existed — the edit
  // field then starts empty, which means "keep what the image has".
  turnTimeoutMs?: string;
}

export interface Install {
  slug: string;
  groupId: string;
  baseUrl: string;
  org: string;
  connectorId: string;
  mcpServer: string;
  hookId: number;
  roles: Record<string, RoleState>;
  excludeLabel: string;
  webhookVerifiedAt?: string;
  // Which event families ("issues", "issue_comment", "pull_request" — reviews count
  // as the pull family) the org has actually delivered, and when the install first saw
  // one. Written only by the background reconcile loop, so it lags a real delivery by
  // up to one pass. Absent until the loop has seen anything.
  coverage?: Record<string, string>;
}

export interface Op {
  kind: string;
  name: string;
  detail: string;
}

export interface Filter {
  field: string;
  op: string;
  value?: string;
  values?: string[];
}

export interface Trigger {
  name: string;
  agentRef: string;
  connectorRef: string;
  filters: Filter[];
}

export interface Verdict {
  blocking: string[];
  warnings: string[];
}

export interface PlanResponse {
  create: Op[];
  change: Op[];
  delete: Op[];
  triggers: Trigger[];
  verdict: Verdict;
}

export interface ApplyReport {
  created: Op[];
  rolledBack: Op[];
  leftovers: Op[];
}

export interface ApplyResponse {
  install: Install;
  report: ApplyReport;
}

// RemoveResponse is what POST /setup/installs/{slug}/remove answers with. `kept` says the
// row survived because the hub kept part of the install: the row is what says which
// resources are the install's, so a removal that could not finish keeps it and is retried.
export interface RemoveResponse {
  deleted: string[];
  checklist: string[];
  kept: boolean;
}

export interface DeliveryResponse {
  verified: boolean;
  at?: string;
}

// PlanRequest is what /setup/plan and /setup/apply take: only the per-install
// facts. The forge MCP URL and the role LLMs are not in it any more — the
// settings store supplies them on every plan and apply (design decision 2).
export interface PlanRequest {
  slug: string;
  groupId: string;
  baseUrl: string;
  org: string;
  adminToken: string;
  excludeLabel: string;
  roles: Record<string, RoleInputRequest>;
}

export interface RoleInputRequest {
  runtime: string;
  agentName: string;
  tools: string[];
  // Sent only when the admin asked for a window. Left out, the plan has no op for the field
  // and the apply writes neither a window nor a clear.
  workingHours?: WorkingHours;
  // Milliseconds as a decimal string; empty means the shipped default on a
  // create and "keep" on an edit (issue #61).
  turnTimeoutMs?: string;
}

// Settings API types

// Setting is one (role, field) of the role settings as GET/PUT /api/settings
// answers it: the value in force, whether an admin put it there, and the
// shipped default behind it. The answer is always the whole registry — 3 roles
// × 6 fields — so the page renders without knowing which fields have rows.
export interface Setting {
  role: string;
  field: string;
  // The effective value: the admin's edit while the field is dirty, the
  // shipped default otherwise.
  value: string;
  // True once an admin has edited the field, until a reset clears it.
  dirty: boolean;
  // The currently shipped default — what a reset re-adopts, and what an
  // upgrade diff is drawn against.
  default: string;
  // The plugin version whose shipped default produced the effective default:
  // the version an edited value was edited away from, or the running version
  // wherever the default is what is in force.
  defaultVersion: string;
  // The choices this row offers as a dropdown, served by the registry on the
  // rows with a fixed list (today: llmProvider — the list plus the row's
  // current value when it is off it). Absent on free-text rows, which render
  // as inputs as they always did.
  options?: { value: string; label: string }[];
}

export interface SettingsResponse {
  settings: Setting[];
  // Fields names a field that saved but did not reach the hub — today only a
  // provider key, whose storage is a write onto the role's agents rather than a
  // row here. It arrives on a successful save, because the save itself did
  // land; a refused batch carries the same shape on the error's body.
  fields?: FieldError[];
}

// FieldError is one field's refusal, named by role and field so the page can
// put the message on the row rather than in a banner over the whole form.
export interface FieldError {
  role: string;
  field: string;
  error: string;
}

// SettingsEdit is one requested change: one field of one role, either an edit
// (marks the field dirty) or a reset (re-adopts the shipped default).
export interface SettingsEdit {
  role: string;
  field: string;
  value?: string;
  reset?: boolean;
}

// FieldError is one refused edit, named — the field-level half of a PUT that
// wrote nothing.
export interface FieldError {
  role: string;
  field: string;
  error: string;
}

// ReconcileSummary is what POST /api/settings/reconcile ("Check now") answers
// with: one entry per resource the pass fixed, one per write the hub refused,
// and one per role whose settings name a model — the reachability answer over
// the distinct (provider, model) pairs. All three lists are always present,
// empty rather than null: "nothing to fix" is an answer and not an absence.
export interface ReconcileChange {
  install?: string;
  role?: string;
  kind: string;
  field: string;
  from?: string;
  to: string;
}

export interface ReconcileRefusal {
  install?: string;
  role?: string;
  kind: string;
  field: string;
  error: string;
}

export interface ReconcileSummary {
  updated: ReconcileChange[];
  refused: ReconcileRefusal[];
  llmChecks: ReconcileLLMCheck[];
}

// ReconcileLLMCheck is one role's LLM pair as the "Check now" reachability check
// found it: the pair the settings name, and the state the hub reported — "ok",
// "unreachable" (the hub asked and the pair cannot serve), or "unknown" (the hub
// would not answer — an old hub, or one that could not be reached). Unknown is
// rendered as "cannot check", never as health.
export interface ReconcileLLMCheck {
  role: string;
  provider: string;
  model: string;
  state: string;
  reason?: string;
}

// fetchSettings reads the whole registry: every (role, field) with its
// effective value, dirty flag and shipped default.
export function fetchSettings(api: HttpClient): Promise<SettingsResponse> {
  return api.get<SettingsResponse>("/settings");
}

// putSettings applies a batch of edits or resets. It resolves with the full set
// as it now stands — plus, in `fields`, any field that saved without reaching
// the hub (a provider key whose agents refused the write). A refused batch
// throws with the named fields on the error's body (the console's client puts
// the parsed body there).
export function putSettings(api: HttpClient, edits: SettingsEdit[]): Promise<SettingsResponse> {
  return api.put<SettingsResponse>("/settings", { settings: edits });
}

// reconcileSettings is "Check now": one pass over every install's resources,
// answered with what it updated and what the hub refused.
export function reconcileSettings(api: HttpClient): Promise<ReconcileSummary> {
  return api.post<ReconcileSummary>("/settings/reconcile");
}

// InstancesResponse is both the GET and the PUT answer: the stored list in its
// stored order, never null. An empty list is the honest "nothing is
// configured", which is what the wizard renders as a link to Settings.
export interface InstancesResponse {
  instances: ForgejoInstance[];
}

// fetchInstances reads the configured Forgejo instances, in the order the
// Settings page lists them — the order the wizard's select draws.
export function fetchInstances(api: HttpClient): Promise<InstancesResponse> {
  return api.get<InstancesResponse>("/instances");
}

// putInstances replaces the whole list. Like the settings PUT, a refused write
// names each bad row on the error's body and changes nothing; on success the
// answer is the stored list, with the addresses already normalized.
export function putInstances(api: HttpClient, instances: ForgejoInstance[]): Promise<InstancesResponse> {
  return api.put<InstancesResponse>("/instances", { instances });
}

// -- the orphan sweep ---------------------------------------------------------------

// OrphanItem is one Forgejo resource the sweep found for a slug whose install row
// is gone: the kind, the name Forgejo shows, the id or login a delete needs, and a
// line saying what deleting it takes with it.
export interface OrphanItem {
  kind: string; // "user" | "team" | "hook"
  name: string;
  id: string;
  detail?: string;
}

// OrphanPlan is what POST /setup/orphans/{slug}/plan answers with: what was found,
// and where it was looked. The delete step re-derives this same set server-side —
// the plan is a rendering of what delete will do, not a ticket that scopes it.
export interface OrphanPlan {
  slug: string;
  baseUrl: string;
  org: string;
  items: OrphanItem[];
}

// OrphanRequest is what both sweep steps take: the instance must be one of the
// configured ones, and the admin token is a body field — never a URL.
export interface OrphanRequest {
  baseUrl: string;
  org: string;
  adminToken: string;
}

export interface OrphanDeleteReport {
  deleted: string[];
  failed: string[];
}

// planOrphans lists what a slug left in Forgejo. It deletes nothing.
export function planOrphans(api: HttpClient, slug: string, req: OrphanRequest): Promise<OrphanPlan> {
  return api.post<OrphanPlan>(`/setup/orphans/${encodeURIComponent(slug)}/plan`, req);
}

// deleteOrphans removes what the plan step lists. Call it only after planOrphans
// has been shown: the sweep deletes by naming convention, and the plan is what
// makes that nameable before it is deletable.
export function deleteOrphans(api: HttpClient, slug: string, req: OrphanRequest): Promise<OrphanDeleteReport> {
  return api.post<OrphanDeleteReport>(`/setup/orphans/${encodeURIComponent(slug)}/delete`, req);
}
