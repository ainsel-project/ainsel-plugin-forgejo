package work

import (
	"testing"

	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/forge"
)

// subjectIn is the subject of a run about an issue in a repository the test names
// — the two-forge case needs two repositories, where the shared helper is pinned
// to one.
func subjectIn(repo string, number int, title string) forge.Subject {
	return forge.Subject{Repo: repo, Number: number, Kind: forge.KindIssue, Title: title}
}

// TestBuildWithBaseLinksEachRunToItsOwnForge is the multi-instance rule: a
// plugin installed against two Forgejos builds each repository's links from the
// address of the install whose connector received the run, not from one address
// for everything. The two repositories live on two hosts, so the two groups
// carry two different bases.
func TestBuildWithBaseLinksEachRunToItsOwnForge(t *testing.T) {
	const (
		baseA = "https://forge-a.example.com"
		baseB = "https://forge-b.example.com"
	)

	runs := []Run{
		{
			ID: "run-1", Agent: "coder", Status: "success", Connector: "install-a",
			Start: at(10, 0), End: ending(at(10, 5)),
			Subject: subjectIn("apps/onboarding", 1, "first"),
		},
		{
			ID: "run-2", Agent: "coder", Status: "success", Connector: "install-b",
			Start: at(11, 0), End: ending(at(11, 5)),
			Subject: subjectIn("libs/other", 7, "second"),
		},
	}

	baseFor := func(r Run) string {
		switch r.Connector {
		case "install-a":
			return baseA
		case "install-b":
			return baseB
		}
		return ""
	}

	groups := BuildWithBase(runs, baseFor)
	if len(groups) != 2 {
		t.Fatalf("got %d repo groups, want 2: %+v", len(groups), groups)
	}

	byRepo := map[string]string{}
	for _, g := range groups {
		if len(g.Items) != 1 {
			t.Fatalf("repo %s has %d items, want 1", g.Repo, len(g.Items))
		}
		byRepo[g.Repo] = g.Items[0].URL
	}
	if want := baseA + "/apps/onboarding/issues/1"; byRepo["apps/onboarding"] != want {
		t.Errorf("apps/onboarding link = %q, want %q", byRepo["apps/onboarding"], want)
	}
	if want := baseB + "/libs/other/issues/7"; byRepo["libs/other"] != want {
		t.Errorf("libs/other link = %q, want %q", byRepo["libs/other"], want)
	}
}

// TestBuildIsOneBaseForEveryRun pins the old shape: Build is BuildWithBase with
// a constant resolver, so a caller with one Forgejo answers exactly as it did.
func TestBuildIsOneBaseForEveryRun(t *testing.T) {
	runs := []Run{
		{
			ID: "run-1", Agent: "coder", Status: "success", Connector: "forgejo",
			Start: at(10, 0), End: ending(at(10, 5)),
			Subject: subjectIn("apps/onboarding", 1, "first"),
		},
		{
			ID: "run-2", Agent: "coder", Status: "success", Connector: "forgejo",
			Start: at(11, 0), End: ending(at(11, 5)),
			Subject: subjectIn("libs/other", 7, "second"),
		},
	}

	single := Build(testBase, runs)
	constant := BuildWithBase(runs, func(Run) string { return testBase })
	if len(single) != len(constant) {
		t.Fatalf("Build and BuildWithBase disagree on the group count: %d vs %d", len(single), len(constant))
	}
	for i := range single {
		if single[i].Repo != constant[i].Repo {
			t.Fatalf("group %d: repo %q vs %q", i, single[i].Repo, constant[i].Repo)
		}
		for j := range single[i].Items {
			if single[i].Items[j].URL != constant[i].Items[j].URL {
				t.Errorf("group %d item %d: URL %q vs %q", i, j, single[i].Items[j].URL, constant[i].Items[j].URL)
			}
		}
	}
}

// TestBuildWithBaseEmptyResolverLeavesURLsEmpty checks the no-address state: a
// resolver that names nothing produces no links, rather than links built from an
// invented host.
func TestBuildWithBaseEmptyResolverLeavesURLsEmpty(t *testing.T) {
	runs := []Run{
		{
			ID: "run-1", Agent: "coder", Status: "success", Connector: "install-a",
			Start: at(10, 0), End: ending(at(10, 5)),
			Subject: subjectIn("apps/onboarding", 1, "first"),
		},
	}

	groups := BuildWithBase(runs, func(Run) string { return "" })
	if len(groups) != 1 || len(groups[0].Items) != 1 {
		t.Fatalf("got %+v, want one group with one item", groups)
	}
	if groups[0].Items[0].URL != "" {
		t.Errorf("URL = %q, want empty for an unresolvable address", groups[0].Items[0].URL)
	}
}
