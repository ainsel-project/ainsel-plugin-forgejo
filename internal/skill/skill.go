// Package skill is the skill document the plugin's HTTP endpoint serves: the one
// document the manifest declares, composed from the per-role texts the settings
// keep.
//
// Nothing here pushes anywhere. The hub re-fetches the body of every skill its
// manifest knows on every sync pass (30s apart) and keeps the body it stored when a
// fetch fails, so a saved settings edit reaches the skill registry on the next
// fetch — no restart, no notify, and no skills:* permission (design decision 5).
// That is why the settings are read per request instead of once at start: a cached
// body is a body the admin's next save does not change.
//
// Membership is not this package's to offer, either. The manifest declares the
// skill, the reconcile pass writes exactly that name into every agent the plugin
// owns, and what an admin edits here is the text of the declared skill — never the
// set of them.
//
// The document is served at content.SkillPath, the path ainsel-plugin.json declares
// it at; CheckManifestRoutes is what keeps the two agreeing.
package skill

import (
	"context"
	"fmt"

	"github.com/ainsel-project/ainsel-plugin-forgejo/content"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/settings"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/setup"
)

// Source is what the served document needs of the settings store: the value in
// force for one (role, field) — the admin's edit, or the shipped default behind it.
// It is the half of settings.Store the endpoint reads, so a caller can be a fake.
type Source interface {
	Effective(ctx context.Context, role setup.Role, field string) (string, error)
}

// Document returns the skill document to serve right now: every role's current text
// composed into the shipped document's shape, each section under the heading naming
// its role.
//
// With an empty settings store — a pod that has never been configured — the answer
// is the shipped document, because the shipped section of a role with no row is what
// its effective text is. A settings read that fails is reported as an error rather
// than answered with the shipped document: the hub keeps the body of its last good
// fetch, and overwriting an admin's text with the default for the length of a broken
// read is the worse answer.
func Document(ctx context.Context, src Source) (string, error) {
	if src == nil {
		return content.Skill(), nil
	}
	texts := make(map[setup.Role]string, len(settings.Roles))
	for _, role := range settings.Roles {
		text, err := src.Effective(ctx, role, settings.FieldSkillText)
		if err != nil {
			return "", err
		}
		texts[role] = text
	}
	return content.ComposeSkill(texts), nil
}

// Text returns the body to serve at one of the plugin's skill paths: the composed
// Document for content.SkillPath. Any other path is an error — this plugin composes
// one document, and a route mounted for a path the composition does not own must not
// answer with a document that belongs to a different one.
func Text(ctx context.Context, src Source, path string) (string, error) {
	if path != content.SkillPath {
		return "", fmt.Errorf("skill: no document is composed for %q", path)
	}
	return Document(ctx, src)
}
