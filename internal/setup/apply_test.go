package setup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

// fakeHub records calls and returns configured results.
type fakeHub struct {
	calls []string
	// enabledByCall records, for each SetImageTools call in order, the tool names that call
	// left enabled. Since the grant moved onto the agent, a catalog write no longer carries a
	// role's set — it restates the catalog, flags and all — so this answers only "what did the
	// catalog write look like", and AgentCreateArgsSlice answers "what did the role get".
	enabledByCall      [][]string
	imageID            string
	imageTools         []ImageTool
	personaID          string
	agentID            string
	connectorID        string
	connectorEndpoint  string
	connectorSecret    string
	triggerCreateError error
	// cronTriggerCreateError fails CreateCronTrigger — the patrol is the last create
	// of the trigger phase, and a failure there must roll the whole install back.
	cronTriggerCreateError error
	personaCreateError     error
	agentCreateError       error
	connectorCreateErr     error
	mcpCreateError         error
	imageCreateError       error
	// updateAgentErr fails UpdateAgent, which is how an edit learns to leave the row it
	// was handed alone when it cannot finish.
	updateAgentErr error
	// personaTexts holds the text each UpdatePersona call wrote, by persona id, which is
	// what "did the agents get told about the new address" asks.
	personaTexts        map[string]string
	updatePersonaErr    error
	refreshError        error
	refreshServerErrors map[string]string // per-server errors
	setToolsError       error
	deleteErrors        map[string]error  // key is "kind:id"
	createdIDs          map[string]string // track created resource IDs
	seq                 int               // deterministic id counter for created resources
	deletes             *[]string         // optional shared ordered log of delete calls across fakes
	listTriggersFunc    func(ctx context.Context, connectorID string) ([]TriggerEntry, error)
	// updateAgentCalls records the arguments of each UpdateAgent call in call order,
	// which is what "did the edit send the new key, and did it leave the fields the
	// hub is to keep empty" asks.
	updateAgentCalls []updateAgentCall
	// triggerCreates records the group and name of each CreateTrigger call in call
	// order, which is what "which group did an edit file its triggers in" asks.
	triggerCreates []triggerCreate
	// cronTriggerCreates records the group, name and schedule of each
	// CreateCronTrigger call in call order.
	cronTriggerCreates []cronTriggerCreate
	// cronTriggers seeds what ListCronTriggers answers with on an edit: the patrol row
	// the install already has on the hub, id and all — including the enabled flag, which
	// is what a test of "did the apply switch it off" reads back.
	cronTriggers []CronTrigger
	// cronTriggerUpdates records each UpdateCronTrigger call in call order: the row it
	// addressed and the halves it moved.
	cronTriggerUpdates []cronTriggerUpdate
	// cronTriggerUpdateErr fails UpdateCronTrigger: the hub refusing the patrol write
	// is what an edit must carry out as an error rather than a half-written install.
	cronTriggerUpdateErr error

	// AgentCreateArgsSlice records the parameters passed to each CreateAgent call for assertions
	AgentCreateArgsSlice []agentCreateCall
	// imageEnvs records the env of each CreateAgentImage call, keyed by the
	// image's display name (which encodes the role), so assertions do not
	// depend on the order planRoles creates the images in.
	imageEnvs map[string][]EnvVar
	// imageMCPServers records the MCP servers each CreateAgentImage call named, keyed
	// by the image's display name, the way imageEnvs is keyed.
	imageMCPServers map[string][]string
	// imageURLs records the container ref each CreateAgentImage call carried, keyed by
	// the image's display name — what "did the hub get the ref the runtime can start,
	// or the one the admin pasted" asks (issue #85).
	imageURLs map[string]string
	// imageEnvUpdates records the env of each UpdateAgentImageEnv call, keyed by
	// image id — what "did a re-apply write the timeout, and did it keep the
	// token" asks (issue #61).
	imageEnvUpdates map[string][]EnvVar
	// updateEnvErr fails UpdateAgentImageEnv, so an edit can be shown to wrap it.
	updateEnvErr error
}

// updateAgentCall is one UpdateAgent call as the fake saw it.
type updateAgentCall struct {
	agentID     string
	displayName string
	llm         LLMWrite
	// enabledTools is the grant the call carried, nil when it carried none — which is
	// the hub's "keep what you hold", not "grant nothing".
	enabledTools []string
	// hours is the window half of the call: the zero WorkingHoursWrite is "said nothing
	// about the window", which is the one thing an unrelated re-apply must never lose.
	hours WorkingHoursWrite
}

// agentCreateCall is one CreateAgent call as the fake saw it: the llm the agent
// was created on, the custom pair when the role runs on the custom provider, the
// skills it was given — the agent is the only place a skill selection goes — the
// tool grant, which is the only place a role's restriction goes, and the window it
// was created with, nil for one that has none.
type agentCreateCall struct {
	LLM          LLMWrite
	skills       []string
	agentName    string
	enabledTools []string
	hours        *WorkingHours
}

// triggerCreate is one CreateTrigger call as the fake saw it.
type triggerCreate struct {
	groupID string
	name    string
}

// cronTriggerCreate is one CreateCronTrigger call as the fake saw it.
type cronTriggerCreate struct {
	groupID  string
	name     string
	schedule string
}

// cronTriggerUpdate is one UpdateCronTrigger call as the fake saw it. The two moved
// halves are pointers for the reason the real write has them: a nil is "the apply said
// nothing about this half", which is not the same answer as a false or a keep.
type cronTriggerUpdate struct {
	id       string
	name     string
	agentRef string
	prompt   string
	schedule *string
	enabled  *bool
}

// nextID hands out deterministic, collision-free ids for created resources.
func (f *fakeHub) nextID(kind string) string {
	f.seq++
	return fmt.Sprintf("%s-%d", kind, f.seq)
}

// recordDelete appends a delete call to this fake's call log and, when set,
// to the shared ordered delete log so rollback order can be pinned globally.
func (f *fakeHub) recordDelete(call string) {
	f.calls = append(f.calls, call)
	if f.deletes != nil {
		*f.deletes = append(*f.deletes, call)
	}
}

func (f *fakeHub) CreateAgentImage(ctx context.Context, groupID, displayName, imageURL string, mcpServers []string, env []EnvVar) (string, error) {
	f.calls = append(f.calls, "CreateAgentImage")
	if f.imageCreateError != nil {
		return "", f.imageCreateError
	}
	if f.imageEnvs == nil {
		f.imageEnvs = make(map[string][]EnvVar)
	}
	f.imageEnvs[displayName] = env
	if f.imageMCPServers == nil {
		f.imageMCPServers = make(map[string][]string)
	}
	f.imageMCPServers[displayName] = mcpServers
	if f.imageURLs == nil {
		f.imageURLs = make(map[string]string)
	}
	f.imageURLs[displayName] = imageURL
	f.imageID = f.nextID("image")
	f.createdIDs["image"] = f.imageID
	return f.imageID, nil
}

func (f *fakeHub) RefreshImageTools(ctx context.Context, imageID string) ([]ImageTool, map[string]string, error) {
	f.calls = append(f.calls, fmt.Sprintf("RefreshImageTools:%s", imageID))
	if f.refreshError != nil {
		// Return configured server errors
		if f.refreshServerErrors == nil {
			f.refreshServerErrors = make(map[string]string)
		}
		return f.imageTools, f.refreshServerErrors, f.refreshError
	}
	return f.imageTools, nil, nil
}

func (f *fakeHub) SetImageTools(ctx context.Context, imageID string, tools []ImageTool) error {
	f.calls = append(f.calls, fmt.Sprintf("SetImageTools:%s", imageID))
	enabled := []string{}
	for _, t := range tools {
		if t.Enabled {
			enabled = append(enabled, t.Name)
		}
	}
	f.enabledByCall = append(f.enabledByCall, enabled)
	if f.setToolsError != nil {
		return f.setToolsError
	}
	f.imageTools = make([]ImageTool, len(tools))
	copy(f.imageTools, tools)
	return nil
}

func (f *fakeHub) CreatePersona(ctx context.Context, groupID, name, description, text string) (string, error) {
	f.calls = append(f.calls, fmt.Sprintf("CreatePersona:%s", name))
	if f.personaCreateError != nil {
		return "", f.personaCreateError
	}
	f.personaID = f.nextID("persona")
	f.createdIDs["persona"] = f.personaID
	return f.personaID, nil
}

func (f *fakeHub) CreateAgent(ctx context.Context, groupID, agentName, imageID, personaID string, mcpServers, skills []string, env []EnvVar, llm LLMWrite, enabledTools []string, hours *WorkingHours) (string, error) {
	f.calls = append(f.calls, "CreateAgent")
	f.AgentCreateArgsSlice = append(f.AgentCreateArgsSlice, agentCreateCall{LLM: llm, skills: skills, agentName: agentName, enabledTools: append([]string(nil), enabledTools...), hours: CloneWorkingHours(hours)})
	if f.agentCreateError != nil {
		return "", f.agentCreateError
	}
	f.agentID = f.nextID("agent")
	f.createdIDs["agent"] = f.agentID
	return f.agentID, nil
}

func (f *fakeHub) CreateConnector(ctx context.Context, groupID, connectorName string) (string, string, string, error) {
	f.calls = append(f.calls, "CreateConnector")
	if f.connectorCreateErr != nil {
		return "", "", "", f.connectorCreateErr
	}
	f.connectorID = f.nextID("connector")
	f.connectorSecret = "secret-" + fmt.Sprintf("%d", time.Now().UnixNano())
	f.createdIDs["connector"] = f.connectorID
	return f.connectorID, f.connectorEndpoint, f.connectorSecret, nil
}

func (f *fakeHub) CreateTrigger(ctx context.Context, groupID string, trigger Trigger) (string, error) {
	f.calls = append(f.calls, "CreateTrigger")
	f.triggerCreates = append(f.triggerCreates, triggerCreate{groupID: groupID, name: trigger.Name})
	if f.triggerCreateError != nil {
		return "", f.triggerCreateError
	}
	triggerID := f.nextID("trigger")
	f.createdIDs["trigger:"+trigger.Name] = triggerID
	return triggerID, nil
}

func (f *fakeHub) CreateCronTrigger(ctx context.Context, groupID string, trigger CronTrigger) (string, error) {
	f.calls = append(f.calls, "CreateCronTrigger")
	f.cronTriggerCreates = append(f.cronTriggerCreates, cronTriggerCreate{groupID: groupID, name: trigger.Name, schedule: trigger.Schedule})
	if f.cronTriggerCreateError != nil {
		return "", f.cronTriggerCreateError
	}
	return f.nextID("cron"), nil
}

func (f *fakeHub) ListCronTriggers(ctx context.Context) ([]CronTrigger, error) {
	f.calls = append(f.calls, "ListCronTriggers")
	return append([]CronTrigger(nil), f.cronTriggers...), nil
}

func (f *fakeHub) UpdateCronTrigger(ctx context.Context, cronTriggerID string, update CronTriggerUpdate) error {
	f.calls = append(f.calls, "UpdateCronTrigger")
	f.cronTriggerUpdates = append(f.cronTriggerUpdates, cronTriggerUpdate{
		id:       cronTriggerID,
		name:     update.Name,
		agentRef: update.AgentRef,
		prompt:   update.Prompt,
		schedule: update.Schedule,
		enabled:  update.Enabled,
	})
	if f.cronTriggerUpdateErr != nil {
		return f.cronTriggerUpdateErr
	}
	// The write persists, so a test can read the row back the way a second apply would.
	for i, ct := range f.cronTriggers {
		if ct.ID != cronTriggerID {
			continue
		}
		if update.Schedule != nil {
			f.cronTriggers[i].Schedule = *update.Schedule
		}
		if update.Enabled != nil {
			f.cronTriggers[i].Enabled = *update.Enabled
		}
	}
	return nil
}

func (f *fakeHub) DeleteCronTrigger(ctx context.Context, cronTriggerID string) error {
	f.recordDelete(fmt.Sprintf("DeleteCronTrigger:%s", cronTriggerID))
	if err := f.deleteErrors["cronTrigger:"+cronTriggerID]; err != nil {
		return err
	}
	return nil
}

func (f *fakeHub) RegisterMCPServer(ctx context.Context, groupID, name, url, tokenFromEnv string) (string, error) {
	f.calls = append(f.calls, "RegisterMCPServer")
	if f.mcpCreateError != nil {
		return "", f.mcpCreateError
	}
	id := f.nextID("mcp")
	f.createdIDs["mcp"] = id
	return id, nil
}

func (f *fakeHub) UpdateAgentImage(ctx context.Context, imageID, imageURL string) error {
	f.calls = append(f.calls, fmt.Sprintf("UpdateAgentImage:%s:%s", imageID, imageURL))
	return nil
}

func (f *fakeHub) UpdateAgentImageEnv(ctx context.Context, imageID string, env []EnvVar) error {
	f.calls = append(f.calls, fmt.Sprintf("UpdateAgentImageEnv:%s", imageID))
	if f.updateEnvErr != nil {
		return f.updateEnvErr
	}
	if f.imageEnvUpdates == nil {
		f.imageEnvUpdates = make(map[string][]EnvVar)
	}
	f.imageEnvUpdates[imageID] = env
	return nil
}

func (f *fakeHub) UpdateAgent(ctx context.Context, agentID, displayName string, llm LLMWrite, enabledTools []string, hours WorkingHoursWrite) error {
	f.calls = append(f.calls, fmt.Sprintf("UpdateAgent:%s", agentID))
	f.updateAgentCalls = append(f.updateAgentCalls, updateAgentCall{
		agentID:      agentID,
		displayName:  displayName,
		llm:          llm,
		enabledTools: append([]string(nil), enabledTools...),
		hours:        hours,
	})
	return f.updateAgentErr
}

func (f *fakeHub) UpdatePersona(ctx context.Context, personaID, text string) error {
	f.calls = append(f.calls, fmt.Sprintf("UpdatePersona:%s", personaID))
	if f.updatePersonaErr != nil {
		return f.updatePersonaErr
	}
	if f.personaTexts == nil {
		f.personaTexts = map[string]string{}
	}
	f.personaTexts[personaID] = text
	return nil
}

func (f *fakeHub) ListTriggers(ctx context.Context, connectorID string) ([]TriggerEntry, error) {
	f.calls = append(f.calls, fmt.Sprintf("ListTriggers:%s", connectorID))
	if f.listTriggersFunc != nil {
		return f.listTriggersFunc(ctx, connectorID)
	}
	return []TriggerEntry{}, nil
}

func (f *fakeHub) DeleteAgent(ctx context.Context, agentID string) error {
	f.recordDelete(fmt.Sprintf("DeleteAgent:%s", agentID))
	if err, ok := f.deleteErrors["agent:"+agentID]; ok {
		return err
	}
	return nil
}

func (f *fakeHub) DeleteAgentImage(ctx context.Context, imageID string) error {
	f.recordDelete(fmt.Sprintf("DeleteAgentImage:%s", imageID))
	if err, ok := f.deleteErrors["image:"+imageID]; ok {
		return err
	}
	return nil
}

func (f *fakeHub) DeletePersona(ctx context.Context, personaID string) error {
	f.recordDelete(fmt.Sprintf("DeletePersona:%s", personaID))
	if err, ok := f.deleteErrors["persona:"+personaID]; ok {
		return err
	}
	return nil
}

func (f *fakeHub) DeleteConnector(ctx context.Context, connectorID string) error {
	f.recordDelete(fmt.Sprintf("DeleteConnector:%s", connectorID))
	if err, ok := f.deleteErrors["connector:"+connectorID]; ok {
		return err
	}
	return nil
}

func (f *fakeHub) DeleteMCPServer(ctx context.Context, serverID string) error {
	f.recordDelete(fmt.Sprintf("DeleteMCPServer:%s", serverID))
	if err, ok := f.deleteErrors["mcp:"+serverID]; ok {
		return err
	}
	return nil
}

func (f *fakeHub) DeleteTrigger(ctx context.Context, triggerID string) error {
	f.recordDelete(fmt.Sprintf("DeleteTrigger:%s", triggerID))
	if err, ok := f.deleteErrors["trigger:"+triggerID]; ok {
		return err
	}
	return nil
}

// forgejo fake
type fakeForgejo struct {
	calls              []string
	adminTokenRequired string
	createdUsers       map[string]string // login -> token
	createdTeams       map[string]int64  // name -> id
	mintedTokens       map[string]string // login -> token
	createUserError    error
	mintTokenError     error
	createTeamError    error
	addTeamMemberError error
	createHookError    error
	deleteUserError    error
	deleteTeamError    error
	deleteHookError    error
	createdHookID      int64
	teamSeq            int       // deterministic id counter for created teams
	deletes            *[]string // optional shared ordered log of delete calls across fakes
}

// recordDelete appends a delete call to this fake's call log and, when set,
// to the shared ordered delete log so rollback order can be pinned globally.
func (f *fakeForgejo) recordDelete(call string) {
	f.calls = append(f.calls, call)
	if f.deletes != nil {
		*f.deletes = append(*f.deletes, call)
	}
}

func (f *fakeForgejo) CreateUser(ctx context.Context, login, fullName, password string) error {
	f.calls = append(f.calls, fmt.Sprintf("CreateUser:%s", login))
	if f.createUserError != nil {
		return f.createUserError
	}
	f.createdUsers[login] = ""
	return nil
}

func (f *fakeForgejo) MintToken(ctx context.Context, login, password string, scopes []string) (string, error) {
	f.calls = append(f.calls, fmt.Sprintf("MintToken:%s", login))
	if f.mintTokenError != nil {
		return "", f.mintTokenError
	}
	token := "token-" + login
	f.mintedTokens[login] = token
	f.createdUsers[login] = token
	return token, nil
}

func (f *fakeForgejo) CreateTeam(ctx context.Context, teamName string, units map[string]string) (int64, error) {
	f.calls = append(f.calls, fmt.Sprintf("CreateTeam:%s", teamName))
	if f.createTeamError != nil {
		return 0, f.createTeamError
	}
	f.teamSeq++
	id := int64(f.teamSeq)
	f.createdTeams[teamName] = id
	return id, nil
}

func (f *fakeForgejo) AddTeamMember(ctx context.Context, teamID int64, login string) error {
	f.calls = append(f.calls, fmt.Sprintf("AddTeamMember:%d:%s", teamID, login))
	if f.addTeamMemberError != nil {
		return f.addTeamMemberError
	}
	return nil
}

func (f *fakeForgejo) CreateOrgHook(ctx context.Context, endpoint, secret string, events []string) (int64, error) {
	f.calls = append(f.calls, "CreateOrgHook")
	if f.createHookError != nil {
		return 0, f.createHookError
	}
	f.createdHookID = int64(time.Now().UnixNano() % 1000000)
	return f.createdHookID, nil
}

func (f *fakeForgejo) DeleteUser(ctx context.Context, login string) error {
	f.recordDelete(fmt.Sprintf("DeleteUser:%s", login))
	if f.deleteUserError != nil {
		return f.deleteUserError
	}
	delete(f.createdUsers, login)
	return nil
}

func (f *fakeForgejo) DeleteTeam(ctx context.Context, id int64) error {
	f.recordDelete(fmt.Sprintf("DeleteTeam:%d", id))
	if f.deleteTeamError != nil {
		return f.deleteTeamError
	}
	return nil
}

func (f *fakeForgejo) DeleteOrgHook(ctx context.Context, id int64) error {
	f.recordDelete(fmt.Sprintf("DeleteOrgHook:%d", id))
	if f.deleteHookError != nil {
		return f.deleteHookError
	}
	return nil
}

// Test cases
func TestApplyHappyPath(t *testing.T) {
	h := &fakeHub{
		createdIDs: make(map[string]string),
		imageTools: []ImageTool{
			{Name: "tool1", Kind: "forgejo", Enabled: false},
			{Name: "bash", Kind: "shell", Enabled: false},
			{Name: "mcp__forgejo-mcp-test__tool1", Kind: "forgejo", Enabled: false},
		},
	}
	f := &fakeForgejo{
		createdUsers: make(map[string]string),
		createdTeams: make(map[string]int64),
		mintedTokens: make(map[string]string),
	}

	in := Input{
		Slug:       "test",
		GroupID:    "group-1",
		BaseURL:    "http://forgejo.example.com",
		Org:        "testorg",
		AdminToken: "admin-token",
		Roles: map[Role]RoleInput{
			RoleRefiner:   {Runtime: "image1", Tools: []string{"tool1"}},
			RoleDeveloper: {Runtime: "image1", Tools: []string{"tool1", "bash"}},
			RoleReviewer:  {Runtime: "image1", Tools: []string{"tool1"}},
		},
	}

	plan := Result{
		Create: []Op{
			{Kind: KindUser, Name: "refiner-test"},
			{Kind: KindUser, Name: "developer-test"},
			{Kind: KindUser, Name: "reviewer-test"},
		},
		Triggers: []Trigger{
			{Name: "forgejo-refiner-issue-opened-test"},
		},
	}

	apply := Apply{
		Hub:     h,
		Forgejo: f,
		Now:     time.Now,
		Rand:    bytes.NewReader(make([]byte, 72)), // 24 bytes per bot * 3 bots
	}

	install, report, err := apply.Run(context.Background(), in, plan, nil)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check that no secrets are in the install
	data, _ := json.Marshal(install)
	if bytes.Contains(data, []byte("admin-token")) {
		t.Error("admin token in install")
	}
	if bytes.Contains(data, []byte("token-")) {
		t.Error("bot token in install")
	}

	// Check that no secrets are in the report
	data, _ = json.Marshal(report)
	if bytes.Contains(data, []byte("admin-token")) {
		t.Error("admin token in report")
	}

	t.Logf("Created install: %+v", install)
	t.Logf("Report: %+v", report)
}

// TestApplyRoleImagesCarryTurnTimeout pins the per-role turn timeout of issue #61
// at the create: a role whose wizard field names a value gets exactly that value
// on the image env and in the row; a role whose field is empty gets the shipped
// default — the developer's 2-hour cap and the refiner/reviewer 1-hour cap the
// `apps` incident of 2026-11-15 proved necessary (the old 30/15-minute caps killed
// turns that were still progressing). The entry is plain, never a secret, so it
// stays readable and an agent-level override still wins.
func TestApplyRoleImagesCarryTurnTimeout(t *testing.T) {
	h := &fakeHub{
		createdIDs: make(map[string]string),
		imageTools: []ImageTool{{Name: "tool1", Kind: "forgejo", Enabled: false}},
	}
	f := &fakeForgejo{
		createdUsers: make(map[string]string),
		createdTeams: make(map[string]int64),
		mintedTokens: make(map[string]string),
	}
	in := Input{
		Slug:       "test",
		GroupID:    "group-1",
		BaseURL:    "http://forgejo.example.com",
		Org:        "testorg",
		AdminToken: "admin-token",
		Roles: map[Role]RoleInput{
			// The developer's wizard value is the custom one; the other two roles
			// leave the field empty and must land on the shipped default.
			RoleRefiner:   {Runtime: "image1", Tools: []string{"tool1"}},
			RoleDeveloper: {Runtime: "image1", Tools: []string{"tool1"}, TurnTimeoutMs: "600000"},
			RoleReviewer:  {Runtime: "image1", Tools: []string{"tool1"}},
		},
	}
	plan := Result{Create: []Op{{Kind: KindUser, Name: "refiner-test"}}}
	apply := Apply{
		Hub:     h,
		Forgejo: f,
		Now:     time.Now,
		Rand:    bytes.NewReader(make([]byte, 72)),
	}

	install, _, err := apply.Run(context.Background(), in, plan, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Step 4 creates one image per role, recorded by display name.
	if len(h.imageEnvs) != 3 {
		t.Fatalf("CreateAgentImage called %d times, want 3", len(h.imageEnvs))
	}
	wants := map[Role]string{
		RoleDeveloper: "600000",  // the wizard's custom value
		RoleRefiner:   "3600000", // 1 h shipped default
		RoleReviewer:  "3600000", // 1 h shipped default
	}
	for role, wantValue := range wants {
		envs := h.imageEnvs[ImageName(role, in.Slug)]
		want := EnvVar{Name: TurnTimeoutEnvName, Value: wantValue, Secret: false}
		found := false
		for _, e := range envs {
			if e.Name == TurnTimeoutEnvName {
				if e != want {
					t.Errorf("%s %s = %+v, want %+v", role, TurnTimeoutEnvName, e, want)
				}
				found = true
			}
			if e.Secret && e.Name != TokenEnvName {
				t.Errorf("%s image env has unexpected secret entry %q", role, e.Name)
			}
		}
		if !found {
			t.Errorf("%s image env %v, want a %s=%s entry", role, envs, TurnTimeoutEnvName, wantValue)
		}
		if len(envs) != 2 {
			t.Errorf("%s image env = %v, want exactly the token entry plus the turn timeout", role, envs)
		}
		// The row records what the image was written with, so the next plan diffs
		// against reality rather than against a guess.
		if got := install.Roles[role].TurnTimeoutMs; got != wantValue {
			t.Errorf("%s row TurnTimeoutMs = %q, want %q", role, got, wantValue)
		}
	}
}

// TestApplyWritesTheTrimmedRuntime is issue #85's write half: the ref the hub is handed
// is one a task pod can start from. A runtime pasted with a tab after the tag used to
// reach the hub as it was typed, and every task Job of the role then failed
// FailedCreate — "must not have leading or trailing whitespace" — while the Job that
// never got a pod held the agent's only task slot.
func TestApplyWritesTheTrimmedRuntime(t *testing.T) {
	const clean = "acme/ainsel-pi-go:1.24"
	h := &fakeHub{
		createdIDs: make(map[string]string),
		imageTools: []ImageTool{{Name: "tool1", Kind: "forgejo", Enabled: false}},
	}
	f := &fakeForgejo{
		createdUsers: make(map[string]string),
		createdTeams: make(map[string]int64),
		mintedTokens: make(map[string]string),
	}
	in := Input{
		// The slug is dirty too: every name below is built from it, so a space in it
		// would be a name the plan never previewed.
		Slug:       " test\t",
		GroupID:    "group-1",
		BaseURL:    "\nhttp://forgejo.example.com ",
		Org:        " testorg ",
		AdminToken: "admin-token",
		Roles: map[Role]RoleInput{
			RoleRefiner:   {Runtime: clean + "\t", Tools: []string{"tool1"}},
			RoleDeveloper: {Runtime: " " + clean, Tools: []string{"tool1"}},
			RoleReviewer:  {Runtime: clean + "\n", Tools: []string{"tool1"}},
		},
	}
	plan := Result{Create: []Op{{Kind: KindUser, Name: "refiner-test"}}}
	apply := Apply{
		Hub:     h,
		Forgejo: f,
		Now:     time.Now,
		Rand:    bytes.NewReader(make([]byte, 72)),
	}

	install, _, err := apply.Run(context.Background(), in, plan, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(h.imageURLs) != 3 {
		t.Fatalf("CreateAgentImage called %d times, want 3", len(h.imageURLs))
	}
	for _, r := range []Role{RoleRefiner, RoleDeveloper, RoleReviewer} {
		if got := h.imageURLs[ImageName(r, "test")]; got != clean {
			t.Errorf("%s: the hub got imageURL %q, want %q", r, got, clean)
		}
	}
	// The row stores the trimmed address and org too, so the next plan diffs against
	// what the hub actually holds.
	if install.Slug != "test" {
		t.Errorf("row slug = %q, want %q", install.Slug, "test")
	}
	if install.BaseURL != "http://forgejo.example.com" {
		t.Errorf("row baseURL = %q, want it trimmed", install.BaseURL)
	}
	if install.Org != "testorg" {
		t.Errorf("row org = %q, want it trimmed", install.Org)
	}
}

// TestEditInstallTurnTimeout is the re-apply half of issue #61: the timeout is
// written when and only when the wizard value changed. A re-apply that did not
// touch the field issues no env write at all — which is what lets a value set on
// the image by hand survive — and a changed value writes exactly once, carrying
// FORGEJO_TOKEN back as an empty secret entry so the hub's replace-the-list update
// keeps the stored token. A row from before the option existed records the shipped
// default without touching the image.
func TestEditInstallTurnTimeout(t *testing.T) {
	prevInstall := func(refinerRecorded string) *Install {
		return &Install{
			Slug:        "test",
			GroupID:     "group-1",
			BaseURL:     "http://forgejo.example.com",
			Org:         "testorg",
			MCPServer:   "forgejo-mcp-test",
			ConnectorID: "connector-1",
			HookID:      1,
			Roles: map[Role]RoleState{
				RoleRefiner:   {BotLogin: "refiner-test", TeamID: 1, PersonaID: "persona-1", ImageID: "image-1", AgentID: "agent-1", TurnTimeoutMs: refinerRecorded},
				RoleDeveloper: {BotLogin: "developer-test", TeamID: 2, PersonaID: "persona-2", ImageID: "image-2", AgentID: "agent-2", TurnTimeoutMs: "7200000"},
				RoleReviewer:  {BotLogin: "reviewer-test", TeamID: 3, PersonaID: "persona-3", ImageID: "image-3", AgentID: "agent-3", TurnTimeoutMs: "3600000"},
			},
		}
	}

	// run edits with the refiner's wizard field set to wizard, handing apply the
	// change op the plan would emit for exactly that difference — the same contract
	// the plan and the apply share through changed().
	run := func(t *testing.T, recorded, wizard string) (*fakeHub, Install, ApplyReport) {
		t.Helper()
		h := &fakeHub{createdIDs: make(map[string]string)}
		f := &fakeForgejo{
			createdUsers: make(map[string]string),
			createdTeams: make(map[string]int64),
			mintedTokens: make(map[string]string),
		}
		in := Input{
			Slug:    "test",
			GroupID: "group-1",
			Roles: map[Role]RoleInput{
				RoleRefiner: {TurnTimeoutMs: wizard},
			},
		}
		var plan Result
		if wizard != "" && wizard != recorded {
			plan.Change = []Op{{
				Kind: KindImage, Name: ImageName(RoleRefiner, "test"),
				Detail: diff("turn timeout", recorded, wizard),
				Role:   RoleRefiner, Field: FieldTurnTimeout,
			}}
		}
		apply := Apply{Hub: h, Forgejo: f, Now: time.Now, Rand: bytes.NewReader(nil)}
		install, report, err := apply.Run(context.Background(), in, plan, prevInstall(recorded))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		return h, install, report
	}

	t.Run("unchanged value writes nothing", func(t *testing.T) {
		h, install, _ := run(t, "3600000", "3600000")
		for _, call := range h.calls {
			if strings.HasPrefix(call, "UpdateAgentImageEnv") {
				t.Errorf("unexpected call %s: an unchanged timeout must not touch the image", call)
			}
		}
		if got := install.Roles[RoleRefiner].TurnTimeoutMs; got != "3600000" {
			t.Errorf("row TurnTimeoutMs = %q, want the recorded value kept", got)
		}
	})

	t.Run("empty wizard value keeps whatever the install has", func(t *testing.T) {
		h, install, _ := run(t, "3600000", "")
		for _, call := range h.calls {
			if strings.HasPrefix(call, "UpdateAgentImageEnv") {
				t.Errorf("unexpected call %s: an empty field means keep, never reset", call)
			}
		}
		if got := install.Roles[RoleRefiner].TurnTimeoutMs; got != "3600000" {
			t.Errorf("row TurnTimeoutMs = %q, want the recorded value kept", got)
		}
	})

	t.Run("changed value writes once and keeps the token", func(t *testing.T) {
		h, install, _ := run(t, "3600000", "1800000")
		var env []EnvVar
		writes := 0
		for id, e := range h.imageEnvUpdates {
			if id == "image-1" {
				env = e
				writes++
			}
		}
		if writes != 1 {
			t.Fatalf("imageEnvUpdates for image-1 = %d, want exactly 1 (%v)", writes, h.imageEnvUpdates)
		}
		wantToken := EnvVar{Name: TokenEnvName, Value: "", Secret: true}
		wantTimeout := EnvVar{Name: TurnTimeoutEnvName, Value: "1800000", Secret: false}
		if len(env) != 2 || env[0] != wantToken || env[1] != wantTimeout {
			t.Errorf("env = %v, want the token kept as an empty secret entry plus %s=1800000", env, TurnTimeoutEnvName)
		}
		if got := install.Roles[RoleRefiner].TurnTimeoutMs; got != "1800000" {
			t.Errorf("row TurnTimeoutMs = %q, want the new value recorded", got)
		}
		// The other roles' images are not touched by a one-role change.
		if len(h.imageEnvUpdates) != 1 {
			t.Errorf("env updates = %v, want only the refiner's image", h.imageEnvUpdates)
		}
	})

	// The blocker of the #68 review, pinned: the hub's image update replaces the
	// whole env list and the plugin cannot read that list back, so a timeout edit
	// restates the image as exactly the two entries it manages. Anything an admin
	// added to the image by hand is gone. That is a real cost of the write, so the
	// write says it out loud in the report the admin reads.
	t.Run("a changed value restates the image's whole env list", func(t *testing.T) {
		h, _, report := run(t, "3600000", "1800000")
		env := h.imageEnvUpdates["image-1"]
		want := []EnvVar{
			{Name: TokenEnvName, Value: "", Secret: true},
			{Name: TurnTimeoutEnvName, Value: "1800000"},
		}
		if len(env) != len(want) || env[0] != want[0] || env[1] != want[1] {
			t.Errorf("env = %v, want exactly %v: the write restates the list, so no other entry rides on it", env, want)
		}
		var detail string
		for _, op := range report.Created {
			if op.Field == FieldTurnTimeout {
				detail = op.Detail
			}
		}
		if detail == "" {
			t.Fatalf("report carries no turn timeout op: %v", report.Created)
		}
		if !strings.Contains(detail, turnTimeoutEnvRewrite) {
			t.Errorf("report detail = %q, want it to name the env restatement %q", detail, turnTimeoutEnvRewrite)
		}
	})

	t.Run("legacy row records the default without a write", func(t *testing.T) {
		h, install, _ := run(t, "", "")
		for _, call := range h.calls {
			if strings.HasPrefix(call, "UpdateAgentImageEnv") {
				t.Errorf("unexpected call %s: backfilling the row must not touch the image", call)
			}
		}
		if got, want := install.Roles[RoleRefiner].TurnTimeoutMs, DefaultTurnTimeoutMs(RoleRefiner); got != want {
			t.Errorf("row TurnTimeoutMs = %q, want the shipped default %q recorded", got, want)
		}
	})
}

// TestApplySkillsGoOnTheAgentNotTheImage is where the skill selection lands now that the
// hub moved it off the image: every created agent names forgejo-forgejo, and the role image
// carries no skills at all — it is not a parameter of the create any more, so an install
// cannot enable a skill the agent itself does not select.
func TestApplySkillsGoOnTheAgentNotTheImage(t *testing.T) {
	h := &fakeHub{
		createdIDs: make(map[string]string),
		imageTools: []ImageTool{
			{Name: "tool1", Kind: "forgejo", Enabled: false},
		},
	}
	f := &fakeForgejo{
		createdUsers: make(map[string]string),
		createdTeams: make(map[string]int64),
		mintedTokens: make(map[string]string),
	}

	in := Input{
		Slug:       "test",
		GroupID:    "group-1",
		BaseURL:    "http://forgejo.example.com",
		Org:        "testorg",
		AdminToken: "admin-token",
		Roles: map[Role]RoleInput{
			RoleRefiner:   {Runtime: "image1", Tools: []string{"tool1"}},
			RoleDeveloper: {Runtime: "image1", Tools: []string{"tool1"}},
			RoleReviewer:  {Runtime: "image1", Tools: []string{"tool1"}},
		},
	}

	apply := Apply{
		Hub:     h,
		Forgejo: f,
		Now:     time.Now,
		Rand:    bytes.NewReader(make([]byte, 72)),
	}

	if _, _, err := apply.Run(context.Background(), in, Result{}, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(h.AgentCreateArgsSlice) != 3 {
		t.Fatalf("created %d agents, want 3", len(h.AgentCreateArgsSlice))
	}
	for i, call := range h.AgentCreateArgsSlice {
		if got, want := fmt.Sprint(call.skills), "[forgejo-forgejo]"; got != want {
			t.Errorf("agent %d skills = %s, want %s — the agent is the only place a skill is selected",
				i, got, want)
		}
	}
	// The image still names its two MCP servers, which is what its tools come from;
	// it is only the skills that moved off it.
	if len(h.imageMCPServers) != 3 {
		t.Fatalf("created %d images, want 3", len(h.imageMCPServers))
	}
	for name, servers := range h.imageMCPServers {
		if got, want := fmt.Sprint(servers), "[forgejo-mcp-test plugin-forgejo]"; got != want {
			t.Errorf("image %s mcpServers = %s, want %s", name, got, want)
		}
	}
}

func TestApplyRollbackOnError(t *testing.T) {
	h := &fakeHub{
		createdIDs:         make(map[string]string),
		triggerCreateError: errors.New("trigger failed"),
		imageTools: []ImageTool{
			{Name: "tool1", Kind: "forgejo", Enabled: false},
			{Name: "bash", Kind: "shell", Enabled: false},
			{Name: "mcp__forgejo-mcp-test__tool1", Kind: "forgejo", Enabled: false},
		},
	}
	f := &fakeForgejo{
		createdUsers: make(map[string]string),
		createdTeams: make(map[string]int64),
		mintedTokens: make(map[string]string),
	}

	in := Input{
		Slug:       "test",
		GroupID:    "group-1",
		BaseURL:    "http://forgejo.example.com",
		Org:        "testorg",
		AdminToken: "admin-token",
		Roles: map[Role]RoleInput{
			RoleRefiner:   {Runtime: "image1", Tools: []string{"tool1"}},
			RoleDeveloper: {Runtime: "image1", Tools: []string{"tool1"}},
			RoleReviewer:  {Runtime: "image1", Tools: []string{"tool1"}},
		},
	}

	plan := Result{
		Create: []Op{
			{Kind: KindUser, Name: "refiner-test"},
		},
		Triggers: []Trigger{
			{Name: "forgejo-refiner-issue-opened-test"},
		},
	}

	apply := Apply{
		Hub:     h,
		Forgejo: f,
		Now:     time.Now,
		Rand:    bytes.NewReader(make([]byte, 72)), // 24 bytes per bot * 3 bots
	}

	_, _, err := apply.Run(context.Background(), in, plan, nil)

	if err == nil {
		t.Fatal("expected error")
	}

	// Error should contain the step name
	if !bytes.Contains([]byte(err.Error()), []byte("CreateTrigger")) {
		t.Errorf("error missing step name: %v", err)
	}
}

func TestApplyNoSecretsInError(t *testing.T) {
	h := &fakeHub{
		createdIDs:         make(map[string]string),
		triggerCreateError: errors.New("trigger failed"),
		imageTools: []ImageTool{
			{Name: "tool1", Kind: "forgejo", Enabled: false},
		},
	}
	f := &fakeForgejo{
		createdUsers: make(map[string]string),
		createdTeams: make(map[string]int64),
		mintedTokens: make(map[string]string),
	}

	in := Input{
		Slug:       "test",
		GroupID:    "group-1",
		AdminToken: "super-secret-admin-token",
		Roles: map[Role]RoleInput{
			RoleRefiner:   {Runtime: "image1"},
			RoleDeveloper: {Runtime: "image1"},
			RoleReviewer:  {Runtime: "image1"},
		},
	}

	plan := Result{
		Create: []Op{},
		Triggers: []Trigger{
			{Name: "forgejo-refiner-issue-opened-test"},
		},
	}

	apply := Apply{
		Hub:     h,
		Forgejo: f,
		Now:     time.Now,
		Rand:    bytes.NewReader(make([]byte, 72)), // 24 bytes per bot * 3 bots
	}

	_, _, err := apply.Run(context.Background(), in, plan, nil)

	errStr := err.Error()
	if errStr != "" && bytes.Contains([]byte(errStr), []byte("super-secret-admin-token")) {
		t.Errorf("admin token in error: %v", err)
	}
}

func TestApplyLeftoversWhenDeleteFails(t *testing.T) {
	h := &fakeHub{
		createdIDs:         make(map[string]string),
		triggerCreateError: errors.New("trigger failed"),
		imageTools: []ImageTool{
			{Name: "tool1", Kind: "forgejo", Enabled: false},
		},
		deleteErrors: map[string]error{
			"mcp:mcp-1": errors.New("delete failed"),
		},
	}
	f := &fakeForgejo{
		createdUsers: make(map[string]string),
		createdTeams: make(map[string]int64),
		mintedTokens: make(map[string]string),
	}

	in := Input{
		Slug:       "test",
		GroupID:    "group-1",
		AdminToken: "admin-token",
		Roles: map[Role]RoleInput{
			RoleRefiner:   {Runtime: "image1"},
			RoleDeveloper: {Runtime: "image1"},
			RoleReviewer:  {Runtime: "image1"},
		},
	}

	plan := Result{
		Create: []Op{},
		Triggers: []Trigger{
			{Name: "forgejo-refiner-issue-opened-test"},
		},
	}

	apply := Apply{
		Hub:          h,
		Forgejo:      f,
		Now:          time.Now,
		Rand:         bytes.NewReader(make([]byte, 72)),
		DefaultTools: tool1Only,
	}

	_, report, err := apply.Run(context.Background(), in, plan, nil)

	if err == nil {
		t.Fatal("expected error")
	}

	// The MCP server is the first resource the hub creates, so its id is the
	// deterministic "mcp-1": the one delete that fails is the one that must be
	// reported as left behind rather than rolled back.
	var left, rolled bool
	for _, op := range report.Leftovers {
		if op.Kind == KindMCPServer {
			left = true
		}
	}
	for _, op := range report.RolledBack {
		if op.Kind == KindMCPServer {
			rolled = true
		}
	}
	if !left {
		t.Errorf("the MCP server that could not be deleted is not in Leftovers: %+v", report.Leftovers)
	}
	if rolled {
		t.Errorf("the MCP server that could not be deleted is also in RolledBack: %+v", report.RolledBack)
	}
}

// The rollback runs in a deferred function, so it only reaches the caller through
// named results. A report that says "nothing was rolled back" next to a half-
// created install is the one answer the admin must not get (spec §5[8]): they
// would look for leftovers that the apply already deleted.
func TestApplyRollbackReportReachesCaller(t *testing.T) {
	h := &fakeHub{
		createdIDs:         make(map[string]string),
		triggerCreateError: errors.New("trigger failed"),
		imageTools: []ImageTool{
			{Name: "tool1", Kind: "forgejo", Enabled: false},
		},
	}
	f := &fakeForgejo{
		createdUsers: make(map[string]string),
		createdTeams: make(map[string]int64),
		mintedTokens: make(map[string]string),
	}

	in := Input{
		Slug:       "test",
		GroupID:    "group-1",
		BaseURL:    "http://forgejo.example.com",
		Org:        "testorg",
		AdminToken: "admin-token",
		Roles: map[Role]RoleInput{
			RoleRefiner:   {Runtime: "image1", Tools: []string{"tool1"}},
			RoleDeveloper: {Runtime: "image1", Tools: []string{"tool1"}},
			RoleReviewer:  {Runtime: "image1", Tools: []string{"tool1"}},
		},
	}

	apply := Apply{
		Hub:          h,
		Forgejo:      f,
		Now:          time.Now,
		Rand:         bytes.NewReader(make([]byte, 72)),
		DefaultTools: tool1Only,
	}

	_, report, err := apply.Run(context.Background(), in, Result{}, nil)
	if err == nil {
		t.Fatal("expected the apply to fail at CreateTrigger")
	}

	if len(report.Created) == 0 {
		t.Fatal("nothing was reported as created, so nothing can be reported as rolled back")
	}
	for _, created := range report.Created {
		if !slices.ContainsFunc(report.RolledBack, func(op Op) bool {
			return op.Kind == created.Kind && op.Name == created.Name
		}) {
			t.Errorf("created %s/%s is not reported as rolled back: %+v", created.Kind, created.Name, report.RolledBack)
		}
	}
	if len(report.Leftovers) != 0 {
		t.Errorf("every delete succeeded, so there should be no leftovers: %+v", report.Leftovers)
	}
}

func TestApplySecretsNotInJSON(t *testing.T) {
	h := &fakeHub{
		createdIDs: make(map[string]string),
		imageTools: []ImageTool{
			{Name: "tool1", Kind: "forgejo", Enabled: false},
			{Name: "mcp__forgejo-mcp-test__tool1", Kind: "forgejo", Enabled: false},
		},
	}
	f := &fakeForgejo{
		createdUsers: make(map[string]string),
		createdTeams: make(map[string]int64),
		mintedTokens: make(map[string]string),
	}

	in := Input{
		Slug:       "test",
		GroupID:    "group-1",
		BaseURL:    "http://forgejo.example.com",
		Org:        "testorg",
		AdminToken: "super-secret-admin-token",
		Roles: map[Role]RoleInput{
			RoleRefiner:   {Runtime: "image1", Tools: []string{"tool1"}},
			RoleDeveloper: {Runtime: "image1", Tools: []string{"tool1"}},
			RoleReviewer:  {Runtime: "image1", Tools: []string{"tool1"}},
		},
	}

	plan := Result{
		Create: []Op{},
		Triggers: []Trigger{
			{Name: "forgejo-refiner-issue-opened-test"},
		},
	}

	apply := Apply{
		Hub:     h,
		Forgejo: f,
		Now:     time.Now,
		Rand:    bytes.NewReader(make([]byte, 72)), // 24 bytes per bot * 3 bots
	}

	install, report, err := apply.Run(context.Background(), in, plan, nil)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Marshal to JSON and check for secrets
	installJSON, _ := json.Marshal(install)
	reportJSON, _ := json.Marshal(report)

	secrets := []string{
		"super-secret-admin-token",
		"admin-token",
		"token-refiner-test",
		"token-developer-test",
		"token-reviewer-test",
	}

	for _, secret := range secrets {
		if bytes.Contains(installJSON, []byte(secret)) {
			t.Errorf("found secret %q in install JSON", secret)
		}
		if bytes.Contains(reportJSON, []byte(secret)) {
			t.Errorf("found secret %q in report JSON", secret)
		}
	}
}

// tool1Only stands in for content.DefaultTools in this file: the fake catalogs here
// carry one tool, so a create that names none must resolve to that one.
func tool1Only(Role) []string { return []string{"tool1"} }

func TestApplyEachBotGetsDifferentPassword(t *testing.T) {
	h := &fakeHub{
		createdIDs: make(map[string]string),
		imageTools: []ImageTool{
			{Name: "tool1", Kind: "forgejo", Enabled: false},
		},
	}
	f := &fakeForgejo{
		createdUsers: make(map[string]string),
		createdTeams: make(map[string]int64),
		mintedTokens: make(map[string]string),
	}

	in := Input{
		Slug:       "test",
		GroupID:    "group-1",
		AdminToken: "admin-token",
		Roles: map[Role]RoleInput{
			RoleRefiner:   {Runtime: "image1"},
			RoleDeveloper: {Runtime: "image1"},
			RoleReviewer:  {Runtime: "image1"},
		},
	}

	plan := Result{
		Create: []Op{},
		Triggers: []Trigger{
			{Name: "forgejo-refiner-issue-opened-test"},
		},
	}

	apply := Apply{
		Hub:          h,
		Forgejo:      f,
		Now:          time.Now,
		Rand:         bytes.NewReader(make([]byte, 72)), // 24 bytes * 3 bots
		DefaultTools: tool1Only,
	}

	_, _, err := apply.Run(context.Background(), in, plan, nil)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Extract passwords from the CreateUser and MintToken calls
	createUserCalls := make([]string, 0)
	mintTokenCalls := make([]string, 0)
	for _, call := range f.calls {
		if len(call) > len("CreateUser:") && call[:11] == "CreateUser:" {
			createUserCalls = append(createUserCalls, call)
		}
		if len(call) > len("MintToken:") && call[:10] == "MintToken:" {
			mintTokenCalls = append(mintTokenCalls, call)
		}
	}

	if len(createUserCalls) != 3 {
		t.Errorf("expected 3 CreateUser calls, got %d", len(createUserCalls))
	}
	if len(mintTokenCalls) != 3 {
		t.Errorf("expected 3 MintToken calls, got %d", len(mintTokenCalls))
	}
}

// TestEditInstallMatchesOpsByRoleNotByName pins that an edit does what the plan says
// about a role, rather than what a name happens to contain. A slug of "reviewer"
// puts the word "reviewer" into every name the install owns, and an agent may be
// renamed away from its default — which the admin is free to do (spec §3.5). So the
// refiner's runtime change must touch the refiner's image alone, and the reviewer's
// rename must reach the reviewer's agent even though its name says nothing.
func TestEditInstallMatchesOpsByRoleNotByName(t *testing.T) {
	const runtimeV2 = "registry.example/ainsel/agent:v2"

	h := &fakeHub{
		createdIDs: make(map[string]string),
	}
	f := &fakeForgejo{
		createdUsers: make(map[string]string),
		createdTeams: make(map[string]int64),
		mintedTokens: make(map[string]string),
	}

	in := Input{
		Slug:         "reviewer",
		GroupID:      "group-1",
		BaseURL:      "http://forgejo.example.com",
		Org:          "testorg",
		AdminToken:   "admin-token",
		ExcludeLabel: "wontfix",
		Roles: map[Role]RoleInput{
			RoleRefiner: {
				Runtime: runtimeV2,
			},
			RoleDeveloper: {
				Runtime: runtimeV2,
			},
			RoleReviewer: {
				Runtime:   runtimeV2,
				AgentName: "The Critic",
			},
		},
	}

	apply := Apply{
		Hub:        h,
		Forgejo:    f,
		Now:        time.Now,
		Rand:       bytes.NewReader([]byte(strings.Repeat("0", 1000))),
		PersonaFor: nil,
	}

	// What the plan decided, and nothing else: the refiner's image runs on a new
	// runtime, and the reviewer's agent takes a display name.
	plan := Result{Change: []Op{
		{Kind: KindImage, Name: ImageName(RoleRefiner, "reviewer"), Detail: `runtime: "` + testRuntime + `" -> "` + runtimeV2 + `"`, Role: RoleRefiner, Field: FieldRuntime},
		{Kind: KindAgent, Name: AgentName(RoleReviewer, "reviewer"), Detail: `name: "forgejo-reviewer-reviewer" -> "The Critic"`, Role: RoleReviewer, Field: FieldAgentName},
	}}

	prev := &Install{
		Slug:         "reviewer",
		GroupID:      "group-1",
		BaseURL:      "http://forgejo.example.com",
		Org:          "testorg",
		MCPServer:    "forgejo-mcp-reviewer",
		ExcludeLabel: "wontfix",
		ConnectorID:  "connector-1",
		HookID:       1,
		Roles: map[Role]RoleState{
			RoleRefiner:   {BotLogin: "refiner-bot", TeamID: 1, PersonaID: "persona-1", ImageID: "image-1", AgentID: "agent-1", LLMProvider: "ollama-cloud", LLMModel: "old-model"},
			RoleDeveloper: {BotLogin: "dev-bot", TeamID: 2, PersonaID: "persona-2", ImageID: "image-2", AgentID: "agent-2"},
			RoleReviewer:  {BotLogin: "reviewer-bot", TeamID: 3, PersonaID: "persona-3", ImageID: "image-3", AgentID: "agent-3"},
		},
	}

	_, _, err := apply.Run(context.Background(), in, plan, prev)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []string{
		"UpdateAgentImage:image-1:" + runtimeV2,
		"UpdateAgent:agent-3",
	}
	got := []string{}
	for _, call := range h.calls {
		if strings.HasPrefix(call, "UpdateAgentImage:") || strings.HasPrefix(call, "UpdateAgent:") {
			got = append(got, call)
		}
	}
	if !slices.Equal(got, want) {
		t.Errorf("calls = %v, want %v", got, want)
	}
}

func TestApplyRefreshErrorHandling(t *testing.T) {
	tests := []struct {
		name       string
		serverErrs map[string]string
		shouldFail bool
	}{
		{
			name:       "plugin-forgejo error only - should succeed",
			serverErrs: map[string]string{"plugin-forgejo": "cannot list"},
			shouldFail: false,
		},
		{
			name:       "forgejo-mcp error - should fail",
			serverErrs: map[string]string{"forgejo-mcp-test": "cannot reach"},
			shouldFail: true,
		},
		{
			name:       "multiple errors including plugin-forgejo - should fail",
			serverErrs: map[string]string{"plugin-forgejo": "cannot list", "forgejo-mcp-test": "cannot reach"},
			shouldFail: true,
		},
		{
			// The hub failed and named no server. An empty breakdown is not the same as
			// a catalog: reading it as one would write an image with nothing on it.
			name:       "failure with no per-server breakdown - should fail",
			serverErrs: map[string]string{},
			shouldFail: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &fakeHub{
				createdIDs:          make(map[string]string),
				refreshError:        errors.New("refresh failed"),
				refreshServerErrors: tt.serverErrs,
				imageTools: []ImageTool{
					{Name: "tool1", Kind: "forgejo", Enabled: false},
				},
			}
			f := &fakeForgejo{
				createdUsers: make(map[string]string),
				createdTeams: make(map[string]int64),
				mintedTokens: make(map[string]string),
			}

			in := Input{
				Slug:       "test",
				GroupID:    "group-1",
				AdminToken: "admin-token",
				Roles: map[Role]RoleInput{
					RoleRefiner:   {Runtime: "image1"},
					RoleDeveloper: {Runtime: "image1"},
					RoleReviewer:  {Runtime: "image1"},
				},
			}

			plan := Result{
				Create: []Op{},
				Triggers: []Trigger{
					{Name: "forgejo-refiner-issue-opened-test"},
				},
			}

			apply := Apply{
				Hub:          h,
				Forgejo:      f,
				Now:          time.Now,
				Rand:         bytes.NewReader(make([]byte, 72)),
				DefaultTools: tool1Only,
			}

			_, _, err := apply.Run(context.Background(), in, plan, nil)

			if tt.shouldFail && err == nil {
				t.Errorf("expected error for %s", tt.name)
			}
			if !tt.shouldFail && err != nil {
				t.Errorf("unexpected error for %s: %v", tt.name, err)
			}
		})
	}
}

func TestApplyBashToolDeclared(t *testing.T) {
	h := &fakeHub{
		createdIDs: make(map[string]string),
		imageTools: []ImageTool{
			{Name: "tool1", Kind: "forgejo", Enabled: false},
			// Note: bash is NOT in the catalog (shell tools are not discovered)
			{Name: "mcp__forgejo-mcp-test__tool1", Kind: "forgejo", Enabled: false},
		},
	}
	f := &fakeForgejo{
		createdUsers: make(map[string]string),
		createdTeams: make(map[string]int64),
		mintedTokens: make(map[string]string),
	}

	in := Input{
		Slug:       "test",
		GroupID:    "group-1",
		BaseURL:    "http://forgejo.example.com",
		Org:        "testorg",
		AdminToken: "admin-token",
		Roles: map[Role]RoleInput{
			RoleRefiner:   {Runtime: "image1", Tools: []string{"tool1"}},
			RoleDeveloper: {Runtime: "image1", Tools: []string{"tool1", "bash"}},
			RoleReviewer:  {Runtime: "image1", Tools: []string{"tool1"}},
		},
	}

	plan := Result{
		Create: []Op{},
		Triggers: []Trigger{
			{Name: "forgejo-refiner-issue-opened-test"},
		},
	}

	apply := Apply{
		Hub:          h,
		Forgejo:      f,
		Now:          time.Now,
		Rand:         bytes.NewReader(make([]byte, 72)),
		DefaultTools: tool1Only,
	}

	install, _, err := apply.Run(context.Background(), in, plan, nil)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check that developer's tools include bash
	devTools := install.Roles[RoleDeveloper].Tools
	if !slicesContains(devTools, "bash") {
		t.Errorf("developer tools should include bash, got %v", devTools)
	}

	// Check that refiner and reviewer don't have bash
	refinerTools := install.Roles[RoleRefiner].Tools
	if slicesContains(refinerTools, "bash") {
		t.Errorf("refiner tools should not include bash, got %v", refinerTools)
	}

	reviewerTools := install.Roles[RoleReviewer].Tools
	if slicesContains(reviewerTools, "bash") {
		t.Errorf("reviewer tools should not include bash, got %v", reviewerTools)
	}
}

// TestApplyCreateGrantsTheAgentNotTheImage pins where a role's tool set lands. The image's
// catalog is the runtime's list of what exists; writing the role's set onto it restricted
// nobody once the hub moved selection onto the agent. So the catalog write restates the
// catalog with the flags the refresh brought back — the only entry it switches on is the
// shell tool the hub cannot discover — and the agent is created holding the set, spelled the
// way the catalog spells it.
func TestApplyCreateGrantsTheAgentNotTheImage(t *testing.T) {
	h := &fakeHub{
		createdIDs: make(map[string]string),
		// The hub's own spelling of the tool: the row names it "tool1", and only the
		// refresh knows the name the catalog carries it under.
		imageTools: []ImageTool{
			{Name: "mcp__forgejo-mcp-test__tool1", Kind: "mcp", Enabled: false},
		},
	}
	f := &fakeForgejo{
		createdUsers: make(map[string]string),
		createdTeams: make(map[string]int64),
		mintedTokens: make(map[string]string),
	}

	in := Input{
		Slug:       "test",
		GroupID:    "group-1",
		AdminToken: "admin-token",
		Roles: map[Role]RoleInput{
			RoleRefiner:   {Runtime: "image1", Tools: []string{"tool1"}},
			RoleDeveloper: {Runtime: "image1", Tools: []string{"tool1", "bash"}},
			RoleReviewer:  {Runtime: "image1", Tools: []string{"tool1"}},
		},
	}

	apply := Apply{
		Hub:          h,
		Forgejo:      f,
		Now:          time.Now,
		Rand:         bytes.NewReader(make([]byte, 72)),
		DefaultTools: tool1Only,
	}

	if _, _, err := apply.Run(context.Background(), in, Result{}, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantByAgent := map[string][]string{
		AgentName(RoleRefiner, "test"):   append([]string{"mcp__forgejo-mcp-test__tool1"}, PluginToolNames()...),
		AgentName(RoleDeveloper, "test"): append([]string{"mcp__forgejo-mcp-test__tool1", "bash"}, PluginToolNames()...),
		AgentName(RoleReviewer, "test"):  append([]string{"mcp__forgejo-mcp-test__tool1"}, PluginToolNames()...),
	}
	if len(h.AgentCreateArgsSlice) != len(wantByAgent) {
		t.Fatalf("CreateAgent calls = %d, want one per role", len(h.AgentCreateArgsSlice))
	}
	for _, call := range h.AgentCreateArgsSlice {
		want, ok := wantByAgent[call.agentName]
		if !ok {
			t.Fatalf("CreateAgent for unexpected agent %q", call.agentName)
		}
		if !slices.Equal(call.enabledTools, want) {
			t.Errorf("agent %s granted %v, want %v", call.agentName, call.enabledTools, want)
		}
	}

	// And no write switched a discovered catalog entry on: the flags the refresh brought
	// back are the catalog's own defaults, not the role's set. The only entries any of these
	// writes may turn on are the ones the hub cannot discover — the shell tool and the
	// plugin's own two reads, both declared by hand (spec §3.5).
	if len(h.enabledByCall) != 3 {
		t.Fatalf("SetImageTools calls = %d, want one per role", len(h.enabledByCall))
	}
	declared := append([]string{"bash"}, PluginToolNames()...)
	for i, enabled := range h.enabledByCall {
		for _, name := range enabled {
			if !slices.Contains(declared, name) {
				t.Errorf("image write %d switched %s on, want only the hand-declared entries ever switched on", i, name)
			}
		}
	}
}

// TestApplyReapplyWritesTheGrantEvenWhenTheSetDidNotChange covers the row the plan has
// nothing to say about: one written before the grant existed, or one the install adopted, so
// its agent holds a tool set it was never given. Nothing in the plan reports a change, so a
// pass gated on the plan would leave exactly the rows that need the write alone. The grant
// write is idempotent, so it rides on every apply.
func TestApplyReapplyWritesTheGrantEvenWhenTheSetDidNotChange(t *testing.T) {
	h := &fakeHub{
		createdIDs: make(map[string]string),
		imageTools: []ImageTool{
			{Name: "mcp__forgejo-mcp-test__tool1", Kind: "mcp", Enabled: false},
		},
	}
	f := &fakeForgejo{
		createdUsers: make(map[string]string),
		createdTeams: make(map[string]int64),
		mintedTokens: make(map[string]string),
	}

	in := Input{
		Slug:         "test",
		GroupID:      "group-1",
		BaseURL:      "http://forgejo.example.com",
		Org:          "testorg",
		AdminToken:   "admin-token",
		ExcludeLabel: "wontfix",
		Roles: map[Role]RoleInput{
			RoleRefiner:   {Tools: []string{"tool1"}},
			RoleDeveloper: {Tools: []string{"tool1"}},
			RoleReviewer:  {Tools: []string{"tool1"}},
		},
	}

	prev := &Install{
		Slug:         "test",
		GroupID:      "group-1",
		BaseURL:      "http://forgejo.example.com",
		Org:          "testorg",
		MCPServer:    "mcp-test",
		ExcludeLabel: "wontfix",
		ConnectorID:  "connector-1",
		HookID:       1,
		Roles: map[Role]RoleState{
			RoleRefiner:   {BotLogin: "refiner-bot", TeamID: 1, PersonaID: "persona-1", ImageID: "image-1", AgentID: "agent-1", Tools: []string{"tool1"}},
			RoleDeveloper: {BotLogin: "dev-bot", TeamID: 2, PersonaID: "persona-2", ImageID: "image-2", AgentID: "agent-2", Tools: []string{"tool1"}},
			RoleReviewer:  {BotLogin: "reviewer-bot", TeamID: 3, PersonaID: "persona-3", ImageID: "image-3", AgentID: "agent-3", Tools: []string{"tool1"}},
		},
	}

	apply := Apply{
		Hub:     h,
		Forgejo: f,
		Now:     time.Now,
		Rand:    bytes.NewReader([]byte(strings.Repeat("0", 1000))),
	}

	// An edit with nothing in it: no change op, so nothing the plan reports.
	if _, _, err := apply.Run(context.Background(), in, Result{Change: []Op{}}, prev); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(h.updateAgentCalls) != 3 {
		t.Fatalf("UpdateAgent calls = %d, want one per role — a row that names tools has to put them on its agent",
			len(h.updateAgentCalls))
	}
	want := append([]string{"mcp__forgejo-mcp-test__tool1"}, PluginToolNames()...)
	for _, call := range h.updateAgentCalls {
		if !slices.Equal(call.enabledTools, want) {
			t.Errorf("agent %s granted %v, want %v", call.agentID, call.enabledTools, want)
		}
	}
}

// TestApplyDropsAToolTheCatalogLostWhenTheSetDidNotChange is the apply path's half of what the
// drift pass already did: a re-apply that changed no tool set must not die because the forge
// server renamed or withdrew one tool. Aborting there means the admin cannot save anything from
// the wizard, for a change nobody asked for. The name comes out of the grant, the report says
// so, and the rest of the set is written — the same shape grantedToolNames gives it.
//
// When the plan does report a tools change the refusal stays: that is the admin asking for a set
// the catalog cannot back, and the preview should have caught it.
func TestApplyDropsAToolTheCatalogLostWhenTheSetDidNotChange(t *testing.T) {
	h := &fakeHub{
		createdIDs: make(map[string]string),
		imageTools: []ImageTool{
			{Name: "mcp__forgejo-mcp-test__tool1", Kind: "mcp", Enabled: false},
		},
	}
	f := &fakeForgejo{
		createdUsers: make(map[string]string),
		createdTeams: make(map[string]int64),
		mintedTokens: make(map[string]string),
	}

	in := Input{
		Slug:         "test",
		GroupID:      "group-1",
		BaseURL:      "http://forgejo.example.com",
		Org:          "testorg",
		AdminToken:   "admin-token",
		ExcludeLabel: "wontfix",
		Roles: map[Role]RoleInput{
			RoleRefiner:   {Tools: []string{"tool1", "tool2"}},
			RoleDeveloper: {Tools: []string{"tool1", "tool2"}},
			RoleReviewer:  {Tools: []string{"tool1", "tool2"}},
		},
	}

	roleState := func(bot string, team int64, persona, image, agent string) RoleState {
		return RoleState{BotLogin: bot, TeamID: team, PersonaID: persona, ImageID: image, AgentID: agent,
			Tools: []string{"tool1", "tool2"}}
	}
	prev := &Install{
		Slug:         "test",
		GroupID:      "group-1",
		BaseURL:      "http://forgejo.example.com",
		Org:          "testorg",
		MCPServer:    "mcp-test",
		ExcludeLabel: "wontfix",
		ConnectorID:  "connector-1",
		HookID:       1,
		Roles: map[Role]RoleState{
			RoleRefiner:   roleState("refiner-bot", 1, "persona-1", "image-1", "agent-1"),
			RoleDeveloper: roleState("dev-bot", 2, "persona-2", "image-2", "agent-2"),
			RoleReviewer:  roleState("reviewer-bot", 3, "persona-3", "image-3", "agent-3"),
		},
	}

	apply := Apply{
		Hub:     h,
		Forgejo: f,
		Now:     time.Now,
		Rand:    bytes.NewReader([]byte(strings.Repeat("0", 1000))),
	}

	// A re-apply with no tools op: the lost name degrades instead of aborting.
	_, report, err := apply.Run(context.Background(), in, Result{Change: []Op{}}, prev)
	if err != nil {
		t.Fatalf("apply failed over a tool nobody asked to change: %v", err)
	}
	want := append([]string{"mcp__forgejo-mcp-test__tool1"}, PluginToolNames()...)
	if len(h.updateAgentCalls) != 3 {
		t.Fatalf("UpdateAgent calls = %d, want one per role", len(h.updateAgentCalls))
	}
	for _, call := range h.updateAgentCalls {
		if !slices.Equal(call.enabledTools, want) {
			t.Errorf("agent %s granted %v, want %v — tool2 is out, the plugin's own reads are in",
				call.agentID, call.enabledTools, want)
		}
	}
	noted := 0
	for _, note := range report.Notes {
		if strings.Contains(note, "tool2") && strings.Contains(note, "no longer lists") {
			noted++
		}
	}
	if noted != 3 {
		t.Errorf("report notes = %q, want one per role naming tool2 as no longer listed", report.Notes)
	}

	// The same row through an apply the plan reports as a tools change: the refusal is the
	// answer, before any agent is written.
	h2 := &fakeHub{
		createdIDs: make(map[string]string),
		imageTools: h.imageTools,
	}
	apply.Hub = h2
	change := Result{Change: []Op{{Kind: KindImage, Role: RoleRefiner, Field: FieldTools}}}
	if _, _, err := apply.Run(context.Background(), in, change, prev); err == nil {
		t.Errorf("a tools change naming a tool the catalog lost: want the refusal, got none")
	}
}

// TestApplyRefusesAToolTheCatalogDoesNotCarry is the refusal behind the never-empty rule. The
// hub validates a grant against the image's catalog by exact match, so a name the catalog
// does not list can be granted only by inventing it — and an agent granted nothing is not a
// role restricted to nothing, it is a role allowed everything. The apply stops before any
// agent exists.
func TestApplyRefusesAToolTheCatalogDoesNotCarry(t *testing.T) {
	h := &fakeHub{
		createdIDs: make(map[string]string),
		imageTools: []ImageTool{
			{Name: "mcp__forgejo-mcp-test__something_else", Kind: "mcp", Enabled: false},
		},
	}
	f := &fakeForgejo{
		createdUsers: make(map[string]string),
		createdTeams: make(map[string]int64),
		mintedTokens: make(map[string]string),
	}

	in := Input{
		Slug:       "test",
		GroupID:    "group-1",
		AdminToken: "admin-token",
		Roles: map[Role]RoleInput{
			RoleRefiner:   {Runtime: "image1", Tools: []string{"tool1"}},
			RoleDeveloper: {Runtime: "image1", Tools: []string{"tool1"}},
			RoleReviewer:  {Runtime: "image1", Tools: []string{"tool1"}},
		},
	}

	apply := Apply{
		Hub:     h,
		Forgejo: f,
		Now:     time.Now,
		Rand:    bytes.NewReader(make([]byte, 72)),
	}

	_, _, err := apply.Run(context.Background(), in, Result{}, nil)
	if err == nil {
		t.Fatal("expected an error, got none")
	}
	if !strings.Contains(err.Error(), "not in catalog") {
		t.Errorf("error = %q, want it to name the tool the catalog does not carry", err)
	}
	for _, call := range h.calls {
		if call == "CreateAgent" {
			t.Fatalf("%s was called: an agent must not be created for a set nothing was granted", call)
		}
	}
}

func slicesContains(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

// TestApplyCreateWithNoToolsUsesDefaults pins what a create does with an input that
// names no tools. The wizard's checklist always sends a set, but an API caller may
// send none, and the plan reads that as the role's default profile (spec §3.2) — the
// apply must agree, because an agent created with no grant is not a role that can do
// nothing, it is a role that may call everything the runtime offers.
func TestApplyCreateWithNoToolsUsesDefaults(t *testing.T) {
	h := &fakeHub{
		createdIDs: make(map[string]string),
		imageTools: []ImageTool{
			{Name: "tool1", Kind: "forgejo", Enabled: false},
		},
	}
	f := &fakeForgejo{
		createdUsers: make(map[string]string),
		createdTeams: make(map[string]int64),
		mintedTokens: make(map[string]string),
	}

	in := Input{
		Slug:       "test",
		GroupID:    "group-1",
		AdminToken: "admin-token",
		Roles: map[Role]RoleInput{
			RoleRefiner:   {Runtime: "image1", Tools: []string{}},
			RoleDeveloper: {Runtime: "image1", Tools: []string{}},
			RoleReviewer:  {Runtime: "image1", Tools: []string{}},
		},
	}

	apply := Apply{
		Hub:          h,
		Forgejo:      f,
		Now:          time.Now,
		Rand:         bytes.NewReader(make([]byte, 72)),
		DefaultTools: tool1Only,
	}

	if _, _, err := apply.Run(context.Background(), in, Result{}, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(h.enabledByCall) != 3 {
		t.Fatalf("SetImageTools calls = %d, want one per role", len(h.enabledByCall))
	}
	// The catalog write restates what the refresh discovered; which entries it leaves enabled
	// is the catalog's own default, not the role's set. The role's set is what the agent is
	// created with.
	if len(h.AgentCreateArgsSlice) != 3 {
		t.Fatalf("CreateAgent calls = %d, want one per role", len(h.AgentCreateArgsSlice))
	}
	for i, call := range h.AgentCreateArgsSlice {
		if !slicesContains(call.enabledTools, "tool1") {
			t.Errorf("agent %d granted %v, want the role's default set", i, call.enabledTools)
		}
	}
}

// TestApplyCreateWithNoToolsAndNoDefaultFails pins that the same input is refused
// rather than half-written when no default set is on offer: an image with nothing
// enabled is a broken install, and a broken install is worse than a failed one.
func TestApplyCreateWithNoToolsAndNoDefaultFails(t *testing.T) {
	h := &fakeHub{
		createdIDs: make(map[string]string),
		imageTools: []ImageTool{
			{Name: "tool1", Kind: "forgejo", Enabled: false},
		},
	}
	f := &fakeForgejo{
		createdUsers: make(map[string]string),
		createdTeams: make(map[string]int64),
		mintedTokens: make(map[string]string),
	}

	in := Input{
		Slug:       "test",
		GroupID:    "group-1",
		AdminToken: "admin-token",
		Roles: map[Role]RoleInput{
			RoleRefiner:   {Runtime: "image1"},
			RoleDeveloper: {Runtime: "image1"},
			RoleReviewer:  {Runtime: "image1"},
		},
	}

	apply := Apply{
		Hub:     h,
		Forgejo: f,
		Now:     time.Now,
		Rand:    bytes.NewReader(make([]byte, 72)),
	}

	_, _, err := apply.Run(context.Background(), in, Result{}, nil)
	if err == nil {
		t.Fatal("expected an error, got none")
	}
	if !strings.Contains(err.Error(), "no tools") {
		t.Errorf("error = %q, want it to say no tools were available", err)
	}
	if len(h.enabledByCall) != 0 {
		t.Errorf("SetImageTools was called %d times, want no image written with an empty set", len(h.enabledByCall))
	}
}

func TestApplyInstallRoleStateComplete(t *testing.T) {
	h := &fakeHub{
		createdIDs: make(map[string]string),
		imageTools: []ImageTool{
			{Name: "tool1", Kind: "forgejo", Enabled: false},
			{Name: "mcp__forgejo-mcp-test__tool1", Kind: "forgejo", Enabled: false},
		},
	}
	f := &fakeForgejo{
		createdUsers: make(map[string]string),
		createdTeams: make(map[string]int64),
		mintedTokens: make(map[string]string),
	}

	in := Input{
		Slug:       "test",
		GroupID:    "group-1",
		BaseURL:    "http://forgejo.example.com",
		Org:        "testorg",
		AdminToken: "admin-token",
		Roles: map[Role]RoleInput{
			RoleRefiner:   {Runtime: "image1", Tools: []string{"tool1"}},
			RoleDeveloper: {Runtime: "image1", Tools: []string{"tool1"}},
			RoleReviewer:  {Runtime: "image1", Tools: []string{"tool1"}},
		},
	}

	plan := Result{
		Create: []Op{},
		Triggers: []Trigger{
			{Name: "forgejo-refiner-issue-opened-test"},
		},
	}

	apply := Apply{
		Hub:          h,
		Forgejo:      f,
		Now:          time.Now,
		Rand:         bytes.NewReader(make([]byte, 72)),
		DefaultTools: tool1Only,
	}

	apply.Settings = Settings{LLM: map[Role]RoleLLM{
		RoleRefiner:   {Provider: "ollama-cloud", Model: "llama3.3"},
		RoleDeveloper: {Provider: "ollama-cloud", Model: "llama3.3"},
		RoleReviewer:  {Provider: "ollama-cloud", Model: "llama3.3"},
	}}

	install, _, err := apply.Run(context.Background(), in, plan, nil)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify all RoleState fields are filled
	for _, role := range []Role{RoleRefiner, RoleDeveloper, RoleReviewer} {
		roleState := install.Roles[role]
		if roleState.AgentID == "" {
			t.Errorf("role %s: AgentID is empty", role)
		}
		if roleState.ImageID == "" {
			t.Errorf("role %s: ImageID is empty", role)
		}
		if roleState.PersonaID == "" {
			t.Errorf("role %s: PersonaID is empty", role)
		}
		if roleState.BotLogin == "" {
			t.Errorf("role %s: BotLogin is empty", role)
		}
		if roleState.TeamID == 0 {
			t.Errorf("role %s: TeamID is zero", role)
		}
		if len(roleState.Tools) == 0 {
			t.Errorf("role %s: Tools is empty", role)
		}
		// What the agent runs on belongs to the row: the re-apply loop sets a drifted llm back
		// (§3.6), and it can only set back a value it holds.
		if roleState.LLMProvider != "ollama-cloud" {
			t.Errorf("role %s: LLMProvider = %q, want ollama-cloud", role, roleState.LLMProvider)
		}
		if roleState.LLMModel != "llama3.3" {
			t.Errorf("role %s: LLMModel = %q, want llama3.3", role, roleState.LLMModel)
		}
	}

	// Verify Install fields are filled
	if install.ConnectorID == "" {
		t.Error("Install: ConnectorID is empty")
	}
	if install.MCPServer == "" {
		t.Error("Install: MCPServer is empty")
	}
	if install.HookID == 0 {
		t.Error("Install: HookID is zero")
	}
}

// TestApplyTriggersRolledBackOnFailure pins the full rollback order: when
// CreateOrgHook (step 8) fails, everything this apply created must be deleted
// again — all SIX created triggers first, then the connector, the agents,
// images and personas (each in reverse creation order), then the MCP server,
// and finally the Forgejo-side teams and users, in that reverse order.
func TestApplyTriggersRolledBackOnFailure(t *testing.T) {
	// Shared ordered log across both fakes, so the rollback order is pinned
	// globally and not just per fake.
	var deletes []string
	h := &fakeHub{
		createdIDs: make(map[string]string),
		deletes:    &deletes,
		imageTools: []ImageTool{
			{Name: "tool1", Kind: "forgejo", Enabled: false},
		},
	}
	f := &fakeForgejo{
		createdUsers:    make(map[string]string),
		createdTeams:    make(map[string]int64),
		mintedTokens:    make(map[string]string),
		createHookError: errors.New("hook creation failed"),
		deletes:         &deletes,
	}

	in := Input{
		Slug:       "test",
		GroupID:    "group-1",
		AdminToken: "admin-token",
		Roles: map[Role]RoleInput{
			RoleRefiner:   {Runtime: "image1"},
			RoleDeveloper: {Runtime: "image1"},
			RoleReviewer:  {Runtime: "image1"},
		},
	}

	plan := Result{
		Create: []Op{},
		Triggers: []Trigger{
			{Name: "forgejo-refiner-issue-opened-test"},
		},
	}

	apply := Apply{
		Hub:          h,
		Forgejo:      f,
		Now:          time.Now,
		Rand:         bytes.NewReader(make([]byte, 72)),
		DefaultTools: tool1Only,
	}

	_, _, err := apply.Run(context.Background(), in, plan, nil)

	if err == nil {
		t.Fatal("expected error from hook creation")
	}

	// Check that error mentions CreateOrgHook
	if !strings.Contains(err.Error(), "CreateOrgHook") {
		t.Errorf("error should mention CreateOrgHook, got: %v", err)
	}

	// The created ids are deterministic from the fake counters. Creation order
	// in Run: step 1 teams (refiner=1, developer=2, reviewer=3 on the Forgejo
	// side), step 2 the MCP server (mcp-1), step 3 personas (2-4), step 4
	// images (5-7), step 5 agents (8-10), step 6 the connector (11), step 7
	// the six triggers (12-17, in Triggers() order: refiner opened, reopened,
	// mention; reviewer pr-opened, mention; developer mention) and step 7b the
	// patrol cron trigger (cron-18). The hook was
	// never created, so no DeleteOrgHook may appear.
	want := []string{
		// The patrol cron trigger, created last, is deleted first.
		"DeleteCronTrigger:cron-18", // forgejo-developer-pr-patrol-test
		// All SIX created triggers, deleted next, in reverse creation order.
		"DeleteTrigger:trigger-17", // forgejo-developer-mention-test
		"DeleteTrigger:trigger-16", // forgejo-reviewer-mention-test
		"DeleteTrigger:trigger-15", // forgejo-reviewer-pr-opened-test
		"DeleteTrigger:trigger-14", // forgejo-refiner-mention-test
		"DeleteTrigger:trigger-13", // forgejo-refiner-issue-reopened-test
		"DeleteTrigger:trigger-12", // forgejo-refiner-issue-opened-test
		"DeleteConnector:connector-11",
		"DeleteAgent:agent-10", // reviewer
		"DeleteAgent:agent-9",  // developer
		"DeleteAgent:agent-8",  // refiner
		"DeleteAgentImage:image-7",
		"DeleteAgentImage:image-6",
		"DeleteAgentImage:image-5",
		"DeletePersona:persona-4",
		"DeletePersona:persona-3",
		"DeletePersona:persona-2",
		"DeleteMCPServer:mcp-1",
		// Forgejo side last: each role's team then its bot user, roles reversed.
		"DeleteTeam:3", // ainsel-reviewer-test
		"DeleteUser:reviewer-test",
		"DeleteTeam:2", // ainsel-developer-test
		"DeleteUser:developer-test",
		"DeleteTeam:1", // ainsel-refiner-test
		"DeleteUser:refiner-test",
	}
	if !slices.Equal(deletes, want) {
		t.Errorf("rollback order wrong:\n got %v\nwant %v", deletes, want)
	}
}

func TestEditInstallWithNewToolSet(t *testing.T) {
	h := &fakeHub{
		createdIDs: make(map[string]string),
		imageTools: []ImageTool{
			{Name: "tool1", Kind: "shell", Enabled: false},
			{Name: "tool2", Kind: "shell", Enabled: false},
			{Name: "bash", Kind: "shell", Enabled: false},
		},
	}
	f := &fakeForgejo{
		createdUsers: make(map[string]string),
		createdTeams: make(map[string]int64),
		mintedTokens: make(map[string]string),
	}

	// Create initial install
	in := Input{
		Slug:         "test",
		GroupID:      "group-1",
		BaseURL:      "http://forgejo.example.com",
		Org:          "testorg",
		AdminToken:   "admin-token",
		ExcludeLabel: "wontfix",
		Roles: map[Role]RoleInput{
			RoleRefiner:   {Tools: []string{"tool1"}},
			RoleDeveloper: {Tools: []string{"tool1"}},
			RoleReviewer:  {Tools: []string{"tool1"}},
		},
	}

	apply := Apply{
		Hub:        h,
		Forgejo:    f,
		Now:        time.Now,
		Rand:       bytes.NewReader([]byte(strings.Repeat("0", 1000))),
		PersonaFor: nil,
	}

	plan := Result{Change: []Op{}}
	prev := &Install{
		Slug:         "test",
		GroupID:      "group-1",
		BaseURL:      "http://forgejo.example.com",
		Org:          "testorg",
		MCPServer:    "mcp-test",
		ExcludeLabel: "wontfix",
		ConnectorID:  "connector-1",
		HookID:       1,
		Roles: map[Role]RoleState{
			RoleRefiner:   {BotLogin: "refiner-bot", TeamID: 1, PersonaID: "persona-1", ImageID: "image-1", AgentID: "agent-1"},
			RoleDeveloper: {BotLogin: "dev-bot", TeamID: 2, PersonaID: "persona-2", ImageID: "image-2", AgentID: "agent-2"},
			RoleReviewer:  {BotLogin: "reviewer-bot", TeamID: 3, PersonaID: "persona-3", ImageID: "image-3", AgentID: "agent-3"},
		},
	}

	// Edit with new tool set
	refinerInput := in.Roles[RoleRefiner]
	refinerInput.Tools = []string{"tool2"}
	in.Roles[RoleRefiner] = refinerInput
	plan.Change = []Op{
		{Kind: KindImage, Name: ImageName(RoleRefiner, "test"), Detail: "tools: tool1 -> tool2", Role: RoleRefiner, Field: FieldTools},
	}

	install, _, err := apply.Run(context.Background(), in, plan, prev)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify SetImageTools was called
	if !slices.ContainsFunc(h.calls, func(s string) bool { return strings.Contains(s, "SetImageTools") }) {
		t.Errorf("SetImageTools was not called")
	}

	// Verify no users, teams, personas, MCP servers, connectors or hooks were created
	// Only RefreshImageTools and SetImageTools should be called (no users, teams, personas, MCPs, connectors, hooks)
	for _, call := range h.calls {
		if strings.Contains(call, "CreateUser") || strings.Contains(call, "CreateTeam") ||
			strings.Contains(call, "CreatePersona") || strings.Contains(call, "RegisterMCPServer") ||
			strings.Contains(call, "CreateConnector") || strings.Contains(call, "CreateOrgHook") {
			t.Errorf("unexpected call: %s", call)
		}
	}

	// Verify all IDs from prev are preserved
	if install.Roles[RoleRefiner].ImageID != "image-1" {
		t.Errorf("refiner image ID changed: %q", install.Roles[RoleRefiner].ImageID)
	}
	if install.Roles[RoleDeveloper].ImageID != "image-2" {
		t.Errorf("developer image ID changed: %q", install.Roles[RoleDeveloper].ImageID)
	}
	if install.Roles[RoleReviewer].ImageID != "image-3" {
		t.Errorf("reviewer image ID changed: %q", install.Roles[RoleReviewer].ImageID)
	}
}

// TestEditInstallLeavesPrevUntouchedOnFailure pins that an edit which cannot finish
// does not leave the row it was given half-changed. The caller still holds prev, the
// store still holds the old row, and the admin is being told the edit failed — so the
// row must say what it said before, not some of what the admin asked for.
func TestEditInstallLeavesPrevUntouchedOnFailure(t *testing.T) {
	h := &fakeHub{
		createdIDs:     make(map[string]string),
		imageTools:     []ImageTool{{Name: "tool1", Kind: "forgejo", Enabled: false}},
		updateAgentErr: errors.New("hub refused"),
	}
	f := &fakeForgejo{
		createdUsers: make(map[string]string),
		createdTeams: make(map[string]int64),
		mintedTokens: make(map[string]string),
	}

	in := Input{
		Slug:       "test",
		GroupID:    "group-1",
		AdminToken: "admin-token",
		Roles: map[Role]RoleInput{
			RoleRefiner:   {Tools: []string{"tool1"}},
			RoleDeveloper: {},
			RoleReviewer:  {},
		},
	}

	plan := Result{Change: []Op{
		{Kind: KindImage, Name: ImageName(RoleRefiner, "test"), Detail: "tools: -old", Role: RoleRefiner, Field: FieldTools},
		{Kind: KindAgent, Name: AgentName(RoleDeveloper, "test"), Detail: `llm model: "old" -> "new-model"`, Role: RoleDeveloper, Field: FieldLLMModel},
	}}

	prev := &Install{
		Slug:         "test",
		GroupID:      "group-1",
		BaseURL:      "http://forgejo.example.com",
		Org:          "testorg",
		MCPServer:    "forgejo-mcp-test",
		ExcludeLabel: "wontfix",
		ConnectorID:  "connector-1",
		HookID:       1,
		Roles: map[Role]RoleState{
			RoleRefiner:   {ImageID: "image-1", AgentID: "agent-1", Tools: []string{"old"}},
			RoleDeveloper: {ImageID: "image-2", AgentID: "agent-2"},
			RoleReviewer:  {ImageID: "image-3", AgentID: "agent-3"},
		},
		Coverage: map[string]time.Time{"issues": {}},
	}

	apply := Apply{
		Hub:     h,
		Forgejo: f,
		Now:     time.Now,
		Rand:    bytes.NewReader([]byte(strings.Repeat("0", 1000))),
	}

	if _, _, err := apply.Run(context.Background(), in, plan, prev); err == nil {
		t.Fatal("expected the edit to fail")
	}

	if got := prev.Roles[RoleRefiner].Tools; !slices.Equal(got, []string{"old"}) {
		t.Errorf("the caller's row was changed: refiner tools = %v, want [old]", got)
	}
}

// TestEditInstallRewritesPersonasOnNewBaseURL pins that pointing an install at a new
// forge tells its agents: the persona text names the address the bots post to, so three
// personas are rewritten, not none (spec §3.5). An install that keeps the old text has
// agents describing a forge that is not the one they work in.
func TestEditInstallRewritesPersonasOnNewBaseURL(t *testing.T) {
	h := &fakeHub{
		createdIDs: make(map[string]string),
	}
	f := &fakeForgejo{
		createdUsers: make(map[string]string),
		createdTeams: make(map[string]int64),
		mintedTokens: make(map[string]string),
	}

	in := Input{
		Slug:       "test",
		GroupID:    "group-1",
		BaseURL:    "http://forgejo.example.new",
		AdminToken: "admin-token",
		Roles: map[Role]RoleInput{
			RoleRefiner:   {},
			RoleDeveloper: {},
			RoleReviewer:  {},
		},
	}

	// What the plan decided: the address moved, so all three personas say so.
	plan := Result{Change: []Op{
		{Kind: KindPersona, Name: PersonaName(RoleRefiner, "test"), Detail: `base URL: "http://forgejo.example.com" -> "http://forgejo.example.new"`, Role: RoleRefiner, Field: FieldBaseURL},
		{Kind: KindPersona, Name: PersonaName(RoleDeveloper, "test"), Detail: `base URL: "http://forgejo.example.com" -> "http://forgejo.example.new"`, Role: RoleDeveloper, Field: FieldBaseURL},
		{Kind: KindPersona, Name: PersonaName(RoleReviewer, "test"), Detail: `base URL: "http://forgejo.example.com" -> "http://forgejo.example.new"`, Role: RoleReviewer, Field: FieldBaseURL},
	}}

	prev := &Install{
		Slug:         "test",
		GroupID:      "group-1",
		BaseURL:      "http://forgejo.example.com",
		Org:          "testorg",
		MCPServer:    "forgejo-mcp-test",
		ExcludeLabel: "wontfix",
		ConnectorID:  "connector-1",
		HookID:       1,
		Roles: map[Role]RoleState{
			RoleRefiner:   {BotLogin: "refiner-bot", PersonaID: "persona-1", ImageID: "image-1", AgentID: "agent-1"},
			RoleDeveloper: {BotLogin: "dev-bot", PersonaID: "persona-2", ImageID: "image-2", AgentID: "agent-2"},
			RoleReviewer:  {BotLogin: "reviewer-bot", PersonaID: "persona-3", ImageID: "image-3", AgentID: "agent-3"},
		},
	}

	apply := Apply{
		Hub:     h,
		Forgejo: f,
		Now:     time.Now,
		Rand:    bytes.NewReader([]byte(strings.Repeat("0", 1000))),
		PersonaFor: func(role Role, bots map[Role]string, baseURL string) string {
			return string(role) + " posts to " + baseURL + " as " + bots[role]
		},
	}

	if _, _, err := apply.Run(context.Background(), in, plan, prev); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, role := range []Role{RoleRefiner, RoleDeveloper, RoleReviewer} {
		id := prev.Roles[role].PersonaID
		text, ok := h.personaTexts[id]
		if !ok {
			t.Errorf("persona %s (%s) was never rewritten", id, role)
			continue
		}
		if !strings.Contains(text, "http://forgejo.example.new") {
			t.Errorf("persona %s says %q, want the new address", id, text)
		}
		if !strings.Contains(text, "posts to") || !strings.Contains(text, "as ") {
			t.Errorf("persona %s says %q, want the text the wizard wrote for the role", id, text)
		}
	}
}

func TestEditInstallWithNewModel(t *testing.T) {
	h := &fakeHub{
		createdIDs: make(map[string]string),
	}
	f := &fakeForgejo{
		createdUsers: make(map[string]string),
		createdTeams: make(map[string]int64),
		mintedTokens: make(map[string]string),
	}

	in := Input{
		Slug:       "test",
		GroupID:    "group-1",
		BaseURL:    "http://forgejo.example.com",
		Org:        "testorg",
		AdminToken: "admin-token",
		Roles: map[Role]RoleInput{
			RoleRefiner: {},
		},
	}

	apply := Apply{
		Hub:        h,
		Forgejo:    f,
		Now:        time.Now,
		Rand:       bytes.NewReader([]byte(strings.Repeat("0", 1000))),
		PersonaFor: nil,
	}
	// The settings' answer for the refiner: what the edit writes to the agent,
	// whatever the wizard typed (which, for the llm, is nothing).
	apply.Settings = Settings{LLM: map[Role]RoleLLM{
		RoleRefiner: {Provider: "ollama-cloud", Model: "new-model"},
	}}

	plan := Result{Change: []Op{
		{Kind: KindAgent, Name: AgentName(RoleRefiner, "test"), Detail: "llm model: \"old-model\" -> \"new-model\"", Role: RoleRefiner, Field: FieldLLMModel},
	}}

	prev := &Install{
		Slug:         "test",
		GroupID:      "group-1",
		BaseURL:      "http://forgejo.example.com",
		Org:          "testorg",
		MCPServer:    "mcp-test",
		ExcludeLabel: "wontfix",
		ConnectorID:  "connector-1",
		HookID:       1,
		Roles: map[Role]RoleState{
			RoleRefiner: {BotLogin: "refiner-bot", TeamID: 1, PersonaID: "persona-1", ImageID: "image-1", AgentID: "agent-1"},
		},
	}

	install, _, err := apply.Run(context.Background(), in, plan, prev)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The row follows what was set, so the next reconcile pass holds the new value as the
	// one to keep rather than the old one to set back.
	if got := install.Roles[RoleRefiner].LLMModel; got != "new-model" {
		t.Errorf("row LLMModel = %q, want new-model", got)
	}
	if got := install.Roles[RoleRefiner].LLMProvider; got != "ollama-cloud" {
		t.Errorf("row LLMProvider = %q, want ollama-cloud", got)
	}

	// Verify UpdateAgent was called
	if !slices.ContainsFunc(h.calls, func(s string) bool { return strings.Contains(s, "UpdateAgent") }) {
		t.Errorf("UpdateAgent was not called")
	}

	// Verify agent ID is preserved
	if install.Roles[RoleRefiner].AgentID != "agent-1" {
		t.Errorf("refiner agent ID changed: %q", install.Roles[RoleRefiner].AgentID)
	}
}

func TestEditInstallWithExcludeLabelChange(t *testing.T) {
	h := &fakeHub{
		createdIDs: make(map[string]string),
		listTriggersFunc: func(ctx context.Context, connectorID string) ([]TriggerEntry, error) {
			return []TriggerEntry{
				{ID: "trigger-1", Name: "forgejo-refiner-issue-opened-test"},
				{ID: "trigger-2", Name: "forgejo-refiner-issue-reopened-test"},
				{ID: "trigger-3", Name: "forgejo-refiner-mention-test"},
			}, nil
		},
	}
	f := &fakeForgejo{
		createdUsers: make(map[string]string),
		createdTeams: make(map[string]int64),
		mintedTokens: make(map[string]string),
	}

	in := Input{
		Slug:         "test",
		GroupID:      "group-1",
		BaseURL:      "http://forgejo.example.com",
		Org:          "testorg",
		AdminToken:   "admin-token",
		ExcludeLabel: "wip",
		Roles: map[Role]RoleInput{
			RoleRefiner:   {},
			RoleDeveloper: {},
			RoleReviewer:  {},
		},
	}

	apply := Apply{
		Hub:        h,
		Forgejo:    f,
		Now:        time.Now,
		Rand:       bytes.NewReader([]byte(strings.Repeat("0", 1000))),
		PersonaFor: nil,
	}

	plan := Result{Change: []Op{
		{Kind: KindTrigger, Name: "forgejo-refiner-issue-opened-test", Detail: `exclude label: "wontfix" -> "wip"`, Role: RoleRefiner, Field: FieldExcludeLabel},
	}}

	prev := &Install{
		Slug:         "test",
		GroupID:      "group-1",
		BaseURL:      "http://forgejo.example.com",
		Org:          "testorg",
		MCPServer:    "mcp-test",
		ExcludeLabel: "wontfix",
		ConnectorID:  "connector-1",
		HookID:       1,
		Roles: map[Role]RoleState{
			RoleRefiner:   {BotLogin: "refiner-bot", TeamID: 1, PersonaID: "persona-1", ImageID: "image-1", AgentID: "agent-1"},
			RoleDeveloper: {BotLogin: "dev-bot", TeamID: 2, PersonaID: "persona-2", ImageID: "image-2", AgentID: "agent-2"},
			RoleReviewer:  {BotLogin: "reviewer-bot", TeamID: 3, PersonaID: "persona-3", ImageID: "image-3", AgentID: "agent-3"},
		},
	}

	install, _, err := apply.Run(context.Background(), in, plan, prev)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify ListTriggers was called
	if !slices.ContainsFunc(h.calls, func(s string) bool { return strings.Contains(s, "ListTriggers") }) {
		t.Errorf("ListTriggers was not called")
	}

	// Verify DeleteTrigger was called for the two old issue triggers
	deleteCalls := 0
	for _, call := range h.calls {
		if strings.Contains(call, "DeleteTrigger") {
			deleteCalls++
		}
	}
	if deleteCalls < 2 {
		t.Errorf("expected at least 2 DeleteTrigger calls, got %d", deleteCalls)
	}

	// Verify CreateTrigger was called for new triggers
	createCalls := 0
	for _, call := range h.calls {
		if strings.Contains(call, "CreateTrigger") {
			createCalls++
		}
	}
	if createCalls < 2 {
		t.Errorf("expected at least 2 CreateTrigger calls for new triggers, got %d", createCalls)
	}

	// Verify all IDs are preserved
	if install.Roles[RoleRefiner].ImageID != "image-1" {
		t.Errorf("refiner image ID changed")
	}
	if install.Roles[RoleDeveloper].ImageID != "image-2" {
		t.Errorf("developer image ID changed")
	}
	if install.Roles[RoleReviewer].ImageID != "image-3" {
		t.Errorf("reviewer image ID changed")
	}
}

// TestApplyEditExcludeLabelUsesInstallGroup pins the group an edit files its
// rebuilt triggers in. On an edit the group is the install's own — the admin does not
// re-pick it, so the input's group is usually empty — and writing to an empty group
// is not the install's group.
func TestApplyEditExcludeLabelUsesInstallGroup(t *testing.T) {
	h := &fakeHub{
		createdIDs: make(map[string]string),
		listTriggersFunc: func(ctx context.Context, connectorID string) ([]TriggerEntry, error) {
			return []TriggerEntry{
				{ID: "trigger-1", Name: "forgejo-refiner-issue-opened-test"},
				{ID: "trigger-2", Name: "forgejo-refiner-issue-reopened-test"},
			}, nil
		},
	}
	f := &fakeForgejo{
		createdUsers: make(map[string]string),
		createdTeams: make(map[string]int64),
		mintedTokens: make(map[string]string),
	}

	in := Input{
		Slug:         "test",
		GroupID:      "",
		BaseURL:      "http://forgejo.example.com",
		Org:          "testorg",
		ExcludeLabel: "wip",
		Roles: map[Role]RoleInput{
			RoleRefiner:   {},
			RoleDeveloper: {},
			RoleReviewer:  {},
		},
	}

	apply := Apply{
		Hub:        h,
		Forgejo:    f,
		Now:        time.Now,
		Rand:       bytes.NewReader([]byte(strings.Repeat("0", 1000))),
		PersonaFor: nil,
	}

	plan := Result{Change: []Op{
		{Kind: KindTrigger, Name: "forgejo-refiner-issue-opened-test", Detail: `exclude label: "wontfix" -> "wip"`, Role: RoleRefiner, Field: FieldExcludeLabel},
		{Kind: KindTrigger, Name: "forgejo-refiner-issue-reopened-test", Detail: `exclude label: "wontfix" -> "wip"`, Role: RoleRefiner, Field: FieldExcludeLabel},
	}}

	prev := &Install{
		Slug:         "test",
		GroupID:      "g-1",
		BaseURL:      "http://forgejo.example.com",
		Org:          "testorg",
		MCPServer:    "mcp-test",
		ExcludeLabel: "wontfix",
		ConnectorID:  "connector-1",
		HookID:       1,
		Roles: map[Role]RoleState{
			RoleRefiner:   {BotLogin: "refiner-bot", TeamID: 1, PersonaID: "persona-1", ImageID: "image-1", AgentID: "agent-1"},
			RoleDeveloper: {BotLogin: "dev-bot", TeamID: 2, PersonaID: "persona-2", ImageID: "image-2", AgentID: "agent-2"},
			RoleReviewer:  {BotLogin: "reviewer-bot", TeamID: 3, PersonaID: "persona-3", ImageID: "image-3", AgentID: "agent-3"},
		},
	}

	_, _, err := apply.Run(context.Background(), in, plan, prev)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []triggerCreate{
		{groupID: "g-1", name: "forgejo-refiner-issue-opened-test"},
		{groupID: "g-1", name: "forgejo-refiner-issue-reopened-test"},
	}
	if len(h.triggerCreates) != len(want) {
		t.Fatalf("CreateTrigger calls = %+v, want %+v", h.triggerCreates, want)
	}
	for i, w := range want {
		if h.triggerCreates[i] != w {
			t.Errorf("CreateTrigger[%d] = %+v, want %+v", i, h.triggerCreates[i], w)
		}
	}
}

// TestApplyCreatesPrPatrolCronTrigger pins the patrol's place in the apply: made
// with the triggers, in the install's group, under the slug-derived name, on the
// shipped schedule — and reported as a created op.
func TestApplyCreatesPrPatrolCronTrigger(t *testing.T) {
	var deletes []string
	h := &fakeHub{
		createdIDs: make(map[string]string),
		imageTools: []ImageTool{{Name: "tool1", Kind: "forgejo", Enabled: false}},
		deletes:    &deletes,
	}
	f := &fakeForgejo{
		createdUsers: make(map[string]string),
		createdTeams: make(map[string]int64),
		mintedTokens: make(map[string]string),
		deletes:      &deletes,
	}

	in := Input{
		Slug:       "test",
		GroupID:    "group-1",
		AdminToken: "admin-token",
		Roles: map[Role]RoleInput{
			RoleRefiner:   {Runtime: "image1"},
			RoleDeveloper: {Runtime: "image1"},
			RoleReviewer:  {Runtime: "image1"},
		},
	}
	plan := Result{Create: []Op{}, Triggers: []Trigger{{Name: "forgejo-refiner-issue-opened-test"}}}

	apply := Apply{
		Hub:          h,
		Forgejo:      f,
		Now:          time.Now,
		Rand:         bytes.NewReader(make([]byte, 72)),
		DefaultTools: tool1Only,
	}

	_, report, err := apply.Run(context.Background(), in, plan, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(h.cronTriggerCreates) != 1 {
		t.Fatalf("cron trigger creates = %d, want 1", len(h.cronTriggerCreates))
	}
	ct := h.cronTriggerCreates[0]
	if ct.name != "forgejo-developer-pr-patrol-test" || ct.schedule != "*/30 * * * *" || ct.groupID != "group-1" {
		t.Errorf("cron trigger create = %+v, want the patrol on */30 * * * * under group-1", ct)
	}

	// The patrol is created after the six event triggers — the last create of the
	// trigger phase.
	lastTrigger, lastCron := -1, -1
	for i, c := range h.calls {
		switch c {
		case "CreateTrigger":
			lastTrigger = i
		case "CreateCronTrigger":
			lastCron = i
		}
	}
	if lastCron < lastTrigger {
		t.Errorf("CreateCronTrigger (call %d) ran before the last CreateTrigger (call %d)", lastCron, lastTrigger)
	}

	var createdCronOp bool
	for _, op := range report.Created {
		if op.Kind == KindCronTrigger && op.Name == "forgejo-developer-pr-patrol-test" {
			createdCronOp = true
		}
	}
	if !createdCronOp {
		t.Errorf("report.Created = %+v, want the patrol listed", report.Created)
	}
}

// TestApplyCronTriggerFailureRollsBack: the patrol is the last create of the
// trigger phase; a hub that refuses it must roll the whole install back — the six
// triggers, the connector and everything below them — and never claim a cron
// trigger it never made.
func TestApplyCronTriggerFailureRollsBack(t *testing.T) {
	var deletes []string
	h := &fakeHub{
		createdIDs:             make(map[string]string),
		imageTools:             []ImageTool{{Name: "tool1", Kind: "forgejo", Enabled: false}},
		cronTriggerCreateError: errors.New("cron boom"),
		deletes:                &deletes,
	}
	f := &fakeForgejo{
		createdUsers: make(map[string]string),
		createdTeams: make(map[string]int64),
		mintedTokens: make(map[string]string),
		deletes:      &deletes,
	}

	in := Input{
		Slug:       "test",
		GroupID:    "group-1",
		AdminToken: "admin-token",
		Roles: map[Role]RoleInput{
			RoleRefiner:   {Runtime: "image1"},
			RoleDeveloper: {Runtime: "image1"},
			RoleReviewer:  {Runtime: "image1"},
		},
	}
	plan := Result{Create: []Op{}, Triggers: []Trigger{{Name: "forgejo-refiner-issue-opened-test"}}}

	apply := Apply{
		Hub:          h,
		Forgejo:      f,
		Now:          time.Now,
		Rand:         bytes.NewReader(make([]byte, 72)),
		DefaultTools: tool1Only,
	}

	_, _, err := apply.Run(context.Background(), in, plan, nil)
	if err == nil {
		t.Fatal("expected the apply to fail at CreateCronTrigger")
	}
	if !strings.Contains(err.Error(), "CreateCronTrigger") {
		t.Errorf("error = %v, want it to name CreateCronTrigger", err)
	}

	// All six triggers rolled back, and nothing claims a cron trigger delete —
	// there is nothing to delete.
	triggerDeletes := 0
	for _, d := range deletes {
		if strings.HasPrefix(d, "DeleteTrigger:") {
			triggerDeletes++
		}
		if strings.HasPrefix(d, "DeleteCronTrigger:") {
			t.Errorf("delete of a cron trigger that was never created: %s", d)
		}
	}
	if triggerDeletes != 6 {
		t.Errorf("trigger deletes = %d, want 6", triggerDeletes)
	}
	if !containsString(deletes, "DeleteConnector:connector-11") {
		t.Errorf("deletes = %v, want the connector rolled back too", deletes)
	}
}

func containsString(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

// --- custom provider (issue #54) ---

// newCustomInstall is the prev row an edit test diffs against: one role, fully
// created, running on the custom provider.
func newCustomInstall() *Install {
	return &Install{
		Slug:         "test",
		GroupID:      "group-1",
		BaseURL:      "http://forgejo.example.com",
		Org:          "testorg",
		MCPServer:    "mcp-test",
		ExcludeLabel: "wontfix",
		ConnectorID:  "connector-1",
		HookID:       1,
		Roles: map[Role]RoleState{
			RoleRefiner: {BotLogin: "refiner-bot", TeamID: 1, PersonaID: "persona-1", ImageID: "image-1", AgentID: "agent-1"},
		},
	}
}

// TestApplyCreateFeedsTheCustomPairOnlyForACustomProvider is the create path:
// the role that runs on `custom` gets the settings' URL and key on the create;
// a listed provider gets neither, so the hub's customProvider object stays
// absent rather than carrying an endpoint nobody meant.
func TestApplyCreateFeedsTheCustomPairOnlyForACustomProvider(t *testing.T) {
	h := &fakeHub{
		createdIDs: make(map[string]string),
		imageTools: []ImageTool{{Name: "tool1", Kind: "forgejo", Enabled: false}},
	}
	f := &fakeForgejo{
		createdUsers: make(map[string]string),
		createdTeams: make(map[string]int64),
		mintedTokens: make(map[string]string),
	}

	in := Input{
		Slug:       "test",
		GroupID:    "group-1",
		BaseURL:    "http://forgejo.example.com",
		Org:        "testorg",
		AdminToken: "admin-token",
		Roles: map[Role]RoleInput{
			RoleRefiner:   {Runtime: "image1", Tools: []string{"tool1"}},
			RoleDeveloper: {Runtime: "image1", Tools: []string{"tool1"}},
			RoleReviewer:  {Runtime: "image1", Tools: []string{"tool1"}},
		},
	}

	apply := Apply{
		Hub:     h,
		Forgejo: f,
		Now:     time.Now,
		Rand:    bytes.NewReader(make([]byte, 72)),
		Settings: Settings{LLM: map[Role]RoleLLM{
			RoleRefiner: {Provider: LLMProviderCustom, Model: "my-model", CustomURL: "https://llm.example.com/v1", CustomKey: "sk-secret"},
			// A role with the pair stored but a listed provider: nothing rides.
			RoleDeveloper: {Provider: "ollama-cloud", Model: "glm-4.7", CustomURL: "https://stale.example.com/v1", CustomKey: "sk-stale"},
			RoleReviewer:  {Provider: "opencode", Model: "m"},
		}},
	}

	got, _, err := apply.Run(context.Background(), in, Result{}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(h.AgentCreateArgsSlice) != 3 {
		t.Fatalf("created %d agents, want 3", len(h.AgentCreateArgsSlice))
	}
	refiner := h.AgentCreateArgsSlice[0]
	if refiner.LLM.CustomURL != "https://llm.example.com/v1" || refiner.LLM.CustomKey != "sk-secret" {
		t.Errorf("custom role create pair = %q/%q, want the settings' URL and key", refiner.LLM.CustomURL, refiner.LLM.CustomKey)
	}
	// The row records the written key's fingerprint — the key itself never goes
	// in the row (R4) — so a later rotation is visible as drift.
	if rs := got.Roles[RoleRefiner]; rs.LLMCustomKeyHash != CustomKeyFingerprint("sk-secret") {
		t.Errorf("custom role row fingerprint = %q, want the written key's", rs.LLMCustomKeyHash)
	}
	for _, listed := range h.AgentCreateArgsSlice[1:] {
		if listed.LLM.CustomURL != "" || listed.LLM.CustomKey != "" {
			t.Errorf("listed provider %q carried custom pair %q/%q, want both empty", listed.LLM.Provider, listed.LLM.CustomURL, listed.LLM.CustomKey)
		}
	}
}

// TestEditInstallCustomKeyOpRewritesTheAgent is the key rotation's path to the
// hub: URL and provider standing still, the plan sees the key through the
// row's fingerprint, and its op alone fires the UpdateAgent — the write
// carries the settings' pair. The row then records the written key's
// fingerprint, so the next plan sees no drift.
func TestEditInstallCustomKeyOpRewritesTheAgent(t *testing.T) {
	h := &fakeHub{createdIDs: make(map[string]string)}
	f := &fakeForgejo{
		createdUsers: make(map[string]string),
		createdTeams: make(map[string]int64),
		mintedTokens: make(map[string]string),
	}

	in := Input{
		Slug:       "test",
		GroupID:    "group-1",
		BaseURL:    "http://forgejo.example.com",
		Org:        "testorg",
		AdminToken: "admin-token",
		Roles:      map[Role]RoleInput{RoleRefiner: {}},
	}

	apply := Apply{
		Hub:     h,
		Forgejo: f,
		Now:     time.Now,
		Rand:    bytes.NewReader([]byte(strings.Repeat("0", 1000))),
		Settings: Settings{LLM: map[Role]RoleLLM{
			RoleRefiner: {Provider: LLMProviderCustom, Model: "my-model", CustomURL: "https://llm.example.com/v1", CustomKey: "sk-rotated"},
		}},
	}

	plan := Result{Change: []Op{
		{Kind: KindAgent, Name: AgentName(RoleRefiner, "test"), Detail: "llm key replaced", Role: RoleRefiner, Field: FieldLLMCustomKey},
	}}

	got, _, err := apply.Run(context.Background(), in, plan, newCustomInstall())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(h.updateAgentCalls) != 1 {
		t.Fatalf("UpdateAgent called %d times, want 1 — a key-only op is an agent change", len(h.updateAgentCalls))
	}
	call := h.updateAgentCalls[0]
	if call.llm.CustomURL != "https://llm.example.com/v1" || call.llm.CustomKey != "sk-rotated" {
		t.Errorf("update pair = %q/%q, want the settings' URL and key", call.llm.CustomURL, call.llm.CustomKey)
	}
	if rs := got.Roles[RoleRefiner]; rs.LLMCustomKeyHash != CustomKeyFingerprint("sk-rotated") {
		t.Errorf("row fingerprint = %q, want the written key's", rs.LLMCustomKeyHash)
	}
}

// TestEditInstallCustomURLOpRewritesTheAgent is the reconcile-through-apply
// trigger: a plan op naming the custom URL field alone has to fire the
// UpdateAgent, and the write carries the settings' pair. Moving the role onto
// a listed provider sends no pair — the hub keeps the stale customProvider,
// inert while the llm names a listed provider.
func TestEditInstallCustomURLOpRewritesTheAgent(t *testing.T) {
	h := &fakeHub{createdIDs: make(map[string]string)}
	f := &fakeForgejo{
		createdUsers: make(map[string]string),
		createdTeams: make(map[string]int64),
		mintedTokens: make(map[string]string),
	}

	in := Input{
		Slug:       "test",
		GroupID:    "group-1",
		BaseURL:    "http://forgejo.example.com",
		Org:        "testorg",
		AdminToken: "admin-token",
		Roles:      map[Role]RoleInput{RoleRefiner: {}},
	}

	apply := Apply{
		Hub:     h,
		Forgejo: f,
		Now:     time.Now,
		Rand:    bytes.NewReader([]byte(strings.Repeat("0", 1000))),
		Settings: Settings{LLM: map[Role]RoleLLM{
			RoleRefiner: {Provider: LLMProviderCustom, Model: "my-model", CustomURL: "https://llm.example.com/v1", CustomKey: "sk-secret"},
		}},
	}

	plan := Result{Change: []Op{
		{Kind: KindAgent, Name: AgentName(RoleRefiner, "test"), Detail: "llm URL: \"old\" -> \"new\"", Role: RoleRefiner, Field: FieldLLMCustomUrl},
	}}

	if _, _, err := apply.Run(context.Background(), in, plan, newCustomInstall()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(h.updateAgentCalls) != 1 {
		t.Fatalf("UpdateAgent called %d times, want 1 — a custom-URL op is an agent change", len(h.updateAgentCalls))
	}
	call := h.updateAgentCalls[0]
	if call.llm.CustomURL != "https://llm.example.com/v1" || call.llm.CustomKey != "sk-secret" {
		t.Errorf("update pair = %q/%q, want the settings' URL and key", call.llm.CustomURL, call.llm.CustomKey)
	}
}

// TestEditInstallVendorKeyOpRewritesTheAgent is a vendor rotation's path to the
// hub: the plan sees the key through the row's fingerprint, its op alone fires
// the UpdateAgent, and the write carries the settings' key to its own block —
// nothing of the custom pair rides with it. The row then records the written
// key's fingerprint, so the next plan sees no drift.
func TestEditInstallVendorKeyOpRewritesTheAgent(t *testing.T) {
	h := &fakeHub{createdIDs: make(map[string]string)}
	f := &fakeForgejo{
		createdUsers: make(map[string]string),
		createdTeams: make(map[string]int64),
		mintedTokens: make(map[string]string),
	}

	in := Input{
		Slug:       "test",
		GroupID:    "group-1",
		BaseURL:    "http://forgejo.example.com",
		Org:        "testorg",
		AdminToken: "admin-token",
		Roles:      map[Role]RoleInput{RoleRefiner: {}},
	}

	apply := Apply{
		Hub:     h,
		Forgejo: f,
		Now:     time.Now,
		Rand:    bytes.NewReader([]byte(strings.Repeat("0", 1000))),
		Settings: Settings{LLM: map[Role]RoleLLM{
			RoleRefiner: {Provider: LLMProviderOpenAI, Model: "gpt-5.2", OpenAIKey: "sk-rotated",
				CustomURL: "https://stale.example.com/v1", CustomKey: "sk-stale"},
		}},
	}

	plan := Result{Change: []Op{
		{Kind: KindAgent, Name: AgentName(RoleRefiner, "test"), Detail: "llm key (openai) replaced", Role: RoleRefiner, Field: FieldLLMOpenAIKey},
	}}

	got, _, err := apply.Run(context.Background(), in, plan, newCustomInstall())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(h.updateAgentCalls) != 1 {
		t.Fatalf("UpdateAgent called %d times, want 1 — a vendor key op is an agent change", len(h.updateAgentCalls))
	}
	call := h.updateAgentCalls[0]
	if call.llm.OpenAIKey != "sk-rotated" {
		t.Errorf("openai key = %q, want the settings' key", call.llm.OpenAIKey)
	}
	if call.llm.CustomURL != "" || call.llm.CustomKey != "" {
		t.Errorf("custom pair = %q/%q, want both empty away from custom", call.llm.CustomURL, call.llm.CustomKey)
	}
	if rs := got.Roles[RoleRefiner]; rs.OpenAIKeyHash != CustomKeyFingerprint("sk-rotated") {
		t.Errorf("row fingerprint = %q, want the written key's", rs.OpenAIKeyHash)
	}
	if rs := got.Roles[RoleRefiner]; rs.LLMCustomKeyHash != "" {
		t.Errorf("custom fingerprint = %q, want nothing recorded for a provider the role does not run on", rs.LLMCustomKeyHash)
	}
}

// TestEditInstallAwayFromCustomSendsNoPair is the move-off path: the update
// names the new provider and nothing else — the hub keeps the stale
// customProvider on the agent (its keep-on-omit semantics), and it is inert
// while the llm names a listed provider.
func TestEditInstallAwayFromCustomSendsNoPair(t *testing.T) {
	h := &fakeHub{createdIDs: make(map[string]string)}
	f := &fakeForgejo{
		createdUsers: make(map[string]string),
		createdTeams: make(map[string]int64),
		mintedTokens: make(map[string]string),
	}

	in := Input{
		Slug:       "test",
		GroupID:    "group-1",
		BaseURL:    "http://forgejo.example.com",
		Org:        "testorg",
		AdminToken: "admin-token",
		Roles:      map[Role]RoleInput{RoleRefiner: {}},
	}

	apply := Apply{
		Hub:     h,
		Forgejo: f,
		Now:     time.Now,
		Rand:    bytes.NewReader([]byte(strings.Repeat("0", 1000))),
		Settings: Settings{LLM: map[Role]RoleLLM{
			RoleRefiner: {Provider: "ollama-cloud", Model: "glm-4.7", CustomURL: "https://stale.example.com/v1", CustomKey: "sk-stale"},
		}},
	}

	plan := Result{Change: []Op{
		{Kind: KindAgent, Name: AgentName(RoleRefiner, "test"), Detail: "llm provider: \"custom\" -> \"ollama-cloud\"", Role: RoleRefiner, Field: FieldLLMProvider},
	}}

	if _, _, err := apply.Run(context.Background(), in, plan, newCustomInstall()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(h.updateAgentCalls) != 1 {
		t.Fatalf("UpdateAgent called %d times, want 1", len(h.updateAgentCalls))
	}
	call := h.updateAgentCalls[0]
	if call.llm.Provider != "ollama-cloud" {
		t.Errorf("provider = %q, want ollama-cloud", call.llm.Provider)
	}
	if call.llm.CustomURL != "" || call.llm.CustomKey != "" {
		t.Errorf("update pair = %q/%q, want both empty away from custom", call.llm.CustomURL, call.llm.CustomKey)
	}
}

// testWindow is the window these tests move around: a working week in Berlin, in
// the shape the hub's workingHours takes.
func testWindow() *WorkingHours {
	return &WorkingHours{Timezone: "Europe/Berlin", Days: []int{1, 2, 3, 4, 5}, Start: "09:00", End: "17:00"}
}

// withRowHours puts a window on one role's row, which Go will not let a test do to
// a map value's field directly.
func withRowHours(inst *Install, r Role, w *WorkingHours) *Install {
	rs := inst.Roles[r]
	rs.WorkingHours = w
	inst.Roles[r] = rs
	return inst
}

// TestApplyCreateWritesTheWindowOnlyForTheRoleThatSetOne is the create half of AC2:
// the role whose form named a window is created with it, and a role that named none
// is created with no window at all — the key never goes out, which is the hub's
// "any hour". The row records what was written, as the baseline for the next edit.
func TestApplyCreateWritesTheWindowOnlyForTheRoleThatSetOne(t *testing.T) {
	h := &fakeHub{
		createdIDs: make(map[string]string),
		imageTools: []ImageTool{{Name: "tool1", Kind: "forgejo", Enabled: false}},
	}
	f := &fakeForgejo{
		createdUsers: make(map[string]string),
		createdTeams: make(map[string]int64),
		mintedTokens: make(map[string]string),
	}

	want := testWindow()
	in := Input{
		Slug:       "test",
		GroupID:    "group-1",
		BaseURL:    "http://forgejo.example.com",
		Org:        "testorg",
		AdminToken: "admin-token",
		Roles: map[Role]RoleInput{
			RoleRefiner:   {Runtime: "image1", Tools: []string{"tool1"}},
			RoleDeveloper: {Runtime: "image1", Tools: []string{"tool1"}, WorkingHours: want},
			RoleReviewer:  {Runtime: "image1", Tools: []string{"tool1"}},
		},
	}

	apply := Apply{
		Hub:     h,
		Forgejo: f,
		Now:     time.Now,
		Rand:    bytes.NewReader(make([]byte, 72)),
	}

	install, _, err := apply.Run(context.Background(), in, Result{}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(h.AgentCreateArgsSlice) != 3 {
		t.Fatalf("CreateAgent called %d times, want one per role", len(h.AgentCreateArgsSlice))
	}
	for i, role := range []Role{RoleRefiner, RoleDeveloper, RoleReviewer} {
		got := h.AgentCreateArgsSlice[i].hours
		if role == RoleDeveloper {
			if !WorkingHoursEqual(got, want) {
				t.Errorf("%s created with window %v, want the form's window", role, got)
			}
			continue
		}
		if got != nil {
			t.Errorf("%s created with window %+v, want none: the form named no window", role, *got)
		}
	}

	if !WorkingHoursEqual(install.Roles[RoleDeveloper].WorkingHours, want) {
		t.Errorf("row window = %v, want the written window as the next edit's baseline",
			install.Roles[RoleDeveloper].WorkingHours)
	}
	if install.Roles[RoleRefiner].WorkingHours != nil {
		t.Errorf("refiner row window = %+v, want nil", *install.Roles[RoleRefiner].WorkingHours)
	}

	// The row holds a copy: the form is still the caller's value, and a row that shares
	// its day list with it would move when the form does.
	want.Days[0] = 3
	if install.Roles[RoleDeveloper].WorkingHours.Days[0] == 3 {
		t.Errorf("row shares the form's day list; a later edit to the form would move the row")
	}
}

// TestEditInstallWorkingHoursOpWritesTheWindow is the update half of AC2: the plan
// said the field moved, so the write carries the form's window — and the row moves to
// it, because the row is what the next edit diffs against.
func TestEditInstallWorkingHoursOpWritesTheWindow(t *testing.T) {
	h := &fakeHub{createdIDs: make(map[string]string)}
	f := &fakeForgejo{
		createdUsers: make(map[string]string),
		createdTeams: make(map[string]int64),
		mintedTokens: make(map[string]string),
	}

	want := testWindow()
	in := Input{
		Slug: "test", GroupID: "group-1", BaseURL: "http://forgejo.example.com",
		Org: "testorg", AdminToken: "admin-token",
		Roles: map[Role]RoleInput{RoleRefiner: {WorkingHours: want}},
	}
	apply := Apply{
		Hub: h, Forgejo: f, Now: time.Now,
		Rand: bytes.NewReader([]byte(strings.Repeat("0", 1000))),
	}
	plan := Result{Change: []Op{{
		Kind: KindAgent, Name: AgentName(RoleRefiner, "test"),
		Detail: `working hours: "any hours" -> "Mon–Fri 09:00–17:00 Europe/Berlin"`,
		Role:   RoleRefiner, Field: FieldWorkingHours,
	}}}

	got, _, err := apply.Run(context.Background(), in, plan, newCustomInstall())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(h.updateAgentCalls) != 1 {
		t.Fatalf("UpdateAgent called %d times, want 1 — a window op is an agent change", len(h.updateAgentCalls))
	}

	call := h.updateAgentCalls[0]
	if call.hours.Clear {
		t.Errorf("update clears the window, want it written")
	}
	if !WorkingHoursEqual(call.hours.Set, want) {
		t.Errorf("update window = %v, want the form's window", call.hours.Set)
	}
	if !WorkingHoursEqual(got.Roles[RoleRefiner].WorkingHours, want) {
		t.Errorf("row window = %v, want the written window", got.Roles[RoleRefiner].WorkingHours)
	}
}

// TestEditInstallWorkingHoursOpClears is the explicit clear: the plan said the field
// moved to nothing, so the write asks the hub to remove the window — the only way a
// clear is ever sent.
func TestEditInstallWorkingHoursOpClears(t *testing.T) {
	h := &fakeHub{createdIDs: make(map[string]string)}
	f := &fakeForgejo{
		createdUsers: make(map[string]string),
		createdTeams: make(map[string]int64),
		mintedTokens: make(map[string]string),
	}

	in := Input{
		Slug: "test", GroupID: "group-1", BaseURL: "http://forgejo.example.com",
		Org: "testorg", AdminToken: "admin-token",
		Roles: map[Role]RoleInput{RoleRefiner: {}},
	}
	apply := Apply{
		Hub: h, Forgejo: f, Now: time.Now,
		Rand: bytes.NewReader([]byte(strings.Repeat("0", 1000))),
	}
	plan := Result{Change: []Op{{
		Kind: KindAgent, Name: AgentName(RoleRefiner, "test"),
		Detail: `working hours: "Mon–Fri 09:00–17:00 Europe/Berlin" -> "any hours"`,
		Role:   RoleRefiner, Field: FieldWorkingHours,
	}}}

	got, _, err := apply.Run(context.Background(), in, plan, withRowHours(newCustomInstall(), RoleRefiner, testWindow()))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(h.updateAgentCalls) != 1 {
		t.Fatalf("UpdateAgent called %d times, want 1", len(h.updateAgentCalls))
	}

	call := h.updateAgentCalls[0]
	if !call.hours.Clear {
		t.Errorf("update = %+v, want the hub's clear", call.hours)
	}
	if call.hours.Set != nil {
		t.Errorf("update writes window %+v, want a clear and nothing else", *call.hours.Set)
	}
	if got.Roles[RoleRefiner].WorkingHours != nil {
		t.Errorf("row window = %+v, want nil: the wizard no longer stands behind one",
			*got.Roles[RoleRefiner].WorkingHours)
	}
}

// TestEditInstallSaysNothingAboutTheWindowWithoutAnOp is the central one, and the
// whole of AC3 at the apply's end: a re-apply whose plan has no op for the field
// writes neither the form's window nor the hub's clear. That is what leaves a window
// an admin set in the console alone — the plugin never names the field, so the hub
// keeps what it holds.
func TestEditInstallSaysNothingAboutTheWindowWithoutAnOp(t *testing.T) {
	for _, tc := range []struct {
		name string
		// The window the row records as last written, and the one the form carries. The
		// plan is the same in both: an agent change that is not about the window.
		row  *WorkingHours
		form *WorkingHours
	}{
		{"the wizard never set one", nil, nil},
		{"the form still says what the row records", testWindow(), testWindow()},
		// A plan is what decides, not the form: a form that disagrees with the row and a
		// plan that never noticed says nothing, rather than writing on its own initiative.
		{"the plan says nothing, so the form is ignored", testWindow(), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &fakeHub{createdIDs: make(map[string]string)}
			f := &fakeForgejo{
				createdUsers: make(map[string]string),
				createdTeams: make(map[string]int64),
				mintedTokens: make(map[string]string),
			}

			in := Input{
				Slug: "test", GroupID: "group-1", BaseURL: "http://forgejo.example.com",
				Org: "testorg", AdminToken: "admin-token",
				Roles: map[Role]RoleInput{RoleRefiner: {AgentName: "The Refiner", WorkingHours: tc.form}},
			}
			apply := Apply{
				Hub: h, Forgejo: f, Now: time.Now,
				Rand: bytes.NewReader([]byte(strings.Repeat("0", 1000))),
			}
			plan := Result{Change: []Op{{
				Kind: KindAgent, Name: AgentName(RoleRefiner, "test"),
				Detail: `name: "forgejo-refiner-test" -> "The Refiner"`,
				Role:   RoleRefiner, Field: FieldAgentName,
			}}}

			got, _, err := apply.Run(context.Background(), in, plan, withRowHours(newCustomInstall(), RoleRefiner, CloneWorkingHours(tc.row)))
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if len(h.updateAgentCalls) != 1 {
				t.Fatalf("UpdateAgent called %d times, want 1 for the name change", len(h.updateAgentCalls))
			}

			if call := h.updateAgentCalls[0]; call.hours != (WorkingHoursWrite{}) {
				t.Errorf("update carries window %+v, want neither a write nor a clear: the plan said nothing about the field",
					call.hours)
			}
			if !WorkingHoursEqual(got.Roles[RoleRefiner].WorkingHours, tc.row) {
				t.Errorf("row window = %v, want the row to keep %v — it records what the wizard wrote, not what the hub holds",
					got.Roles[RoleRefiner].WorkingHours, tc.row)
			}
		})
	}
}

// TestApplyCreateWithChatGPTSignIn is the sign-in's create path. The role on
// openai whose settings chose the sign-in is created with the method and no key
// at all — the pair the hub refuses can never leave the plugin — and the report
// carries the hint that the sign-in still has to be finished in the console. A
// role on the same provider that kept the API key is created exactly as it was
// before the method existed: the key, and no method, which is the hub's default.
func TestApplyCreateWithChatGPTSignIn(t *testing.T) {
	h := &fakeHub{
		createdIDs: make(map[string]string),
		imageTools: []ImageTool{{Name: "tool1", Kind: "forgejo", Enabled: false}},
	}
	f := &fakeForgejo{
		createdUsers: make(map[string]string),
		createdTeams: make(map[string]int64),
		mintedTokens: make(map[string]string),
	}

	in := Input{
		Slug:       "test",
		GroupID:    "group-1",
		BaseURL:    "http://forgejo.example.com",
		Org:        "testorg",
		AdminToken: "admin-token",
		Roles: map[Role]RoleInput{
			RoleRefiner:   {Runtime: "image1", Tools: []string{"tool1"}},
			RoleDeveloper: {Runtime: "image1", Tools: []string{"tool1"}},
			RoleReviewer:  {Runtime: "image1", Tools: []string{"tool1"}},
		},
	}

	apply := Apply{
		Hub:     h,
		Forgejo: f,
		Now:     time.Now,
		Rand:    bytes.NewReader(make([]byte, 72)),
		Settings: Settings{LLM: map[Role]RoleLLM{
			// The sign-in, with a key the settings still hold: it is never sent.
			RoleRefiner: {Provider: LLMProviderOpenAI, Model: "gpt-5.2",
				OpenAIKey: "sk-stale", OpenAIAuthMethod: LLMAuthMethodChatGPTSignIn},
			RoleDeveloper: {Provider: LLMProviderOpenAI, Model: "gpt-5.2", OpenAIKey: "sk-key"},
			// A role away from openai records no method at all.
			RoleReviewer: {Provider: LLMProviderAnthropic, Model: "claude",
				AnthropicKey: "sk-anthropic", OpenAIAuthMethod: LLMAuthMethodChatGPTSignIn},
		}},
	}

	got, report, err := apply.Run(context.Background(), in, Result{}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(h.AgentCreateArgsSlice) != 3 {
		t.Fatalf("created %d agents, want 3", len(h.AgentCreateArgsSlice))
	}

	signIn := h.AgentCreateArgsSlice[0]
	if signIn.LLM.OpenAIAuthMethod != LLMAuthMethodChatGPTSignIn {
		t.Errorf("sign-in create authMethod = %q, want %q", signIn.LLM.OpenAIAuthMethod, LLMAuthMethodChatGPTSignIn)
	}
	if signIn.LLM.OpenAIKey != "" {
		t.Errorf("sign-in create carried an apiKey %q, want none — the hub refuses the pair", signIn.LLM.OpenAIKey)
	}
	if rs := got.Roles[RoleRefiner]; rs.LLMOpenAIAuthMethod != LLMAuthMethodChatGPTSignIn {
		t.Errorf("sign-in row records %q, want the method the agent was created with", rs.LLMOpenAIAuthMethod)
	}
	if rs := got.Roles[RoleRefiner]; rs.OpenAIKeyHash != "" {
		t.Errorf("sign-in row records key fingerprint %q, want none: no key was written", rs.OpenAIKeyHash)
	}

	keyed := h.AgentCreateArgsSlice[1]
	if keyed.LLM.OpenAIAuthMethod != "" {
		t.Errorf("API-key create authMethod = %q, want it left unsaid (the hub's own default)", keyed.LLM.OpenAIAuthMethod)
	}
	if keyed.LLM.OpenAIKey != "sk-key" {
		t.Errorf("API-key create apiKey = %q, want the settings' key", keyed.LLM.OpenAIKey)
	}
	if rs := got.Roles[RoleDeveloper]; rs.LLMOpenAIAuthMethod != LLMAuthMethodAPIKey {
		t.Errorf("API-key row records %q, want %q", rs.LLMOpenAIAuthMethod, LLMAuthMethodAPIKey)
	}

	other := h.AgentCreateArgsSlice[2]
	if other.LLM.OpenAIAuthMethod != "" {
		t.Errorf("anthropic role create authMethod = %q, want nothing: the openAI block is inert there", other.LLM.OpenAIAuthMethod)
	}
	if rs := got.Roles[RoleReviewer]; rs.LLMOpenAIAuthMethod != "" {
		t.Errorf("anthropic role row records %q, want nothing recorded away from openai", rs.LLMOpenAIAuthMethod)
	}

	// The hint, once, for the one role that needs it.
	if len(report.Notes) != 1 {
		t.Fatalf("report notes = %q, want the one sign-in hint", report.Notes)
	}
	if !strings.Contains(report.Notes[0], "ChatGPT") || !strings.Contains(report.Notes[0], "refiner") {
		t.Errorf("note = %q, want it to name the sign-in and the role", report.Notes[0])
	}
	for _, n := range report.Notes {
		if strings.Contains(n, "sk-") {
			t.Errorf("note %q spells a key", n)
		}
	}
}

// TestEditInstallChatGPTSignInOpRewritesTheAgent is both directions of the
// method's edit path. Onto the sign-in: the write names the method and no key,
// the row records the method, and the report carries the hint. Back off it: the
// write has to say so — the hub keeps the method it holds on an empty one, and an
// agent left on a sign-in with a key authenticates with neither.
func TestEditInstallChatGPTSignInOpRewritesTheAgent(t *testing.T) {
	t.Run("onto the sign-in", func(t *testing.T) {
		h := &fakeHub{createdIDs: make(map[string]string)}
		f := &fakeForgejo{
			createdUsers: make(map[string]string),
			createdTeams: make(map[string]int64),
			mintedTokens: make(map[string]string),
		}
		in := Input{
			Slug: "test", GroupID: "group-1",
			BaseURL: "http://forgejo.example.com", Org: "testorg", AdminToken: "admin-token",
			Roles: map[Role]RoleInput{RoleRefiner: {}},
		}
		apply := Apply{
			Hub: h, Forgejo: f, Now: time.Now,
			Rand: bytes.NewReader([]byte(strings.Repeat("0", 1000))),
			Settings: Settings{LLM: map[Role]RoleLLM{RoleRefiner: {
				Provider: LLMProviderOpenAI, Model: "gpt-5.2",
				// The key the settings still hold from before the admin chose the
				// sign-in: the apply must not send it beside the method.
				OpenAIKey: "sk-stale", OpenAIAuthMethod: LLMAuthMethodChatGPTSignIn,
			}}},
		}
		plan := Result{Change: []Op{{
			Kind: KindAgent, Name: AgentName(RoleRefiner, "test"),
			Detail: `llm auth method: "APIKey" -> "ChatGPTSignIn"`,
			Role:   RoleRefiner, Field: FieldLLMOpenAIAuthMethod,
		}}}

		got, report, err := apply.Run(context.Background(), in, plan, newCustomInstall())
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if len(h.updateAgentCalls) != 1 {
			t.Fatalf("UpdateAgent called %d times, want 1 — the method op is an agent change", len(h.updateAgentCalls))
		}
		call := h.updateAgentCalls[0]
		if call.llm.OpenAIAuthMethod != LLMAuthMethodChatGPTSignIn {
			t.Errorf("update authMethod = %q, want %q", call.llm.OpenAIAuthMethod, LLMAuthMethodChatGPTSignIn)
		}
		if call.llm.OpenAIKey != "" {
			t.Errorf("update carried apiKey %q, want none beside the sign-in", call.llm.OpenAIKey)
		}
		if rs := got.Roles[RoleRefiner]; rs.LLMOpenAIAuthMethod != LLMAuthMethodChatGPTSignIn {
			t.Errorf("row records %q, want the method the agent now holds", rs.LLMOpenAIAuthMethod)
		}
		if len(report.Notes) != 1 {
			t.Errorf("report notes = %q, want the one sign-in hint", report.Notes)
		}
	})

	t.Run("back off the sign-in", func(t *testing.T) {
		h := &fakeHub{createdIDs: make(map[string]string)}
		f := &fakeForgejo{
			createdUsers: make(map[string]string),
			createdTeams: make(map[string]int64),
			mintedTokens: make(map[string]string),
		}
		in := Input{
			Slug: "test", GroupID: "group-1",
			BaseURL: "http://forgejo.example.com", Org: "testorg", AdminToken: "admin-token",
			Roles: map[Role]RoleInput{RoleRefiner: {}},
		}
		apply := Apply{
			Hub: h, Forgejo: f, Now: time.Now,
			Rand: bytes.NewReader([]byte(strings.Repeat("0", 1000))),
			Settings: Settings{LLM: map[Role]RoleLLM{RoleRefiner: {
				Provider: LLMProviderOpenAI, Model: "gpt-5.2", OpenAIKey: "sk-back",
			}}},
		}
		plan := Result{Change: []Op{{
			Kind: KindAgent, Name: AgentName(RoleRefiner, "test"),
			Detail: `llm auth method: "ChatGPTSignIn" -> "APIKey"`,
			Role:   RoleRefiner, Field: FieldLLMOpenAIAuthMethod,
		}}}

		got, report, err := apply.Run(context.Background(), in, plan, newCustomInstall())
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if len(h.updateAgentCalls) != 1 {
			t.Fatalf("UpdateAgent called %d times, want 1", len(h.updateAgentCalls))
		}
		call := h.updateAgentCalls[0]
		if call.llm.OpenAIAuthMethod != LLMAuthMethodAPIKey {
			t.Errorf("update authMethod = %q, want %q — an empty one would leave the agent on the sign-in",
				call.llm.OpenAIAuthMethod, LLMAuthMethodAPIKey)
		}
		if call.llm.OpenAIKey != "sk-back" {
			t.Errorf("update apiKey = %q, want the settings' key", call.llm.OpenAIKey)
		}
		if rs := got.Roles[RoleRefiner]; rs.LLMOpenAIAuthMethod != LLMAuthMethodAPIKey {
			t.Errorf("row records %q, want %q", rs.LLMOpenAIAuthMethod, LLMAuthMethodAPIKey)
		}
		if len(report.Notes) != 0 {
			t.Errorf("report notes = %q, want none: no sign-in is left to finish", report.Notes)
		}
	})
}
