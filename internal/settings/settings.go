// Package settings keeps the per-role configuration the admin owns: one value
// per (role, field), each carrying the dirty flag that says whether an admin
// put it there. It is the read and write path behind GET/PUT /api/settings;
// nothing here talks to the hub — applying a value to the agents is the
// reconcile pass, not this package.
//
// The package also keeps the plugin's list of configured Forgejo instances
// (see instances.go): the addresses the setup wizard may install against, read
// and replaced whole through GET/PUT /api/instances.
//
// A field with no row is a field nobody has edited: its value is the shipped
// default, and it stays the shipped default through every upgrade until an
// admin edits it. The dirty flag is what tells the two apart — the design's
// winner rule. The default_version column records which shipped default an
// edited field was edited away from (or last re-adopted), so a later upgrade
// can say "new version available" against the right text.
//
// The rows live in the same SQLite file as the installs: the settings are the
// plugin's, and one file keeps one directory to mount and one thing to back
// up. The MCP token field stores the name of the env var a token arrives in,
// never a token. Like the install store, this table holds no secret: a role's
// LLM provider keys (llmCustomKey, llmOpenAIKey, llmAnthropicKey, llmGoogleKey)
// are a write-only pass-through here (see isLLMKeyField) — Save accepts them
// and writes no row, every answer for them is empty, and their home is the
// hub's per-agent secret on the role's agents (reconcile.Engine.PushLLMKey).
// The plugin keeps no copy, so there is nothing here to hand a backup, and
// nothing to leak off the plugin's volume.
package settings

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	plugin "github.com/ainsel-project/ainsel-plugin-forgejo"
	"github.com/ainsel-project/ainsel-plugin-forgejo/content"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/manifest"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/setup"

	// The pure-Go SQLite driver; the plugin image is distroless with CGO off.
	_ "modernc.org/sqlite"
)

// dbFileName is the SQLite file inside the directory given to Open — the same
// file internal/store opens. Two handles on one database are fine: SQLite
// serialises the writes between them, and busy_timeout makes a write that
// arrives during the other handle's wait wait too rather than fail.
const dbFileName = "forgejo.db"

// Roles are the roles settings exist for: the three the wizard installs.
// Every answer lists this set in this order, so the API's list is stable.
var Roles = []setup.Role{setup.RoleRefiner, setup.RoleDeveloper, setup.RoleReviewer}

// The settings fields, one per row an admin can edit. The names are the API's
// vocabulary: persona and skill texts, the LLM the role's agent runs on, and
// the two values of the install's MCP server row (the shape
// Hub.RegisterMCPServer takes: a URL and the env var its token arrives in).
const (
	FieldPersonaText  = "personaText"
	FieldSkillText    = "skillText"
	FieldLLMProvider  = "llmProvider"
	FieldLLMModel     = "llmModel"
	FieldLLMCustomUrl = "llmCustomUrl"
	// FieldLLMCustomKey is the API key the custom endpoint expects. It is a
	// pass-through, not a setting this store keeps: Save accepts it and writes
	// no row, and every answer for it is empty. Its home is the hub's secret
	// store, on each of the role's agents (see reconcile.Engine.PushLLMKey,
	// which the settings route calls), and the plugin keeps no copy — so there
	// is nothing here to mask, nothing to read back, and no plaintext on the
	// plugin's volume. Entering it empty means "leave what the agents hold";
	// replacing it means typing the new key.
	FieldLLMCustomKey = "llmCustomKey"
	// The three provider keys the plugin delivers for the vendor providers it
	// has credentials for. Each is a pass-through exactly like llmCustomKey —
	// see isLLMKeyField.
	FieldLLMOpenAIKey = "llmOpenAIKey"
	// FieldLLMOpenAIAuthMethod is the dropdown that chooses how a role on the
	// openai provider authenticates: an API key (the shipped default, and what
	// llmOpenAIKey feeds) or a ChatGPT sign-in the admin finishes per agent in
	// the console. Like the vendor key rows, it means something only while the
	// role's provider names openai; elsewhere it is stored and never sent.
	FieldLLMOpenAIAuthMethod = "llmOpenAIAuthMethod"
	FieldLLMAnthropicKey     = "llmAnthropicKey"
	FieldLLMGoogleKey        = "llmGoogleKey"
	FieldMCPUrl              = "mcpUrl"
	FieldMCPTokenFromEnv     = "mcpTokenFromEnv"
)

// Fields lists every settings field, in the order the API lists them.
var Fields = []string{
	FieldPersonaText,
	FieldSkillText,
	FieldLLMProvider,
	FieldLLMModel,
	FieldLLMCustomUrl,
	FieldLLMCustomKey,
	FieldLLMOpenAIKey,
	FieldLLMOpenAIAuthMethod,
	FieldLLMAnthropicKey,
	FieldLLMGoogleKey,
	FieldMCPUrl,
	FieldMCPTokenFromEnv,
}

// The shipped LLM wiring: the provider and model a role runs on until an
// admin says otherwise. The model is free text; the provider is not — it has
// to be one of LLMProviders, which the settings page offers as a dropdown and
// checkEdit refuses anything outside.
const (
	DefaultLLMProvider = "ollama-cloud"
	DefaultLLMModel    = "llama3.3"
)

// LLMAuthMethods is the compile-time list of the two auth methods an openai role
// may pick, in the same shape the llmProvider dropdown uses: Value is what the
// hub's openAI.authMethod names, Label what the settings page shows. The API key
// is first because it is the shipped default and the case every other provider
// is in — the sign-in is the one that leaves work for the admin to finish.
//
// A hub that predates the core's sign-in flow accepts the write and ignores the
// field, leaving a keyless agent that reads as an API-key one — the same
// older-hub caveat the vendor provider list carries.
var LLMAuthMethods = []LLMProvider{
	{Value: setup.LLMAuthMethodAPIKey, Label: "API key"},
	{Value: setup.LLMAuthMethodChatGPTSignIn, Label: "Sign in with ChatGPT"},
}

// LLMProvider is one choice of the llmProvider dropdown: Value is the provider
// id the hub's create-agent contract names the provider by, Label what the
// settings page shows for it — the same wording the hub console's own provider
// picker uses, so the two lists never disagree.
type LLMProvider struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// LLMProviders is the compile-time list of providers an admin may pick for a
// role: the providers this plugin defines credentials for (the key shapes of
// hub.AgentSpec), plus the custom endpoint the two llmCustom fields point at.
// It is what the settings page renders and what checkEdit enforces — one
// source of truth, served to the console on the llmProvider rows. Adding a
// provider means a plugin release, the same way Fields and the shipped
// defaults arrive.
//
// The plugin delivers a key for `openai`, `anthropic`, `google` and `custom`
// (see isLLMKeyField); `ollama-cloud`, `opencode` and `alibaba-cloud` keep the
// credentials the hub itself holds for them, so no plugin-side key row exists.
//
// "custom" names the hub's customProvider object: the URL is required on the
// effective value (see Save's batch check), the key is optional and never
// echoed back on a read (see MaskSecrets).
//
// A hub that predates ainsel-project/ainsel#242 rejects the three vendor ids
// and their key blocks, so this list is only as good as the hub it is pointed
// at — the plugin ships the newer list.
var LLMProviders = []LLMProvider{
	{Value: "ollama-cloud", Label: "Ollama Cloud"},
	{Value: "opencode", Label: "OpenCode"},
	{Value: "alibaba-cloud", Label: "Alibaba Token Plan"},
	{Value: setup.LLMProviderOpenAI, Label: "OpenAI"},
	{Value: setup.LLMProviderAnthropic, Label: "Anthropic"},
	{Value: setup.LLMProviderGoogle, Label: "Google Gemini"},
	{Value: setup.LLMProviderCustom, Label: "Custom"},
}

// isLLMKeyField says whether a field is one of the four pass-through LLM
// provider keys: the custom pair's key plus the three vendor provider keys.
// They share one contract — no answer ever carries the value (All, Effective,
// MaskSecrets), Save accepts the field and stores nothing, and the key's home
// is the hub's per-agent secret — so the rules are set-membership checks over
// this one list rather than four copies of the same branch.
func isLLMKeyField(field string) bool {
	switch field {
	case FieldLLMCustomKey, FieldLLMOpenAIKey, FieldLLMAnthropicKey, FieldLLMGoogleKey:
		return true
	}
	return false
}

// IsLLMKeyField is the exported reading of the same membership: the routes
// that refuse, forward or mask a key field ask it by name rather than keeping
// their own copy of the four-field list.
func IsLLMKeyField(field string) bool { return isLLMKeyField(field) }

// llmKeyFields are the four key rows in the registry's own order — the list
// the upgrade migration reads and prunes by (PendingLLMKeys, DeleteLLMKeys).
var llmKeyFields = []string{
	FieldLLMCustomKey,
	FieldLLMOpenAIKey,
	FieldLLMAnthropicKey,
	FieldLLMGoogleKey,
}

// DefaultMCPUrl is empty: which forge MCP endpoint to point at is a fact about
// the network the plugin is deployed on, not a fact the plugin ships with.
// The default says "nobody has said yet", which the settings page shows as an
// empty field.
const DefaultMCPUrl = ""

// pluginVersion is the version the plugin ships with, read off the same
// embedded manifest the hub reads — the way the plugin already surfaces its
// version, so a default can be tagged with the version that shipped it
// without a second source to drift.
var pluginVersion = readPluginVersion()

func readPluginVersion() string {
	m, err := manifest.Parse(plugin.ManifestJSON)
	if err != nil {
		// Unreachable: the manifest is embedded and the plugin refuses to
		// start with an unparseable one (cmd/plugin checks it too).
		panic("settings: embedded manifest is invalid: " + err.Error())
	}
	return m.Version
}

// PluginVersion returns the shipped plugin version, the tag every default is
// produced by until an upgrade ships a new one.
func PluginVersion() string {
	return pluginVersion
}

// Default returns the shipped default of one field of one role: the persona or the
// role's skill section for the texts, a compile-time const for the wiring. An
// unknown role or field answers "" — callers that take admin input check the names
// first (see Save).
//
// skillText is per role because the served skill document is composed of one
// section per role: what an admin edits here is that role's section of it, and the
// edit changes no other role's text (see content.ComposeSkill).
func Default(role setup.Role, field string) string {
	switch field {
	case FieldPersonaText:
		return content.Persona(role)
	case FieldSkillText:
		return content.SkillSection(role)
	case FieldLLMProvider:
		return DefaultLLMProvider
	case FieldLLMModel:
		return DefaultLLMModel
	case FieldLLMOpenAIAuthMethod:
		return setup.LLMAuthMethodAPIKey
	case FieldMCPUrl:
		return DefaultMCPUrl
	case FieldMCPTokenFromEnv:
		return setup.TokenEnvName
	}
	return ""
}

// Setting is one (role, field) as the API answers with it: the value in
// force, whether an admin put it there, and the shipped default behind it.
type Setting struct {
	Role  string `json:"role"`
	Field string `json:"field"`

	// Value is the effective value: the admin's edit when Dirty, the shipped
	// default otherwise. An untouched row that predates an upgrade reads as
	// the new default, not the stale stored text — the design's auto-adopt.
	Value string `json:"value"`

	// Dirty is true once an admin has edited the field, and until a reset
	// clears it. Edited fields survive upgrades; untouched ones follow the
	// shipped default.
	Dirty bool `json:"dirty"`

	// Default is the currently shipped default, whatever the admin's value
	// is — what a reset re-adopts, and what an upgrade diff is drawn against.
	Default string `json:"default"`

	// DefaultVersion names the plugin version whose shipped default produced
	// the effective default: the version an edited value was edited away
	// from, or the running version wherever the default is what is in force.
	DefaultVersion string `json:"defaultVersion"`

	// Options lists the choices this row offers as a dropdown, for the fields
	// with a fixed list — today the llmProvider rows (see LLMProviders) and the
	// OpenAI auth-method rows (see LLMAuthMethods), each plus the row's current
	// value when it is off the list, so a stored value stays visible and
	// selectable). Absent on every free-text row: a console renders those as it
	// always did.
	Options []LLMProvider `json:"options,omitempty"`
}

// Edit is one requested change: one field of one role. A reset re-adopts the
// shipped default and clears the dirty flag, and its Value is ignored.
type Edit struct {
	Role  string
	Field string
	Value string
	Reset bool
}

// FieldError names the edit that was refused and why — the field-level half
// of a refused PUT.
type FieldError struct {
	Role  string `json:"role"`
	Field string `json:"field"`
	Error string `json:"error"`
}

// ValidationError says some edit was refused, and which. A Save that returns
// one has written nothing: every edit is checked before the first row moves,
// so a PUT either lands whole or not at all.
type ValidationError struct {
	Fields []FieldError
}

func (e *ValidationError) Error() string {
	parts := make([]string, 0, len(e.Fields))
	for _, f := range e.Fields {
		parts = append(parts, f.Role+"."+f.Field+": "+f.Error)
	}
	return "settings: refused: " + strings.Join(parts, "; ")
}

// key identifies one row.
type key struct {
	role  string
	field string
}

// storedRow is a row as the table holds it, before the defaults are folded in.
type storedRow struct {
	value          string
	dirty          bool
	defaultVersion string
}

// schema is the settings table: one row per (role, field), keyed by both. The
// whole design fits in five columns — a row exists only to say "an admin
// edited this (or reset it) since vN", and a missing row says it cheaply
// enough that no default row is ever written. Created IF NOT EXISTS, like
// every table in this database.
const schema = `
CREATE TABLE IF NOT EXISTS settings (
	role TEXT NOT NULL,
	field TEXT NOT NULL,
	value TEXT NOT NULL,
	dirty INTEGER NOT NULL,
	default_version TEXT NOT NULL,
	PRIMARY KEY (role, field)
);
CREATE TABLE IF NOT EXISTS forgejo_instances (
	position INTEGER NOT NULL PRIMARY KEY,
	name TEXT NOT NULL,
	base_url TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS meta (
	key TEXT NOT NULL PRIMARY KEY,
	value TEXT NOT NULL
);`

// settingColumns is the column list every read names, so a read and a scan
// cannot disagree about the order of a row.
const settingColumns = "role, field, value, dirty, default_version"

// Store holds the open database handle the settings rows live in. It is safe
// for concurrent use: database/sql hands each query its own connection, and
// SQLite serialises the writes between them.
type Store struct {
	db *sql.DB
}

// Open opens the settings in dir — the same directory (and file) the install
// store was opened on. The table is created if it is not there yet; the
// settings of a database that predates this table start empty, which is
// exactly the state of a plugin nobody has configured.
func Open(dir string) (*Store, error) {
	if dir == "" {
		return nil, errors.New("settings: dir is required")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("settings: cannot create dir %q: %w", dir, err)
	}

	// busy_timeout makes a write that finds the file locked — including by
	// the install store's handle on the same file — wait for the lock to
	// clear instead of failing on the spot.
	db, err := sql.Open("sqlite", filepath.Join(dir, dbFileName)+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("settings: cannot open database in %q: %w", dir, err)
	}
	// One connection, not a pool: SQLite has a single writer, and a query
	// that waits for the write lock on a second connection is exactly the
	// deadlock a pool invites.
	db.SetMaxOpenConns(1)

	if _, err := db.ExecContext(context.Background(), schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("settings: cannot create schema in %q: %w", dir, err)
	}
	return &Store{db: db}, nil
}

// Close releases the database handle. Open pairs with it.
func (s *Store) Close() error {
	if err := s.db.Close(); err != nil {
		return fmt.Errorf("settings: cannot close database: %w", err)
	}
	return nil
}

// All returns every setting — each role × each field, in the registry's order
// — with the shipped defaults folded in, so a field with no row still answers
// with its default. The answer is always the same set of rows: every role ×
// every field, edited or not, which is what lets the console render the page
// without knowing the registry.
func (s *Store) All(ctx context.Context) ([]Setting, error) {
	const q = `SELECT ` + settingColumns + ` FROM settings;`
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("settings: cannot read settings: %w", err)
	}
	defer rows.Close()

	stored := map[key]storedRow{}
	for rows.Next() {
		var k key
		var r storedRow
		if err := rows.Scan(&k.role, &k.field, &r.value, &r.dirty, &r.defaultVersion); err != nil {
			return nil, fmt.Errorf("settings: cannot read a setting: %w", err)
		}
		// A key row is never an answer. The upgrade migration forwards the
		// stored keys onto the agents and deletes the rows (PendingLLMKeys,
		// DeleteLLMKeys); until it has, or for a row an older binary wrote by
		// hand, reading one would put a plaintext key back into an answer the
		// console, the wizard and the pass all see.
		if isLLMKeyField(k.field) {
			continue
		}
		stored[k] = r
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("settings: cannot read settings: %w", err)
	}

	out := make([]Setting, 0, len(Roles)*len(Fields))
	for _, role := range Roles {
		for _, field := range Fields {
			def := Default(role, field)
			set := Setting{
				Role:           string(role),
				Field:          field,
				Value:          def,
				Default:        def,
				DefaultVersion: PluginVersion(),
			}
			if r, ok := stored[key{string(role), field}]; ok && r.dirty {
				// Only an edited row wins: an un-dirty row is a reset or an
				// auto-adopt, and both leave the default in force — which is
				// what a reader gets, whatever the row's stored text says.
				set.Value = r.value
				set.Dirty = true
				set.DefaultVersion = r.defaultVersion
			}
			if field == FieldLLMProvider {
				set.Options = llmProviderOptions(set.Value)
			}
			if field == FieldLLMOpenAIAuthMethod {
				set.Options = llmAuthMethodOptions(set.Value)
			}
			out = append(out, set)
		}
	}
	return out, nil
}

// Effective returns the value in force for one (role, field): the admin's edit
// while its row is dirty, the shipped default otherwise — the same winner rule All
// answers with, for one row instead of the whole table.
//
// A key field answers its empty default whatever the row says: the store keeps
// no key (see isLLMKeyField), so it never answers one either.
//
// This is the read the skill endpoint does per request. The hub re-fetches every
// declared skill's body on every sync pass, so the answer has to reflect a PUT that
// landed seconds ago rather than a value read at start; one lookup by primary key
// is the cheapest read this table has, and a settings save needs no invalidation
// step because nothing here is cached.
func (s *Store) Effective(ctx context.Context, role setup.Role, field string) (string, error) {
	if isLLMKeyField(field) {
		return Default(role, field), nil
	}
	const q = `SELECT value, dirty FROM settings WHERE role = ? AND field = ?;`
	var value string
	var dirty int
	switch err := s.db.QueryRowContext(ctx, q, string(role), field).Scan(&value, &dirty); {
	case errors.Is(err, sql.ErrNoRows):
		// No row at all: a field nobody has edited.
		return Default(role, field), nil
	case err != nil:
		return "", fmt.Errorf("settings: cannot read %s.%s: %w", role, field, err)
	case dirty == 0:
		// A row that is not dirty is a reset or an auto-adopt: the default is in
		// force, whatever text the row still holds.
		return Default(role, field), nil
	}
	return value, nil
}

// Save applies the edits, all of them or none. Every edit is checked first —
// known role, known field, and text still valid for its field — and a single
// refusal comes back as a ValidationError naming each bad edit without the
// database seeing any of them. What is left goes in one transaction, so a
// crash cannot land half a PUT.
//
// The four key fields are accepted and written nowhere (see isLLMKeyField):
// the value arrives here only so the route that called Save can forward it to
// the role's agents, where the hub keeps it as the agent's own secret. A store
// that holds no copy of a key has nothing to keep on an empty save and nothing
// to clear on a reset — the hub has no way back to "no key", only forward to a
// new one.
func (s *Store) Save(ctx context.Context, edits []Edit) error {
	var bad []FieldError
	seen := map[key]bool{}
	for _, e := range edits {
		if err := checkEdit(e); err != nil {
			bad = append(bad, *err)
			continue
		}
		k := key{e.Role, e.Field}
		if seen[k] {
			bad = append(bad, FieldError{Role: e.Role, Field: e.Field, Error: "field given twice"})
			continue
		}
		seen[k] = true
	}
	if len(bad) > 0 {
		return &ValidationError{Fields: bad}
	}

	// The custom-provider rule is a batch rule: edits land whole or not at
	// all, so a save that moves a role onto `custom` while naming the URL in
	// the same batch is valid even though neither edit stands on its own.
	// Check the post-batch effective value, not each edit in isolation.
	if errErr := s.checkCustomProviderRule(ctx, edits); errErr != nil {
		return errErr
	}

	// The OpenAI auth-method rule is a batch rule too, for the same reason: a
	// save that picks the ChatGPT sign-in while naming an API key in the same
	// batch is the pair the hub refuses on the agent, and it has to be refused
	// here rather than mid-apply.
	if errErr := checkOpenAIAuthMethodRule(edits); errErr != nil {
		return errErr
	}

	// Resolve every edit's written value before the transaction takes the one
	// connection: a read through the store while the write holds it would wait
	// on itself (the pool is deliberately one connection deep).
	values := make([]string, len(edits))
	dirtyFlags := make([]int, len(edits))
	for i, e := range edits {
		value, dirty := e.Value, 1
		if e.Reset {
			// The row stays — a reset is "the default, as of this version",
			// not "no opinion" — so the version recorded here is the version
			// the admin re-adopted.
			value, dirty = Default(setup.Role(e.Role), e.Field), 0
		}
		values[i], dirtyFlags[i] = value, dirty
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("settings: cannot begin the settings write: %w", err)
	}
	defer tx.Rollback()

	const q = `
INSERT INTO settings (role, field, value, dirty, default_version)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT (role, field) DO UPDATE SET
	value = excluded.value,
	dirty = excluded.dirty,
	default_version = excluded.default_version;`
	for i, e := range edits {
		if isLLMKeyField(e.Field) {
			// The key's home is the hub, on the role's agents. Writing the row
			// here is the thing this field stopped doing: the plugin would be
			// holding a plaintext copy of a secret it can never read back out.
			continue
		}
		if _, err := tx.ExecContext(ctx, q, e.Role, e.Field, values[i], dirtyFlags[i], PluginVersion()); err != nil {
			return fmt.Errorf("settings: cannot save %s.%s: %w", e.Role, e.Field, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("settings: cannot commit the settings write: %w", err)
	}
	return nil
}

// checkCustomProviderRule enforces the custom provider's one hard rule
// against the post-batch state of each touched role: a role whose effective
// provider is `custom` must have a non-empty effective URL. An edit that
// moves a role onto `custom` while the stored URL is empty is refused — as is
// an edit that empties the URL under a stored `custom` — because the agent
// would be pointed at no usable endpoint. The key is never required: the hub
// treats its apiKey as optional, and a key-less endpoint is legitimate.
func (s *Store) checkCustomProviderRule(ctx context.Context, edits []Edit) error {
	type batch struct {
		provider    string
		hasProvider bool
		url         string
		hasURL      bool
	}
	byRole := map[string]*batch{}
	for _, e := range edits {
		if e.Field != FieldLLMProvider && e.Field != FieldLLMCustomUrl {
			continue
		}
		b := byRole[e.Role]
		if b == nil {
			b = &batch{}
			byRole[e.Role] = b
		}
		value := e.Value
		if e.Reset {
			value = Default(setup.Role(e.Role), e.Field)
		}
		if e.Field == FieldLLMProvider {
			b.provider, b.hasProvider = value, true
		} else {
			b.url, b.hasURL = value, true
		}
	}
	if len(byRole) == 0 {
		return nil
	}

	var bad []FieldError
	// Walk the roles in the shipped order, not the map's: with two roles
	// failing, the refusal list has to arrive in the same order every time.
	for _, role := range Roles {
		b := byRole[string(role)]
		if b == nil {
			continue
		}
		provider := b.provider
		if !b.hasProvider {
			v, err := s.Effective(ctx, role, FieldLLMProvider)
			if err != nil {
				return err
			}
			provider = v
		}
		if provider != setup.LLMProviderCustom {
			continue
		}
		url := b.url
		if !b.hasURL {
			v, err := s.Effective(ctx, role, FieldLLMCustomUrl)
			if err != nil {
				return err
			}
			url = v
		}
		if strings.TrimSpace(url) != "" {
			continue
		}
		// Name the edit that cannot stand: the provider move if this batch
		// makes it, otherwise the URL edit that empties it under a stored
		// `custom`.
		field := FieldLLMCustomUrl
		if b.hasProvider {
			field = FieldLLMProvider
		}
		bad = append(bad, FieldError{
			Role:  string(role),
			Field: field,
			Error: "custom provider needs a URL (set " + FieldLLMCustomUrl + " in the same save)",
		})
	}
	if len(bad) > 0 {
		return &ValidationError{Fields: bad}
	}
	return nil
}

// checkOpenAIAuthMethodRule refuses the one pair the hub refuses on the agent:
// a role moved onto the ChatGPT sign-in while the same batch carries an OpenAI
// API key. The hub answers that combination with a 400 ("send an API key or
// choose ChatGPT sign-in, not both"), and an apply that reached it would fail
// mid-install for a choice the admin made in one screen.
//
// Only what the batch carries counts. A key already stored under a role that
// moves to the sign-in is refused nothing, because the apply never sends it
// beside the sign-in method — it sits inert in the hub's block until a Reset
// clears it, which is the documented contract of the key rows.
func checkOpenAIAuthMethodRule(edits []Edit) error {
	type batch struct {
		method string
		signIn bool
		keySet bool
	}
	byRole := map[string]*batch{}
	for _, e := range edits {
		if e.Field != FieldLLMOpenAIAuthMethod && e.Field != FieldLLMOpenAIKey {
			continue
		}
		b := byRole[e.Role]
		if b == nil {
			b = &batch{}
			byRole[e.Role] = b
		}
		switch e.Field {
		case FieldLLMOpenAIAuthMethod:
			value := e.Value
			if e.Reset {
				value = Default(setup.Role(e.Role), e.Field)
			}
			b.method, b.signIn = value, setup.IsChatGPTSignIn(value)
		case FieldLLMOpenAIKey:
			// An empty key save keeps the stored key rather than carrying one, so it
			// is not the pair the rule is about.
			b.keySet = !e.Reset && strings.TrimSpace(e.Value) != ""
		}
	}

	var bad []FieldError
	// The shipped role order, so two failing roles refuse in the same order every
	// time — the rule the custom provider's check follows.
	for _, role := range Roles {
		b := byRole[string(role)]
		if b == nil || !b.signIn || !b.keySet {
			continue
		}
		// Name the method row: it is the edit that cannot stand beside the key.
		bad = append(bad, FieldError{
			Role:  string(role),
			Field: FieldLLMOpenAIAuthMethod,
			Error: "ChatGPT sign-in sends no API key (leave " + FieldLLMOpenAIKey + " empty to keep what the agents hold, or keep " + setup.LLMAuthMethodAPIKey + ")",
		})
	}
	if len(bad) > 0 {
		return &ValidationError{Fields: bad}
	}
	return nil
}

// MaskSecrets blanks the value of every secret-bearing row of a settings
// answer, in place: the four LLM provider key rows (isLLMKeyField). The rule
// is belt-and-braces now that no key field has a row to answer with: a field
// that must never reach a browser is blanked on the way out whatever the store
// says about it — a legacy row the migration has not forwarded yet, or a
// future one written by hand, cannot reach a console through this answer.
// The dirty flag stays, so the row still reads as edited. The store keeps the
// raw value only for the migration; only an answer being served is masked.
func MaskSecrets(settings []Setting) {
	for i := range settings {
		if isLLMKeyField(settings[i].Field) {
			settings[i].Value = ""
		}
	}
}

// PendingLLMKey is one stored provider key the upgrade migration has not
// forwarded yet: the (role, field) it was stored under and the value.
//
// This is the one read in this package that hands back a stored key, and it
// exists for exactly one caller — the startup migration (cmd/plugin) that
// forwards each key onto the role's agents and then deletes the row. It is not
// a settings answer: nothing that serves the console, the wizard or the
// reconcile pass goes through it.
type PendingLLMKey struct {
	Role  string
	Field string
	Value string
}

// PendingLLMKeys reads the key rows an older plugin left behind, in the
// registry's role and field order. A row whose value is empty is not pending:
// there is no key to forward, and the migration deletes it with the rest.
func (s *Store) PendingLLMKeys(ctx context.Context) ([]PendingLLMKey, error) {
	var out []PendingLLMKey
	for _, role := range Roles {
		for _, field := range llmKeyFields {
			const q = `SELECT value FROM settings WHERE role = ? AND field = ?;`
			var value string
			switch err := s.db.QueryRowContext(ctx, q, string(role), field).Scan(&value); {
			case errors.Is(err, sql.ErrNoRows):
				continue
			case err != nil:
				return nil, fmt.Errorf("settings: cannot read %s.%s: %w", role, field, err)
			}
			out = append(out, PendingLLMKey{Role: string(role), Field: field, Value: value})
		}
	}
	return out, nil
}

// DeleteLLMKeys removes the named key rows. The migration calls it only for a
// key it has forwarded (or one there was nowhere to forward — a role with no
// agent); a key whose forward the hub refused keeps its row and is retried on
// the next start, because until it lands, the row is the only copy there is.
func (s *Store) DeleteLLMKeys(ctx context.Context, keys []PendingLLMKey) error {
	if len(keys) == 0 {
		return nil
	}
	const q = `DELETE FROM settings WHERE role = ? AND field = ?;`
	for _, k := range keys {
		if !isLLMKeyField(k.Field) {
			return fmt.Errorf("settings: %q is not a provider key field", k.Field)
		}
		if _, err := s.db.ExecContext(ctx, q, k.Role, k.Field); err != nil {
			return fmt.Errorf("settings: cannot delete %s.%s: %w", k.Role, k.Field, err)
		}
	}
	return nil
}

// Adopted names one (role, field) the upgrade pass moved onto the current
// shipped default: the row's value and its default_version both moved, and the
// admin who never edited the field never sees the difference.
type Adopted struct {
	Role  string `json:"role"`
	Field string `json:"field"`
}

// AdoptDefaults is the winner rule of a plugin upgrade, the half that belongs
// to the store: every row that is not dirty and whose default_version names an
// older plugin version is no one's opinion, so its value and its version move
// to what this plugin ships. A dirty row is left exactly as it is — value and
// version both. The version on a dirty row is not bookkeeping to refresh: it
// is the fact the console diffs against, “this edit was made against the
// default v0.x shipped; the running v0.y ships something new” — so touching it
// on an upgrade would erase the very signal that says “new version available”.
// It returns the rows that moved, in the registry's order.
func (s *Store) AdoptDefaults(ctx context.Context) ([]Adopted, error) {
	// A key row is never adopted: its value is a stored key the upgrade
	// migration forwards onto the agents before it deletes the row, and
	// overwriting it with the empty default here would destroy the only copy
	// before anything had been forwarded.
	const q = `SELECT role, field FROM settings WHERE dirty = 0 AND default_version <> ?
		AND field NOT IN ('llmCustomKey', 'llmOpenAIKey', 'llmAnthropicKey', 'llmGoogleKey');`
	rows, err := s.db.QueryContext(ctx, q, PluginVersion())
	if err != nil {
		return nil, fmt.Errorf("settings: cannot read stale defaults: %w", err)
	}
	var stale []key
	for rows.Next() {
		var k key
		if err := rows.Scan(&k.role, &k.field); err != nil {
			rows.Close()
			return nil, fmt.Errorf("settings: cannot read a stale default: %w", err)
		}
		stale = append(stale, k)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("settings: cannot read stale defaults: %w", err)
	}
	rows.Close()

	if len(stale) == 0 {
		return nil, nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("settings: cannot begin the default write: %w", err)
	}
	defer tx.Rollback()

	const u = `UPDATE settings SET value = ?, default_version = ? WHERE role = ? AND field = ? AND dirty = 0;`
	adopted := make([]Adopted, 0, len(stale))
	for _, k := range stale {
		if _, err := tx.ExecContext(ctx, u, Default(setup.Role(k.role), k.field), PluginVersion(), k.role, k.field); err != nil {
			return nil, fmt.Errorf("settings: cannot adopt the default for %s.%s: %w", k.role, k.field, err)
		}
		adopted = append(adopted, Adopted{Role: k.role, Field: k.field})
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("settings: cannot commit the default write: %w", err)
	}

	// The registry's order, not the database's: the answer is a list of fields,
	// and a list that changes order between runs is a list nothing compares.
	sortAdopted(adopted)
	return adopted, nil
}

// sortAdopted puts adopted rows in the registry's order: roles first (in
// Roles' order), then fields (in Fields' order). A row naming a role or field
// the registry no longer knows sorts last, and stays in the answer — it moved,
// whatever it used to be called.
func sortAdopted(adopted []Adopted) {
	slices.SortFunc(adopted, func(a, b Adopted) int {
		if ra, rb := roleRank(a.Role), roleRank(b.Role); ra != rb {
			return ra - rb
		}
		return fieldRank(a.Field) - fieldRank(b.Field)
	})
}

func roleRank(role string) int {
	for i, r := range Roles {
		if string(r) == role {
			return i
		}
	}
	return len(Roles)
}

func fieldRank(field string) int {
	for i, f := range Fields {
		if f == field {
			return i
		}
	}
	return len(Fields)
}

// checkEdit refuses an edit that cannot be applied, naming it. It returns nil
// for an edit that can. A reset needs no value check: whatever was edited
// away from, the default is valid by construction.
func checkEdit(e Edit) *FieldError {
	bad := func(msg string) *FieldError {
		return &FieldError{Role: e.Role, Field: e.Field, Error: msg}
	}

	if !isRole(e.Role) {
		return bad("unknown role")
	}
	if !isField(e.Field) {
		return bad("unknown field")
	}
	if e.Reset {
		return nil
	}
	if e.Field == FieldSkillText {
		if err := checkSkillText(setup.Role(e.Role), e.Value); err != nil {
			return bad(err.Error())
		}
	}
	if e.Field == FieldLLMProvider {
		// The dropdown's list is the write path's rule too: a provider saved
		// here is a provider the apply path will name to the hub, and a typo
		// would only surface mid-work, when the model gets asked of the wrong
		// endpoint. Refuse it at the save instead.
		if !isLLMProvider(e.Value) {
			return bad("unknown LLM provider (want " + strings.Join(llmProviderValues(), ", ") + ")")
		}
	}
	if e.Field == FieldLLMOpenAIAuthMethod {
		// Same rule for the same reason: the value goes to the hub as
		// openAI.authMethod, and a string the hub does not know would surface as
		// a refused agent write, not as a refused settings save.
		if !isLLMAuthMethod(e.Value) {
			return bad("unknown LLM auth method (want " + strings.Join(llmAuthMethodValues(), " or ") + ")")
		}
	}
	return nil
}

// isLLMAuthMethod says whether a value is one of the two listed auth methods.
func isLLMAuthMethod(value string) bool {
	for _, m := range LLMAuthMethods {
		if m.Value == value {
			return true
		}
	}
	return false
}

// llmAuthMethodValues lists the auth-method values in the list's order — the
// names checkEdit spells out in its refusal.
func llmAuthMethodValues() []string {
	out := make([]string, 0, len(LLMAuthMethods))
	for _, m := range LLMAuthMethods {
		out = append(out, m.Value)
	}
	return out
}

// llmAuthMethodOptions is the auth-method dropdown for one row, built exactly
// like llmProviderOptions: the list, plus the row's stored value when it is not
// on it, labeled as such.
func llmAuthMethodOptions(value string) []LLMProvider {
	if isLLMAuthMethod(value) {
		return LLMAuthMethods
	}
	label := value + " (current, not in list)"
	if value == "" {
		label = "(empty, current value)"
	}
	return append(LLMAuthMethods[:len(LLMAuthMethods):len(LLMAuthMethods)], LLMProvider{Value: value, Label: label})
}

// isLLMProvider says whether a value is one of the listed providers.
func isLLMProvider(value string) bool {
	for _, p := range LLMProviders {
		if p.Value == value {
			return true
		}
	}
	return false
}

// llmProviderValues lists the provider ids in the list's order — the names
// checkEdit spells out in its refusal.
func llmProviderValues() []string {
	out := make([]string, 0, len(LLMProviders))
	for _, p := range LLMProviders {
		out = append(out, p.Value)
	}
	return out
}

// llmProviderOptions is the dropdown for one row: the list, plus the row's
// current value when it is not on the list — labeled as such, so an install
// holding a legacy or unknown provider stays visible and the admin can move it
// onto a listed provider instead of facing a dropdown that cannot show what is
// stored. (A Save of that stored value is still refused: the way off it is to
// pick a listed provider.)
func llmProviderOptions(value string) []LLMProvider {
	if isLLMProvider(value) {
		return LLMProviders
	}
	label := value + " (current, not in list)"
	if value == "" {
		label = "(empty, current value)"
	}
	// Capped so the append cannot write into LLMProviders' backing array.
	return append(LLMProviders[:len(LLMProviders):len(LLMProviders)], LLMProvider{Value: value, Label: label})
}

func isRole(role string) bool {
	for _, r := range Roles {
		if string(r) == role {
			return true
		}
	}
	return false
}

func isField(field string) bool {
	for _, f := range Fields {
		if f == field {
			return true
		}
	}
	return false
}

// checkSkillText enforces the one text rule the design names: a SKILL.md is
// registered by the frontmatter it opens with (its name and description), so an
// edited skill text may not drop it — as long as the shipped default for that role
// carries one, an edit must keep it.
//
// The rule is about the text standing on its own as a SKILL.md. The served
// document drops a section's frontmatter and serves the shell's, so an edit that
// lost its own would not break the document — it is an edit that cannot be
// recognised as a skill text at all.
func checkSkillText(role setup.Role, value string) error {
	if _, shipped := content.SkillFrontmatterKeys(Default(role, FieldSkillText)); !shipped {
		// Nothing shipped to keep: the rule is about not losing the
		// manifest's frontmatter, and there is none to lose.
		return nil
	}
	keys, ok := content.SkillFrontmatterKeys(value)
	if !ok {
		return errors.New("skill text must keep the SKILL.md frontmatter")
	}
	for _, name := range []string{"name", "description"} {
		if !slices.Contains(keys, name) {
			return fmt.Errorf("skill text frontmatter must keep %q", name)
		}
	}
	return nil
}
