package content

import (
	"encoding/json"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/setup"
)

func TestPersonasNotEmpty(t *testing.T) {
	for _, role := range []setup.Role{setup.RoleRefiner, setup.RoleDeveloper, setup.RoleReviewer} {
		if text := Persona(role); strings.TrimSpace(text) == "" {
			t.Errorf("Persona(%q) is empty", role)
		}
	}
}

func TestSkillNotEmpty(t *testing.T) {
	if strings.TrimSpace(Skill()) == "" {
		t.Error("Skill() is empty")
	}
}

// The served document is one document, made of three role sections. Its shape is
// what the hub's skill row and the agents reading it depend on, so it is pinned
// exactly: the shell's frontmatter once, then each role's text under the heading
// naming it, in the order the settings list the roles.
func TestComposeSkillShape(t *testing.T) {
	got := ComposeSkill(map[setup.Role]string{
		setup.RoleRefiner:   "---\nname: forgejo\ndescription: kept\n---\n\n### Reading the work\n\nRefiner text.\n",
		setup.RoleDeveloper: "### Reading the work\n\nDeveloper text.\n",
		setup.RoleReviewer:  "   \n",
	})

	want := strings.TrimRight(skillShell, "\n") + "\n\n" +
		"## Refiner\n\n### Reading the work\n\nRefiner text.\n\n" +
		"## Developer\n\n### Reading the work\n\nDeveloper text.\n"
	if got != want {
		t.Errorf("composed document =\n%q\nwant\n%q", got, want)
	}
}

// Three rules the composition exists to hold: the document carries exactly one
// frontmatter (a second --- block mid-document is text, not frontmatter), every role
// is named by a heading, and an edit to one role changes that section alone.
func TestShippedSkillComposition(t *testing.T) {
	skill := Skill()

	if n := frontmatterDelimiters(skill); n != 2 {
		t.Errorf("document carries %d --- delimiters, want the shell's opening pair", n)
	}

	for _, heading := range []string{"## Refiner\n", "## Developer\n", "## Reviewer\n"} {
		if strings.Count(skill, "\n"+heading) != 1 {
			t.Errorf("document does not carry the role heading %q exactly once", strings.TrimRight(heading, "\n"))
		}
	}

	// Each role's shipped text is in its own section, and no other role's frontmatter
	// survived into the body: the section delimiters are the shell's pair and nothing
	// else.
	for _, role := range skillRoles {
		_, body, ok := SkillFrontmatter(SkillSection(role))
		if !ok {
			t.Fatalf("SkillSection(%q) is not a SKILL.md-shaped text", role)
		}
		if !strings.Contains(skill, strings.TrimSpace(body)) {
			t.Errorf("document is missing the shipped section of %s", role)
		}
	}

	// The hub reads at most 64 KiB of a plugin's skill document and refuses the rest,
	// so a longer body is a skill that never lands at all. Composing three sections is
	// what could push it over.
	if len(skill) > 64<<10 {
		t.Errorf("skill body is %d bytes, over the hub's 64 KiB cap", len(skill))
	}
}

// An edited role text lands in its own section and nowhere else: the shared shell and
// the other two sections are served as shipped.
func TestComposeSkillEditsOneSection(t *testing.T) {
	const marker = "### An admin's own heading\n\nOnly the reviewer says this."
	skill := ComposeSkill(map[setup.Role]string{setup.RoleReviewer: "---\nname: forgejo\ndescription: kept\n---\n\n" + marker})

	if !strings.Contains(skill, marker) {
		t.Error("the edited section is not in the document")
	}
	for _, role := range []setup.Role{setup.RoleRefiner, setup.RoleDeveloper} {
		_, body, _ := SkillFrontmatter(SkillSection(role))
		if !strings.Contains(skill, strings.TrimSpace(body)) {
			t.Errorf("%s's shipped section is missing from a document that only edited %s", role, setup.RoleReviewer)
		}
	}
	// The edited text's own frontmatter belongs to the text, not to the document: a
	// second one would read as text in the middle of the page.
	if strings.Contains(skill, "description: kept") {
		t.Error("a section's frontmatter was served inside the document")
	}
}

// The skill is global — one document for every install — so unlike a persona it can
// never carry an install's names: a {{...}} placeholder in it would reach the hub's
// skill row unreplaced, because nothing renders a served skill.
func TestSkillCarriesNoPlaceholders(t *testing.T) {
	if strings.Contains(Skill(), "{{") {
		t.Error("skill carries a template placeholder no install will substitute")
	}
}

// frontmatterDelimiters counts the lines of a document that are a --- delimiter: two
// is a document with one frontmatter, and more than two is a stray block of YAML that
// a markdown reader shows as text.
func frontmatterDelimiters(text string) int {
	n := 0
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "---" {
			n++
		}
	}
	return n
}

func TestSkillCarriesConventions(t *testing.T) {
	skill := Skill()
	for _, want := range []string{"Closes #", "(#"} {
		if !strings.Contains(skill, want) {
			t.Errorf("skill does not contain %q", want)
		}
	}
}

// The skill names the plugin's tools by their bare names only — the hub's
// listing prefix is derived from the plugin id and tools/list serves bare
// names, so a prefixed literal in the body would name a tool that was never
// registered.
func TestSkillUsesBareToolNames(t *testing.T) {
	skill := Skill()
	if strings.Contains(skill, "forgejo_") {
		t.Error("skill contains a hub-prefixed tool name")
	}
	for _, want := range []string{"list_work", "get_work_item"} {
		if !strings.Contains(skill, want) {
			t.Errorf("skill does not mention %q", want)
		}
	}
}

// The trigger set has no reviewer re-trigger on push, so the developer persona
// must not promise an automatic re-review; a human re-requests it.
func TestDeveloperPersonaNoAutoRetrigger(t *testing.T) {
	if text := Persona(setup.RoleDeveloper); strings.Contains(text, "re-triggered automatically") {
		t.Error("developer persona promises an automatic reviewer re-trigger")
	}
}

// Each persona is a template: it must carry at least one {{...Bot}}
// placeholder and must not leak the dev install's concrete bot handles or
// Forgejo address (R14).
func TestPersonasAreTemplated(t *testing.T) {
	roles := []setup.Role{setup.RoleRefiner, setup.RoleDeveloper, setup.RoleReviewer}
	for _, role := range roles {
		text := Persona(role)
		if !strings.Contains(text, "}}") || !strings.Contains(text, "Bot}}") {
			t.Errorf("Persona(%q) has no {{...Bot}} placeholder", role)
		}
		for _, leak := range []string{"refine-agent", "dev-agent", "review-agent", "duckdns"} {
			if strings.Contains(text, leak) {
				t.Errorf("Persona(%q) leaks install-specific name %q", role, leak)
			}
		}
	}
}

// The default tool sets, pinned to spec §3.3 (the dev instance's role images,
// mcp__forgejo-mcp-server__ prefixes stripped, mem0 tools dropped). Sorted.
var wantTools = map[setup.Role][]string{
	setup.RoleRefiner: {
		"add_issue_labels", "create_issue_comment", "edit_issue_comment",
		"get_file_content", "get_issue_by_index", "get_issue_comment",
		"get_my_user_info", "get_pull_request_by_index", "get_repo_tree",
		"list_issue_comments", "list_repo_contents", "list_repo_issues",
		"list_repo_labels", "update_issue",
	},
	setup.RoleDeveloper: {
		"add_issue_labels", "bash", "check_notifications", "create_branch",
		"create_file", "create_issue_comment", "create_pull_request",
		"create_review_requests", "delete_branch", "delete_file",
		"delete_issue_comment", "edit_issue_comment", "get_action_job_logs",
		"get_file_content", "get_issue_by_index",
		"get_issue_comment", "get_my_user_info", "get_pull_request_by_index",
		"get_pull_request_diff", "get_pull_request_status", "get_pull_review",
		"get_repo_tree",
		"get_workflow_run", "issue_state_change", "list_action_run_jobs",
		"list_branches",
		"list_issue_comments", "list_my_repos", "list_pull_request_files",
		"list_pull_review_comments", "list_pull_reviews", "list_repo_commits",
		"list_repo_contents", "list_repo_issues", "list_repo_labels",
		"list_repo_pull_requests", "list_workflow_runs", "mark_notification_read",
		"mark_repo_notifications_read", "remove_issue_labels", "search_repos",
		"update_file", "update_issue", "update_pull_request",
	},
	setup.RoleReviewer: {
		"create_issue_comment", "create_pull_review", "get_commit_statuses",
		"get_file_content", "get_issue_by_index",
		"get_issue_comment", "get_my_user_info", "get_pull_request_by_index",
		"get_pull_request_diff", "get_pull_request_status",
		"get_pull_review", "get_repo_tree",
		"list_branches", "list_issue_comments", "list_my_repos",
		"list_pull_request_files", "list_pull_review_comments", "list_pull_reviews",
		"list_repo_commits", "list_repo_contents", "list_repo_pull_requests",
		"search_repos", "submit_pull_review",
	},
}

// refiner 14, developer 44 (43 + bash), reviewer 23 — spec §3.3 plus the CI reads
// each role's persona performs.
func TestDefaultToolCounts(t *testing.T) {
	want := map[setup.Role]int{setup.RoleRefiner: 14, setup.RoleDeveloper: 44, setup.RoleReviewer: 23}
	for role, n := range want {
		if got := len(DefaultTools(role)); got != n {
			t.Errorf("DefaultTools(%q) has %d tools, want %d", role, got, n)
		}
	}
}

// The names are exactly spec §3.3's, sorted, and the embedded JSON carries the
// same list (the shipped data and the code's view may not drift apart).
func TestDefaultToolsExactNames(t *testing.T) {
	for role, want := range wantTools {
		got := DefaultTools(role)
		if !slices.Equal(got, want) {
			t.Errorf("DefaultTools(%q) =\n%s\nwant\n%s", role, strings.Join(got, "\n"), strings.Join(want, "\n"))
		}
		if !slices.IsSorted(got) {
			t.Errorf("DefaultTools(%q) is not sorted", role)
		}
		var doc struct {
			Tools []string `json:"tools"`
		}
		data, err := profilesFS.ReadFile("profiles/" + string(role) + ".json")
		if err != nil {
			t.Fatalf("read embedded profile %q: %v", role, err)
		}
		if err := json.Unmarshal(data, &doc); err != nil {
			t.Fatalf("parse embedded profile %q: %v", role, err)
		}
		sorted := slices.Clone(doc.Tools)
		slices.Sort(sorted)
		if !slices.Equal(sorted, want) {
			t.Errorf("embedded profile %q disagrees with the expected set", role)
		}
	}
}

// The reads a role's persona performs must be in that role's default set: a persona
// that names a tool the install never granted describes a step that can only fail,
// and an agent that cannot read CI has to conclude something anyway. (The shipped
// reviewer persona said "check CI — call list_workflow_runs" while the reviewer's
// set held no CI tool at all, and told it to read a context file it could not open.)
func TestPersonaReadsAreGrantedToTheRole(t *testing.T) {
	want := map[setup.Role][]string{
		setup.RoleRefiner:   {"get_file_content", "get_pull_request_by_index", "list_repo_contents", "get_repo_tree"},
		setup.RoleDeveloper: {"get_file_content", "get_pull_request_status", "list_workflow_runs", "list_action_run_jobs", "get_action_job_logs"},
		setup.RoleReviewer:  {"get_file_content", "get_pull_request_status", "get_commit_statuses", "get_pull_request_by_index", "list_repo_contents"},
	}
	for role, tools := range want {
		have := map[string]bool{}
		for _, tool := range DefaultTools(role) {
			have[tool] = true
		}
		for _, tool := range tools {
			if !have[tool] {
				t.Errorf("Persona(%q) reads with %q, which the role is not granted", role, tool)
			}
		}
	}
}

// Tool names no forgejo-mcp server has ever served. A persona or skill naming one
// instructs the agent to call a tool that cannot answer, so the name is the bug.
func TestContentNamesNoImaginaryTools(t *testing.T) {
	gone := []string{"list_directory_contents", "search_code", "`get_pull_request`"}
	texts := map[string]string{
		"persona:refiner":   Persona(setup.RoleRefiner),
		"persona:developer": Persona(setup.RoleDeveloper),
		"persona:reviewer":  Persona(setup.RoleReviewer),
	}
	for _, role := range []setup.Role{setup.RoleRefiner, setup.RoleDeveloper, setup.RoleReviewer} {
		texts["skill:"+string(role)] = SkillSection(role)
	}
	for name, text := range texts {
		for _, tool := range gone {
			if strings.Contains(text, tool) {
				t.Errorf("%s names the non-existent tool %q", name, tool)
			}
		}
	}
}

// Callers edit tool sets; handing out the shared slice would let one install's
// edit change every later DefaultTools result.
func TestDefaultToolsReturnsACopy(t *testing.T) {
	got := DefaultTools(setup.RoleRefiner)
	got[0] = "mutated"
	if again := DefaultTools(setup.RoleRefiner); again[0] == "mutated" {
		t.Error("DefaultTools returned a slice that aliases internal state")
	}
}

// Every default set passes its own guard rails.
func TestCheckToolsAcceptsDefaults(t *testing.T) {
	for _, role := range []setup.Role{setup.RoleRefiner, setup.RoleDeveloper, setup.RoleReviewer} {
		if err := CheckTools(role, DefaultTools(role)); err != nil {
			t.Errorf("CheckTools(%q, DefaultTools(%q)) = %v, want nil", role, role, err)
		}
	}
}

// R7: the refiner may not get write tools.
func TestCheckToolsRefinerForbidden(t *testing.T) {
	tools := append(slices.Clone(DefaultTools(setup.RoleRefiner)), "create_pull_request")
	err := CheckTools(setup.RoleRefiner, tools)
	if err == nil {
		t.Fatal("CheckTools accepted create_pull_request for the refiner")
	}
	if !strings.Contains(err.Error(), "create_pull_request") {
		t.Errorf("error %q does not name create_pull_request", err)
	}
}

// R7: only the reviewer may write reviews, so the developer is denied them too.
func TestCheckToolsDeveloperForbiddenReview(t *testing.T) {
	err := CheckTools("developer", []string{"create_pull_review"})
	if err == nil {
		t.Fatal("CheckTools accepted create_pull_review for the developer")
	}
	if !strings.Contains(err.Error(), "create_pull_review") {
		t.Errorf("error %q does not name create_pull_review", err)
	}
}

// The error names every violating tool, not just the first.
func TestCheckToolsNamesEveryViolation(t *testing.T) {
	err := CheckTools("reviewer", []string{"create_branch", "bash", "update_pull_request", "get_repo_tree"})
	if err == nil {
		t.Fatal("CheckTools accepted forbidden tools for the reviewer")
	}
	for _, want := range []string{"create_branch", "bash", "update_pull_request"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "get_repo_tree") {
		t.Errorf("error %q names the allowed tool get_repo_tree", err)
	}
}

// R7: the reviewer's own review-writing tools are allowed.
func TestCheckToolsReviewerMayWriteReviews(t *testing.T) {
	if err := CheckTools("reviewer", []string{"create_pull_review", "submit_pull_review"}); err != nil {
		t.Errorf("CheckTools(reviewer, review tools) = %v, want nil", err)
	}
}

// A tool name that violates for an unknown role must not be silently accepted.
func TestCheckToolsUnknownRole(t *testing.T) {
	if err := CheckTools("translator", []string{"bash"}); err == nil {
		t.Error("CheckTools accepted tools for an unknown role")
	}
}

// PersonaFor fills every placeholder: the result carries no {{ or }} left.
// TestForbiddenToolsIsWhatCheckToolsRefuses keeps the two readings of the R7 rails in step.
// The wizard offers what ForbiddenTools lists and the plan refuses what CheckTools refuses;
// if the two disagree, a tool the admin was offered turns out to be illegal, or one the
// rails forbid sits in the checklist waiting to be checked.
func TestForbiddenToolsIsWhatCheckToolsRefuses(t *testing.T) {
	for _, role := range []setup.Role{setup.RoleRefiner, setup.RoleDeveloper, setup.RoleReviewer} {
		forbidden := ForbiddenTools(role)
		if len(forbidden) == 0 {
			t.Fatalf("ForbiddenTools(%q) is empty, want every role to have rails", role)
		}
		if !slices.IsSorted(forbidden) {
			t.Errorf("ForbiddenTools(%q) = %v, want a stable order", role, forbidden)
		}

		err := CheckTools(role, forbidden)
		if err == nil {
			t.Fatalf("CheckTools(%q, forbidden) = nil, want the same list refused", role)
		}
		for _, tool := range forbidden {
			if !strings.Contains(err.Error(), tool) {
				t.Errorf("CheckTools(%q, forbidden) error %q does not name %q", role, err, tool)
			}
		}

		// The profile the admin starts from has to be inside the rails, or the shipped
		// defaults and the shipped rails contradict each other.
		if err := CheckTools(role, DefaultTools(role)); err != nil {
			t.Errorf("CheckTools(%q, defaults) = %v, want the shipped profile inside the rails", role, err)
		}
	}
}

// TestForbiddenToolsReturnsACopy is the same rule DefaultTools has: a caller may sort or
// trim what it is given without corrupting what the next caller is given.
func TestForbiddenToolsReturnsACopy(t *testing.T) {
	first := ForbiddenTools(setup.RoleRefiner)
	if len(first) == 0 {
		t.Fatal("no forbidden tools to tamper with")
	}
	first[0] = "tampered"

	if slices.Contains(ForbiddenTools(setup.RoleRefiner), "tampered") {
		t.Error("the shipped rails were changed through the result")
	}
}

func TestPersonaForFillsPlaceholders(t *testing.T) {
	bots := map[setup.Role]string{
		setup.RoleRefiner:   "refiner-acme",
		setup.RoleDeveloper: "developer-acme",
		setup.RoleReviewer:  "reviewer-acme",
	}
	for _, role := range []setup.Role{setup.RoleRefiner, setup.RoleDeveloper, setup.RoleReviewer} {
		text := PersonaFor(role, bots, "https://code.example/go")
		if strings.TrimSpace(text) == "" {
			t.Fatalf("PersonaFor(%q) is empty", role)
		}
		if strings.Contains(text, "{{") || strings.Contains(text, "}}") {
			t.Errorf("PersonaFor(%q) left an unsubstituted placeholder", role)
		}
		placeholder := map[string]string{
			"{{refinerBot}}":   bots[setup.RoleRefiner],
			"{{developerBot}}": bots[setup.RoleDeveloper],
			"{{reviewerBot}}":  bots[setup.RoleReviewer],
			"{{forgejoURL}}":   "https://code.example/go",
		}
		for ph, bot := range placeholder {
			if strings.Contains(Persona(role), ph) && !strings.Contains(text, bot) {
				t.Errorf("PersonaFor(%q) left %q unsubstituted (want %q)", role, ph, bot)
			}
		}
	}
}

// The patrol prompt is fired verbatim by the hub with no event context, so the
// tools it names are the only ones the patrolling developer has to go on. Pin
// them to the shipped developer profile: a prompt naming a tool the role was
// not granted starts its first step with a tool-not-found, silently, 48 times
// a day.
func TestCronPatrolPromptNamesOnlyGrantedDeveloperTools(t *testing.T) {
	granted := DefaultTools(setup.RoleDeveloper)
	for _, name := range toolNameRe.FindAllString(setup.CronPatrolPrompt, -1) {
		if !slices.Contains(granted, name) {
			t.Errorf("patrol prompt names %q, which is not in the developer tool profile %v", name, granted)
		}
	}
	// The patrol's first step depends on listing its own pulls; if that tool
	// ever changes name, the prompt must change with it.
	if !strings.Contains(setup.CronPatrolPrompt, "list_repo_pull_requests") {
		t.Error("patrol prompt must name the list-pulls tool it relies on")
	}
}

// toolNameRe matches snake_case identifiers that read as MCP tool names.
var toolNameRe = regexp.MustCompile(`\b[a-z][a-z0-9]*(?:_[a-z0-9]+)+\b`)
