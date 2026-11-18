package skill

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ainsel-project/ainsel-plugin-forgejo/content"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/settings"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/setup"
)

// openStore opens a settings store in a fresh directory and closes it with the test.
// It is the state of a plugin pod that has started and never been configured.
func openStore(t *testing.T) *settings.Store {
	t.Helper()
	s, err := settings.Open(t.TempDir())
	if err != nil {
		t.Fatalf("settings.Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// editedSection is the role's shipped skill section with an admin's line appended — a
// save the settings store accepts, because it keeps the frontmatter.
func editedSection(role setup.Role) string {
	return content.SkillSection(role) + "\n### Edited for the test\n\nOnly " + string(role) + " says this.\n"
}

// TestDocumentIsShippedWithNoRows is the cold start: a settings table with nothing in
// it answers with the shipped document, because the shipped section of a role nobody
// has edited is what its effective text is. The hub's first fetch of a fresh pod gets
// a real document, not an empty one.
func TestDocumentIsShippedWithNoRows(t *testing.T) {
	s := openStore(t)

	got, err := Document(context.Background(), s)
	if err != nil {
		t.Fatalf("Document: %v", err)
	}
	if got != content.Skill() {
		t.Errorf("cold-start document is not the shipped document (%d bytes vs %d)", len(got), len(content.Skill()))
	}
	if !strings.HasPrefix(got, "---\nname: forgejo\n") {
		t.Error("document does not open with the skill's frontmatter")
	}
}

// TestDocumentFollowsASaveWithoutARestart is design decision 5's whole mechanism: the
// hub re-fetches this body every pass, so a save that lands now is the body the next
// fetch reads — no restart, no push, and no cached copy in the way. It is the same
// Save the settings PUT route performs.
func TestDocumentFollowsASaveWithoutARestart(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	before, err := Document(ctx, s)
	if err != nil {
		t.Fatalf("Document: %v", err)
	}

	edits := []settings.Edit{{Role: string(setup.RoleDeveloper), Field: settings.FieldSkillText, Value: editedSection(setup.RoleDeveloper)}}
	if err := s.Save(ctx, edits); err != nil {
		t.Fatalf("Save: %v", err)
	}

	after, err := Document(ctx, s)
	if err != nil {
		t.Fatalf("Document after Save: %v", err)
	}
	if after == before {
		t.Fatal("the document did not change after a save; the endpoint is serving a cached body")
	}
	if !strings.Contains(after, "Only developer says this.") {
		t.Error("the saved text is not in the document")
	}
	// The save was one role's; the other two sections are still the shipped text.
	for _, role := range []setup.Role{setup.RoleRefiner, setup.RoleReviewer} {
		_, body, _ := content.SkillFrontmatter(content.SkillSection(role))
		if !strings.Contains(after, strings.TrimSpace(body)) {
			t.Errorf("%s's shipped section is missing after a %s edit", role, setup.RoleDeveloper)
		}
	}
	// And it is still one document with the shell's one frontmatter.
	if strings.Count(after, "\n---\n") != 1 {
		t.Error("the served document no longer carries exactly one frontmatter block")
	}
}

// TestDocumentFallsBackToShippedOnReset: a reset clears the dirty flag, and the field
// is the shipped section again — the same fallback a cold start answers with, reached
// without a restart either.
func TestDocumentFallsBackToShippedOnReset(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	edits := []settings.Edit{{Role: string(setup.RoleRefiner), Field: settings.FieldSkillText, Value: editedSection(setup.RoleRefiner)}}
	if err := s.Save(ctx, edits); err != nil {
		t.Fatalf("Save: %v", err)
	}
	edited, err := Document(ctx, s)
	if err != nil {
		t.Fatalf("Document: %v", err)
	}
	if !strings.Contains(edited, "Only refiner says this.") {
		t.Fatal("the edit did not land before the reset")
	}

	resets := []settings.Edit{{Role: string(setup.RoleRefiner), Field: settings.FieldSkillText, Reset: true}}
	if err := s.Save(ctx, resets); err != nil {
		t.Fatalf("reset Save: %v", err)
	}
	back, err := Document(ctx, s)
	if err != nil {
		t.Fatalf("Document after reset: %v", err)
	}
	if back != content.Skill() {
		t.Error("after a reset the document is not the shipped document again")
	}
}

// TestTextServesTheDeclaredPathOnly: the plugin composes one document, and a route
// mounted for a path the composition does not own must not answer with a document that
// belongs to another. This is the case a second manifest skill would hit.
func TestTextServesTheDeclaredPathOnly(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	got, err := Text(ctx, s, content.SkillPath)
	if err != nil {
		t.Fatalf("Text(%q): %v", content.SkillPath, err)
	}
	if got != content.Skill() {
		t.Errorf("Text(%q) is not the composed document", content.SkillPath)
	}

	if _, err := Text(ctx, s, "/skills/other/SKILL.md"); err == nil {
		t.Error("Text served a document for a path it does not own")
	}
}

// brokenSource is a settings store whose read fails.
type brokenSource struct{}

func (brokenSource) Effective(context.Context, setup.Role, string) (string, error) {
	return "", errors.New("the settings store is gone")
}

// TestDocumentReportsAFailedRead: a hub fetch that fails keeps the body the hub
// stored, so the honest answer to a broken read is the error — a document served from
// the shipped defaults would overwrite what the admin saved with the text they
// replaced.
func TestDocumentReportsAFailedRead(t *testing.T) {
	if _, err := Document(context.Background(), brokenSource{}); err == nil {
		t.Error("Document answered a broken read as if nothing had failed")
	}
}

// TestDocumentWithoutAStoreIsShipped: a plugin built with no settings source serves the
// shipped document, the way it serves shipped defaults everywhere else.
func TestDocumentWithoutAStoreIsShipped(t *testing.T) {
	got, err := Document(context.Background(), nil)
	if err != nil {
		t.Fatalf("Document: %v", err)
	}
	if got != content.Skill() {
		t.Error("Document with no source is not the shipped document")
	}
}
