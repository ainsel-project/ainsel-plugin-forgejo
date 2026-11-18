// Package content holds the texts this plugin ships for an install: the three
// default role personas and the forgejo skill. They are embedded so the binary
// is self-contained — the setup flow hands a persona to AInsel when it creates
// an agent, and the hub fetches the skill from the plugin's verified endpoint.
//
// The personas are templates, not final texts. A persona names the install's
// bots and forge by placeholder, and the wizard substitutes them when it
// creates the agent, so the shipped text never carries one install's names:
//
//	{{refinerBot}}, {{developerBot}}, {{reviewerBot}} — the install's bot logins, used as @-mentions
//	{{forgejoURL}} — the install's Forgejo base URL
//
// The package also ships the roles' default tool sets as embedded JSON, one
// profile per role, and the R7 guard rails that constrain which tools an
// admin's edited set may contain.
//
// The skill is shipped in pieces, because the plugin serves one skill document
// made of all three roles: a shell (the frontmatter, the title, and the framing
// that says how the sections are read) and one text per role. ComposeSkill is
// the shape of the served document; the settings' skillText values are the
// editable half of it, and an admin's edit to one role replaces that role's
// section alone.
package content

import (
	"embed"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/setup"
)

//go:embed personas/refiner.md
var refinerPersona string

//go:embed personas/developer.md
var developerPersona string

//go:embed personas/reviewer.md
var reviewerPersona string

// SkillPath is where the plugin's skill document is served, and the path
// ainsel-plugin.json declares it at — the one Go-side name for it, so the shipped
// document's map key and the served document cannot disagree about where the hub
// comes to read it.
const SkillPath = "/skills/forgejo/SKILL.md"

// skillShell is the served skill document's shipped framing: frontmatter,
// title, and the paragraph telling a role how to read the sections. It is not
// an editable setting — the frontmatter is the identity the skill is registered
// under, and it is the same for every role.
//
//go:embed skills/forgejo/SKILL.md
var skillShell string

// The three role sections of the served skill document. Each is a complete
// SKILL.md — frontmatter included, so it stands on its own as an admin's
// skillText — and each writes its headings at the level it appears at once the
// composition has put its role heading above it.
//
//go:embed skills/forgejo/refiner.md
var refinerSkill string

//go:embed skills/forgejo/developer.md
var developerSkill string

//go:embed skills/forgejo/reviewer.md
var reviewerSkill string

// skillRoles is the order the roles' sections are composed in, and therefore
// the order the served document reads in.
var skillRoles = []setup.Role{setup.RoleRefiner, setup.RoleDeveloper, setup.RoleReviewer}

// skillHeadings names each role in the heading its section is served under.
var skillHeadings = map[setup.Role]string{
	setup.RoleRefiner:   "Refiner",
	setup.RoleDeveloper: "Developer",
	setup.RoleReviewer:  "Reviewer",
}

// profilesFS holds the default tool sets, one JSON per role, shaped as
// {"tools": [...]} with bare Forgejo tool names (no mcp__…__ prefix; the
// prefix is per install and added by the caller when it talks to AInsel).
//
//go:embed profiles/*.json
var profilesFS embed.FS

// defaultTools holds each role's parsed, sorted tool set. Callers get copies.
var defaultTools = map[setup.Role][]string{}

func init() {
	for _, role := range []setup.Role{setup.RoleRefiner, setup.RoleDeveloper, setup.RoleReviewer} {
		data, err := profilesFS.ReadFile("profiles/" + string(role) + ".json")
		if err != nil {
			panic("content: embedded profile " + string(role) + " is missing: " + err.Error())
		}
		var doc struct {
			Tools []string `json:"tools"`
		}
		if err := json.Unmarshal(data, &doc); err != nil {
			panic("content: embedded profile " + string(role) + " is malformed: " + err.Error())
		}
		slices.Sort(doc.Tools)
		defaultTools[role] = doc.Tools
	}
}

// DefaultTools returns the role's default tool set (spec §3.3), sorted; the
// developer's set carries "bash" as well. The result is a fresh slice: callers
// edit tool sets and must not be able to corrupt the shipped defaults.
func DefaultTools(r setup.Role) []string {
	return slices.Clone(defaultTools[r])
}

// The R7 guard rails, as lists of bare Forgejo tool names.
//
// nonDeveloperTools: the refiner and the reviewer may never have these —
// they write code or open PRs, which only the developer does.
var nonDeveloperTools = []string{
	"bash",
	"create_branch",
	"delete_branch",
	"create_file",
	"update_file",
	"delete_file",
	"create_pull_request",
	"update_pull_request",
}

// reviewWriteTools: only the reviewer may write a review; the developer
// requests reviews (create_review_requests) but does not cast them.
var reviewWriteTools = []string{
	"create_pull_review",
	"submit_pull_review",
}

// ForbiddenTools are the tools a role may never be given, as the R7 guard rails state
// them. A caller that offers an admin a checklist needs the same rails from the other
// side: a tool the role may not have should not sit in front of them to be checked. It is
// the same list CheckTools reads, so the two cannot disagree about what is allowed.
//
// The result is a fresh, sorted slice — a caller may sort or trim it without touching the
// shipped rails. An unknown role has no forbidden set, which is not the same as an empty
// one being safe; CheckTools is the one that reports an unknown role as an error.
func ForbiddenTools(r setup.Role) []string {
	var out []string
	switch r {
	case setup.RoleRefiner, setup.RoleReviewer:
		out = append(out, nonDeveloperTools...)
	}
	if r != setup.RoleReviewer {
		out = append(out, reviewWriteTools...)
	}
	slices.Sort(out)
	return out
}

// CheckTools reports whether a role's tool set respects the R7 guard rails.
// The error names every violating tool, not just the first, so one round trip
// is enough to fix an edited set. Removing a tool the persona relies on is a
// warning for the caller, not an error here.
func CheckTools(r setup.Role, tools []string) error {
	switch r {
	case setup.RoleRefiner, setup.RoleDeveloper, setup.RoleReviewer:
	default:
		return fmt.Errorf("unknown role %q", r)
	}
	forbidden := map[string]bool{}
	for _, t := range ForbiddenTools(r) {
		forbidden[t] = true
	}
	var bad []string
	seen := map[string]bool{}
	for _, t := range tools {
		if forbidden[t] && !seen[t] {
			bad = append(bad, t)
			seen[t] = true
		}
	}
	if len(bad) == 0 {
		return nil
	}
	slices.Sort(bad)
	return fmt.Errorf("role %q may not use: %s", r, strings.Join(bad, ", "))
}

// Persona returns the template persona text for a role, or "" for a role this
// plugin does not know. The result still carries the {{...Bot}} and
// {{forgejoURL}} placeholders; the caller substitutes them per install.
func Persona(r setup.Role) string {
	switch r {
	case setup.RoleRefiner:
		return refinerPersona
	case setup.RoleDeveloper:
		return developerPersona
	case setup.RoleReviewer:
		return reviewerPersona
	}
	return ""
}

// PersonaFor returns the role's persona with the install's names filled in:
// every {{...Bot}} placeholder takes the install's bot login from bots and
// {{forgejoURL}} takes baseURL. A bot the map does not name is substituted
// with the empty string, so the result never carries a raw placeholder.
func PersonaFor(r setup.Role, bots map[setup.Role]string, baseURL string) string {
	return RenderPersona(Persona(r), bots, baseURL)
}

// RenderPersona fills an install's names into any persona template the same way
// PersonaFor fills them into the shipped one. The settings' personaText is a
// template — an admin edits the text with its placeholders intact and every
// install renders its own bots and address from it — so the reconcile pass
// renders the effective text here rather than only the shipped default.
func RenderPersona(text string, bots map[setup.Role]string, baseURL string) string {
	replacer := strings.NewReplacer(
		"{{refinerBot}}", bots[setup.RoleRefiner],
		"{{developerBot}}", bots[setup.RoleDeveloper],
		"{{reviewerBot}}", bots[setup.RoleReviewer],
		"{{forgejoURL}}", baseURL,
	)
	return replacer.Replace(text)
}

// SkillSection returns the role's shipped skill text — the section the plugin
// serves under that role's heading — frontmatter included, or "" for a role this
// plugin does not know. It is the shipped default of that role's skillText
// setting, and so the text a reset re-adopts.
func SkillSection(r setup.Role) string {
	switch r {
	case setup.RoleRefiner:
		return refinerSkill
	case setup.RoleDeveloper:
		return developerSkill
	case setup.RoleReviewer:
		return reviewerSkill
	}
	return ""
}

// Skill returns the shipped skill document — the shell with every role's shipped
// section composed into it. It is what the plugin's skill path serves while no role
// has been edited (a settings table with no rows answers with each role's shipped
// section, so the composition is this), and what a reset re-adopts. A read of the
// settings that fails is reported as a failure, not answered with this.
func Skill() string {
	return ComposeSkill(nil)
}

// ComposeSkill is the shape of the served skill document: the shipped shell, then
// one section per role in skillRoles' order, each under a heading naming the role
// and separated from the last by a blank line. The document therefore carries
// exactly one frontmatter block — the shell's — whatever an admin types into a
// role's text: a role text's own frontmatter is dropped, because a second ---
// block in the middle of a markdown document is not a frontmatter, it is text.
//
// A role the map leaves out contributes its shipped section, which is why
// Skill() is ComposeSkill(nil). A role mapped to empty (or whitespace-only)
// text contributes no section at all: a heading over nothing says nothing.
// Trailing whitespace on a role's text is trimmed, so the blank line between two
// sections is one blank line whoever wrote the text.
func ComposeSkill(texts map[setup.Role]string) string {
	parts := []string{strings.TrimRight(skillShell, "\n")}
	for _, role := range skillRoles {
		text, ok := texts[role]
		if !ok {
			text = SkillSection(role)
		}
		_, body, hasFrontmatter := SkillFrontmatter(text)
		if !hasFrontmatter {
			// Not a SKILL.md-shaped text: it is still the admin's text, so it
			// is served as the section's content — the heading above it is
			// what makes it a section.
			body = text
		}
		body = strings.TrimSpace(body)
		if body == "" {
			continue
		}
		parts = append(parts, "## "+skillHeadings[role]+"\n\n"+body)
	}
	return strings.Join(parts, "\n\n") + "\n"
}

// SkillFrontmatter splits a SKILL.md-shaped text into its frontmatter — the
// lines between the opening pair of --- — and the text after it. ok is false when
// the text does not open with a frontmatter or never closes it. A SKILL.md is
// registered by the name and description in this block, which is why an edited skill
// text may not lose it (a skill the hub declares from a plugin manifest takes its row
// name and description from the manifest instead, but the document an agent reads is
// still this one). It is also why the served document carries exactly one.
func SkillFrontmatter(text string) (head, body string, ok bool) {
	lines := strings.Split(text, "\n")
	if len(lines) == 0 || strings.TrimSpace(strings.TrimPrefix(lines[0], "\ufeff")) != "---" {
		return "", "", false
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			return strings.Join(lines[1:i], "\n"), strings.Join(lines[i+1:], "\n"), true
		}
	}
	return "", "", false
}

// SkillFrontmatterKeys returns the top-level keys of a skill text's frontmatter —
// the lines that start one, since a SKILL.md frontmatter is a flat mapping and an
// indented line is a continuation, not a key. ok is false when the text carries no
// frontmatter at all.
func SkillFrontmatterKeys(text string) (keys []string, ok bool) {
	head, _, ok := SkillFrontmatter(text)
	if !ok {
		return nil, false
	}
	for _, line := range strings.Split(head, "\n") {
		if line == strings.TrimLeft(line, " \t") {
			if name, _, found := strings.Cut(line, ":"); found {
				keys = append(keys, strings.TrimSpace(name))
			}
		}
	}
	return keys, true
}
