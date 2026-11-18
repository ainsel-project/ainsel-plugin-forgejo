package reconcile

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ainsel-project/ainsel-plugin-forgejo/content"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/hub"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/settings"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/setup"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/store"
)

// TestOnceWritesTheAgentGrantTheRowHolds is the enforcement point of the whole tool model:
// the row's tool set restricts an agent through the agent's enabledTools, and the image's
// catalog only lists what the runtime offers — a catalog entry the hub holds disabled
// restricts no agent. An agent with no grant runs with everything the runtime offers, so
// the pass that reads a row with a tool set writes that set onto the agent.
func TestOnceWritesTheAgentGrantTheRowHolds(t *testing.T) {
	ctx := context.Background()
	st, cleanup := tmpStore(t)
	defer cleanup()

	// Seed an install whose image lists the role's one tool, flag disabled: the flag is
	// the catalog's own default and says nothing about what the agent may call.
	img := hub.Image{
		ID:       "img-refiner",
		ImageURL: "image:1.0",
		// The catalog as an apply left it: the discovered MCP tool plus the two reads
		// the wizard declares into the image by hand.
		Tools: catalogWithPluginReads(
			hub.ImageTool{Name: "mcp__forgejo-mcp-app__create_issue_comment", Kind: "mcp", Enabled: false},
		),
		Env:        []string{"FORGEJO_TOKEN"},
		MCPServers: []string{"forgejo-mcp-app", "plugin-forgejo"},
	}

	install := setup.Install{
		Slug:          "app",
		LayoutVersion: setup.LayoutManagedAgents,
		GroupID:       "g-1",
		Roles: map[setup.Role]setup.RoleState{
			setup.RoleRefiner: {
				AgentID:   "a-refiner",
				ImageID:   "img-refiner",
				PersonaID: "p-refiner",
				BotLogin:  "refiner-app",
				Tools:     []string{"create_issue_comment"}, // The grant the agent is owed
			},
		},
		ConnectorID: "c-app",
	}
	if err := st.Save(ctx, install); err != nil {
		t.Fatalf("Save: %v", err)
	}

	logs := []string{}
	f := &hub.Fake{
		PluginGroupsSeed: []string{"g-1"},
		SeedImages: map[string]hub.Image{
			"img-refiner": img,
		},
		Triggers: map[string][]setup.Trigger{},
		SeedAgents: map[string]hub.Agent{
			"a-refiner": {
				ID:        "a-refiner",
				ImageRef:  "img-refiner",
				PersonaID: "p-refiner",
				// No EnabledTools: the row's set has never reached the agent.
			},
		},
	}

	engine := Engine{
		Hub:   f,
		Store: st,

		Log: func(slug, msg string) {
			logs = append(logs, msg)
		},
	}

	if _, err := engine.Pass(ctx, ScopeAll); err != nil {
		t.Fatalf("Once: %v", err)
	}

	// The image is left alone: its flags restrict nobody, so there is nothing to fix there.
	if len(f.ImageToolSets) != 0 {
		t.Fatalf("ImageToolSets: got %d, want 0 — the pass no longer enables or disables catalog entries", len(f.ImageToolSets))
	}

	if len(f.UpdatedAgents) != 1 {
		t.Fatalf("UpdatedAgents: got %d, want 1", len(f.UpdatedAgents))
	}
	updated := f.UpdatedAgents[0]
	if updated.ID != "a-refiner" {
		t.Fatalf("UpdateAgent id: got %q, want %q", updated.ID, "a-refiner")
	}
	want := append([]string{"mcp__forgejo-mcp-app__create_issue_comment"}, setup.PluginToolNames()...)
	if got := grantedTo(updated.Update); !reflect.DeepEqual(got, want) {
		t.Fatalf("enabledTools: got %v, want %v — the grant is written in the catalog's spelling", got, want)
	}

	// Check log
	if len(logs) == 0 {
		t.Fatalf("Log: expected at least one log line, got %d", len(logs))
	}
	found := false
	for _, log := range logs {
		if contains(log, "enabledTools") && contains(log, "forgejo-refiner-app") {
			found = true
		}
	}
	if !found {
		t.Logf("logs: %v", logs)
		t.Fatalf("Log: did not find expected message about the agent's tool grant")
	}

	// The write is the exact wanted value, so a second pass has nothing left to write.
	f.UpdatedAgents = nil
	if _, err := engine.Pass(ctx, ScopeAll); err != nil {
		t.Fatalf("second Once: %v", err)
	}
	if len(f.UpdatedAgents) != 0 {
		t.Fatalf("second pass wrote agents again: %+v", f.UpdatedAgents[0].Update)
	}
}

// TestOnceWritesNoGrantWhenTheCatalogListsNothing pins the one rule every grant write
// obeys: an empty grant is never sent, because the runtime reads an unset one as "every
// tool the image offers". A catalog that carries none of the row's names — an image read
// that answers nothing, or a catalog the hub rewrote — leaves the agent's grant exactly as
// the hub holds it rather than clearing it.
func TestOnceWritesNoGrantWhenTheCatalogListsNothing(t *testing.T) {
	ctx := context.Background()
	st, cleanup := tmpStore(t)
	defer cleanup()

	install := setup.Install{
		Slug:          "app",
		LayoutVersion: setup.LayoutManagedAgents,
		GroupID:       "g-1",
		Roles: map[setup.Role]setup.RoleState{
			setup.RoleRefiner: {
				AgentID:   "a-refiner",
				ImageID:   "img-refiner",
				PersonaID: "p-refiner",
				BotLogin:  "refiner-app",
				Tools:     []string{"create_issue_comment"},
			},
		},
		ConnectorID: "c-app",
	}
	if err := st.Save(ctx, install); err != nil {
		t.Fatalf("Save: %v", err)
	}

	f := &hub.Fake{
		PluginGroupsSeed: []string{"g-1"},
		SeedImages: map[string]hub.Image{
			"img-refiner": {ID: "img-refiner", ImageURL: "image:1.0"},
		},
		Triggers: map[string][]setup.Trigger{},
		SeedAgents: map[string]hub.Agent{
			"a-refiner": {
				ID:           "a-refiner",
				ImageRef:     "img-refiner",
				PersonaID:    "p-refiner",
				EnabledTools: []string{"mcp__forgejo-mcp-app__create_issue_comment"},
			},
		},
	}

	engine := Engine{Hub: f, Store: st, Log: func(slug, msg string) {}}
	if _, err := engine.Pass(ctx, ScopeAll); err != nil {
		t.Fatalf("Once: %v", err)
	}

	for _, u := range f.UpdatedAgents {
		if u.Update.EnabledTools != nil {
			t.Fatalf("UpdateAgent carried a grant (%v), want none — an empty grant is a widening", *u.Update.EnabledTools)
		}
	}
	if got := f.SeedAgents["a-refiner"].EnabledTools; len(got) != 1 {
		t.Fatalf("enabledTools: got %v, want the grant the hub held left alone", got)
	}
}

// grantedTo is the tool grant one agent update carried, nil when it carried none.
func grantedTo(u hub.AgentUpdate) []string {
	if u.EnabledTools == nil {
		return nil
	}
	return *u.EnabledTools
}

func TestOnceSimilarResourceNotTouched(t *testing.T) {
	ctx := context.Background()
	st, cleanup := tmpStore(t)
	defer cleanup()

	install := setup.Install{
		Slug:          "app",
		LayoutVersion: setup.LayoutManagedAgents,
		GroupID:       "g-1",
		Roles: map[setup.Role]setup.RoleState{
			setup.RoleRefiner: {
				AgentID:   "a-refiner",
				ImageID:   "img-refiner",
				PersonaID: "p-refiner",
				BotLogin:  "refiner-app",
			},
		},
		ConnectorID: "c-app",
	}
	if err := st.Save(ctx, install); err != nil {
		t.Fatalf("Save: %v", err)
	}

	f := &hub.Fake{
		PluginGroupsSeed: []string{"g-1"},
		SeedImages: map[string]hub.Image{
			"img-refiner": {
				ID:         "img-refiner",
				ImageURL:   "image:1.0",
				Tools:      []hub.ImageTool{},
				Env:        []string{"FORGEJO_TOKEN"},
				MCPServers: []string{"forgejo-mcp-app", "plugin-forgejo"},
			},
		},
		Triggers: map[string][]setup.Trigger{},
		SeedAgents: map[string]hub.Agent{
			"a-refiner": {
				ID:        "a-refiner",
				ImageRef:  "img-refiner",
				PersonaID: "p-refiner",
			},
		},
	}

	engine := Engine{
		Hub:   f,
		Store: st,

		Log: func(slug, msg string) {
			// No logs expected for unchanged state
		},
	}

	if _, err := engine.Pass(ctx, ScopeAll); err != nil {
		t.Fatalf("Once: %v", err)
	}

	// A similar-named image that is NOT in the install should not be touched
	// The fake seeds only "img-refiner" which is in the install, so we just verify
	// that the calls made were only for resources we know about.
	if len(f.DeletedAgentImages) > 0 {
		t.Fatalf("DeletedAgentImages: got %d, want 0 (should not delete other images)", len(f.DeletedAgentImages))
	}
}

func TestOnceSkipsGroupNotInPluginGroups(t *testing.T) {
	ctx := context.Background()
	st, cleanup := tmpStore(t)
	defer cleanup()

	install := setup.Install{
		Slug:          "app",
		LayoutVersion: setup.LayoutManagedAgents,
		GroupID:       "g-unknown", // Not in plugin groups
		Roles: map[setup.Role]setup.RoleState{
			setup.RoleRefiner: {
				AgentID:   "a-refiner",
				ImageID:   "img-refiner",
				PersonaID: "p-refiner",
				BotLogin:  "refiner-app",
			},
		},
		ConnectorID: "c-app",
	}
	if err := st.Save(ctx, install); err != nil {
		t.Fatalf("Save: %v", err)
	}

	f := &hub.Fake{
		PluginGroupsSeed: []string{"g-1"}, // Does not include g-unknown
		SeedImages: map[string]hub.Image{
			"img-refiner": {
				ID:       "img-refiner",
				ImageURL: "image:1.0",
				Tools:    []hub.ImageTool{},
			},
		},
		Triggers: map[string][]setup.Trigger{},
	}

	callsMade := []string{}
	engine := Engine{
		Hub:   f,
		Store: st,

		Log: func(slug, msg string) {
			callsMade = append(callsMade, msg)
		},
	}

	if _, err := engine.Pass(ctx, ScopeAll); err != nil {
		t.Fatalf("Once: %v", err)
	}

	// Check that we called PluginGroups
	found := false
	for _, call := range f.Calls {
		if call == "PluginGroups" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("PluginGroups should be called")
	}

	// GetAgentImage should not be called for a skipped install
	getAgentImageCount := 0
	for _, call := range f.Calls {
		if call == "GetAgentImage" {
			getAgentImageCount++
		}
	}
	if getAgentImageCount > 0 {
		t.Fatalf("GetAgentImage should not be called for skipped install, but was called %d times", getAgentImageCount)
	}
}

func TestOnceContextIsMarkedBackground(t *testing.T) {
	ctx := context.Background()
	st, cleanup := tmpStore(t)
	defer cleanup()

	install := setup.Install{
		Slug:          "app",
		LayoutVersion: setup.LayoutManagedAgents,
		GroupID:       "g-1",
		Roles:         make(map[setup.Role]setup.RoleState),
		ConnectorID:   "c-app",
	}
	if err := st.Save(ctx, install); err != nil {
		t.Fatalf("Save: %v", err)
	}

	f := &hub.Fake{
		PluginGroupsSeed: []string{"g-1"},
		Triggers:         map[string][]setup.Trigger{},
	}

	engine := Engine{
		Hub:   f,
		Store: st,

		Log: func(slug, msg string) {},
	}

	if _, err := engine.Pass(ctx, ScopeAll); err != nil {
		t.Fatalf("Once: %v", err)
	}

	// Verify that PluginGroups was called, which is a hub call that
	// requires WithBackground context
	found := false
	for _, call := range f.Calls {
		if call == "PluginGroups" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("PluginGroups should be called with background context")
	}
}

func TestOnceIssueCommentEventLandsInCoverage(t *testing.T) {
	ctx := context.Background()
	st, cleanup := tmpStore(t)
	defer cleanup()

	install := setup.Install{
		Slug:          "app",
		LayoutVersion: setup.LayoutManagedAgents,
		GroupID:       "g-1",
		Roles: map[setup.Role]setup.RoleState{
			setup.RoleRefiner: {
				AgentID:   "a-refiner",
				ImageID:   "img-refiner",
				PersonaID: "p-refiner",
				BotLogin:  "refiner-app",
			},
		},
		ConnectorID: "c-app",
		Coverage:    make(map[string]time.Time),
	}
	if err := st.Save(ctx, install); err != nil {
		t.Fatalf("Save: %v", err)
	}

	now := time.Now()
	f := &hub.Fake{
		PluginGroupsSeed: []string{"g-1"},
		SeedImages: map[string]hub.Image{
			"img-refiner": {
				ID:         "img-refiner",
				ImageURL:   "image:1.0",
				Tools:      []hub.ImageTool{},
				Env:        []string{"FORGEJO_TOKEN"},
				MCPServers: []string{"forgejo-mcp-app", "plugin-forgejo"},
			},
		},
		Triggers: map[string][]setup.Trigger{},
		SeedAgents: map[string]hub.Agent{
			"a-refiner": {
				ID:        "a-refiner",
				ImageRef:  "img-refiner",
				PersonaID: "p-refiner",
			},
		},
		SeedEvents: []hub.Event{
			{ID: "e-1", Type: "issue_comment", At: now},
		},
	}

	engine := Engine{
		Hub:   f,
		Store: st,

		Log: func(slug, msg string) {},
	}

	if _, err := engine.Pass(ctx, ScopeAll); err != nil {
		t.Fatalf("Once: %v", err)
	}

	// Read back the install from store
	installs, err := st.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(installs) != 1 {
		t.Fatalf("installs: got %d, want 1", len(installs))
	}

	updatedInstall := installs[0]

	// Check that the issue_comment event was folded into Coverage
	if _, ok := updatedInstall.Coverage["issue_comment"]; !ok {
		t.Fatalf("Coverage[issue_comment] not found")
	}
}

func TestOnceMissingTriggerIsCreated(t *testing.T) {
	ctx := context.Background()
	st, cleanup := tmpStore(t)
	defer cleanup()

	install := setup.Install{
		Slug:          "app",
		LayoutVersion: setup.LayoutManagedAgents,
		GroupID:       "g-1",
		Roles: map[setup.Role]setup.RoleState{
			setup.RoleRefiner: {
				AgentID:   "a-refiner",
				ImageID:   "img-refiner",
				PersonaID: "p-refiner",
				BotLogin:  "refiner-app",
			},
		},
		ConnectorID: "c-app",
	}
	if err := st.Save(ctx, install); err != nil {
		t.Fatalf("Save: %v", err)
	}

	f := &hub.Fake{
		PluginGroupsSeed: []string{"g-1"},
		SeedImages: map[string]hub.Image{
			"img-refiner": {
				ID:         "img-refiner",
				ImageURL:   "image:1.0",
				Tools:      []hub.ImageTool{},
				Env:        []string{"FORGEJO_TOKEN"},
				MCPServers: []string{"forgejo-mcp-app", "plugin-forgejo"},
			},
		},
		// No existing triggers - so all will be missing
		Triggers: map[string][]setup.Trigger{},
		SeedAgents: map[string]hub.Agent{
			"a-refiner": {
				ID:        "a-refiner",
				ImageRef:  "img-refiner",
				PersonaID: "p-refiner",
			},
		},
	}

	engine := Engine{
		Hub:   f,
		Store: st,

		Log: func(slug, msg string) {},
	}

	if _, err := engine.Pass(ctx, ScopeAll); err != nil {
		t.Fatalf("Once: %v", err)
	}

	// Should have created at least one trigger (refiner issue-opened)
	if len(f.CreatedTriggers) < 1 {
		t.Fatalf("CreatedTriggers: got %d, want at least 1", len(f.CreatedTriggers))
	}

	// Verify the created trigger is the issue-opened trigger
	trigger := f.CreatedTriggers[0]
	if !strings.Contains(trigger.Name, "issue-opened") {
		t.Fatalf("First trigger should be issue-opened, got %q", trigger.Name)
	}
	if trigger.AgentRef != "a-refiner" {
		t.Fatalf("Trigger agentRef: got %q, want a-refiner", trigger.AgentRef)
	}
}

func TestOnceCoverageStartsEmpty(t *testing.T) {
	ctx := context.Background()
	st, cleanup := tmpStore(t)
	defer cleanup()

	install := setup.Install{
		Slug:          "app",
		LayoutVersion: setup.LayoutManagedAgents,
		GroupID:       "g-1",
		Roles: map[setup.Role]setup.RoleState{
			setup.RoleRefiner: {
				AgentID:   "a-refiner",
				ImageID:   "img-refiner",
				PersonaID: "p-refiner",
				BotLogin:  "refiner-app",
			},
		},
		ConnectorID: "c-app",
		Coverage:    nil, // Start with no coverage
	}
	if err := st.Save(ctx, install); err != nil {
		t.Fatalf("Save: %v", err)
	}

	f := &hub.Fake{
		PluginGroupsSeed: []string{"g-1"},
		SeedImages: map[string]hub.Image{
			"img-refiner": {
				ID:         "img-refiner",
				ImageURL:   "image:1.0",
				Tools:      []hub.ImageTool{},
				Env:        []string{"FORGEJO_TOKEN"},
				MCPServers: []string{"forgejo-mcp-app", "plugin-forgejo"},
			},
		},
		Triggers: map[string][]setup.Trigger{},
		SeedAgents: map[string]hub.Agent{
			"a-refiner": {
				ID:        "a-refiner",
				ImageRef:  "img-refiner",
				PersonaID: "p-refiner",
			},
		},
		SeedEvents: []hub.Event{},
	}

	engine := Engine{
		Hub:   f,
		Store: st,

		Log: func(slug, msg string) {},
	}

	if _, err := engine.Pass(ctx, ScopeAll); err != nil {
		t.Fatalf("Once: %v", err)
	}

	// Read back the install
	installs, err := st.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(installs) != 1 {
		t.Fatalf("installs: got %d, want 1", len(installs))
	}

	// Coverage should be initialized (even if empty)
	if installs[0].Coverage == nil {
		t.Fatalf("Coverage should not be nil after pass")
	}
}

func TestOnceAgentDriftImageRefAndPersonaReset(t *testing.T) {
	ctx := context.Background()
	st, cleanup := tmpStore(t)
	defer cleanup()

	install := setup.Install{
		Slug:          "app",
		LayoutVersion: setup.LayoutManagedAgents,
		GroupID:       "g-1",
		Roles: map[setup.Role]setup.RoleState{
			setup.RoleRefiner: {
				AgentID:   "a-refiner",
				ImageID:   "img-refiner",
				PersonaID: "p-refiner",
				BotLogin:  "refiner-app",
			},
		},
		ConnectorID: "c-app",
	}
	if err := st.Save(ctx, install); err != nil {
		t.Fatalf("Save: %v", err)
	}

	logs := []string{}
	f := &hub.Fake{
		PluginGroupsSeed: []string{"g-1"},
		SeedImages: map[string]hub.Image{
			"img-refiner": {
				ID:         "img-refiner",
				ImageURL:   "image:1.0",
				Tools:      []hub.ImageTool{},
				Env:        []string{"FORGEJO_TOKEN"},
				MCPServers: []string{"forgejo-mcp-app", "plugin-forgejo"},
			},
		},
		Triggers: map[string][]setup.Trigger{},
		// Agent has drifted: imageRef and persona don't match install
		SeedAgents: map[string]hub.Agent{
			"a-refiner": {
				ID:        "a-refiner",
				ImageRef:  "img-wrong", // Drifted from img-refiner
				PersonaID: "p-wrong",   // Drifted from p-refiner
			},
		},
	}

	engine := Engine{
		Hub:   f,
		Store: st,

		Log: func(slug, msg string) {
			logs = append(logs, msg)
		},
	}

	if _, err := engine.Pass(ctx, ScopeAll); err != nil {
		t.Fatalf("Once: %v", err)
	}

	// Check that UpdateAgent was called
	if len(f.UpdatedAgents) != 1 {
		t.Fatalf("UpdatedAgents: got %d, want 1", len(f.UpdatedAgents))
	}

	updated := f.UpdatedAgents[0]
	if updated.ID != "a-refiner" {
		t.Fatalf("UpdateAgent id: got %q, want %q", updated.ID, "a-refiner")
	}

	// Check that both imageRef and persona were updated
	if updated.Update.ImageRef == nil || *updated.Update.ImageRef != "img-refiner" {
		t.Fatalf("UpdateAgent imageRef: got %v, want ptr to img-refiner", updated.Update.ImageRef)
	}
	if updated.Update.PersonaID == nil || *updated.Update.PersonaID != "p-refiner" {
		t.Fatalf("UpdateAgent personaID: got %v, want ptr to p-refiner", updated.Update.PersonaID)
	}

	// Check logs for reset messages
	if len(logs) < 2 {
		t.Fatalf("logs: expected at least 2 log lines, got %d", len(logs))
	}
	foundImageRef := false
	foundPersona := false
	for _, log := range logs {
		if contains(log, "reset imageRef") && contains(log, "forgejo-refiner-app") {
			foundImageRef = true
		}
		if contains(log, "reset persona") && contains(log, "forgejo-refiner-app") {
			foundPersona = true
		}
	}
	if !foundImageRef {
		t.Logf("logs: %v", logs)
		t.Fatalf("Log: did not find expected message about resetting imageRef")
	}
	if !foundPersona {
		t.Logf("logs: %v", logs)
		t.Fatalf("Log: did not find expected message about resetting persona")
	}
}

func TestOnceAgentDriftLLMReset(t *testing.T) {
	// §3.6 counts "agent image, persona and llm" as managed state: an admin who points the
	// agent at another model gets it set back, the same way a wrong imageRef is.
	ctx := context.Background()
	st, cleanup := tmpStore(t)
	defer cleanup()

	install := setup.Install{
		Slug:          "app",
		LayoutVersion: setup.LayoutManagedAgents,
		GroupID:       "g-1",
		Roles: map[setup.Role]setup.RoleState{
			setup.RoleRefiner: {
				AgentID:     "a-refiner",
				ImageID:     "img-refiner",
				PersonaID:   "p-refiner",
				BotLogin:    "refiner-app",
				LLMProvider: "ollama-cloud",
				LLMModel:    "llama3.3",
			},
		},
		ConnectorID: "c-app",
	}
	if err := st.Save(ctx, install); err != nil {
		t.Fatalf("Save: %v", err)
	}

	logs := []string{}
	f := &hub.Fake{
		PluginGroupsSeed: []string{"g-1"},
		SeedImages: map[string]hub.Image{
			"img-refiner": {ID: "img-refiner", ImageURL: "image:1.0"},
		},
		Triggers: map[string][]setup.Trigger{},
		SeedAgents: map[string]hub.Agent{
			"a-refiner": {
				ID:        "a-refiner",
				ImageRef:  "img-refiner",
				PersonaID: "p-refiner",
				// Drifted: somebody pointed the agent at another model.
				LLM: &hub.LLMSpec{Provider: "opencode", Model: "gpt-5"},
			},
		},
	}

	engine := Engine{Hub: f, Store: st, Log: func(slug, msg string) {
		logs = append(logs, msg)
	}}
	if _, err := engine.Pass(ctx, ScopeAll); err != nil {
		t.Fatalf("Once: %v", err)
	}

	if len(f.UpdatedAgents) != 1 {
		t.Fatalf("UpdatedAgents: got %d, want 1", len(f.UpdatedAgents))
	}
	got := f.UpdatedAgents[0].Update.LLM
	if got == nil {
		t.Fatalf("UpdateAgent llm: got nil, want the stored provider and model back")
	}
	if got.Provider != "ollama-cloud" || got.Model != "llama3.3" {
		t.Fatalf("UpdateAgent llm: got %+v, want ollama-cloud/llama3.3", *got)
	}

	found := false
	for _, log := range logs {
		if contains(log, "reset llm") && contains(log, "forgejo-refiner-app") {
			found = true
		}
	}
	if !found {
		t.Logf("logs: %v", logs)
		t.Fatalf("Log: no message about resetting the llm")
	}
}

func TestOnceSettingsWithoutALLMLeavesTheAgentAlone(t *testing.T) {
	// The settings are the source of the model now — but a settings reading that
	// names nothing for the role says nothing, and saying nothing must not read
	// as "set this agent to no model". The agent keeps what the hub has.
	ctx := context.Background()
	st, cleanup := tmpStore(t)
	defer cleanup()

	install := setup.Install{
		Slug:          "app",
		LayoutVersion: setup.LayoutManagedAgents,
		GroupID:       "g-1",
		Roles: map[setup.Role]setup.RoleState{
			setup.RoleRefiner: {
				AgentID:     "a-refiner",
				ImageID:     "img-refiner",
				PersonaID:   "p-refiner",
				BotLogin:    "refiner-app",
				LLMProvider: "opencode",
				LLMModel:    "gpt-5",
			},
		},
		ConnectorID: "c-app",
	}
	if err := st.Save(ctx, install); err != nil {
		t.Fatalf("Save: %v", err)
	}

	f := &hub.Fake{
		PluginGroupsSeed: []string{"g-1"},
		SeedImages: map[string]hub.Image{
			"img-refiner": {ID: "img-refiner", ImageURL: "image:1.0"},
		},
		Triggers: map[string][]setup.Trigger{},
		SeedAgents: map[string]hub.Agent{
			"a-refiner": {
				ID:         "a-refiner",
				ImageRef:   "img-refiner",
				PersonaID:  "p-refiner",
				LLM:        &hub.LLMSpec{Provider: "opencode", Model: "gpt-5"},
				MCPServers: []string{"forgejo-mcp-app", "plugin-forgejo"},
				Skills:     []string{"forgejo-forgejo"},
			},
		},
	}

	// The settings answer with the two llm fields empty — the shape an admin
	// leaves when the model is the image's business, not theirs.
	silent := fakeSettings{rows: []settings.Setting{
		{Role: "refiner", Field: settings.FieldLLMProvider, Value: ""},
		{Role: "refiner", Field: settings.FieldLLMModel, Value: ""},
	}}

	engine := Engine{Hub: f, Store: st, Settings: silent, Log: func(slug, msg string) {}}
	if _, err := engine.Pass(ctx, ScopeAll); err != nil {
		t.Fatalf("Pass: %v", err)
	}

	if len(f.UpdatedAgents) != 0 {
		t.Fatalf("UpdatedAgents: got %d, want 0 — settings naming no model must not clear one: %+v", len(f.UpdatedAgents), f.UpdatedAgents[0].Update)
	}
}

func TestOnceAllHubCallsUseBackground(t *testing.T) {
	ctx := context.Background()
	st, cleanup := tmpStore(t)
	defer cleanup()

	install := setup.Install{
		Slug:          "app",
		LayoutVersion: setup.LayoutManagedAgents,
		GroupID:       "g-1",
		Roles: map[setup.Role]setup.RoleState{
			setup.RoleDeveloper: {
				AgentID:   "a-developer",
				ImageID:   "img-developer",
				PersonaID: "p-developer",
				BotLogin:  "developer-app",
			},
		},
		ConnectorID: "c-app",
	}
	if err := st.Save(ctx, install); err != nil {
		t.Fatalf("Save: %v", err)
	}

	f := &hub.Fake{
		PluginGroupsSeed: []string{"g-1"},
		SeedImages: map[string]hub.Image{
			"img-developer": {
				ID:         "img-developer",
				ImageURL:   "image:1.0",
				Tools:      []hub.ImageTool{},
				Env:        []string{"FORGEJO_TOKEN"},
				MCPServers: []string{"forgejo-mcp-app", "plugin-forgejo"},
			},
		},
		Triggers: map[string][]setup.Trigger{},
		SeedAgents: map[string]hub.Agent{
			"a-developer": {
				ID:        "a-developer",
				ImageRef:  "img-developer",
				PersonaID: "p-developer",
			},
		},
		SeedEvents: []hub.Event{},
	}

	engine := Engine{
		Hub:   f,
		Store: st,

		Log: func(slug, msg string) {},
	}

	if _, err := engine.Pass(ctx, ScopeAll); err != nil {
		t.Fatalf("Once: %v", err)
	}

	// Verify that all expected hub methods were called.
	// Since Once wraps the context with hub.WithBackground at the start,
	// all hub calls receive the background-marked context. If a method
	// that requires background context (like PluginGroups) succeeds, it
	// proves the context had the background marker.
	expectedMethods := []string{"PluginGroups", "GetAgentImage", "GetAgent", "ListTriggers", "Events"}
	for _, method := range expectedMethods {
		found := false
		for _, call := range f.Calls {
			if call == method {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("Expected method %q not found in calls: %v", method, f.Calls)
		}
	}
}

// TestReconcileKeepsBashTheRowGives: a hand-declared shell tool the catalog still carries
// is granted like any other name — the pass reads the catalog, and the entry is in it.
func TestReconcileKeepsBashTheRowGives(t *testing.T) {
	// apply grants a requested tool under whichever name the catalog has: the bare name when
	// the catalog lists it, and bash under its bare name because the hub discovers no shell
	// tools. The row keeps the bare names, so the pass has to accept both spellings — the
	// MCP-spelled one it assumed and the bare one apply actually wrote — or it would read a
	// grant an apply just wrote as drift and rewrite it every pass.
	ctx := context.Background()
	st, cleanup := tmpStore(t)
	defer cleanup()

	install := setup.Install{
		Slug:          "app",
		LayoutVersion: setup.LayoutManagedAgents,
		GroupID:       "g-1",
		Roles: map[setup.Role]setup.RoleState{
			setup.RoleReviewer: {
				AgentID:   "a-reviewer",
				ImageID:   "img-reviewer",
				PersonaID: "p-reviewer",
				BotLogin:  "reviewer-app",
				Tools:     []string{"bash", "get_issue"},
			},
		},
		ConnectorID: "c-app",
	}
	if err := st.Save(ctx, install); err != nil {
		t.Fatalf("Save: %v", err)
	}

	f := &hub.Fake{
		PluginGroupsSeed: []string{"g-1"},
		SeedImages: map[string]hub.Image{
			"img-reviewer": {
				ID:       "img-reviewer",
				ImageURL: "image:1.0",
				Tools: catalogWithPluginReads(
					hub.ImageTool{Name: "bash", Kind: "shell", Enabled: true},
					hub.ImageTool{Name: "mcp__forgejo-mcp-app__get_issue", Kind: "mcp", Enabled: true},
				),
			},
		},
		Triggers: map[string][]setup.Trigger{},
		SeedAgents: map[string]hub.Agent{
			"a-reviewer": {
				ID:        "a-reviewer",
				ImageRef:  "img-reviewer",
				PersonaID: "p-reviewer",
				// The grant as the row spells it, while the catalog carries the MCP tool
				// under its prefixed name. Both read as one set.
				EnabledTools: []string{"bash", "get_issue"},
			},
		},
	}

	engine := Engine{Hub: f, Store: st, Log: func(slug, msg string) {}}
	if _, err := engine.Pass(ctx, ScopeAll); err != nil {
		t.Fatalf("Once: %v", err)
	}

	if len(f.ImageToolSets) != 0 {
		t.Fatalf("ImageToolSets: got %d, want 0 — the row gives bash and the bare-named tool, so nothing drifted",
			len(f.ImageToolSets))
	}

	// The grant the row resolves to is the catalog's two names plus the plugin's own two
	// reads, which every grant carries. The agent holds the same Forgejo set under the row's
	// spelling, so the write is the prefixed spelling and the two names it was missing.
	want := []string{"bash", "mcp__forgejo-mcp-app__get_issue"}
	want = append(want, setup.PluginToolNames()...)
	if len(f.UpdatedAgents) != 1 {
		t.Fatalf("UpdatedAgents: got %d, want 1 — the grant is restated in the catalog's spelling", len(f.UpdatedAgents))
	}
	if got := grantedTo(f.UpdatedAgents[0].Update); !reflect.DeepEqual(got, want) {
		t.Fatalf("enabledTools: got %v, want %v", got, want)
	}
}

// TestReconcileDropsBashTheRowDropped is the same rule from the other side: a grant is the
// row's tool set and nothing else, so a tool an admin took off the developer comes off the
// agent the same way any other does. The catalog keeps its own entry — the pass no longer
// switches catalog entries off.
func TestReconcileDropsBashTheRowDropped(t *testing.T) {
	// Bash is granted exactly when the row lists it.
	ctx := context.Background()
	st, cleanup := tmpStore(t)
	defer cleanup()

	install := setup.Install{
		Slug:          "app",
		LayoutVersion: setup.LayoutManagedAgents,
		GroupID:       "g-1",
		Roles: map[setup.Role]setup.RoleState{
			setup.RoleDeveloper: {
				AgentID:   "a-developer",
				ImageID:   "img-developer",
				PersonaID: "p-developer",
				BotLogin:  "developer-app",
				Tools:     []string{"get_issue"},
			},
		},
		ConnectorID: "c-app",
	}
	if err := st.Save(ctx, install); err != nil {
		t.Fatalf("Save: %v", err)
	}

	f := &hub.Fake{
		PluginGroupsSeed: []string{"g-1"},
		SeedImages: map[string]hub.Image{
			"img-developer": {
				ID:       "img-developer",
				ImageURL: "image:1.0",
				Tools: catalogWithPluginReads(
					hub.ImageTool{Name: "bash", Kind: "shell", Enabled: true},
					hub.ImageTool{Name: "mcp__forgejo-mcp-app__get_issue", Kind: "mcp", Enabled: true},
				),
			},
		},
		Triggers: map[string][]setup.Trigger{},
		SeedAgents: map[string]hub.Agent{
			"a-developer": {
				ID:           "a-developer",
				ImageRef:     "img-developer",
				PersonaID:    "p-developer",
				EnabledTools: []string{"bash", "mcp__forgejo-mcp-app__get_issue"},
			},
		},
	}

	engine := Engine{Hub: f, Store: st, Log: func(slug, msg string) {}}
	if _, err := engine.Pass(ctx, ScopeAll); err != nil {
		t.Fatalf("Once: %v", err)
	}

	if len(f.ImageToolSets) != 0 {
		t.Fatalf("ImageToolSets: got %d, want 0 — the catalog is not the pass's to switch off", len(f.ImageToolSets))
	}

	if len(f.UpdatedAgents) != 1 {
		t.Fatalf("UpdatedAgents: got %d, want 1 — bash is not in the row and has to come off the grant", len(f.UpdatedAgents))
	}
	want := append([]string{"mcp__forgejo-mcp-app__get_issue"}, setup.PluginToolNames()...)
	if got := grantedTo(f.UpdatedAgents[0].Update); !reflect.DeepEqual(got, want) {
		t.Fatalf("enabledTools: got %v, want %v", got, want)
	}
}

// TestReconcileDropsBashTheCatalogDropped is the other half of "the catalog is not the pass's
// to write": a hand-declared entry a refresh lost cannot be put back here, and naming it in
// a grant would be a write the hub refuses — which would leave every other correction this
// role needs unwritten. So the name comes out of the grant, the log says so, and the rest of
// the grant still lands.
func TestReconcileDropsBashTheCatalogDropped(t *testing.T) {
	ctx := context.Background()
	st, cleanup := tmpStore(t)
	defer cleanup()

	install := setup.Install{
		Slug:          "app",
		LayoutVersion: setup.LayoutManagedAgents,
		GroupID:       "g-1",
		Roles: map[setup.Role]setup.RoleState{
			setup.RoleDeveloper: {
				AgentID:   "a-developer",
				ImageID:   "img-developer",
				PersonaID: "p-developer",
				BotLogin:  "developer-app",
				Tools:     []string{"bash", "get_issue"},
			},
		},
		ConnectorID: "c-app",
	}
	if err := st.Save(ctx, install); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// The catalog a refresh left behind: the shell declaration is gone.
	f := &hub.Fake{
		PluginGroupsSeed: []string{"g-1"},
		SeedImages: map[string]hub.Image{
			"img-developer": {
				ID:       "img-developer",
				ImageURL: "image:1.0",
				Tools: catalogWithPluginReads(
					hub.ImageTool{Name: "mcp__forgejo-mcp-app__get_issue", Kind: "mcp", Enabled: true},
				),
			},
		},
		Triggers: map[string][]setup.Trigger{},
		SeedAgents: map[string]hub.Agent{
			"a-developer": {
				ID:        "a-developer",
				ImageRef:  "img-developer",
				PersonaID: "p-developer",
			},
		},
	}

	var logs []string
	engine := Engine{Hub: f, Store: st, Log: func(slug, msg string) { logs = append(logs, msg) }}
	if _, err := engine.Pass(ctx, ScopeAll); err != nil {
		t.Fatalf("Once: %v", err)
	}

	if len(f.UpdatedAgents) != 1 {
		t.Fatalf("UpdatedAgents: got %d, want 1 — the rest of the grant is still the pass's to write", len(f.UpdatedAgents))
	}
	want := append([]string{"mcp__forgejo-mcp-app__get_issue"}, setup.PluginToolNames()...)
	if got := grantedTo(f.UpdatedAgents[0].Update); !reflect.DeepEqual(got, want) {
		t.Fatalf("enabledTools: got %v, want %v — a name the catalog dropped is left out of the grant", got, want)
	}

	named := false
	for _, log := range logs {
		if contains(log, "bash") && contains(log, "left out") {
			named = true
		}
	}
	if !named {
		t.Errorf("logs = %q, want one naming bash as left out of the grant", logs)
	}
}

// catalogWithPluginReads is a catalog as an apply leaves it: the tools the refresh
// discovered plus the plugin's own two read-only tools, which setRoleTools declares into the
// image by hand because the hub cannot list the plugin's own MCP server. Reconcile reads the
// catalog and never writes it, so a test whose grant is expected to name those two has to
// seed them — that is the state apply creates.
func catalogWithPluginReads(tools ...hub.ImageTool) []hub.ImageTool {
	for _, name := range setup.PluginToolNames() {
		tools = append(tools, hub.ImageTool{Name: name, Kind: "mcp", Enabled: true})
	}
	return tools
}

// TestReconcileLeavesOutPluginReadsTheCatalogLacks is the legacy-row case: an install an
// older plugin version created has a catalog that never carried the plugin's own two reads,
// because the old code declared only the Forgejo tools and bash. The catalog is the hub's
// grant allowlist, so naming them there would be a write the hub refuses — and it rides the
// same AgentUpdate as the name/llm/skills corrections, which would leave every other fix this
// pass owes the role unwritten until an admin re-applies. So they are treated like any other
// catalog-absent name: left out, logged, and the rest of the grant still lands.
func TestReconcileLeavesOutPluginReadsTheCatalogLacks(t *testing.T) {
	ctx := context.Background()
	st, cleanup := tmpStore(t)
	defer cleanup()

	install := setup.Install{
		Slug:          "app",
		LayoutVersion: setup.LayoutManagedAgents,
		GroupID:       "g-1",
		Roles: map[setup.Role]setup.RoleState{
			setup.RoleDeveloper: {
				AgentID:   "a-developer",
				ImageID:   "img-developer",
				PersonaID: "p-developer",
				BotLogin:  "developer-app",
				Tools:     []string{"get_issue"},
			},
		},
		ConnectorID: "c-app",
	}
	if err := st.Save(ctx, install); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// The catalog an older plugin version left behind: the row's tool is there, the
	// plugin's own two reads never were.
	f := &hub.Fake{
		PluginGroupsSeed: []string{"g-1"},
		SeedImages: map[string]hub.Image{
			"img-developer": {
				ID:       "img-developer",
				ImageURL: "image:1.0",
				Tools: []hub.ImageTool{
					{Name: "mcp__forgejo-mcp-app__get_issue", Kind: "mcp", Enabled: true},
				},
			},
		},
		Triggers: map[string][]setup.Trigger{},
		SeedAgents: map[string]hub.Agent{
			"a-developer": {
				ID:        "a-developer",
				ImageRef:  "img-developer",
				PersonaID: "p-developer",
			},
		},
	}

	var logs []string
	engine := Engine{Hub: f, Store: st, Log: func(slug, msg string) { logs = append(logs, msg) }}
	if _, err := engine.Pass(ctx, ScopeAll); err != nil {
		t.Fatalf("Once: %v", err)
	}

	if len(f.UpdatedAgents) != 1 {
		t.Fatalf("UpdatedAgents: got %d, want 1 — the correction the row needs must not ride on the two names", len(f.UpdatedAgents))
	}
	got := grantedTo(f.UpdatedAgents[0].Update)
	if len(got) != 1 || got[0] != "mcp__forgejo-mcp-app__get_issue" {
		t.Fatalf("enabledTools: got %v, want only the catalogued tool — a grant naming an un-catalogued name is a write the hub refuses", got)
	}

	for _, name := range setup.PluginToolNames() {
		named := false
		for _, log := range logs {
			if contains(log, name) && contains(log, "left out") {
				named = true
			}
		}
		if !named {
			t.Errorf("logs = %q, want one naming %s as left out of the grant", logs, name)
		}
	}
}

func TestReconcileDoesNotOverwriteConcurrentEdit(t *testing.T) {
	// The pass reads a row, works on it, and writes it back. An edit the API saved while the
	// pass ran must survive it: the loop owns coverage, and nothing else on the row.
	ctx := context.Background()
	seen := time.Now().Add(-time.Hour)
	newEvent := time.Now()

	v1 := setup.Install{
		Slug:          "app",
		LayoutVersion: setup.LayoutManagedAgents,
		GroupID:       "g-1",
		ConnectorID:   "c-app",
		ExcludeLabel:  "do-not-refine",
		Roles: map[setup.Role]setup.RoleState{
			setup.RoleRefiner: {
				AgentID:   "a-refiner",
				ImageID:   "img-refiner",
				PersonaID: "p-refiner",
				BotLogin:  "refiner-app",
			},
		},
		Coverage: map[string]time.Time{"issues": seen},
	}
	st := &fakeStore{rows: []setup.Install{v1}}

	f := &hub.Fake{
		PluginGroupsSeed: []string{"g-1"},
		SeedImages: map[string]hub.Image{
			"img-refiner": {ID: "img-refiner", ImageURL: "image:1.0"},
		},
		Triggers: map[string][]setup.Trigger{},
		SeedAgents: map[string]hub.Agent{
			"a-refiner": {ID: "a-refiner", ImageRef: "img-refiner", PersonaID: "p-refiner"},
		},
		SeedEvents: []hub.Event{{ID: "e-1", Type: "pull_request", At: newEvent}},
	}

	// The edit that lands mid-pass: the admin renames the exclude label while the loop is
	// reading this install's events.
	h := &eventsOnceHub{Fake: f, action: func() {
		v2 := cloneInstall(v1)
		v2.ExcludeLabel = "hold-off"
		st.rows = []setup.Install{v2}
	}}

	engine := Engine{Hub: h, Store: st, Log: func(slug, msg string) {}}
	if _, err := engine.Pass(ctx, ScopeAll); err != nil {
		t.Fatalf("Once: %v", err)
	}

	stored, ok := st.row("app")
	if !ok {
		t.Fatalf("install app: gone from the store")
	}
	if stored.ExcludeLabel != "hold-off" {
		t.Fatalf("ExcludeLabel: got %q, want the edit the API saved during the pass to survive", stored.ExcludeLabel)
	}
	if got, ok := stored.Coverage["pull_request"]; !ok || !got.Equal(newEvent) {
		t.Fatalf("Coverage[pull_request]: got %v (present %v), want the new event %v", got, ok, newEvent)
	}
	if got, ok := stored.Coverage["issues"]; !ok || !got.Equal(seen) {
		t.Fatalf("Coverage[issues]: got %v (present %v), want the family the row already held", got, ok)
	}
}

func TestReconcileSkipsSaveWithoutNewCoverage(t *testing.T) {
	// A pass writes a row for what it earns: coverage, the cursor, a first
	// verification, or the llm the row says the agents run on. With no new
	// event and an install that already agrees with the settings there is
	// nothing to write, and writing the row anyway is how it stomps a
	// concurrent edit.
	ctx := context.Background()

	install := setup.Install{
		Slug:          "app",
		LayoutVersion: setup.LayoutManagedAgents,
		GroupID:       "g-1",
		ConnectorID:   "c-app",
		Roles: map[setup.Role]setup.RoleState{
			setup.RoleRefiner: {
				AgentID:     "a-refiner",
				ImageID:     "img-refiner",
				PersonaID:   "p-refiner",
				BotLogin:    "refiner-app",
				LLMProvider: settings.Default(setup.RoleRefiner, settings.FieldLLMProvider),
				LLMModel:    settings.Default(setup.RoleRefiner, settings.FieldLLMModel),
			},
		},
		Coverage: map[string]time.Time{"issues": time.Now().Add(-time.Hour)},
	}
	st := &fakeStore{rows: []setup.Install{install}}

	f := &hub.Fake{
		PluginGroupsSeed: []string{"g-1"},
		SeedImages: map[string]hub.Image{
			"img-refiner": {ID: "img-refiner", ImageURL: "image:1.0"},
		},
		Triggers: map[string][]setup.Trigger{},
		SeedAgents: map[string]hub.Agent{
			"a-refiner": {
				ID:         "a-refiner",
				ImageRef:   "img-refiner",
				PersonaID:  "p-refiner",
				LLM:        &hub.LLMSpec{Provider: settings.Default(setup.RoleRefiner, settings.FieldLLMProvider), Model: settings.Default(setup.RoleRefiner, settings.FieldLLMModel)},
				MCPServers: []string{"forgejo-mcp-app", "plugin-forgejo"},
				Skills:     []string{"forgejo-forgejo"},
			},
		},
		SeedPersonas: map[string]hub.Persona{
			"p-refiner": {ID: "p-refiner", Text: content.PersonaFor(setup.RoleRefiner, map[setup.Role]string{setup.RoleRefiner: "refiner-app"}, "")},
		},
		SeedEvents: []hub.Event{},
	}

	engine := Engine{Hub: f, Store: st, Log: func(slug, msg string) {}}
	if _, err := engine.Pass(ctx, ScopeAll); err != nil {
		t.Fatalf("Once: %v", err)
	}

	if len(st.saved) != 0 {
		t.Fatalf("Save: got %d calls, want 0 — nothing new was learned about coverage", len(st.saved))
	}
}

func TestReconcileDoesNotResurrectRemovedInstall(t *testing.T) {
	// An admin who removes the install while the pass runs has removed it. The row this pass
	// read is not a reason to put it back.
	ctx := context.Background()

	install := setup.Install{
		Slug:          "app",
		LayoutVersion: setup.LayoutManagedAgents,
		GroupID:       "g-1",
		ConnectorID:   "c-app",
		Roles: map[setup.Role]setup.RoleState{
			setup.RoleRefiner: {
				AgentID:   "a-refiner",
				ImageID:   "img-refiner",
				PersonaID: "p-refiner",
				BotLogin:  "refiner-app",
			},
		},
		Coverage: map[string]time.Time{"issues": time.Now().Add(-time.Hour)},
	}
	st := &fakeStore{rows: []setup.Install{install}}

	f := &hub.Fake{
		PluginGroupsSeed: []string{"g-1"},
		SeedImages: map[string]hub.Image{
			"img-refiner": {ID: "img-refiner", ImageURL: "image:1.0"},
		},
		Triggers: map[string][]setup.Trigger{},
		SeedAgents: map[string]hub.Agent{
			"a-refiner": {ID: "a-refiner", ImageRef: "img-refiner", PersonaID: "p-refiner"},
		},
		// A new event, so the pass does have a coverage write it wants to make.
		SeedEvents: []hub.Event{{ID: "e-1", Type: "pull_request", At: time.Now()}},
	}

	h := &eventsOnceHub{Fake: f, action: func() { st.rows = nil }}

	engine := Engine{Hub: h, Store: st, Log: func(slug, msg string) {}}
	if _, err := engine.Pass(ctx, ScopeAll); err != nil {
		t.Fatalf("Once: %v", err)
	}

	if len(st.saved) != 0 {
		t.Fatalf("Save: got %d calls, want 0 — the install was removed during the pass", len(st.saved))
	}
}

func TestReconcileMarksVerifiedOnFirstEvent(t *testing.T) {
	// S7's own poll is not the only proof the hook reaches the connector. Forgejo has no way to
	// test an org hook, so the event that proves it can arrive long after an admin continued
	// past that screen — and the row would have gone on saying "Not verified" for ever. The
	// first event a pass folds in marks the install verified, at the earliest of them.
	ctx := context.Background()

	t1 := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	t2 := t1.Add(time.Minute)

	install := setup.Install{
		Slug:          "app",
		LayoutVersion: setup.LayoutManagedAgents,
		GroupID:       "g-1",
		ConnectorID:   "c-app",
		Roles:         map[setup.Role]setup.RoleState{},
	}
	st := &fakeStore{rows: []setup.Install{install}}

	h := &eventsRecordHub{
		Fake: &hub.Fake{
			PluginGroupsSeed: []string{"g-1"},
			Triggers:         map[string][]setup.Trigger{},
		},
		// Newest first: the earliest event is the one that proves the hook, whatever order the
		// hub happens to answer in.
		events: []hub.Event{
			{ID: "e-2", Type: "issue_comment", At: t2},
			{ID: "e-1", Type: "issues", At: t1},
		},
	}

	engine := Engine{Hub: h, Store: st, Log: func(slug, msg string) {}}
	if _, err := engine.Pass(ctx, ScopeAll); err != nil {
		t.Fatalf("Once: %v", err)
	}

	stored, ok := st.row("app")
	if !ok {
		t.Fatalf("install app: gone from the store")
	}
	if stored.WebhookVerifiedAt == nil {
		t.Fatalf("WebhookVerifiedAt: got nothing, want the pass that read events to verify the install")
	}
	if !stored.WebhookVerifiedAt.Equal(t1) {
		t.Fatalf("WebhookVerifiedAt: got %v, want the earliest event %v", stored.WebhookVerifiedAt, t1)
	}
}

func TestReconcileKeepsAnExistingVerification(t *testing.T) {
	// The deliveries endpoint sets WebhookVerifiedAt when S7's poll sees a delivery. A pass
	// reading events later has nothing to say about that moment, so the save it makes must
	// carry the row's own value, not the pass's.
	ctx := context.Background()

	t0 := time.Date(2026, 10, 20, 11, 0, 0, 0, time.UTC)
	t1 := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	t2 := t1.Add(time.Minute)
	cursor := t1

	install := setup.Install{
		Slug:              "app",
		LayoutVersion:     setup.LayoutManagedAgents,
		GroupID:           "g-1",
		ConnectorID:       "c-app",
		Roles:             map[setup.Role]setup.RoleState{},
		Coverage:          map[string]time.Time{"issues": t1},
		EventsReadUntil:   &cursor,
		WebhookVerifiedAt: &t0,
	}
	st := &fakeStore{rows: []setup.Install{install}}

	h := &eventsRecordHub{
		Fake: &hub.Fake{
			PluginGroupsSeed: []string{"g-1"},
			Triggers:         map[string][]setup.Trigger{},
		},
		// A family the row does not have yet, so the pass has coverage to write and does save.
		events: []hub.Event{{ID: "e-1", Type: "pull_request", At: t2}},
	}

	engine := Engine{Hub: h, Store: st, Log: func(slug, msg string) {}}
	if _, err := engine.Pass(ctx, ScopeAll); err != nil {
		t.Fatalf("Once: %v", err)
	}

	stored, ok := st.row("app")
	if !ok {
		t.Fatalf("install app: gone from the store")
	}
	if got, ok := stored.Coverage["pull_request"]; !ok || !got.Equal(t2) {
		t.Fatalf("Coverage[pull_request]: got %v (present %v), want the new family at %v", got, ok, t2)
	}
	if stored.WebhookVerifiedAt == nil || !stored.WebhookVerifiedAt.Equal(t0) {
		t.Fatalf("WebhookVerifiedAt: got %v, want the %v the API recorded", stored.WebhookVerifiedAt, t0)
	}
}

func TestReconcileSavesWhenOnlyVerificationIsNew(t *testing.T) {
	// The hub can answer with an event the cursor already covers, so a pass can fold in its
	// first event with nothing new to say about coverage or the cursor. A verification on its
	// own is still worth the write.
	ctx := context.Background()

	t1 := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	t3 := t1.Add(time.Hour)
	t5 := t1.Add(2 * time.Hour)
	cursor := t5

	install := setup.Install{
		Slug:            "app",
		LayoutVersion:   setup.LayoutManagedAgents,
		GroupID:         "g-1",
		ConnectorID:     "c-app",
		Roles:           map[setup.Role]setup.RoleState{},
		Coverage:        map[string]time.Time{"issues": t1},
		EventsReadUntil: &cursor,
	}
	st := &fakeStore{rows: []setup.Install{install}}

	h := &eventsRecordHub{
		Fake: &hub.Fake{
			PluginGroupsSeed: []string{"g-1"},
			Triggers:         map[string][]setup.Trigger{},
		},
		// Later than the family's first-seen time and earlier than the cursor: neither moves.
		events: []hub.Event{{ID: "e-1", Type: "issues", At: t3}},
	}

	engine := Engine{Hub: h, Store: st, Log: func(slug, msg string) {}}
	if _, err := engine.Pass(ctx, ScopeAll); err != nil {
		t.Fatalf("Once: %v", err)
	}

	if len(st.saved) != 1 {
		t.Fatalf("Save: got %d calls, want 1 — a verification alone is worth the write", len(st.saved))
	}
	stored, ok := st.row("app")
	if !ok {
		t.Fatalf("install app: gone from the store")
	}
	if stored.WebhookVerifiedAt == nil || !stored.WebhookVerifiedAt.Equal(t3) {
		t.Fatalf("WebhookVerifiedAt: got %v, want the event this pass read %v", stored.WebhookVerifiedAt, t3)
	}
}

func TestReconcileCoverageReadsFromCursor(t *testing.T) {
	// The cursor, not the first-seen times, is where a pass starts reading: the first-seen
	// times never move forward, so reading from them re-reads every event ever seen.
	ctx := context.Background()

	cursor := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	install := setup.Install{
		Slug:            "app",
		LayoutVersion:   setup.LayoutManagedAgents,
		GroupID:         "g-1",
		ConnectorID:     "c-app",
		Roles:           map[setup.Role]setup.RoleState{},
		Coverage:        map[string]time.Time{"issues": cursor.Add(-time.Hour)},
		EventsReadUntil: &cursor,
	}
	st := &fakeStore{rows: []setup.Install{install}}

	h := &eventsRecordHub{Fake: &hub.Fake{
		PluginGroupsSeed: []string{"g-1"},
		Triggers:         map[string][]setup.Trigger{},
	}}

	engine := Engine{Hub: h, Store: st, Log: func(slug, msg string) {}}
	if _, err := engine.Pass(ctx, ScopeAll); err != nil {
		t.Fatalf("Once: %v", err)
	}

	if len(h.since) != 1 {
		t.Fatalf("Events: got %d calls, want 1", len(h.since))
	}
	if !h.since[0].Equal(cursor) {
		t.Fatalf("Events since: got %v, want the cursor %v", h.since[0], cursor)
	}
}

func TestReconcileCoverageAdvancesCursor(t *testing.T) {
	// A pass leaves the cursor on the newest event it folded in, so the next pass starts
	// after them. Coverage still records the earliest delivery per family.
	ctx := context.Background()

	t1 := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	t2 := t1.Add(time.Minute)

	install := setup.Install{
		Slug:          "app",
		LayoutVersion: setup.LayoutManagedAgents,
		GroupID:       "g-1",
		ConnectorID:   "c-app",
		Roles:         map[setup.Role]setup.RoleState{},
		Coverage:      map[string]time.Time{},
	}
	st := &fakeStore{rows: []setup.Install{install}}

	h := &eventsRecordHub{
		Fake: &hub.Fake{
			PluginGroupsSeed: []string{"g-1"},
			Triggers:         map[string][]setup.Trigger{},
		},
		events: []hub.Event{
			{ID: "e-2", Type: "issues", At: t2},
			{ID: "e-1", Type: "issues", At: t1},
		},
	}

	engine := Engine{Hub: h, Store: st, Log: func(slug, msg string) {}}
	if _, err := engine.Pass(ctx, ScopeAll); err != nil {
		t.Fatalf("Once: %v", err)
	}

	stored, ok := st.row("app")
	if !ok {
		t.Fatalf("install app: gone from the store")
	}
	if stored.EventsReadUntil == nil || !stored.EventsReadUntil.Equal(t2) {
		t.Fatalf("EventsReadUntil: got %v, want the newest event %v", stored.EventsReadUntil, t2)
	}
	if got, ok := stored.Coverage["issues"]; !ok || !got.Equal(t1) {
		t.Fatalf("Coverage[issues]: got %v (present %v), want the earliest event %v", got, ok, t1)
	}
}

func TestReconcileCoverageCursorNeverMovesBack(t *testing.T) {
	// The hub can answer with an event from the same second the cursor already covers.
	// Reading again is harmless; moving the cursor back over it is not.
	ctx := context.Background()

	t3 := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	t5 := t3.Add(2 * time.Minute)

	install := setup.Install{
		Slug:            "app",
		LayoutVersion:   setup.LayoutManagedAgents,
		GroupID:         "g-1",
		ConnectorID:     "c-app",
		Roles:           map[setup.Role]setup.RoleState{},
		Coverage:        map[string]time.Time{"issues": t3},
		EventsReadUntil: &t5,
	}
	st := &fakeStore{rows: []setup.Install{install}}

	h := &eventsRecordHub{
		Fake: &hub.Fake{
			PluginGroupsSeed: []string{"g-1"},
			Triggers:         map[string][]setup.Trigger{},
		},
		// A new family at an event older than the cursor: coverage earns a write, and the
		// save must carry the cursor forward, not back.
		events: []hub.Event{{ID: "e-1", Type: "pull_request", At: t3}},
	}

	engine := Engine{Hub: h, Store: st, Log: func(slug, msg string) {}}
	if _, err := engine.Pass(ctx, ScopeAll); err != nil {
		t.Fatalf("Once: %v", err)
	}

	stored, ok := st.row("app")
	if !ok {
		t.Fatalf("install app: gone from the store")
	}
	if stored.EventsReadUntil == nil || !stored.EventsReadUntil.Equal(t5) {
		t.Fatalf("EventsReadUntil: got %v, want it to stay at the cursor %v", stored.EventsReadUntil, t5)
	}
}

func TestReconcileCoverageNoEventsNoSave(t *testing.T) {
	// A pass that reads nothing new learns nothing, and a row with nothing new learned is
	// a row that must not be written — the same rule coverage already follows.
	ctx := context.Background()

	t3 := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	t5 := t3.Add(2 * time.Minute)

	install := setup.Install{
		Slug:            "app",
		LayoutVersion:   setup.LayoutManagedAgents,
		GroupID:         "g-1",
		ConnectorID:     "c-app",
		Roles:           map[setup.Role]setup.RoleState{},
		Coverage:        map[string]time.Time{"issues": t3},
		EventsReadUntil: &t5,
	}
	st := &fakeStore{rows: []setup.Install{install}}

	h := &eventsRecordHub{
		Fake: &hub.Fake{
			PluginGroupsSeed: []string{"g-1"},
			Triggers:         map[string][]setup.Trigger{},
		},
		events: []hub.Event{},
	}

	engine := Engine{Hub: h, Store: st, Log: func(slug, msg string) {}}
	if _, err := engine.Pass(ctx, ScopeAll); err != nil {
		t.Fatalf("Once: %v", err)
	}

	if len(st.saved) != 0 {
		t.Fatalf("Save: got %d calls, want 0 — no events came back", len(st.saved))
	}
}

func TestReconcileCoverageLegacyRowFallsBack(t *testing.T) {
	// A row written before the loop kept a cursor has nothing to read from, so the old
	// reading stands: the latest first-seen time.
	ctx := context.Background()

	t1 := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	t2 := t1.Add(time.Minute)

	install := setup.Install{
		Slug:          "app",
		LayoutVersion: setup.LayoutManagedAgents,
		GroupID:       "g-1",
		ConnectorID:   "c-app",
		Roles:         map[setup.Role]setup.RoleState{},
		Coverage:      map[string]time.Time{"issues": t1, "pull_request": t2},
	}
	st := &fakeStore{rows: []setup.Install{install}}

	h := &eventsRecordHub{
		Fake: &hub.Fake{
			PluginGroupsSeed: []string{"g-1"},
			Triggers:         map[string][]setup.Trigger{},
		},
		events: []hub.Event{},
	}

	engine := Engine{Hub: h, Store: st, Log: func(slug, msg string) {}}
	if _, err := engine.Pass(ctx, ScopeAll); err != nil {
		t.Fatalf("Once: %v", err)
	}

	if len(h.since) != 1 {
		t.Fatalf("Events: got %d calls, want 1", len(h.since))
	}
	if !h.since[0].Equal(t2) {
		t.Fatalf("Events since: got %v, want the latest first-seen time %v", h.since[0], t2)
	}
}

// eventsRecordHub is a hub.Fake that answers Events with its own events regardless of the
// since it is given — the real hub can overlap the second a pass stops on — and records
// what since every call received.
type eventsRecordHub struct {
	*hub.Fake
	events []hub.Event
	since  []time.Time
}

var _ hub.Client = (*eventsRecordHub)(nil)

func (h *eventsRecordHub) Events(ctx context.Context, connectorID string, since time.Time) ([]hub.Event, error) {
	h.since = append(h.since, since)
	return h.events, nil
}

// tmpStore creates a temporary store for testing
func tmpStore(t *testing.T) (*store.Store, func()) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return st, func() {
		if err := st.Close(); err != nil {
			t.Logf("Close: %v", err)
		}
	}
}

// fakeStore is the pass's store as a test drives it: the rows it holds and every write it
// was asked to make, so "did the pass save" is a question with an answer. Rows are copied
// in and out, so a pass cannot edit a stored row through the slice it was handed.
type fakeStore struct {
	rows  []setup.Install
	saved []setup.Install
}

func (f *fakeStore) List(ctx context.Context) ([]setup.Install, error) {
	out := make([]setup.Install, 0, len(f.rows))
	for _, in := range f.rows {
		out = append(out, cloneInstall(in))
	}
	return out, nil
}

func (f *fakeStore) Save(ctx context.Context, in setup.Install) error {
	f.saved = append(f.saved, cloneInstall(in))
	for i, row := range f.rows {
		if row.Slug == in.Slug {
			f.rows[i] = cloneInstall(in)
			return nil
		}
	}
	f.rows = append(f.rows, cloneInstall(in))
	return nil
}

// row reads back a stored row, and whether it is there at all.
func (f *fakeStore) row(slug string) (setup.Install, bool) {
	for _, in := range f.rows {
		if in.Slug == slug {
			return in, true
		}
	}
	return setup.Install{}, false
}

// eventsOnceHub is a hub.Fake that runs an action while a pass reads its events — the moment
// an API request lands between the loop's read of a row and its write. The action runs once.
type eventsOnceHub struct {
	*hub.Fake
	action func()
	done   bool
}

var _ hub.Client = (*eventsOnceHub)(nil)

func (h *eventsOnceHub) Events(ctx context.Context, connectorID string, since time.Time) ([]hub.Event, error) {
	if !h.done {
		h.done = true
		h.action()
	}
	return h.Fake.Events(ctx, connectorID, since)
}

// cloneInstall copies the row, maps included, so two holders of it cannot write through
// each other's copy.
func cloneInstall(in setup.Install) setup.Install {
	out := in
	if in.Roles != nil {
		out.Roles = make(map[setup.Role]setup.RoleState, len(in.Roles))
		for role, state := range in.Roles {
			out.Roles[role] = state
		}
	}
	if in.Coverage != nil {
		out.Coverage = make(map[string]time.Time, len(in.Coverage))
		for family, at := range in.Coverage {
			out.Coverage[family] = at
		}
	}
	if in.EventsReadUntil != nil {
		at := *in.EventsReadUntil
		out.EventsReadUntil = &at
	}
	return out
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 || stringIn(substr, s))
}

func stringIn(substr, s string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// --- settings-driven pass (PR 3) ---

// fakeSettings is the pass's settings source as a test drives it: fixed rows
// standing in for the settings store. Rows it does not name keep the shipped
// default, which is exactly how the pass treats a partial answer.
type fakeSettings struct{ rows []settings.Setting }

func (f fakeSettings) All(context.Context) ([]settings.Setting, error) { return f.rows, nil }

// fakeAdopter is the startup's default-adopter as a test drives it: it reports
// what it "moved" and lets the engine's half of Startup be judged on its own.
type fakeAdopter struct {
	adopted []settings.Adopted
	err     error
}

func (f fakeAdopter) AdoptDefaults(context.Context) ([]settings.Adopted, error) {
	return f.adopted, f.err
}

// inSyncInstall is an install the pass must agree with completely: the row, the
// image, the agent, the persona and the MCP row all say what the shipped
// settings say. Tests about drift start here and break exactly one thing.
func inSyncInstall(slug string) setup.Install {
	bots := inSyncBots()
	at := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	return setup.Install{
		Slug:          slug,
		LayoutVersion: setup.LayoutManagedAgents,
		GroupID:       "g-1",
		ConnectorID:   "c-" + slug,
		BaseURL:       "https://forge.example",
		Roles: map[setup.Role]setup.RoleState{
			setup.RoleRefiner: {
				AgentID:     "a-refiner",
				ImageID:     "img-refiner",
				PersonaID:   "p-refiner",
				BotLogin:    bots[setup.RoleRefiner],
				LLMProvider: settings.Default(setup.RoleRefiner, settings.FieldLLMProvider),
				LLMModel:    settings.Default(setup.RoleRefiner, settings.FieldLLMModel),
			},
		},
		Coverage:        map[string]time.Time{"issues": at},
		EventsReadUntil: &at,
	}
}

func inSyncBots() map[setup.Role]string {
	return map[setup.Role]string{setup.RoleRefiner: "refiner-app"}
}

// inSyncHub is the hub side of an install the pass must not touch: the agent
// names the manifest's fixed sets and runs the settings' model, the persona
// says the settings' text rendered with this install's names, the MCP row is
// wired as the settings wire it, and every trigger the install wants exists.
func inSyncHub(in setup.Install) *hub.Fake {
	f := &hub.Fake{
		PluginGroupsSeed: []string{in.GroupID},
		SeedImages: map[string]hub.Image{
			"img-refiner": {ID: "img-refiner", ImageURL: "image:1.0", Tools: []hub.ImageTool{}},
		},
		SeedAgents: map[string]hub.Agent{
			"a-refiner": {
				ID:         "a-refiner",
				ImageRef:   "img-refiner",
				PersonaID:  "p-refiner",
				LLM:        &hub.LLMSpec{Provider: settings.Default(setup.RoleRefiner, settings.FieldLLMProvider), Model: settings.Default(setup.RoleRefiner, settings.FieldLLMModel)},
				MCPServers: []string{setup.MCPServerName(in.Slug), "plugin-forgejo"},
				Skills:     []string{"forgejo-forgejo"},
			},
		},
		SeedPersonas: map[string]hub.Persona{
			"p-refiner": {ID: "p-refiner", Text: content.PersonaFor(setup.RoleRefiner, inSyncBots(), in.BaseURL)},
		},
		MCPServers: []hub.MCPServer{
			{
				Name:         setup.MCPServerName(in.Slug),
				URL:          settings.Default(setup.RoleRefiner, settings.FieldMCPUrl),
				TokenFromEnv: settings.Default(setup.RoleRefiner, settings.FieldMCPTokenFromEnv),
			},
		},
		Triggers: map[string][]setup.Trigger{in.ConnectorID: setup.Triggers(in)},
	}
	return f
}

func TestPassInSyncInstallWritesNothing(t *testing.T) {
	// The no-op pass is the common case — every trigger fires on an install
	// that is usually already right — and a no-op must be silent and still:
	// no hub write, no row write, and an empty summary.
	ctx := context.Background()
	in := inSyncInstall("app")
	st := &fakeStore{rows: []setup.Install{cloneInstall(in)}}
	f := inSyncHub(in)

	engine := Engine{Hub: f, Store: st, Log: func(slug, msg string) {}}
	sum, err := engine.Pass(ctx, ScopeAll)
	if err != nil {
		t.Fatalf("Pass: %v", err)
	}

	if len(f.UpdatedAgents) != 0 || len(f.PersonaUpdates) != 0 || len(f.ImageToolSets) != 0 ||
		len(f.RegisteredMCPServers) != 0 || len(f.CreatedTriggers) != 0 {
		t.Errorf("a fully in-sync install got hub writes anyway: agents=%d personas=%d images=%d mcp=%d triggers=%d",
			len(f.UpdatedAgents), len(f.PersonaUpdates), len(f.ImageToolSets), len(f.RegisteredMCPServers), len(f.CreatedTriggers))
	}
	if len(st.saved) != 0 {
		t.Errorf("Save: got %d, want 0", len(st.saved))
	}
	if len(sum.Updated) != 0 || len(sum.Refused) != 0 {
		t.Errorf("summary: got %d updated / %d refused, want nothing", len(sum.Updated), len(sum.Refused))
	}
}

func TestPassWritesTheExactSettingsValue(t *testing.T) {
	// The settings are the source; the agent is the copy. A drifted agent is
	// set to the settings' value exactly — not the row's older copy of them,
	// not a merge with what the drift introduced — and the row follows.
	// The second pass then has nothing to say, which is what "writes the
	// diff" means: the diff is what is left after the diff is written.
	ctx := context.Background()
	st, cleanup := tmpStore(t)
	defer cleanup()

	in := inSyncInstall("app")
	// The row carries what an older version created the agent on.
	in.Roles[setup.RoleRefiner] = setup.RoleState{
		AgentID: "a-refiner", ImageID: "img-refiner", PersonaID: "p-refiner",
		BotLogin: "refiner-app", LLMProvider: "stale-provider", LLMModel: "stale-model",
	}
	if err := st.Save(ctx, in); err != nil {
		t.Fatalf("Save: %v", err)
	}

	f := inSyncHub(in)
	agent := f.SeedAgents["a-refiner"]
	agent.LLM = &hub.LLMSpec{Provider: "opencode", Model: "gpt-4"}
	f.SeedAgents["a-refiner"] = agent

	set := fakeSettings{rows: []settings.Setting{
		{Role: "refiner", Field: settings.FieldLLMProvider, Value: "custom"},
		{Role: "refiner", Field: settings.FieldLLMModel, Value: "model-x"},
	}}

	engine := Engine{Hub: f, Store: st, Settings: set, Log: func(slug, msg string) {}}
	sum, err := engine.Pass(ctx, ScopeAll)
	if err != nil {
		t.Fatalf("Pass: %v", err)
	}

	if len(f.UpdatedAgents) != 1 {
		t.Fatalf("UpdatedAgents: got %d, want 1", len(f.UpdatedAgents))
	}
	got := f.UpdatedAgents[0].Update.LLM
	if got == nil || *got != (hub.LLMSpec{Provider: "custom", Model: "model-x"}) {
		t.Fatalf("agent llm: got %+v, want the settings' value exactly", got)
	}

	wantChange := Change{Install: "app", Role: "refiner", Kind: "agent", Field: "llm"}
	found := false
	for _, c := range sum.Updated {
		if c.Install == wantChange.Install && c.Role == wantChange.Role && c.Kind == wantChange.Kind && c.Field == wantChange.Field {
			found = true
			if c.To != "custom/model-x" {
				t.Errorf("summary says the llm moved to %q, want custom/model-x", c.To)
			}
		}
	}
	if !found {
		t.Errorf("summary does not name the llm correction: %+v", sum.Updated)
	}

	// The row now says what the agent was set to run on.
	rows, _ := st.List(ctx)
	if rs := rows[0].Roles[setup.RoleRefiner]; rs.LLMProvider != "custom" || rs.LLMModel != "model-x" {
		t.Errorf("row llm: got %s/%s, want custom/model-x", rs.LLMProvider, rs.LLMModel)
	}

	// The same pass again: the diff is gone, so the writes are gone.
	f.UpdatedAgents = nil
	st2 := st // the real store kept the row; f kept the agent (Fake applies writes)
	engine2 := Engine{Hub: f, Store: st2, Settings: set, Log: func(slug, msg string) {}}
	sum2, err := engine2.Pass(ctx, ScopeAll)
	if err != nil {
		t.Fatalf("second Pass: %v", err)
	}
	if len(f.UpdatedAgents) != 0 {
		t.Fatalf("second pass wrote agents again: %+v", f.UpdatedAgents)
	}
	for _, c := range sum2.Updated {
		if c.Field == "llm" {
			t.Errorf("second pass still reports an llm correction: %+v", c)
		}
	}
}

func TestPassEmptyMCPUrlNeverBlanksExistingRow(t *testing.T) {
	// An empty mcpUrl setting is "nobody has said where the forge is yet" —
	// a fact about the settings, not a write. It must not register an empty
	// server and, above all, must not blank the URL of a row that exists:
	// the settings PUT accepts an empty value, so this state is reachable.
	ctx := context.Background()
	in := inSyncInstall("app")
	st := &fakeStore{rows: []setup.Install{cloneInstall(in)}}
	f := inSyncHub(in)
	// The row exists and points at a live forge: an earlier install set it.
	const liveURL = "https://forge.example/api/mcp"
	seeded := false
	for i := range f.MCPServers {
		if f.MCPServers[i].Name == setup.MCPServerName(in.Slug) {
			f.MCPServers[i].URL = liveURL
			seeded = true
		}
	}
	if !seeded {
		t.Fatal("inSyncHub did not seed the forge MCP row")
	}

	// The settings now say nothing about the URL — an empty mcpUrl is savable.
	set := fakeSettings{rows: []settings.Setting{
		{Role: "refiner", Field: settings.FieldMCPUrl, Value: ""},
	}}

	engine := Engine{Hub: f, Store: st, Settings: set, Log: func(slug, msg string) {}}
	sum, err := engine.Pass(ctx, ScopeAll)
	if err != nil {
		t.Fatalf("Pass: %v", err)
	}

	if len(f.RegisteredMCPServers) != 0 {
		t.Fatalf("empty mcpUrl registered an MCP server: %+v", f.RegisteredMCPServers)
	}
	for _, row := range f.MCPServers {
		if row.Name == setup.MCPServerName(in.Slug) && row.URL != liveURL {
			t.Errorf("existing MCP row was blanked: got URL %q, want %q", row.URL, liveURL)
		}
	}
	for _, c := range sum.Updated {
		if c.Kind == "mcpServer" {
			t.Errorf("summary reports an mcp write from an empty url: %+v", c)
		}
	}
	for _, r := range sum.Refused {
		if r.Kind == "mcpServer" {
			t.Errorf("summary reports an mcp refusal from an empty url: %+v", r)
		}
	}
}

func TestPassRefusedWriteIsAWarningNotAHalt(t *testing.T) {
	// One hub that will not take a write does not stop the pass: the refusal
	// lands in the summary as a named line, the resources around it are still
	// attended to, and the pass answers with no error.
	ctx := context.Background()
	in := inSyncInstall("app")
	st := &fakeStore{rows: []setup.Install{cloneInstall(in)}}
	f := inSyncHub(in)

	// Two things drifted — the persona's text and the agent's model — and the
	// hub refuses every state-changing write.
	persona := f.SeedPersonas["p-refiner"]
	persona.Text = "the old text nobody wrote"
	f.SeedPersonas["p-refiner"] = persona
	agent := f.SeedAgents["a-refiner"]
	agent.LLM = &hub.LLMSpec{Provider: "opencode", Model: "gpt-4"}
	f.SeedAgents["a-refiner"] = agent
	// A trigger is missing too: its creation is not subject to the refusal,
	// and it is the proof the pass ran on past the refusals.
	f.Triggers[in.ConnectorID] = setup.Triggers(in)[:len(setup.Triggers(in))-1]
	f.UpdateErr = errors.New("hub offline")

	engine := Engine{Hub: f, Store: st, Log: func(slug, msg string) {}}
	sum, err := engine.Pass(ctx, ScopeAll)
	if err != nil {
		t.Fatalf("a refused write must not fail the pass: %v", err)
	}

	kinds := map[string]int{}
	for _, r := range sum.Refused {
		kinds[r.Kind]++
		if r.Error == "" {
			t.Errorf("refusal %s/%s carries no reason", r.Kind, r.Field)
		}
	}
	if kinds["persona"] == 0 {
		t.Errorf("no persona refusal in the summary: %+v", sum.Refused)
	}
	if kinds["agent"] == 0 {
		t.Errorf("no agent refusal in the summary: %+v", sum.Refused)
	}

	// The pass went on: the missing trigger was still created.
	if len(f.CreatedTriggers) != 1 {
		t.Errorf("CreatedTriggers: got %d, want 1 — the pass stopped at the refusals", len(f.CreatedTriggers))
	}
}

func TestPassPersonaScopeWritesOnlyPersonas(t *testing.T) {
	// The targeted trigger: a saved persona text reconciles the personas. The
	// agents run no risk from it, so the pass does not even read them.
	ctx := context.Background()
	in := inSyncInstall("app")
	st := &fakeStore{rows: []setup.Install{cloneInstall(in)}}
	f := inSyncHub(in)

	// Both a persona and an agent drifted; only the persona is in scope.
	persona := f.SeedPersonas["p-refiner"]
	persona.Text = "old text"
	f.SeedPersonas["p-refiner"] = persona
	agent := f.SeedAgents["a-refiner"]
	agent.LLM = &hub.LLMSpec{Provider: "opencode", Model: "gpt-4"}
	f.SeedAgents["a-refiner"] = agent

	engine := Engine{Hub: f, Store: st, Log: func(slug, msg string) {}}
	if _, err := engine.Pass(ctx, ScopePersona); err != nil {
		t.Fatalf("Pass: %v", err)
	}

	if len(f.PersonaUpdates) != 1 {
		t.Fatalf("PersonaUpdates: got %d, want 1", len(f.PersonaUpdates))
	}
	if len(f.UpdatedAgents) != 0 {
		t.Errorf("UpdatedAgents: got %d, want 0 — persona scope must not touch agents", len(f.UpdatedAgents))
	}
	for _, call := range f.Calls {
		if call == "GetAgent" || call == "ListMCPServers" || call == "ListTriggers" || call == "Events" {
			t.Errorf("persona scope made a %s call", call)
		}
	}
	if len(st.saved) != 0 {
		t.Errorf("Save: got %d, want 0 — a persona pass earns nothing on the row", len(st.saved))
	}
}

func TestPassLeavesLegacyInstallsAlone(t *testing.T) {
	// A row without the managed-agents stamp describes resources the old
	// wizard may have adopted. The plugin did not create them and has no
	// claim to write them: the pass says so and moves on.
	ctx := context.Background()
	in := inSyncInstall("app")
	in.LayoutVersion = 0
	st := &fakeStore{rows: []setup.Install{cloneInstall(in)}}
	f := inSyncHub(in)
	persona := f.SeedPersonas["p-refiner"]
	persona.Text = "old text"
	f.SeedPersonas["p-refiner"] = persona

	var logs []string
	engine := Engine{Hub: f, Store: st, Log: func(slug, msg string) { logs = append(logs, slug+": "+msg) }}
	if _, err := engine.Pass(ctx, ScopeAll); err != nil {
		t.Fatalf("Pass: %v", err)
	}

	for _, call := range f.Calls {
		if call == "GetAgent" || call == "GetPersona" || call == "GetAgentImage" {
			t.Fatalf("the legacy install was read anyway (%s)", call)
		}
	}
	if len(f.PersonaUpdates) != 0 {
		t.Fatalf("legacy install's persona was rewritten")
	}
	legacy := false
	for _, l := range logs {
		if strings.Contains(l, "legacy") {
			legacy = true
		}
	}
	if !legacy {
		t.Errorf("no log line about the legacy install: %v", logs)
	}
}

func TestStartupAdoptsDefaultsThenReconciles(t *testing.T) {
	// Startup is the upgrade's move: the settings adopt what nobody edited
	// (the store's job, tested there), and the engine's half is that the
	// adoption shows in the answer and the reconcile runs right after.
	ctx := context.Background()
	in := inSyncInstall("app")
	st := &fakeStore{rows: []setup.Install{cloneInstall(in)}}
	f := inSyncHub(in)
	agent := f.SeedAgents["a-refiner"]
	agent.Skills = nil
	f.SeedAgents["a-refiner"] = agent // one piece of drift to catch

	engine := Engine{
		Hub:      f,
		Store:    st,
		Defaults: fakeAdopter{adopted: []settings.Adopted{{Role: "refiner", Field: "personaText"}}},
		Log:      func(slug, msg string) {},
	}
	sum, err := engine.Startup(ctx)
	if err != nil {
		t.Fatalf("Startup: %v", err)
	}

	setting := false
	for _, c := range sum.Updated {
		if c.Kind == "setting" && c.Field == "refiner.personaText" {
			setting = true
		}
	}
	if !setting {
		t.Errorf("the adoption is not in the summary: %+v", sum.Updated)
	}
	if len(f.UpdatedAgents) != 1 {
		t.Errorf("UpdatedAgents: got %d, want 1 — the reconcile half did not run", len(f.UpdatedAgents))
	}
}

func TestStartupAdoptionFailureStillReconciles(t *testing.T) {
	// The upgrade half failing is a logged warning, not a cancelled reconcile:
	// the resources still get their pass, because "the defaults did not move"
	// does not mean "the hub may not be written".
	ctx := context.Background()
	in := inSyncInstall("app")
	st := &fakeStore{rows: []setup.Install{cloneInstall(in)}}
	f := inSyncHub(in)
	agent := f.SeedAgents["a-refiner"]
	agent.Skills = nil
	f.SeedAgents["a-refiner"] = agent

	engine := Engine{
		Hub:      f,
		Store:    st,
		Defaults: fakeAdopter{err: errors.New("db busy")},
		Log:      func(slug, msg string) {},
	}
	if _, err := engine.Startup(ctx); err != nil {
		t.Fatalf("Startup: %v", err)
	}
	if len(f.UpdatedAgents) != 1 {
		t.Errorf("UpdatedAgents: got %d, want 1 — a failed adoption cancelled the pass", len(f.UpdatedAgents))
	}
}

// TestPassCreatesPrPatrolOnce: a pass creates the missing patrol, and the
// next pass reports no drift — no duplicate per pass.
func TestPassCreatesPrPatrolOnce(t *testing.T) {
	ctx := context.Background()
	st, cleanup := tmpStore(t)
	defer cleanup()

	install := setup.Install{
		Slug:          "app",
		LayoutVersion: setup.LayoutManagedAgents,
		GroupID:       "g-1",
		ConnectorID:   "c-app",
		Roles: map[setup.Role]setup.RoleState{
			setup.RoleDeveloper: {AgentID: "a-developer"},
		},
	}
	if err := st.Save(ctx, install); err != nil {
		t.Fatalf("Save: %v", err)
	}

	f := &hub.Fake{
		PluginGroupsSeed: []string{"g-1"},
		Triggers:         map[string][]setup.Trigger{},
	}
	engine := Engine{Hub: f, Store: st, Log: func(slug, msg string) {}}

	if _, err := engine.Pass(ctx, ScopeAll); err != nil {
		t.Fatalf("Pass: %v", err)
	}
	if len(f.CreatedCronTriggers) != 1 {
		t.Fatalf("created cron triggers = %d, want 1", len(f.CreatedCronTriggers))
	}
	ct := f.CreatedCronTriggers[0]
	if ct.Name != "forgejo-developer-pr-patrol-app" || ct.GroupID != "g-1" ||
		ct.Schedule != "*/30 * * * *" || !ct.Enabled {
		t.Errorf("created cron trigger = %+v, want the patrol filed under g-1 on the shipped schedule", ct)
	}

	// The pass that finds it there creates nothing.
	if _, err := engine.Pass(ctx, ScopeAll); err != nil {
		t.Fatalf("second Pass: %v", err)
	}
	if len(f.CreatedCronTriggers) != 1 {
		t.Errorf("created cron triggers after the second pass = %d, want still 1", len(f.CreatedCronTriggers))
	}
}

// TestPassLeavesARetunedPatrolAlone: the pass matches by name, so an admin
// who retuned the schedule on the hub is not fought by the next pass.
func TestPassLeavesARetunedPatrolAlone(t *testing.T) {
	ctx := context.Background()
	st, cleanup := tmpStore(t)
	defer cleanup()

	install := setup.Install{
		Slug:          "app",
		LayoutVersion: setup.LayoutManagedAgents,
		GroupID:       "g-1",
		ConnectorID:   "c-app",
		Roles: map[setup.Role]setup.RoleState{
			setup.RoleDeveloper: {AgentID: "a-developer"},
		},
	}
	if err := st.Save(ctx, install); err != nil {
		t.Fatalf("Save: %v", err)
	}

	f := &hub.Fake{
		PluginGroupsSeed: []string{"g-1"},
		Triggers:         map[string][]setup.Trigger{},
		CronTriggers: []setup.CronTrigger{{
			ID: "c-9", Name: "forgejo-developer-pr-patrol-app",
			AgentRef: "a-developer", Schedule: "0 2 * * *", Prompt: "Retuned.", Enabled: true,
		}},
	}
	engine := Engine{Hub: f, Store: st, Log: func(slug, msg string) {}}

	if _, err := engine.Pass(ctx, ScopeAll); err != nil {
		t.Fatalf("Pass: %v", err)
	}
	if len(f.CreatedCronTriggers) != 0 {
		t.Errorf("created cron triggers = %d, want 0 — a retuned patrol is not drift", len(f.CreatedCronTriggers))
	}
}

// TestPassSkipsPatrolWithoutDeveloperAgent: a half-created install has no
// agent to fire at, and the hub refuses a trigger without an agentRef — the
// pass leaves it for a later one.
func TestPassSkipsPatrolWithoutDeveloperAgent(t *testing.T) {
	ctx := context.Background()
	st, cleanup := tmpStore(t)
	defer cleanup()

	install := setup.Install{
		Slug:          "app",
		LayoutVersion: setup.LayoutManagedAgents,
		GroupID:       "g-1",
		ConnectorID:   "c-app",
		Roles:         map[setup.Role]setup.RoleState{},
	}
	if err := st.Save(ctx, install); err != nil {
		t.Fatalf("Save: %v", err)
	}

	f := &hub.Fake{
		PluginGroupsSeed: []string{"g-1"},
		Triggers:         map[string][]setup.Trigger{},
	}
	engine := Engine{Hub: f, Store: st, Log: func(slug, msg string) {}}

	if _, err := engine.Pass(ctx, ScopeAll); err != nil {
		t.Fatalf("Pass: %v", err)
	}
	if len(f.CreatedCronTriggers) != 0 {
		t.Errorf("created cron triggers = %d, want 0 without a developer agent", len(f.CreatedCronTriggers))
	}
}

// TestPassCorrectsADriftedCustomURL is the custom endpoint's drift rule: while
// the role runs on the custom provider, the agent's URL is readable from the
// hub, so a hub that drifted off the settings' URL is set back — and the
// settings' key rides with the write, because the pair is written as the
// settings hold it. A second pass has nothing left to write.
func TestPassCorrectsADriftedCustomURL(t *testing.T) {
	ctx := context.Background()
	st, cleanup := tmpStore(t)
	defer cleanup()

	in := inSyncInstall("app")
	in.Roles[setup.RoleRefiner] = setup.RoleState{
		AgentID: "a-refiner", ImageID: "img-refiner", PersonaID: "p-refiner",
		BotLogin: "refiner-app", LLMProvider: "custom", LLMModel: "model-x",
	}
	if err := st.Save(ctx, in); err != nil {
		t.Fatalf("Save: %v", err)
	}

	f := inSyncHub(in)
	agent := f.SeedAgents["a-refiner"]
	agent.LLM = &hub.LLMSpec{Provider: "custom", Model: "model-x"}
	agent.CustomURL = "https://drifted.example/v1"
	f.SeedAgents["a-refiner"] = agent

	set := fakeSettings{rows: []settings.Setting{
		{Role: "refiner", Field: settings.FieldLLMProvider, Value: "custom"},
		{Role: "refiner", Field: settings.FieldLLMModel, Value: "model-x"},
		{Role: "refiner", Field: settings.FieldLLMCustomUrl, Value: "https://llm.example/v1"},
		{Role: "refiner", Field: settings.FieldLLMCustomKey, Value: "sk-secret"},
	}}

	engine := Engine{Hub: f, Store: st, Settings: set, Log: func(slug, msg string) {}}
	sum, err := engine.Pass(ctx, ScopeAll)
	if err != nil {
		t.Fatalf("Pass: %v", err)
	}

	if len(f.UpdatedAgents) != 1 {
		t.Fatalf("UpdatedAgents: got %d, want 1", len(f.UpdatedAgents))
	}
	up := f.UpdatedAgents[0].Update
	if up.LLM != nil {
		t.Errorf("llm written (%+v), want untouched: the agent already names custom/model-x", *up.LLM)
	}
	if up.CustomURL == nil || *up.CustomURL != "https://llm.example/v1" {
		t.Fatalf("CustomURL = %v, want the settings' URL set back", up.CustomURL)
	}
	if up.CustomKey == nil || *up.CustomKey != "sk-secret" {
		t.Errorf("CustomKey = %v, want the settings' key to ride with the URL", up.CustomKey)
	}
	found := false
	for _, c := range sum.Updated {
		if c.Field == "customURL" {
			found = true
		}
	}
	if !found {
		t.Errorf("summary does not name the customURL correction: %+v", sum.Updated)
	}

	// The drift is gone: the same pass writes nothing more.
	f.UpdatedAgents = nil
	engine2 := Engine{Hub: f, Store: st, Settings: set, Log: func(slug, msg string) {}}
	if _, err := engine2.Pass(ctx, ScopeAll); err != nil {
		t.Fatalf("second Pass: %v", err)
	}
	if len(f.UpdatedAgents) != 0 {
		t.Errorf("second pass wrote %d agents, want 0", len(f.UpdatedAgents))
	}
}

// TestPassRefreshesADriftedKeyWithoutURLDrift is the key rotation reaching the
// agent on its own: the URL stands where the settings name it, so there is no
// URL drift to ride — the row's fingerprint is what shows the settings' key has
// not been written. The correction names the key's presence, never its value
// (R4), and the row moves to the written key's fingerprint so the next pass
// writes nothing.
func TestPassRefreshesADriftedKeyWithoutURLDrift(t *testing.T) {
	ctx := context.Background()
	st, cleanup := tmpStore(t)
	defer cleanup()

	in := inSyncInstall("app")
	in.Roles[setup.RoleRefiner] = setup.RoleState{
		AgentID: "a-refiner", ImageID: "img-refiner", PersonaID: "p-refiner",
		BotLogin: "refiner-app", LLMProvider: "custom", LLMModel: "model-x",
		LLMCustomKeyHash: setup.CustomKeyFingerprint("sk-old"),
	}
	if err := st.Save(ctx, in); err != nil {
		t.Fatalf("Save: %v", err)
	}

	f := inSyncHub(in)
	agent := f.SeedAgents["a-refiner"]
	agent.LLM = &hub.LLMSpec{Provider: "custom", Model: "model-x"}
	agent.CustomURL = "https://llm.example/v1" // where the settings name it: no URL drift
	f.SeedAgents["a-refiner"] = agent

	set := fakeSettings{rows: []settings.Setting{
		{Role: "refiner", Field: settings.FieldLLMProvider, Value: "custom"},
		{Role: "refiner", Field: settings.FieldLLMModel, Value: "model-x"},
		{Role: "refiner", Field: settings.FieldLLMCustomUrl, Value: "https://llm.example/v1"},
		{Role: "refiner", Field: settings.FieldLLMCustomKey, Value: "sk-rotated"},
	}}

	engine := Engine{Hub: f, Store: st, Settings: set, Log: func(slug, msg string) {}}
	sum, err := engine.Pass(ctx, ScopeAll)
	if err != nil {
		t.Fatalf("Pass: %v", err)
	}

	if len(f.UpdatedAgents) != 1 {
		t.Fatalf("UpdatedAgents: got %d, want 1 — a drifted key is a write of its own", len(f.UpdatedAgents))
	}
	up := f.UpdatedAgents[0].Update
	if up.CustomURL != nil {
		t.Errorf("CustomURL = %v, want nil: the URL never moved", *up.CustomURL)
	}
	if up.CustomKey == nil || *up.CustomKey != "sk-rotated" {
		t.Fatalf("CustomKey = %v, want the settings' rotated key", up.CustomKey)
	}
	found := false
	for _, c := range sum.Updated {
		if c.Field == "customKey" {
			found = true
			if strings.Contains(c.From, "sk-") || strings.Contains(c.To, "sk-") {
				t.Errorf("correction %q -> %q spells a key", c.From, c.To)
			}
		}
	}
	if !found {
		t.Errorf("summary does not name the customKey correction: %+v", sum.Updated)
	}

	// The row now records the written key: the same pass has nothing left.
	f.UpdatedAgents = nil
	engine2 := Engine{Hub: f, Store: st, Settings: set, Log: func(slug, msg string) {}}
	if _, err := engine2.Pass(ctx, ScopeAll); err != nil {
		t.Fatalf("second Pass: %v", err)
	}
	if len(f.UpdatedAgents) != 0 {
		t.Errorf("second pass wrote %d agents, want 0 — the row records the key it wrote", len(f.UpdatedAgents))
	}
}

// TestPassRefreshesADriftedVendorKey is the vendor rotation reaching the agent
// the same way: the row's fingerprint of that provider is what shows the
// settings' key has not been written, the correction names the provider and the
// key's presence and never its value (R4), and the row moves to the written
// key's fingerprint so the next pass writes nothing.
func TestPassRefreshesADriftedVendorKey(t *testing.T) {
	ctx := context.Background()
	st, cleanup := tmpStore(t)
	defer cleanup()

	in := inSyncInstall("app")
	in.Roles[setup.RoleRefiner] = setup.RoleState{
		AgentID: "a-refiner", ImageID: "img-refiner", PersonaID: "p-refiner",
		BotLogin: "refiner-app", LLMProvider: "openai", LLMModel: "model-x",
		OpenAIKeyHash: setup.CustomKeyFingerprint("sk-old"),
	}
	if err := st.Save(ctx, in); err != nil {
		t.Fatalf("Save: %v", err)
	}

	f := inSyncHub(in)
	agent := f.SeedAgents["a-refiner"]
	agent.LLM = &hub.LLMSpec{Provider: "openai", Model: "model-x"}
	f.SeedAgents["a-refiner"] = agent

	set := fakeSettings{rows: []settings.Setting{
		{Role: "refiner", Field: settings.FieldLLMProvider, Value: "openai"},
		{Role: "refiner", Field: settings.FieldLLMModel, Value: "model-x"},
		{Role: "refiner", Field: settings.FieldLLMOpenAIKey, Value: "sk-rotated"},
		// A key for a provider the role does not run on: inert, never written.
		{Role: "refiner", Field: settings.FieldLLMAnthropicKey, Value: "sk-stray"},
	}}

	engine := Engine{Hub: f, Store: st, Settings: set, Log: func(slug, msg string) {}}
	sum, err := engine.Pass(ctx, ScopeAll)
	if err != nil {
		t.Fatalf("Pass: %v", err)
	}

	if len(f.UpdatedAgents) != 1 {
		t.Fatalf("UpdatedAgents: got %d, want 1 — a drifted key is a write of its own", len(f.UpdatedAgents))
	}
	up := f.UpdatedAgents[0].Update
	if up.LLM != nil {
		t.Errorf("llm written (%+v), want untouched: the agent already names openai/model-x", *up.LLM)
	}
	if up.OpenAIKey == nil || *up.OpenAIKey != "sk-rotated" {
		t.Fatalf("OpenAIKey = %v, want the settings' rotated key", up.OpenAIKey)
	}
	if up.AnthropicKey != nil {
		t.Errorf("AnthropicKey = %v, want nil: a key of a provider the role does not run on stays put", *up.AnthropicKey)
	}
	found := false
	for _, c := range sum.Updated {
		if c.Field == "openaiKey" {
			found = true
			if strings.Contains(c.From, "sk-") || strings.Contains(c.To, "sk-") {
				t.Errorf("correction %q -> %q spells a key", c.From, c.To)
			}
		}
	}
	if !found {
		t.Errorf("summary does not name the openaiKey correction: %+v", sum.Updated)
	}

	// The row now records the written key: the same pass has nothing left.
	f.UpdatedAgents = nil
	engine2 := Engine{Hub: f, Store: st, Settings: set, Log: func(slug, msg string) {}}
	if _, err := engine2.Pass(ctx, ScopeAll); err != nil {
		t.Fatalf("second Pass: %v", err)
	}
	if len(f.UpdatedAgents) != 0 {
		t.Errorf("second pass wrote %d agents, want 0 — the row records the key it wrote", len(f.UpdatedAgents))
	}
}

// TestPassLeavesTheCustomEndpointAwayFromCustom: a role on a listed provider
// has an inert customProvider on its agent — unread drift of it is nobody's
// business, and the pass writes nothing for it.
func TestPassLeavesTheCustomEndpointAwayFromCustom(t *testing.T) {
	ctx := context.Background()
	in := inSyncInstall("app")
	in.Roles[setup.RoleRefiner] = setup.RoleState{
		AgentID: "a-refiner", ImageID: "img-refiner", PersonaID: "p-refiner",
		BotLogin:    "refiner-app",
		LLMProvider: settings.Default(setup.RoleRefiner, settings.FieldLLMProvider),
		LLMModel:    settings.Default(setup.RoleRefiner, settings.FieldLLMModel),
	}
	st := &fakeStore{rows: []setup.Install{cloneInstall(in)}}

	f := inSyncHub(in)
	agent := f.SeedAgents["a-refiner"]
	agent.CustomURL = "https://stale.example/v1" // left over from a former custom run
	f.SeedAgents["a-refiner"] = agent

	set := fakeSettings{rows: []settings.Setting{
		{Role: "refiner", Field: settings.FieldLLMCustomUrl, Value: "https://llm.example/v1"},
	}}

	engine := Engine{Hub: f, Store: st, Settings: set, Log: func(slug, msg string) {}}
	if _, err := engine.Pass(ctx, ScopeAll); err != nil {
		t.Fatalf("Pass: %v", err)
	}
	for _, ua := range f.UpdatedAgents {
		if ua.Update.CustomURL != nil || ua.Update.CustomKey != nil {
			t.Errorf("pass wrote a custom pair (%v/%v) for a listed provider, want nothing",
				ua.Update.CustomURL, ua.Update.CustomKey)
		}
	}
}

// TestPassMovesARoleOntoChatGPTSignIn is the settings save's other half: the admin
// chose the sign-in and saved only that row, and the pass is what carries it to the
// agent. The write names the method and no key — the hub refuses the pair — so the
// rotated key the settings still hold is left alone, inert in the hub's block. The
// row records the method the pass wrote, which is what makes the next pass quiet.
func TestPassMovesARoleOntoChatGPTSignIn(t *testing.T) {
	ctx := context.Background()
	st, cleanup := tmpStore(t)
	defer cleanup()

	in := inSyncInstall("app")
	in.Roles[setup.RoleRefiner] = setup.RoleState{
		AgentID: "a-refiner", ImageID: "img-refiner", PersonaID: "p-refiner",
		BotLogin: "refiner-app", LLMProvider: "openai", LLMModel: "model-x",
		OpenAIKeyHash:       setup.CustomKeyFingerprint("sk-old"),
		LLMOpenAIAuthMethod: setup.LLMAuthMethodAPIKey,
	}
	if err := st.Save(ctx, in); err != nil {
		t.Fatalf("Save: %v", err)
	}

	f := inSyncHub(in)
	agent := f.SeedAgents["a-refiner"]
	agent.LLM = &hub.LLMSpec{Provider: "openai", Model: "model-x"}
	f.SeedAgents["a-refiner"] = agent

	set := fakeSettings{rows: []settings.Setting{
		{Role: "refiner", Field: settings.FieldLLMProvider, Value: "openai"},
		{Role: "refiner", Field: settings.FieldLLMModel, Value: "model-x"},
		// A key the row does not record: drift the pass must NOT write, because the
		// role signs in and the hub refuses a key beside the sign-in method.
		{Role: "refiner", Field: settings.FieldLLMOpenAIKey, Value: "sk-rotated"},
		{Role: "refiner", Field: settings.FieldLLMOpenAIAuthMethod, Value: setup.LLMAuthMethodChatGPTSignIn},
	}}

	engine := Engine{Hub: f, Store: st, Settings: set, Log: func(slug, msg string) {}}
	sum, err := engine.Pass(ctx, ScopeAll)
	if err != nil {
		t.Fatalf("Pass: %v", err)
	}
	if len(f.UpdatedAgents) != 1 {
		t.Fatalf("UpdatedAgents: got %d, want 1 — the moved method is a write of its own", len(f.UpdatedAgents))
	}
	up := f.UpdatedAgents[0].Update
	if up.OpenAIAuthMethod == nil || *up.OpenAIAuthMethod != setup.LLMAuthMethodChatGPTSignIn {
		t.Fatalf("OpenAIAuthMethod = %v, want the sign-in", up.OpenAIAuthMethod)
	}
	if up.OpenAIKey != nil {
		t.Errorf("OpenAIKey = %q, want nil: a sign-in agent is never given a key", *up.OpenAIKey)
	}
	if up.LLM != nil {
		t.Errorf("llm written (%+v), want untouched", *up.LLM)
	}
	found := false
	for _, c := range sum.Updated {
		if strings.Contains(c.Field, "authMethod") {
			found = true
			if strings.Contains(c.From, "sk-") || strings.Contains(c.To, "sk-") {
				t.Errorf("correction %q -> %q spells a key", c.From, c.To)
			}
		}
	}
	if !found {
		t.Errorf("summary does not name the auth-method correction: %+v", sum.Updated)
	}

	// The row now records the method, and the key the pass was right never to
	// write stays un-recorded: a second pass has nothing left to do.
	f.UpdatedAgents = nil
	engine2 := Engine{Hub: f, Store: st, Settings: set, Log: func(slug, msg string) {}}
	if _, err := engine2.Pass(ctx, ScopeAll); err != nil {
		t.Fatalf("second Pass: %v", err)
	}
	if len(f.UpdatedAgents) != 0 {
		t.Errorf("second pass wrote %d agents, want 0 — the row records the method it wrote", len(f.UpdatedAgents))
	}
}

// TestPassMovesARoleOffTheSignIn: the way back has to say so. The hub keeps the
// method it holds on a write that names none, so an agent left on a sign-in while
// the settings hold a key authenticates with neither — the pass names the method
// and the key together.
func TestPassMovesARoleOffTheSignIn(t *testing.T) {
	ctx := context.Background()
	st, cleanup := tmpStore(t)
	defer cleanup()

	in := inSyncInstall("app")
	in.Roles[setup.RoleRefiner] = setup.RoleState{
		AgentID: "a-refiner", ImageID: "img-refiner", PersonaID: "p-refiner",
		BotLogin: "refiner-app", LLMProvider: "openai", LLMModel: "model-x",
		LLMOpenAIAuthMethod: setup.LLMAuthMethodChatGPTSignIn,
	}
	if err := st.Save(ctx, in); err != nil {
		t.Fatalf("Save: %v", err)
	}

	f := inSyncHub(in)
	agent := f.SeedAgents["a-refiner"]
	agent.LLM = &hub.LLMSpec{Provider: "openai", Model: "model-x"}
	agent.OpenAIAuthMethod = setup.LLMAuthMethodChatGPTSignIn
	f.SeedAgents["a-refiner"] = agent

	set := fakeSettings{rows: []settings.Setting{
		{Role: "refiner", Field: settings.FieldLLMProvider, Value: "openai"},
		{Role: "refiner", Field: settings.FieldLLMModel, Value: "model-x"},
		{Role: "refiner", Field: settings.FieldLLMOpenAIKey, Value: "sk-back"},
		{Role: "refiner", Field: settings.FieldLLMOpenAIAuthMethod, Value: setup.LLMAuthMethodAPIKey},
	}}

	engine := Engine{Hub: f, Store: st, Settings: set, Log: func(slug, msg string) {}}
	if _, err := engine.Pass(ctx, ScopeAll); err != nil {
		t.Fatalf("Pass: %v", err)
	}
	if len(f.UpdatedAgents) != 1 {
		t.Fatalf("UpdatedAgents: got %d, want 1", len(f.UpdatedAgents))
	}
	up := f.UpdatedAgents[0].Update
	if up.OpenAIAuthMethod == nil || *up.OpenAIAuthMethod != setup.LLMAuthMethodAPIKey {
		t.Fatalf("OpenAIAuthMethod = %v, want %q — an empty one leaves the agent on the sign-in",
			up.OpenAIAuthMethod, setup.LLMAuthMethodAPIKey)
	}
	if up.OpenAIKey == nil || *up.OpenAIKey != "sk-back" {
		t.Fatalf("OpenAIKey = %v, want the settings' key", up.OpenAIKey)
	}
}
