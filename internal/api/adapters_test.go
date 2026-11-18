package api

import (
	"context"
	"testing"

	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/hub"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/setup"
)

// The adapter carries no keys or URLs of its own (R4): the settings store holds
// those, and the wizard has none to hand over. The pairs and keys that ride
// through are the ones the settings name for the provider the role runs on — the
// custom URL and key, or one vendor key — which the hub's matching block takes.
// What the adapter still owns is the agent's name, its image and tools, and the
// provider/model pair it copies onto the spec.

func TestCreateAgentWritesProviderAndModel(t *testing.T) {
	h := &hub.Fake{}
	adapter := &hubAdapter{h: h}

	_, err := adapter.CreateAgent(context.Background(),
		"group-1", "test-agent", "image-1", "persona-1",
		[]string{}, []string{}, []setup.EnvVar{},
		setup.LLMWrite{Provider: "ollama-cloud", Model: "glm-4.7"}, nil, nil)

	if err != nil {
		t.Fatalf("CreateAgent failed: %v", err)
	}

	agent := h.CreatedAgents[0]
	if agent.LLM == nil {
		t.Fatalf("LLM = nil, want the provider and model the settings hold")
	}
	if agent.LLM.Provider != "ollama-cloud" || agent.LLM.Model != "glm-4.7" {
		t.Errorf("LLM = %+v, want ollama-cloud/glm-4.7", *agent.LLM)
	}
}

func TestCreateAgentWithEmptyProvider(t *testing.T) {
	h := &hub.Fake{}
	adapter := &hubAdapter{h: h}

	_, err := adapter.CreateAgent(context.Background(),
		"group-1", "test-agent", "image-1", "persona-1",
		[]string{}, []string{}, []setup.EnvVar{},
		setup.LLMWrite{}, nil, nil)

	if err != nil {
		t.Fatalf("CreateAgent failed: %v", err)
	}

	if agent := h.CreatedAgents[0]; agent.LLM != nil {
		t.Errorf("LLM = %+v, want nil: no provider, nothing to name", agent.LLM)
	}
}

// TestCreateAgentWritesTheCustomPair is the custom provider's create path: the
// URL and key the settings name reach the spec the hub's customProvider object
// is built from.
func TestCreateAgentWritesTheCustomPair(t *testing.T) {
	h := &hub.Fake{}
	adapter := &hubAdapter{h: h}

	_, err := adapter.CreateAgent(context.Background(),
		"group-1", "test-agent", "image-1", "persona-1",
		[]string{}, []string{}, []setup.EnvVar{},
		setup.LLMWrite{Provider: "custom", Model: "my-model", CustomURL: "https://llm.example.com/v1", CustomKey: "sk-secret"}, nil, nil)

	if err != nil {
		t.Fatalf("CreateAgent failed: %v", err)
	}

	agent := h.CreatedAgents[0]
	if agent.CustomURL != "https://llm.example.com/v1" || agent.CustomKey != "sk-secret" {
		t.Errorf("custom pair = %q/%q, want the settings' URL and key", agent.CustomURL, agent.CustomKey)
	}
}

// TestCreateAgentWritesTheVendorKey is the same path for a vendor provider: the
// key the settings hold for the provider the role runs on reaches the spec, and
// the blocks of the providers it does not run on stay empty — the write the
// apply builds never names them.
func TestCreateAgentWritesTheVendorKey(t *testing.T) {
	h := &hub.Fake{}
	adapter := &hubAdapter{h: h}

	_, err := adapter.CreateAgent(context.Background(),
		"group-1", "test-agent", "image-1", "persona-1",
		[]string{}, []string{}, []setup.EnvVar{},
		setup.LLMWrite{Provider: "openai", Model: "gpt-5.2", OpenAIKey: "sk-openai"}, nil, nil)

	if err != nil {
		t.Fatalf("CreateAgent failed: %v", err)
	}

	agent := h.CreatedAgents[0]
	if agent.OpenAIKey != "sk-openai" {
		t.Errorf("openai key = %q, want the settings' key", agent.OpenAIKey)
	}
	if agent.AnthropicKey != "" || agent.GoogleKey != "" || agent.CustomKey != "" || agent.CustomURL != "" {
		t.Errorf("other blocks = %q/%q/%q/%q, want all empty",
			agent.AnthropicKey, agent.GoogleKey, agent.CustomKey, agent.CustomURL)
	}
}

func TestRefreshImageToolsSuppressionOnlyForPluginForgejo(t *testing.T) {
	h := &hub.Fake{
		DefaultRefreshImageTools: []hub.ImageTool{},
		ImageMCPServers: map[string][]string{
			"img-1": {"plugin-forgejo"},
		},
	}
	adapter := &hubAdapter{h: h}

	tools, serverErrors, err := adapter.RefreshImageTools(context.Background(), "img-1")

	if err != nil {
		t.Errorf("error should be nil when only plugin-forgejo is missing, got %v", err)
	}
	if serverErrors != nil {
		t.Errorf("serverErrors should be nil when only plugin-forgejo is missing, got %v", serverErrors)
	}
	if tools == nil {
		t.Errorf("tools should be returned even on error")
	}
}

func TestUpdateAgentImage(t *testing.T) {
	h := &hub.Fake{}
	adapter := &hubAdapter{h: h}

	err := adapter.UpdateAgentImage(context.Background(), "image-1", "https://new-runtime.example.com")
	if err != nil {
		t.Fatalf("UpdateAgentImage failed: %v", err)
	}

	if len(h.ImageUpdates) != 1 {
		t.Fatalf("expected 1 image update, got %d", len(h.ImageUpdates))
	}

	update := h.ImageUpdates[0]
	if update.Update.ImageURL == nil || *update.Update.ImageURL != "https://new-runtime.example.com" {
		t.Errorf("ImageURL not set correctly in update")
	}
}

func TestUpdateAgentWritesSettingsLLM(t *testing.T) {
	h := &hub.Fake{}
	adapter := &hubAdapter{h: h}

	err := adapter.UpdateAgent(context.Background(), "agent-1", "",
		setup.LLMWrite{Provider: "ollama-cloud", Model: "new-model"}, nil, setup.WorkingHoursWrite{})
	if err != nil {
		t.Fatalf("UpdateAgent failed: %v", err)
	}

	if len(h.UpdatedAgents) != 1 {
		t.Fatalf("expected 1 agent update, got %d", len(h.UpdatedAgents))
	}

	update := h.UpdatedAgents[0].Update
	if update.LLM == nil {
		t.Fatalf("LLM = nil, want the settings' provider and model")
	}
	if update.LLM.Provider != "ollama-cloud" || update.LLM.Model != "new-model" {
		t.Errorf("LLM = %+v, want ollama-cloud/new-model", *update.LLM)
	}
	if update.CustomURL != nil || update.CustomKey != nil {
		t.Errorf("custom pair = %v/%v, want nil: a listed provider sends nothing",
			update.CustomURL, update.CustomKey)
	}
}

// TestUpdateAgentWritesTheCustomPair is the edit path: a named URL and key ride
// to the hub's customProvider object; empty strings leave both halves alone —
// the hub keeps what it holds.
func TestUpdateAgentWritesTheCustomPair(t *testing.T) {
	h := &hub.Fake{}
	adapter := &hubAdapter{h: h}

	if err := adapter.UpdateAgent(context.Background(), "agent-1", "",
		setup.LLMWrite{Provider: "custom", Model: "my-model", CustomURL: "https://llm.example.com/v1", CustomKey: "sk-secret"}, nil, setup.WorkingHoursWrite{}); err != nil {
		t.Fatalf("UpdateAgent: %v", err)
	}

	update := h.UpdatedAgents[0].Update
	if update.CustomURL == nil || *update.CustomURL != "https://llm.example.com/v1" {
		t.Errorf("CustomURL = %v, want the settings' URL", update.CustomURL)
	}
	if update.CustomKey == nil || *update.CustomKey != "sk-secret" {
		t.Errorf("CustomKey = %v, want the settings' key", update.CustomKey)
	}

	if err := adapter.UpdateAgent(context.Background(), "agent-1", "",
		setup.LLMWrite{Provider: "custom", Model: "my-model"}, nil, setup.WorkingHoursWrite{}); err != nil {
		t.Fatalf("UpdateAgent (keep): %v", err)
	}
	keep := h.UpdatedAgents[1].Update
	if keep.CustomURL != nil || keep.CustomKey != nil {
		t.Errorf("keep update = %v/%v, want both nil", keep.CustomURL, keep.CustomKey)
	}
}

// TestUpdateAgentWritesTheVendorKey is a vendor rotation's edit path: the named
// provider's key rides to its own block, and the blocks of every other provider
// stay nil — the hub keeps whatever it holds for them.
func TestUpdateAgentWritesTheVendorKey(t *testing.T) {
	h := &hub.Fake{}
	adapter := &hubAdapter{h: h}

	if err := adapter.UpdateAgent(context.Background(), "agent-1", "",
		setup.LLMWrite{Provider: "anthropic", Model: "claude", AnthropicKey: "sk-ant"}, nil, setup.WorkingHoursWrite{}); err != nil {
		t.Fatalf("UpdateAgent: %v", err)
	}

	update := h.UpdatedAgents[0].Update
	if update.AnthropicKey == nil || *update.AnthropicKey != "sk-ant" {
		t.Errorf("AnthropicKey = %v, want the settings' key", update.AnthropicKey)
	}
	if update.OpenAIKey != nil || update.GoogleKey != nil || update.CustomKey != nil || update.CustomURL != nil {
		t.Errorf("other blocks = %v/%v/%v/%v, want all nil",
			update.OpenAIKey, update.GoogleKey, update.CustomKey, update.CustomURL)
	}
}

// TestUpdateAgentCarriesTheNameTheAdminChose is the display-name path: a rename typed in
// the wizard has to reach the hub. It used to stop in the adapter, which had nothing to
// put it in — the agent kept its old name and the admin was told it had changed.
func TestUpdateAgentCarriesTheNameTheAdminChose(t *testing.T) {
	h := &hub.Fake{}
	adapter := &hubAdapter{h: h}

	if err := adapter.UpdateAgent(context.Background(), "agent-1", "The Critic", setup.LLMWrite{}, nil, setup.WorkingHoursWrite{}); err != nil {
		t.Fatalf("UpdateAgent: %v", err)
	}

	got := h.UpdatedAgents[0].Update.Name
	if got == nil || *got != "The Critic" {
		t.Errorf("Name = %v, want the display name the admin chose", got)
	}
}

// TestUpdateAgentSendsNoNameWhenNoneChanged is the other half: an edit that renames
// nothing sends no name, so the hub keeps the one it has rather than being handed an empty
// one to rename the agent to. An empty provider/model sends no LLM either — an empty
// field of an update is the hub's "keep what you have".
func TestUpdateAgentSendsNoNameWhenNoneChanged(t *testing.T) {
	h := &hub.Fake{}
	adapter := &hubAdapter{h: h}

	if err := adapter.UpdateAgent(context.Background(), "agent-1", "", setup.LLMWrite{}, nil, setup.WorkingHoursWrite{}); err != nil {
		t.Fatalf("UpdateAgent: %v", err)
	}

	update := h.UpdatedAgents[0].Update
	if name := update.Name; name != nil {
		t.Errorf("Name = %q, want nothing sent when no name changed", *name)
	}
	if update.LLM != nil {
		t.Errorf("LLM = %+v, want nothing sent when provider and model are empty", *update.LLM)
	}
}

// TestUpdatePersonaCarriesTheText checks the persona text reaches the hub: it is the field
// that names the forge, so an install pointed at a new address has to be able to write it.
func TestUpdatePersonaCarriesTheText(t *testing.T) {
	h := &hub.Fake{}
	adapter := &hubAdapter{h: h}

	if err := adapter.UpdatePersona(context.Background(), "persona-1", "You work at http://forgejo.example."); err != nil {
		t.Fatalf("UpdatePersona: %v", err)
	}

	if len(h.PersonaUpdates) != 1 {
		t.Fatalf("PersonaUpdates = %d, want 1", len(h.PersonaUpdates))
	}
	got := h.PersonaUpdates[0]
	if got.ID != "persona-1" || got.Text != "You work at http://forgejo.example." {
		t.Errorf("update = %+v, want the persona id and the text", got)
	}
}

func TestListTriggers(t *testing.T) {
	h := &hub.Fake{
		Triggers: map[string][]setup.Trigger{
			"connector-1": {
				{ID: "trigger-1", Name: "trigger-refiner-issue-opened", AgentRef: "agent-refiner"},
				{ID: "trigger-2", Name: "trigger-refiner-issue-reopened", AgentRef: "agent-refiner"},
			},
		},
	}
	adapter := &hubAdapter{h: h}

	entries, err := adapter.ListTriggers(context.Background(), "connector-1")
	if err != nil {
		t.Fatalf("ListTriggers failed: %v", err)
	}

	if len(entries) != 2 {
		t.Fatalf("expected 2 triggers, got %d", len(entries))
	}

	if entries[0].ID != "trigger-1" || entries[0].Name != "trigger-refiner-issue-opened" {
		t.Errorf("first trigger = %+v, want ID=trigger-1 Name=trigger-refiner-issue-opened", entries[0])
	}

	if entries[1].ID != "trigger-2" || entries[1].Name != "trigger-refiner-issue-reopened" {
		t.Errorf("second trigger = %+v, want ID=trigger-2 Name=trigger-refiner-issue-reopened", entries[1])
	}
}
