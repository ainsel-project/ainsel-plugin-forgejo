package api

import (
	"context"
	"testing"

	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/hub"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/setup"
	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/work"
)

// The second Forgejo: the address of the second install, distinct from testBase.
const testBase2 = "https://forge-two.example.test"

// twoInstallsAreTwoForges is the issue's read-path rule: a plugin installed
// against several Forgejos links each run on the forge it happened on. Two
// installs, two connectors, two addresses — and the work list carries two
// different bases, one per repository, rather than the one address the
// environment named.
func TestWorkLinksEachInstallToItsOwnForgejo(t *testing.T) {
	const (
		connectorA = "c-a"
		connectorB = "c-b"
		repoA      = "team-a/alpha"
		repoB      = "team-b/beta"
	)

	installA := setup.Install{
		Slug:        "a",
		GroupID:     "g-1",
		BaseURL:     testBase,
		ConnectorID: connectorA,
		Roles:       map[setup.Role]setup.RoleState{setup.RoleDeveloper: {AgentID: "agent-a"}},
	}
	installB := setup.Install{
		Slug:        "b",
		GroupID:     "g-1",
		BaseURL:     testBase2,
		ConnectorID: connectorB,
		Roles:       map[setup.Role]setup.RoleState{setup.RoleDeveloper: {AgentID: "agent-b"}},
	}

	runs := []work.Run{
		{
			ID: "run-a", Agent: "agent-a", Status: "success", Connector: connectorA,
			Start: at(9, 0), End: ending(at(9, 30)), CostUSD: 0.1, CostKnown: true,
			Subject: issueOf(repoA, 1, "on the first forge"),
		},
		{
			ID: "run-b", Agent: "agent-b", Status: "success", Connector: connectorB,
			Start: at(10, 0), End: ending(at(10, 30)), CostUSD: 0.2, CostKnown: true,
			Subject: issueOf(repoB, 2, "on the second forge"),
		},
	}

	svc := NewWorkService(&hub.Fake{SeedRuns: runs}, testStore(t, installA, installB), testBase)

	groups, err := svc.Work(context.Background(), Filter{})
	if err != nil {
		t.Fatalf("Work: %v", err)
	}
	if len(groups) != 2 {
		t.Fatalf("got %d repo groups, want 2: %+v", len(groups), groups)
	}

	urls := map[string]string{}
	for _, g := range groups {
		if len(g.Items) != 1 {
			t.Fatalf("repo %s has %d items, want 1", g.Repo, len(g.Items))
		}
		urls[g.Repo] = g.Items[0].URL
	}
	if want := testBase + "/" + repoA + "/issues/1"; urls[repoA] != want {
		t.Errorf("%s link = %q, want %q", repoA, urls[repoA], want)
	}
	if want := testBase2 + "/" + repoB + "/issues/2"; urls[repoB] != want {
		t.Errorf("%s link = %q, want %q — the second install's work must not be linked on the env forge", repoB, urls[repoB], want)
	}
}

// TestNowLinksEachInstallToItsOwnForgejo checks the same rule on the Now page,
// including the linked-issue URL a pull request carries: both halves of a run's
// answer come from the install the run belongs to.
func TestNowLinksEachInstallToItsOwnForgejo(t *testing.T) {
	const (
		connectorA = "c-a"
		connectorB = "c-b"
		repoA      = "team-a/alpha"
		repoB      = "team-b/beta"
	)

	installA := setup.Install{
		Slug: "a", GroupID: "g-1", BaseURL: testBase, ConnectorID: connectorA,
		Roles: map[setup.Role]setup.RoleState{setup.RoleDeveloper: {AgentID: "agent-a"}},
	}
	installB := setup.Install{
		Slug: "b", GroupID: "g-1", BaseURL: testBase2, ConnectorID: connectorB,
		Roles: map[setup.Role]setup.RoleState{setup.RoleDeveloper: {AgentID: "agent-b"}},
	}

	runs := []work.Run{
		{
			ID: "run-a", Agent: "agent-a", Status: "running", Connector: connectorA,
			Start:   at(9, 0),
			Subject: issueOf(repoA, 1, "on the first forge"),
		},
		{
			// A pull on the second forge whose title names the issue it closes:
			// both of its links belong on that forge.
			ID: "run-b", Agent: "agent-b", Status: "running", Connector: connectorB,
			Start:   at(10, 0),
			Subject: pullOf(repoB, 2, "fix(#5): on the second forge", "fix/5-second-forge"),
		},
	}

	svc := NewWorkService(&hub.Fake{SeedRuns: runs}, testStore(t, installA, installB), testBase)

	out, err := svc.Now(context.Background())
	if err != nil {
		t.Fatalf("Now: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("Now returned %d runs, want 2: %+v", len(out), out)
	}

	byID := map[string]NowRun{}
	for _, r := range out {
		byID[r.Run.ID] = r
	}
	if got, want := byID["run-a"].URL, testBase+"/"+repoA+"/issues/1"; got != want {
		t.Errorf("run-a URL = %q, want %q", got, want)
	}
	if got, want := byID["run-b"].URL, testBase2+"/"+repoB+"/pulls/2"; got != want {
		t.Errorf("run-b URL = %q, want %q", got, want)
	}
	if got, want := byID["run-b"].IssueURL, testBase2+"/"+repoB+"/issues/5"; got != want {
		t.Errorf("run-b issueUrl = %q, want %q — the linked issue is on the same forge as the pull", got, want)
	}
}

// TestWorkFallsBackToTheConfiguredAddress checks the fallback: an install that
// carries no address — a row from before instances existed — still links, from
// the address the plugin was started with.
func TestWorkFallsBackToTheConfiguredAddress(t *testing.T) {
	install := setup.Install{
		Slug: "no-base", GroupID: "g-1", BaseURL: "", ConnectorID: connectorID,
		Roles: map[setup.Role]setup.RoleState{setup.RoleDeveloper: {AgentID: "forgejo-developer"}},
	}
	runs := []work.Run{
		{
			ID: "run-1", Agent: "forgejo-developer", Status: "success", Connector: connectorID,
			Start: at(9, 0), End: ending(at(9, 30)), CostUSD: 0.1, CostKnown: true,
			Subject: issueOf(appsRepo, 12, "Search the word list"),
		},
	}

	svc := NewWorkService(&hub.Fake{SeedRuns: runs}, testStore(t, install), testBase)

	groups, err := svc.Work(context.Background(), Filter{})
	if err != nil {
		t.Fatalf("Work: %v", err)
	}
	if len(groups) != 1 || len(groups[0].Items) != 1 {
		t.Fatalf("got %+v, want one item", groups)
	}
	if got, want := groups[0].Items[0].URL, testBase+"/"+appsRepo+"/issues/12"; got != want {
		t.Errorf("URL = %q, want the fallback %q", got, want)
	}
}
