package setup

import "strings"

// Normalize is the one place the wizard's answer is cleaned, so both entry points
// — the HTTP setup API and the MCP tools — read the same form of the same answer.
//
// It exists because a container image URL with a trailing tab is a real thing a
// copy-paste produces, and the hub accepts it onto the AgentImage while Kubernetes
// refuses the pod: every task Job of that role then fails with FailedCreate, holds
// the agent's only task slot, and nothing shows in the hub. Trimming here means the
// ref that reaches the hub is the ref the runtime can start.
//
// What it trims: the fields whose value is a name or an address, where surrounding
// whitespace is never meant to be there — the slug, the org, the Forgejo address,
// the exclude label, and per role the runtime, the agent name and the turn timeout.
// What it leaves alone: AdminToken, which is a credential whose bytes are the
// answer (a token with a space in it is that token), and GroupID, which is an
// opaque hub id the caller matched against their own groups verbatim.
//
// It is a value copy, and it copies the role map rather than writing into the
// caller's, so a caller who reuses its own Input sees no surprise. It is
// idempotent, so Plan and Apply.Run can each call it without agreeing on who did.
//
// Trimming only removes whitespace around a value. One inside it — "img: 1.24" —
// survives, because there is nothing sensible to replace it with; Plan blocks that
// (see runtimeHasWhitespace).
func Normalize(in Input) Input {
	in.Slug = strings.TrimSpace(in.Slug)
	in.Org = strings.TrimSpace(in.Org)
	in.BaseURL = strings.TrimSpace(in.BaseURL)
	in.ExcludeLabel = strings.TrimSpace(in.ExcludeLabel)

	if len(in.Roles) > 0 {
		roles := make(map[Role]RoleInput, len(in.Roles))
		for r, ri := range in.Roles {
			ri.Runtime = strings.TrimSpace(ri.Runtime)
			ri.AgentName = strings.TrimSpace(ri.AgentName)
			ri.TurnTimeoutMs = strings.TrimSpace(ri.TurnTimeoutMs)
			roles[r] = ri
		}
		in.Roles = roles
	}
	return in
}
