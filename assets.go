// Package forgejo embeds the files the plugin serves verbatim: the manifest
// it describes itself with, the built UI bundle the AInsel console loads
// at runtime, and the skill document agents read. They live at the repo root
// so the go:embed patterns here can reach them.
package forgejo

import (
	"embed"
	"io/fs"

	"github.com/ainsel-project/ainsel-plugin-forgejo/content"
)

// ManifestJSON is the shipped plugin manifest, the source of truth served
// at GET /ainsel-plugin.json.
//
//go:embed ainsel-plugin.json
var ManifestJSON []byte

// uiDist holds the built UI bundle (ui/dist after `make ui`). The all:
// prefix keeps dotfiles so the directory is never empty for the embedder.
//
//go:embed all:ui/dist
var uiDist embed.FS

// Skills returns the skill documents the plugin ships, keyed by the path each is
// served at, ready to hand to server.Config.Skills. The hub registers each one the
// manifest declares, so the text, this path and that declaration are one change —
// content.SkillPath is the path, and CheckManifestRoutes is what keeps the manifest
// naming it: the hub comes to the manifest's path and stores what it finds there, so
// a rename on one side alone leaves a skill row holding nothing.
//
// This is the shipped text. What the route answers at request time is the server's
// SkillText resolver when the plugin has one — the same document with the roles'
// saved texts composed into it — which is what makes a saved settings edit the body
// the hub's next fetch sees.
func Skills() map[string]string {
	return map[string]string{content.SkillPath: content.Skill()}
}

// UI returns the embedded UI bundle as a filesystem rooted at ui/dist,
// ready to serve with http.FS.
func UI() fs.FS {
	sub, err := fs.Sub(uiDist, "ui/dist")
	if err != nil {
		// Unreachable: the "ui/dist" directory is embedded, so fs.Sub
		// cannot fail for it.
		panic("forgejo: embedded ui/dist missing: " + err.Error())
	}
	return sub
}
