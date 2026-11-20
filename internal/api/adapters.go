package api

import (
	"context"
	"errors"
	"slices"

	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/forgejo"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/hub"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/setup"
)

// hubAdapter adapts hub.Client to setup.HubOps.
type hubAdapter struct {
	h hub.Client
}

var _ setup.HubOps = (*hubAdapter)(nil)

func (a *hubAdapter) CreateAgentImage(ctx context.Context, groupID, displayName, imageURL string, mcpServers []string, env []setup.EnvVar) (imageID string, err error) {
	hubEnv := make([]hub.EnvVar, len(env))
	for i, e := range env {
		hubEnv[i] = hub.EnvVar{Name: e.Name, Value: e.Value, Secret: e.Secret}
	}
	spec := hub.ImageSpec{
		GroupID:     groupID,
		DisplayName: displayName,
		ImageURL:    imageURL,
		MCPServers:  mcpServers,
		Env:         hubEnv,
	}
	ref, err := a.h.CreateAgentImage(ctx, spec)
	return ref.ID, err
}

func (a *hubAdapter) RefreshImageTools(ctx context.Context, imageID string) ([]setup.ImageTool, map[string]string, error) {
	tools, err := a.h.RefreshImageTools(ctx, imageID)
	if err == nil {
		result := make([]setup.ImageTool, len(tools))
		for i, t := range tools {
			result[i] = setup.ImageTool{Name: t.Name, Kind: t.Kind, Enabled: t.Enabled}
		}
		return result, nil, nil
	}

	// Check if error is a tool refresh error with server details
	var tfe *hub.ToolRefreshError
	if errors.As(err, &tfe) {
		// Suppress the error only when it's the plugin-forgejo server alone
		if tfe.OnlyMissing("plugin-forgejo") {
			// Still return the tools we got (usually empty on refresh error)
			result := make([]setup.ImageTool, len(tools))
			for i, t := range tools {
				result[i] = setup.ImageTool{Name: t.Name, Kind: t.Kind, Enabled: t.Enabled}
			}
			return result, nil, nil
		}

		// Build server errors map from the servers list
		serverErrors := make(map[string]string)
		for _, server := range tfe.Servers {
			serverErrors[server] = "hub could not read MCP server"
		}
		// Still try to return the tools we got (usually empty on refresh error)
		result := make([]setup.ImageTool, len(tools))
		for i, t := range tools {
			result[i] = setup.ImageTool{Name: t.Name, Kind: t.Kind, Enabled: t.Enabled}
		}
		return result, serverErrors, err
	}
	return nil, nil, err
}

func (a *hubAdapter) SetImageTools(ctx context.Context, imageID string, tools []setup.ImageTool) error {
	hubTools := make([]hub.ImageTool, len(tools))
	for i, t := range tools {
		hubTools[i] = hub.ImageTool{Name: t.Name, Kind: t.Kind, Enabled: t.Enabled}
	}
	return a.h.SetImageTools(ctx, imageID, hubTools)
}

func (a *hubAdapter) CreatePersona(ctx context.Context, groupID, name, description, text string) (personaID string, err error) {
	ref, err := a.h.CreatePersona(ctx, groupID, name, description, text)
	return ref.ID, err
}

// CreateAgent files the role agent with the hub. The llm is the settings' value
// for the role: the wizard carries no key and no endpoint URL of its own, so the
// listed providers' key fields on the spec are never filled here — whatever keys
// those providers need are the hub's to hold, not the plugin's to pass (R4).
// The custom pair and the three vendor keys are the exception the settings store
// exists for: each rides to its own hub block only when the role runs on that
// provider (setup.llmWriteFor leaves the rest empty).
func (a *hubAdapter) CreateAgent(ctx context.Context, groupID, agentName, imageID, personaID string, mcpServers, skills []string, env []setup.EnvVar, llm setup.LLMWrite, enabledTools []string, hours *setup.WorkingHours) (agentID string, err error) {
	hubEnv := make([]hub.EnvVar, len(env))
	for i, e := range env {
		hubEnv[i] = hub.EnvVar{Name: e.Name, Value: e.Value, Secret: e.Secret}
	}
	spec := hub.AgentSpec{
		Name:         agentName,
		GroupID:      groupID,
		ImageRef:     imageID,
		PersonaID:    personaID,
		MCPServers:   mcpServers,
		Skills:       skills,
		EnabledTools: enabledTools,
		Env:          hubEnv,
		CustomURL:    llm.CustomURL,
		CustomKey:    llm.CustomKey,
		OpenAIKey:    llm.OpenAIKey,
		AnthropicKey: llm.AnthropicKey,
		GoogleKey:    llm.GoogleKey,
		// The sign-in method rides alone: llmWriteFor leaves the key empty when a
		// role signs in, so the block the hub is told about names a method and no
		// key — the pair the hub refuses can never be built here.
		OpenAIAuthMethod: llm.OpenAIAuthMethod,
	}
	if llm.Provider != "" || llm.Model != "" {
		spec.LLM = &hub.LLMSpec{Provider: llm.Provider, Model: llm.Model}
	}
	spec.WorkingHours = hubWorkingHours(hours)

	ref, err := a.h.CreateAgent(ctx, spec)
	return ref.ID, err
}

func (a *hubAdapter) CreateConnector(ctx context.Context, groupID, connectorName string) (connectorID, webhookEndpoint, webhookSecret string, err error) {
	conn, err := a.h.CreateConnector(ctx, groupID, connectorName)
	return conn.ID, conn.WebhookEndpoint, conn.WebhookSecretValue, err
}

func (a *hubAdapter) CreateTrigger(ctx context.Context, groupID string, trigger setup.Trigger) (triggerID string, err error) {
	ref, err := a.h.CreateTrigger(ctx, groupID, trigger)
	return ref.ID, err
}

func (a *hubAdapter) CreateCronTrigger(ctx context.Context, groupID string, trigger setup.CronTrigger) (cronTriggerID string, err error) {
	ref, err := a.h.CreateCronTrigger(ctx, groupID, trigger)
	return ref.ID, err
}

func (a *hubAdapter) DeleteCronTrigger(ctx context.Context, cronTriggerID string) error {
	return a.h.DeleteCronTrigger(ctx, cronTriggerID)
}

func (a *hubAdapter) RegisterMCPServer(ctx context.Context, groupID, name, url, tokenFromEnv string) (serverID string, err error) {
	spec := hub.MCPServerSpec{
		Name:         name,
		GroupID:      groupID,
		URL:          url,
		TokenFromEnv: tokenFromEnv,
	}
	ref, err := a.h.RegisterMCPServer(ctx, spec)
	return ref.ID, err
}

func (a *hubAdapter) DeleteAgent(ctx context.Context, agentID string) error {
	return a.h.DeleteAgent(ctx, agentID)
}

func (a *hubAdapter) DeleteAgentImage(ctx context.Context, imageID string) error {
	return a.h.DeleteAgentImage(ctx, imageID)
}

func (a *hubAdapter) DeletePersona(ctx context.Context, personaID string) error {
	return a.h.DeletePersona(ctx, personaID)
}

func (a *hubAdapter) UpdatePersona(ctx context.Context, personaID, text string) error {
	return a.h.UpdatePersona(ctx, personaID, text)
}

func (a *hubAdapter) DeleteConnector(ctx context.Context, connectorID string) error {
	return a.h.DeleteConnector(ctx, connectorID)
}

func (a *hubAdapter) DeleteMCPServer(ctx context.Context, serverID string) error {
	return a.h.DeleteMCPServer(ctx, serverID)
}

func (a *hubAdapter) DeleteTrigger(ctx context.Context, triggerID string) error {
	return a.h.DeleteTrigger(ctx, triggerID)
}

func (a *hubAdapter) UpdateAgentImage(ctx context.Context, imageID, imageURL string) error {
	update := hub.ImageUpdate{
		ImageURL: &imageURL,
	}
	return a.h.UpdateAgentImage(ctx, imageID, update)
}

// UpdateAgentImageEnv restates an image's whole env list. The hub replaces the env
// it holds with exactly what arrives, so the caller sends every entry the image is
// to keep; an empty value on a name the hub stores as a secret keeps the stored
// value, which is how the turn-timeout write resends FORGEJO_TOKEN without ever
// having read it (setup.HubOps).
func (a *hubAdapter) UpdateAgentImageEnv(ctx context.Context, imageID string, env []setup.EnvVar) error {
	hubEnv := make([]hub.EnvVar, len(env))
	for i, e := range env {
		hubEnv[i] = hub.EnvVar{Name: e.Name, Value: e.Value, Secret: e.Secret}
	}
	return a.h.UpdateAgentImage(ctx, imageID, hub.ImageUpdate{Env: &hubEnv})
}

// UpdateAgent writes whatever the edit changed. llm.Provider and llm.Model are
// the only carriers of hubUpdate.LLM, and they carry the settings' values. Each
// key rides when the caller named it (the role runs on that provider); an empty
// one leaves that block alone — the hub keeps what it holds, which is what
// "keep the key the agents hold" means end to end.
//
// enabledTools is the role's tool grant, in the image catalog's spellings. An empty
// one is left off the request, which is the hub keeping the grant it holds: sending
// an empty one would read at the runtime as "every tool the image offers", the exact
// opposite of a restriction.
func (a *hubAdapter) UpdateAgent(ctx context.Context, agentID, displayName string, llm setup.LLMWrite, enabledTools []string, hours setup.WorkingHoursWrite) error {
	hubUpdate := hub.AgentUpdate{}
	if displayName != "" {
		hubUpdate.Name = &displayName
	}
	if len(enabledTools) > 0 {
		grant := enabledTools
		hubUpdate.EnabledTools = &grant
	}
	if llm.Provider != "" || llm.Model != "" {
		hubUpdate.LLM = &hub.LLMSpec{Provider: llm.Provider, Model: llm.Model}
	}
	if llm.CustomURL != "" {
		hubUpdate.CustomURL = &llm.CustomURL
	}
	if llm.CustomKey != "" {
		hubUpdate.CustomKey = &llm.CustomKey
	}
	if llm.OpenAIKey != "" {
		hubUpdate.OpenAIKey = &llm.OpenAIKey
	}
	if llm.OpenAIAuthMethod != "" {
		hubUpdate.OpenAIAuthMethod = &llm.OpenAIAuthMethod
	}
	if llm.AnthropicKey != "" {
		hubUpdate.AnthropicKey = &llm.AnthropicKey
	}
	if llm.GoogleKey != "" {
		hubUpdate.GoogleKey = &llm.GoogleKey
	}
	// The window's three states pass through as they stand: nothing, a window, or the
	// hub's clear. Collapsing them here is what the two separate fields exist to stop.
	switch {
	case hours.Clear:
		hubUpdate.ClearWorkingHours = true
	case hours.Set != nil:
		hubUpdate.WorkingHours = hubWorkingHours(hours.Set)
	}
	return a.h.UpdateAgent(ctx, agentID, hubUpdate)
}

// hubWorkingHours is one window in the hub's shape. setup keeps its own type because setup
// does not import hub — the dependency runs the other way, the way the trigger types on this
// file's request shapes already show — so the two shapes are translated here, once.
func hubWorkingHours(w *setup.WorkingHours) *hub.WorkingHours {
	if w == nil {
		return nil
	}
	return &hub.WorkingHours{
		Timezone: w.Timezone,
		Days:     slices.Clone(w.Days),
		Start:    w.Start,
		End:      w.End,
	}
}

func (a *hubAdapter) ListTriggers(ctx context.Context, connectorID string) ([]setup.TriggerEntry, error) {
	triggers, err := a.h.ListTriggers(ctx, connectorID)
	if err != nil {
		return nil, err
	}
	result := make([]setup.TriggerEntry, len(triggers))
	for i, t := range triggers {
		result[i] = setup.TriggerEntry{ID: t.ID, Name: t.Name}
	}
	return result, nil
}

// forgejoAdapter adapts forgejo.Admin and forgejo.Client to setup.ForgejoOps.
type forgejoAdapter struct {
	client *forgejo.Client
	admin  *forgejo.Admin
	org    string
}

var _ setup.ForgejoOps = (*forgejoAdapter)(nil)

func (a *forgejoAdapter) CreateUser(ctx context.Context, login, fullName, password string) error {
	return a.admin.CreateUser(ctx, login, fullName, password)
}

func (a *forgejoAdapter) MintToken(ctx context.Context, login, password string, scopes []string) (token string, err error) {
	// MintToken takes a name parameter (token name); we use the login as the token name
	return a.client.MintToken(ctx, login, password, "token-"+login, scopes)
}

func (a *forgejoAdapter) CreateTeam(ctx context.Context, teamName string, units map[string]string) (teamID int64, err error) {
	return a.admin.CreateTeam(ctx, a.org, teamName, units)
}

func (a *forgejoAdapter) AddTeamMember(ctx context.Context, teamID int64, login string) error {
	return a.admin.AddTeamMember(ctx, teamID, login)
}

func (a *forgejoAdapter) CreateOrgHook(ctx context.Context, url, secret string, events []string) (hookID int64, err error) {
	hook := forgejo.Hook{
		URL:    url,
		Secret: secret,
		Events: events,
	}
	return a.admin.CreateOrgHook(ctx, a.org, hook)
}

func (a *forgejoAdapter) DeleteUser(ctx context.Context, login string) error {
	return a.admin.DeleteUser(ctx, login)
}

func (a *forgejoAdapter) DeleteTeam(ctx context.Context, teamID int64) error {
	return a.admin.DeleteTeam(ctx, teamID)
}

func (a *forgejoAdapter) DeleteOrgHook(ctx context.Context, hookID int64) error {
	return a.admin.DeleteOrgHook(ctx, a.org, hookID)
}
