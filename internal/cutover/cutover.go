// Package cutover runs the fresh-start handover the managed-agents design
// decided on: the first time the plugin starts under the created-only layout,
// every install row the legacy wizard left behind is retired. The cutover
// deletes the hub resources the row still tracks ids for — the ones the plugin
// is certain it created — drops the row once nothing of it is left to do, and
// logs what it did, loudly.
//
// Adopt-by-absence is the whole safety story here. The legacy wizard could
// adopt resources it never created, and its rows cannot tell those apart from
// its own; the cutover therefore only ever deletes what a row names by id, and
// everything the row never tracked is left standing for a human to judge. That
// is strictly safer than the old removal, which deleted adopted agents too.
//
// Three outcomes, and only one of them keeps the row. A deletion that
// succeeded — including one the hub answered 404, since a resource that is
// already gone is a resource that is done — and a deletion the cutover
// deliberately skips (no Forgejo admin token to delete the bots, teams and hook
// with, which stays a logged manual step) both let the row go. A deletion that
// failed for any other reason is pending: the hub said no, the hub was not
// reachable, the background credential could not be minted, the trigger list
// could not be read. Then the row is kept, stamped Retiring, and the next start
// re-runs the same pass. Dropping the row on a failed delete would be the bug
// this package had: the row is the only record of which resources the plugin
// created, so a row dropped over a failed delete turns a transient hub failure
// into a permanent orphan nothing can ever name again.
//
// A retiring row is hidden from the wizard and from the work view — it is not
// an install the plugin manages any more — but its slug stays taken, so a new
// install cannot be applied over it and erase the pointer, and the remove route
// still reaches it, which is how an admin accepts a resource the hub will not
// delete and drops the row by hand.
//
// Nothing here is urgent enough to hold the pod open: the pass runs once, at
// start, before the reconcile loop or the API see a single row, and a pass that
// crashes mid-way simply finds the remaining rows on the next start and retires
// them too — deleting what is already deleted is one more logged no-op, which
// is what makes the pass safe to re-run.
package cutover

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	forgejoapi "github.com/ainsel-project/ainsel-plugin-forgejo/internal/forgejo"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/hub"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/setup"
)

// hubOps is what the cutover needs of the hub: read the triggers of a
// connector the row names, and delete the resources the row names. hub.Client
// satisfies it; a test can hold a fake that refuses chosen ids.
type hubOps interface {
	ListTriggers(ctx context.Context, connectorID string) ([]setup.Trigger, error)
	DeleteTrigger(ctx context.Context, id string) error
	DeleteConnector(ctx context.Context, id string) error
	DeleteAgent(ctx context.Context, id string) error
	DeleteAgentImage(ctx context.Context, id string) error
	DeletePersona(ctx context.Context, id string) error
	DeleteMCPServer(ctx context.Context, id string) error
}

// forgejoOps is what the cutover needs of Forgejo: the admin's verbs, because
// the bots, the teams and the org hook live in the forge and only a site admin
// can delete them. *forgejo.Admin satisfies it. A nil Forgejo — no admin token
// in the environment, which is the state of a plugin that was never given one —
// leaves every Forgejo resource standing, named in the log.
type forgejoOps interface {
	DeleteOrgHook(ctx context.Context, org string, id int64) error
	DeleteTeam(ctx context.Context, id int64) error
	DeleteUser(ctx context.Context, login string) error
}

// storeOps is what the cutover needs of the install store: read every row once,
// keep one with a pending cleanup stamped on it, and drop one by slug.
// *store.Store satisfies it.
type storeOps interface {
	List(context.Context) ([]setup.Install, error)
	Save(context.Context, setup.Install) error
	Delete(context.Context, string) error
}

// Cutover retires the legacy install rows, one start at a time.
type Cutover struct {
	Hub     hubOps
	Forgejo forgejoOps // may be nil: then the forge side is left standing, logged
	Store   storeOps

	// Log receives one line per install the pass touched — a row kept retiring gets a
	// second one — and one final line. It must not panic; wire it to log.Printf with a
	// "cutover:" prefix.
	Log func(msg string)
}

// Run retires every legacy install it finds. A store that cannot be read is the
// only failure: there is nothing to say about rows that could not be listed,
// and the next start tries again. Everything else is logged and carried on past —
// including a delete that failed, which keeps its row retiring so the next start
// retries it.
func (c Cutover) Run(ctx context.Context) error {
	// The cutover has no user behind it: it runs at start, before any request
	// can arrive, and acts as the plugin itself.
	ctx = hub.WithBackground(ctx)

	installs, err := c.Store.List(ctx)
	if err != nil {
		return fmt.Errorf("cutover: cannot read the installs: %w", err)
	}

	var retired, retiring, kept int
	for _, in := range installs {
		if in.LayoutVersion == setup.LayoutManagedAgents && !in.Retiring {
			continue // created under the new layout: nothing to hand over
		}
		deleted, skipped, pending := c.dropInstall(ctx, in)

		// A row already retiring is a retry, and the log says so: the same install
		// naming the same pending resources across two starts is the signal that
		// the hub is not answering, not that the cutover is new.
		action := "retired"
		if in.Retiring {
			action = "retry"
		}
		c.Log(fmt.Sprintf("legacy install %q %s: %d resources deleted, %d left standing (skipped), %d pending (%s)",
			in.Slug, action, len(deleted), len(skipped), len(pending), strings.Join(summarise(deleted, skipped, pending), "; ")))

		if len(pending) > 0 {
			// The row is the only thing that names what is still standing: drop it
			// here and no code path can ever reach those resources again. Keep it,
			// stamped, and the next start re-runs this same pass over it.
			in.Retiring = true
			if err := c.Store.Save(ctx, in); err != nil {
				c.Log(fmt.Sprintf("install %q: row kept, cannot be stamped retiring: %v", in.Slug, err))
				kept++
				continue
			}
			c.Log(fmt.Sprintf("install %q kept retiring: %d pending deletions, the next start retries them", in.Slug, len(pending)))
			retiring++
			continue
		}

		if err := c.Store.Delete(ctx, in.Slug); err != nil {
			// The row survives, so the next start retires it again — re-deleting
			// what is already gone is one logged no-op more, which is safe.
			c.Log(fmt.Sprintf("install %q: row kept, cannot be dropped: %v", in.Slug, err))
			kept++
			continue
		}
		retired++
	}

	if retired > 0 || retiring > 0 || kept > 0 {
		c.Log(fmt.Sprintf("fresh-start cutover done: %d legacy installs dropped, %d kept retiring for the next start, %d rows kept for the next start",
			retired, retiring, kept))
	}
	return nil
}

// dropInstall deletes what one legacy row tracks and reports the three things
// that can come of it, each as "kind:name" names: what is deleted (gone, or
// already gone when asked), what was deliberately left standing (skipped), and
// what failed and has to be retried (pending). Only the last one decides whether
// the row survives. Nothing it does is decided by anything outside the row: a
// name the row does not carry is a name the cutover never touches.
func (c Cutover) dropInstall(ctx context.Context, in setup.Install) (deleted, skipped, pending []string) {
	hubDelete := func(kind, name string, del func() error) { recordOutcome(&deleted, &pending, kind, name, del()) }
	forgeDelete := func(kind, name string, del func() error) { recordOutcome(&deleted, &pending, kind, name, del()) }

	// Reverse dependency order, the same sequence the removal flow runs: the
	// trigger list first (it hangs off the connector), then the connector, then
	// what the triggers point at, then what those are built from.

	// Triggers: their ids are not on the row, but every name this install gave
	// one carries its slug, and no other install's does.
	if in.ConnectorID != "" {
		triggers, err := c.Hub.ListTriggers(ctx, in.ConnectorID)
		if err != nil {
			// The list is how the cutover names its own triggers: without it there
			// is no saying they are gone, so the install stays pending and the next
			// start asks again.
			pending = append(pending, "triggers:list failed ("+err.Error()+")")
		}
		for _, t := range triggers {
			if !strings.HasPrefix(t.Name, "forgejo-") || !strings.HasSuffix(t.Name, "-"+in.Slug) {
				continue // not this install's: the cutover takes nothing it did not name
			}
			id := t.ID
			hubDelete("trigger", id, func() error { return c.Hub.DeleteTrigger(ctx, id) })
		}
	}

	if in.ConnectorID != "" {
		id := in.ConnectorID
		hubDelete("connector", id, func() error { return c.Hub.DeleteConnector(ctx, id) })
	}

	for _, r := range []setup.Role{setup.RoleRefiner, setup.RoleDeveloper, setup.RoleReviewer} {
		if id := in.Roles[r].AgentID; id != "" {
			hubDelete("agent", id, func() error { return c.Hub.DeleteAgent(ctx, id) })
		}
	}
	for _, r := range []setup.Role{setup.RoleRefiner, setup.RoleDeveloper, setup.RoleReviewer} {
		if id := in.Roles[r].ImageID; id != "" {
			hubDelete("image", id, func() error { return c.Hub.DeleteAgentImage(ctx, id) })
		}
	}
	for _, r := range []setup.Role{setup.RoleRefiner, setup.RoleDeveloper, setup.RoleReviewer} {
		if id := in.Roles[r].PersonaID; id != "" {
			hubDelete("persona", id, func() error { return c.Hub.DeletePersona(ctx, id) })
		}
	}
	if in.MCPServer != "" {
		name := in.MCPServer
		hubDelete("mcp", name, func() error { return c.Hub.DeleteMCPServer(ctx, name) })
	}

	// The forge side. Without an admin token none of it can be reached, and a
	// bot or a hook left behind is exactly the kind of leftover the cutover
	// logs rather than guesses at.
	if c.Forgejo == nil {
		var untracked []string
		if in.HookID != 0 {
			untracked = append(untracked, "hook:"+strconv.FormatInt(in.HookID, 10))
		}
		for _, r := range []setup.Role{setup.RoleRefiner, setup.RoleDeveloper, setup.RoleReviewer} {
			st := in.Roles[r]
			if st.TeamID != 0 {
				untracked = append(untracked, "team:"+strconv.FormatInt(st.TeamID, 10))
			}
			if st.BotLogin != "" {
				untracked = append(untracked, "user:"+st.BotLogin)
			}
		}
		if len(untracked) > 0 {
			skipped = append(skipped, strings.Join(untracked, ", ")+" (no Forgejo admin token to delete them)")
		}
		return deleted, skipped, pending
	}

	if in.HookID != 0 {
		hookID := in.HookID
		forgeDelete("hook", strconv.FormatInt(hookID, 10), func() error {
			return c.Forgejo.DeleteOrgHook(ctx, in.Org, hookID)
		})
	}
	for _, r := range []setup.Role{setup.RoleRefiner, setup.RoleDeveloper, setup.RoleReviewer} {
		st := in.Roles[r]
		if st.TeamID != 0 {
			teamID := st.TeamID
			forgeDelete("team", strconv.FormatInt(teamID, 10), func() error { return c.Forgejo.DeleteTeam(ctx, teamID) })
		}
		if st.BotLogin != "" {
			login := st.BotLogin
			forgeDelete("user", login, func() error { return c.Forgejo.DeleteUser(ctx, login) })
		}
	}

	return deleted, skipped, pending
}

// recordOutcome puts one delete attempt into the right bucket: gone (or already
// gone, which is the same finished thing) into deleted, anything else into
// pending, which is what keeps the row for another start.
func recordOutcome(deleted, pending *[]string, kind, name string, err error) {
	switch {
	case err == nil:
		*deleted = append(*deleted, kind+":"+name)
	case alreadyGone(err):
		// The resource answered that it has no such thing: the deletion is done,
		// and a retry would say the same. Counting it as pending instead would
		// pin the row to a resource that stopped existing.
		*deleted = append(*deleted, kind+":"+name+" (already gone)")
	default:
		*pending = append(*pending, kind+":"+name+" ("+err.Error()+")")
	}
}

// alreadyGone reports whether a failed delete says the resource is not there any
// more — which is a completed deletion, not one to retry. Only a 404 that came
// from the resource itself counts: a failure of the background-credential mint
// carries a status of its own (the credentials endpoint answering 404 while the
// hub is still starting up is exactly the shape this classification must call
// pending), so a chain containing a hub.CredentialError is never "gone".
func alreadyGone(err error) bool {
	var ce *hub.CredentialError
	if errors.As(err, &ce) {
		return false
	}
	var he *hub.StatusError
	if errors.As(err, &he) {
		return he.Code == http.StatusNotFound
	}
	var fe *forgejoapi.StatusError
	if errors.As(err, &fe) {
		return fe.Code == http.StatusNotFound
	}
	return false
}

// summarise folds the three lists into the words the loud line reads with: how
// the deletions went, what stands by choice, and what stands because it could
// not be deleted and will be retried.
func summarise(deleted, skipped, pending []string) []string {
	parts := []string{}
	if len(deleted) > 0 {
		parts = append(parts, "deleted "+strings.Join(deleted, ", "))
	}
	if len(skipped) > 0 {
		parts = append(parts, "left standing (skipped): "+strings.Join(skipped, ", "))
	}
	if len(pending) > 0 {
		parts = append(parts, "pending, will retry: "+strings.Join(pending, ", "))
	}
	if len(parts) == 0 {
		parts = append(parts, "the row tracked nothing")
	}
	return parts
}
