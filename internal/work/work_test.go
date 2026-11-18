package work

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/forge"
)

// The forge every test build is pointed at, and the repository most runs are
// about. Expected URLs are written out in full rather than rebuilt by the same
// code that produces them, so a change to the URL shape fails here.
const (
	testBase = "https://git.example.com/forgejo"
	testRepo = "apps/lingo-swipe"
)

// day is the date the test runs are anchored to; the times below read as clock
// times on it, which keeps a case like "the run that ended at 12:05 is the
// newest" readable.
var day = time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC)

// at is a run time on the test day: at(9, 15) is 09:15 UTC.
func at(hour, minute int) time.Time {
	return day.Add(time.Duration(hour)*time.Hour + time.Duration(minute)*time.Minute)
}

// ending spells a finished run's end time as the *time.Time the type carries.
func ending(t time.Time) *time.Time { return &t }

// issue is the subject of a run about an issue in the test repository.
func issue(number int, title string) forge.Subject {
	return forge.Subject{Repo: testRepo, Number: number, Kind: forge.KindIssue, Title: title}
}

// pull is the subject of a run about a pull request in the test repository,
// carrying the title and branch the linking rule reads.
func pull(number int, title, branch string) forge.Subject {
	return forge.Subject{
		Repo:   testRepo,
		Number: number,
		Kind:   forge.KindPull,
		Title:  title,
		Branch: branch,
	}
}

// label names an item the way a failure message should read it.
func label(it Item) string {
	return string(it.Subject.Kind) + " " + strconv.Itoa(it.Subject.Number)
}

// TestBuildNestsPullRequestUnderItsIssue is the shape the work list is built
// around: a pull request opened for an issue is not a row of its own, it is the
// issue's. One run on issue #264 and one on pull #265, whose title carries the
// issue in its scope, have to come back as one repository holding one item for
// #264 with #265 nested in it — and the repository's cost has to be the two
// runs' costs added once each, because a cost counted under both the pull and
// the issue would be a bill the admin cannot reconcile.
func TestBuildNestsPullRequestUnderItsIssue(t *testing.T) {
	runs := []Run{
		{
			ID: "run-1", Agent: "pm", Role: "planner", Status: "success", Connector: "forgejo",
			Start: at(10, 0), End: ending(at(10, 5)), CostUSD: 0.25, CostKnown: true,
			Subject: issue(264, "Search the word list"),
		},
		{
			ID: "run-2", Agent: "coder", Role: "implementer", Status: "success", Connector: "forgejo",
			Start: at(10, 20), End: ending(at(10, 50)), CostUSD: 0.5, CostKnown: true,
			Subject: pull(265, "feat(#264): search the word list", "feat/264-word-list-search"),
		},
	}

	groups := Build(testBase, runs)
	if len(groups) != 1 {
		t.Fatalf("Build returned %d repo groups, want 1: %+v", len(groups), groups)
	}

	g := groups[0]
	if g.Repo != testRepo {
		t.Errorf("repo group = %q, want %q", g.Repo, testRepo)
	}
	if want := testBase + "/" + testRepo; g.URL != want {
		t.Errorf("repo URL = %q, want %q", g.URL, want)
	}
	if len(g.Items) != 1 {
		t.Fatalf("repo has %d top-level items, want 1: %s", len(g.Items), labels(g.Items))
	}

	issueItem := g.Items[0]
	if issueItem.Subject.Number != 264 || issueItem.Subject.Kind != forge.KindIssue {
		t.Fatalf("top-level item = %s, want issue 264", label(issueItem))
	}
	if want := testBase + "/" + testRepo + "/issues/264"; issueItem.URL != want {
		t.Errorf("issue URL = %q, want %q", issueItem.URL, want)
	}
	// The issue's own totals are its own runs only: the pull's run belongs to
	// the pull, and counting it twice is what the nesting must not do.
	if len(issueItem.Runs) != 1 || issueItem.Runs[0].ID != "run-1" {
		t.Errorf("issue runs = %+v, want only run-1", issueItem.Runs)
	}
	if want := 5 * time.Minute; issueItem.Duration != want {
		t.Errorf("issue duration = %v, want %v", issueItem.Duration, want)
	}
	if want := 0.25; issueItem.CostUSD != want {
		t.Errorf("issue cost = %v, want %v", issueItem.CostUSD, want)
	}

	if len(issueItem.PRs) != 1 {
		t.Fatalf("issue has %d nested pulls, want 1: %s", len(issueItem.PRs), labels(issueItem.PRs))
	}
	pr := issueItem.PRs[0]
	if pr.Subject.Number != 265 || pr.Subject.Kind != forge.KindPull {
		t.Fatalf("nested item = %s, want pull 265", label(pr))
	}
	if want := testBase + "/" + testRepo + "/pulls/265"; pr.URL != want {
		t.Errorf("pull URL = %q, want %q", pr.URL, want)
	}
	if len(pr.Runs) != 1 || pr.Runs[0].ID != "run-2" {
		t.Errorf("pull runs = %+v, want only run-2", pr.Runs)
	}
	if want := 30 * time.Minute; pr.Duration != want {
		t.Errorf("pull duration = %v, want %v", pr.Duration, want)
	}
	if len(pr.PRs) != 0 {
		t.Errorf("nested pull carries %d pulls of its own, want 0", len(pr.PRs))
	}

	// The repository total is every run exactly once: 0.25 + 0.5, not the
	// 1.0 that adding the nested pull to its issue's total and then the issue
	// to the repo's total would give.
	if want := 0.75; g.CostUSD != want {
		t.Errorf("repo cost = %v, want %v", g.CostUSD, want)
	}
	if !g.CostKnown {
		t.Error("repo cost is not known, want known")
	}
}

// TestBuildKeepsAnUnreferencedPullRequestAtTheTopLevel covers the pull that says
// nowhere which issue it belongs to. Nesting it under a guessed issue would put
// work under a thread that never asked for it, so it stays a row on its own.
func TestBuildKeepsAnUnreferencedPullRequestAtTheTopLevel(t *testing.T) {
	runs := []Run{
		{
			ID: "run-1", Status: "success", Start: at(9, 0), End: ending(at(9, 30)),
			CostUSD: 0.5, CostKnown: true,
			Subject: pull(300, "chore: bump the dependencies", "renovate/deps"),
		},
	}

	groups := Build(testBase, runs)
	if len(groups) != 1 {
		t.Fatalf("Build returned %d repo groups, want 1: %+v", len(groups), groups)
	}
	items := groups[0].Items
	if len(items) != 1 {
		t.Fatalf("repo has %d items, want 1: %s", len(items), labels(items))
	}
	if items[0].Subject.Number != 300 || items[0].Subject.Kind != forge.KindPull {
		t.Errorf("top-level item = %s, want pull 300", label(items[0]))
	}
	if len(items[0].PRs) != 0 {
		t.Errorf("unreferenced pull carries nested pulls: %s", labels(items[0].PRs))
	}
	if want := testBase + "/" + testRepo + "/pulls/300"; items[0].URL != want {
		t.Errorf("pull URL = %q, want %q", items[0].URL, want)
	}
}

// TestBuildDropsRunsWithoutASubject: a run whose subject has no number is not
// work on the forge — there is no issue or pull request page to show it under.
// Such a run is dropped rather than grouping under number 0, and a repository
// whose only runs are those does not appear at all.
func TestBuildDropsRunsWithoutASubject(t *testing.T) {
	runs := []Run{
		{
			ID: "run-numberless", Status: "success", Start: at(9, 0), End: ending(at(9, 10)),
			CostUSD: 4, CostKnown: true,
			Subject: forge.Subject{Repo: "apps/other", Number: 0, Kind: forge.KindIssue, Title: "no page"},
		},
		{
			ID: "run-zero", Status: "success", Start: at(10, 0), End: ending(at(10, 10)),
			CostUSD: 2, CostKnown: true,
			Subject: issue(0, "also no page"),
		},
		{
			ID: "run-real", Status: "success", Start: at(11, 0), End: ending(at(11, 10)),
			CostUSD: 0.5, CostKnown: true,
			Subject: issue(7, "a real issue"),
		},
	}

	groups := Build(testBase, runs)
	if len(groups) != 1 {
		t.Fatalf("Build returned %d repo groups, want 1 (the numberless runs' repo is not work): %+v", len(groups), groups)
	}
	g := groups[0]
	if g.Repo != testRepo {
		t.Errorf("repo group = %q, want %q", g.Repo, testRepo)
	}
	if len(g.Items) != 1 || g.Items[0].Subject.Number != 7 {
		t.Fatalf("repo has %d items, want only issue 7: %s", len(g.Items), labels(g.Items))
	}
	if len(g.Items[0].Runs) != 1 || g.Items[0].Runs[0].ID != "run-real" {
		t.Errorf("issue 7 runs = %+v, want only run-real", g.Items[0].Runs)
	}
	if want := 0.5; g.CostUSD != want {
		t.Errorf("repo cost = %v, want %v — a run about nothing is not billable work", g.CostUSD, want)
	}
}

// TestBuildGroupsRunsBySubject is the plain grouping: runs about the same issue
// or pull are one item, and the only things that separate two items are the
// repository, the number and what kind of thing the number is. A repo's issue 5
// and its pull 5 are two threads Forgejo numbers separately, so they are two
// items; the runs inside an item are newest first, the way the list is read.
func TestBuildGroupsRunsBySubject(t *testing.T) {
	runs := []Run{
		{
			ID: "run-early", Status: "success", Start: at(8, 0), End: ending(at(8, 10)),
			CostUSD: 0.25, CostKnown: true,
			Subject: issue(7, "Handle an empty list"),
		},
		{
			ID: "run-late", Status: "success", Start: at(14, 0), End: ending(at(14, 20)),
			CostUSD: 0.5, CostKnown: true,
			Subject: issue(7, "Handle an empty list"),
		},
		{
			ID: "run-issue-five", Status: "success", Start: at(9, 0), End: ending(at(9, 5)),
			CostUSD: 0.125, CostKnown: true,
			Subject: issue(5, "An issue and a pull share the number"),
		},
		{
			ID: "run-pull-five", Status: "success", Start: at(9, 30), End: ending(at(9, 35)),
			CostUSD: 0.125, CostKnown: true,
			Subject: pull(5, "chore: a pull of the same number", "chore/unrelated"),
		},
		{
			ID: "run-other-repo", Status: "success", Start: at(10, 0), End: ending(at(10, 15)),
			CostUSD: 0.25, CostKnown: true,
			Subject: forge.Subject{Repo: "apps/other", Number: 3, Kind: forge.KindIssue, Title: "Another repository"},
		},
	}

	groups := Build(testBase, runs)
	if len(groups) != 2 {
		t.Fatalf("Build returned %d repo groups, want 2: %+v", len(groups), groups)
	}

	var main *RepoGroup
	var other *RepoGroup
	for i := range groups {
		switch groups[i].Repo {
		case testRepo:
			main = &groups[i]
		case "apps/other":
			other = &groups[i]
		}
	}
	if main == nil || other == nil {
		t.Fatalf("groups = %+v, want one for %q and one for \"apps/other\"", groups, testRepo)
	}

	if want := testBase + "/apps/other"; other.URL != want {
		t.Errorf("other repo URL = %q, want %q", other.URL, want)
	}
	if len(other.Items) != 1 || other.Items[0].Subject.Number != 3 {
		t.Fatalf("other repo items = %s, want only issue 3", labels(other.Items))
	}

	// Issue 5 and pull 5 stay apart, and issue 7's two runs are one item whose
	// runs read newest first.
	if len(main.Items) != 3 {
		t.Fatalf("repo has %d items, want 3 (issue 5, pull 5, issue 7): %s", len(main.Items), labels(main.Items))
	}
	var seven *Item
	var issueFive, pullFive *Item
	for i := range main.Items {
		it := &main.Items[i]
		switch {
		case it.Subject.Number == 7:
			seven = it
		case it.Subject.Number == 5 && it.Subject.Kind == forge.KindIssue:
			issueFive = it
		case it.Subject.Number == 5 && it.Subject.Kind == forge.KindPull:
			pullFive = it
		}
	}
	if seven == nil || issueFive == nil || pullFive == nil {
		t.Fatalf("repo items = %s, want issue 5, pull 5 and issue 7", labels(main.Items))
	}
	if len(seven.Runs) != 2 {
		t.Fatalf("issue 7 has %d runs, want 2", len(seven.Runs))
	}
	if seven.Runs[0].ID != "run-late" || seven.Runs[1].ID != "run-early" {
		t.Errorf("issue 7 runs = %s, want run-late then run-early (newest first)", runIDs(seven.Runs))
	}
	// The item describes the thread, and it takes the description from the
	// newest run that carries one.
	if seven.Subject.Title != "Handle an empty list" {
		t.Errorf("issue 7 title = %q, want %q", seven.Subject.Title, "Handle an empty list")
	}
	if want := 1.0; main.CostUSD != want {
		t.Errorf("repo cost = %v, want %v", main.CostUSD, want)
	}
}

// TestBuildCreatesAParentForAPullRequestWhoseIssueHasNoRuns covers the case
// where the issue a pull references was never worked on — the agent coded the
// pull and nobody ran anything on the issue. The pull still belongs under the
// issue, so the issue appears as an item with no runs of its own and its URL
// set, rather than the pull being dropped to the top level or thrown away.
func TestBuildCreatesAParentForAPullRequestWhoseIssueHasNoRuns(t *testing.T) {
	runs := []Run{
		{
			ID: "run-pr", Status: "success", Start: at(13, 0), End: ending(at(13, 45)),
			CostUSD: 0.5, CostKnown: true,
			Subject: pull(265, "feat(#264): search the word list", "feat/264-word-list-search"),
		},
	}

	groups := Build(testBase, runs)
	if len(groups) != 1 {
		t.Fatalf("Build returned %d repo groups, want 1: %+v", len(groups), groups)
	}
	items := groups[0].Items
	if len(items) != 1 {
		t.Fatalf("repo has %d items, want the one placeholder issue: %s", len(items), labels(items))
	}
	parent := items[0]
	if parent.Subject.Number != 264 || parent.Subject.Kind != forge.KindIssue {
		t.Fatalf("top-level item = %s, want issue 264", label(parent))
	}
	if want := testBase + "/" + testRepo + "/issues/264"; parent.URL != want {
		t.Errorf("placeholder issue URL = %q, want %q — it has no runs but it has a page", parent.URL, want)
	}
	if len(parent.Runs) != 0 {
		t.Errorf("placeholder issue carries runs: %s", runIDs(parent.Runs))
	}
	if parent.Duration != 0 {
		t.Errorf("placeholder issue duration = %v, want 0", parent.Duration)
	}
	if parent.CostUSD != 0 {
		t.Errorf("placeholder issue cost = %v, want 0", parent.CostUSD)
	}
	// Nothing about an item with no runs is unmeasured, so its empty cost reads
	// as known rather than as a run whose price was lost.
	if !parent.CostKnown {
		t.Error("placeholder issue cost is unknown, want known")
	}
	if len(parent.PRs) != 1 || parent.PRs[0].Subject.Number != 265 {
		t.Fatalf("placeholder issue pulls = %s, want pull 265", labels(parent.PRs))
	}

	// The pull's cost is the repository's: the placeholder contributes nothing.
	if want := 0.5; groups[0].CostUSD != want {
		t.Errorf("repo cost = %v, want %v", groups[0].CostUSD, want)
	}
}

// TestBuildSortsItemsNewestFirst is how the list is read: the work that most
// recently happened is at the top, both among a repository's items and among the
// pulls nested under one of them.
func TestBuildSortsItemsNewestFirst(t *testing.T) {
	runs := []Run{
		{
			ID: "run-stale", Status: "success", Start: at(8, 0), End: ending(at(8, 5)),
			CostKnown: true, Subject: issue(1, "The oldest thread"),
		},
		{
			ID: "run-mid", Status: "success", Start: at(10, 0), End: ending(at(10, 5)),
			CostKnown: true, Subject: pull(3, "chore: an unreferenced pull", "chore/unrelated"),
		},
		{
			ID: "run-recent", Status: "success", Start: at(12, 0), End: ending(at(12, 5)),
			CostKnown: true, Subject: issue(2, "The newest thread"),
		},
	}

	groups := Build(testBase, runs)
	if len(groups) != 1 {
		t.Fatalf("Build returned %d repo groups, want 1: %+v", len(groups), groups)
	}
	items := groups[0].Items
	if len(items) != 3 {
		t.Fatalf("repo has %d items, want 3: %s", len(items), labels(items))
	}
	want := []int{2, 3, 1}
	for i, n := range want {
		if items[i].Subject.Number != n {
			t.Errorf("item %d = %s, want %s (newest activity first)", i, label(items[i]), strconv.Itoa(n))
		}
	}
}

// TestBuildSortsARunslessParentByItsPullsActivity: an issue with no runs of its
// own is a row of the list because of the pull nested in it, so it belongs where
// that pull's activity puts it. Sorting it by its own (absent) activity would
// bury a thread somebody just pushed to at the bottom of the repository.
func TestBuildSortsARunslessParentByItsPullsActivity(t *testing.T) {
	runs := []Run{
		{
			ID: "run-stale", Status: "success", Start: at(8, 0), End: ending(at(8, 5)),
			CostKnown: true, Subject: issue(1, "The oldest thread"),
		},
		{
			ID: "run-mid", Status: "success", Start: at(10, 0), End: ending(at(10, 5)),
			CostKnown: true, Subject: pull(3, "chore: an unreferenced pull", "chore/unrelated"),
		},
		{
			ID: "run-recent", Status: "success", Start: at(12, 0), End: ending(at(12, 5)),
			CostKnown: true, Subject: issue(2, "The newest thread"),
		},
		{
			// The pull that makes issue 9 a row, and the only activity it has.
			ID: "run-nested", Status: "success", Start: at(11, 0), End: ending(at(11, 0)),
			CostKnown: true, Subject: pull(20, "feat(#9): a pull on an unrun issue", "feat/9-x"),
		},
	}

	groups := Build(testBase, runs)
	if len(groups) != 1 {
		t.Fatalf("Build returned %d repo groups, want 1: %+v", len(groups), groups)
	}
	items := groups[0].Items
	if len(items) != 4 {
		t.Fatalf("repo has %d items, want 4: %s", len(items), labels(items))
	}
	want := []int{2, 9, 3, 1}
	for i, n := range want {
		if items[i].Subject.Number != n {
			t.Errorf("item %d = %s, want %s", i, label(items[i]), strconv.Itoa(n))
		}
	}
	// Reading it as ordering only: the placeholder's totals are still its own,
	// which is to say empty.
	if items[1].CostUSD != 0 || items[1].Duration != 0 || len(items[1].Runs) != 0 {
		t.Errorf("placeholder issue 9 = %+v, want no runs, no duration, no cost of its own", items[1])
	}
}

// TestBuildSortsNestedPullRequests applies the same order inside an item: the
// pull that most recently saw activity is the one an admin is asked to look at.
func TestBuildSortsNestedPullRequests(t *testing.T) {
	runs := []Run{
		{
			ID: "run-issue", Status: "success", Start: at(9, 0), End: ending(at(9, 5)),
			CostKnown: true, Subject: issue(1, "Thread with two pulls"),
		},
		{
			ID: "run-pr-old", Status: "success", Start: at(9, 30), End: ending(at(9, 40)),
			CostKnown: true, Subject: pull(10, "feat(#1): first cut", "feat/1-first-cut"),
		},
		{
			ID: "run-pr-new", Status: "success", Start: at(11, 0), End: ending(at(11, 40)),
			CostKnown: true, Subject: pull(11, "feat(#1): second cut", "feat/1-second-cut"),
		},
	}

	groups := Build(testBase, runs)
	if len(groups) != 1 || len(groups[0].Items) != 1 {
		t.Fatalf("Build = %+v, want one repo with one item", groups)
	}
	prs := groups[0].Items[0].PRs
	if len(prs) != 2 {
		t.Fatalf("issue has %d nested pulls, want 2: %s", len(prs), labels(prs))
	}
	if prs[0].Subject.Number != 11 || prs[1].Subject.Number != 10 {
		t.Errorf("nested pulls = %s, want pull 11 then pull 10", labels(prs))
	}
	// The issue's totals stay its own even with two pulls nested in it.
	if want := 5 * time.Minute; groups[0].Items[0].Duration != want {
		t.Errorf("issue duration = %v, want %v", groups[0].Items[0].Duration, want)
	}
}

// TestBuildSortsRepoGroupsByActivity puts the repository with the most recent
// work first, so the two repos of a multi-repo install do not swap places
// between two calls because nothing broke the tie.
func TestBuildSortsRepoGroupsByActivity(t *testing.T) {
	runs := []Run{
		{
			ID: "run-quiet", Status: "success", Start: at(8, 0), End: ending(at(8, 5)),
			CostKnown: true,
			Subject:   forge.Subject{Repo: "apps/quiet", Number: 1, Kind: forge.KindIssue, Title: "Quiet repo"},
		},
		{
			ID: "run-busy", Status: "success", Start: at(18, 0), End: ending(at(18, 5)),
			CostKnown: true,
			Subject:   forge.Subject{Repo: "apps/busy", Number: 1, Kind: forge.KindIssue, Title: "Busy repo"},
		},
	}

	groups := Build(testBase, runs)
	if len(groups) != 2 {
		t.Fatalf("Build returned %d repo groups, want 2: %+v", len(groups), groups)
	}
	if groups[0].Repo != "apps/busy" || groups[1].Repo != "apps/quiet" {
		t.Errorf("repo groups = %q, %q, want apps/busy then apps/quiet", groups[0].Repo, groups[1].Repo)
	}
}

// TestBuildDuration adds up the time the finished runs took. A run with no end
// is still going — it has no finished duration, and counting the time since it
// started would make a long-running job look like a finished one.
func TestBuildDuration(t *testing.T) {
	runs := []Run{
		{
			ID: "run-ten", Status: "success", Start: at(8, 0), End: ending(at(8, 10)),
			CostKnown: true, Subject: issue(7, "Ten minutes"),
		},
		{
			ID: "run-five", Status: "failure", Start: at(9, 0), End: ending(at(9, 5)),
			CostKnown: true, Subject: issue(7, "Five minutes"),
		},
		{
			ID: "run-running", Status: "running", Start: at(10, 0),
			CostKnown: true, Subject: issue(7, "Still going"),
		},
		{
			// Bad data from a hub that ended a run before it started: it adds
			// nothing rather than subtracting from the total.
			ID: "run-backwards", Status: "success", Start: at(12, 0), End: ending(at(11, 0)),
			CostKnown: true, Subject: issue(7, "Ends before it starts"),
		},
	}

	groups := Build(testBase, runs)
	if len(groups) != 1 || len(groups[0].Items) != 1 {
		t.Fatalf("Build = %+v, want one repo with one item", groups)
	}
	if want := 15 * time.Minute; groups[0].Items[0].Duration != want {
		t.Errorf("duration = %v, want %v (finished runs only, no negative terms)", groups[0].Items[0].Duration, want)
	}
}

// TestBuildCostKnown is ruling R11: AInsel's token metrics only cover the last
// seven days, so an older run's cost is unknown rather than zero. An item's cost
// is known only when every one of its runs' costs is, an unknown run adds
// nothing to the sum, and the flag — not the number — is what tells the UI to
// draw "—" instead of a figure that was never measured.
func TestBuildCostKnown(t *testing.T) {
	runs := []Run{
		{
			ID: "run-known", Status: "success", Start: at(8, 0), End: ending(at(8, 10)),
			CostUSD: 0.5, CostKnown: true,
			Subject: issue(7, "Priced run"),
		},
		{
			// An unpriced run that still carries a figure: the figure means
			// nothing and must not reach the total.
			ID: "run-unknown", Status: "success", Start: at(9, 0), End: ending(at(9, 10)),
			CostUSD: 9, CostKnown: false,
			Subject: issue(7, "Run outside the metrics window"),
		},
		{
			ID: "run-other-repo", Status: "success", Start: at(10, 0), End: ending(at(10, 10)),
			CostUSD: 0.25, CostKnown: true,
			Subject: forge.Subject{Repo: "apps/priced", Number: 3, Kind: forge.KindIssue, Title: "All runs priced"},
		},
	}

	groups := Build(testBase, runs)
	if len(groups) != 2 {
		t.Fatalf("Build returned %d repo groups, want 2: %+v", len(groups), groups)
	}

	var partial, priced *RepoGroup
	for i := range groups {
		switch groups[i].Repo {
		case testRepo:
			partial = &groups[i]
		case "apps/priced":
			priced = &groups[i]
		}
	}
	if partial == nil || priced == nil {
		t.Fatalf("groups = %+v, want one for %q and one for \"apps/priced\"", groups, testRepo)
	}

	if len(partial.Items) != 1 {
		t.Fatalf("repo has %d items, want 1: %s", len(partial.Items), labels(partial.Items))
	}
	item := partial.Items[0]
	if item.CostKnown {
		t.Error("item cost is known, want unknown (one of its runs is unpriced)")
	}
	if want := 0.5; item.CostUSD != want {
		t.Errorf("item cost = %v, want %v — the unpriced run contributes nothing", item.CostUSD, want)
	}
	if partial.CostKnown {
		t.Error("repo cost is known, want unknown (one of its runs is unpriced)")
	}
	if want := 0.5; partial.CostUSD != want {
		t.Errorf("repo cost = %v, want %v", partial.CostUSD, want)
	}

	if !priced.CostKnown {
		t.Error("priced repo cost is unknown, want known")
	}
	if len(priced.Items) != 1 || !priced.Items[0].CostKnown {
		t.Errorf("priced repo item = %+v, want a known cost", priced.Items)
	}
}

// TestBuildLinksFromTheMostRecentlyDescribedRun pins which of a pull's runs
// decides its issue. A pull's subject is what the last run saw — the title and
// branch change as the pull is edited — so the newest run that says anything is
// the one read, and a newer run that says nothing leaves the older description
// in place rather than losing the link.
func TestBuildLinksFromTheMostRecentlyDescribedRun(t *testing.T) {
	t.Run("newest description answers", func(t *testing.T) {
		runs := []Run{
			{
				ID: "run-old", Status: "success", Start: at(8, 0), End: ending(at(8, 5)),
				CostKnown: true, Subject: pull(265, "wip", ""),
			},
			{
				ID: "run-new", Status: "success", Start: at(12, 0), End: ending(at(12, 5)),
				CostKnown: true, Subject: pull(265, "feat(#264): search the word list", "feat/264-word-list-search"),
			},
			{
				ID: "run-issue", Status: "success", Start: at(7, 0), End: ending(at(7, 5)),
				CostKnown: true, Subject: issue(264, "Search the word list"),
			},
		}

		groups := Build(testBase, runs)
		if len(groups) != 1 || len(groups[0].Items) != 1 {
			t.Fatalf("Build = %+v, want one repo with one item", groups)
		}
		item := groups[0].Items[0]
		if item.Subject.Number != 264 {
			t.Fatalf("top-level item = %s, want issue 264", label(item))
		}
		if len(item.PRs) != 1 || item.PRs[0].Subject.Number != 265 {
			t.Fatalf("nested pulls = %s, want pull 265", labels(item.PRs))
		}
		if want := "feat(#264): search the word list"; item.PRs[0].Subject.Title != want {
			t.Errorf("pull title = %q, want %q", item.PRs[0].Subject.Title, want)
		}
	})

	t.Run("a silent newest run leaves the description alone", func(t *testing.T) {
		runs := []Run{
			{
				ID: "run-described", Status: "success", Start: at(8, 0), End: ending(at(8, 5)),
				CostKnown: true,
				Subject:   pull(265, "feat(#264): search the word list", "feat/264-word-list-search"),
			},
			{
				// A run whose event carried no title, branch or body at
				// all — it does not get to say the pull is unlinked.
				ID: "run-blank", Status: "success", Start: at(12, 0), End: ending(at(12, 5)),
				CostKnown: true,
				Subject:   forge.Subject{Repo: testRepo, Number: 265, Kind: forge.KindPull},
			},
		}

		groups := Build(testBase, runs)
		if len(groups) != 1 || len(groups[0].Items) != 1 {
			t.Fatalf("Build = %+v, want one repo with one item", groups)
		}
		item := groups[0].Items[0]
		if item.Subject.Number != 264 {
			t.Fatalf("top-level item = %s, want the placeholder issue 264", label(item))
		}
		if len(item.PRs) != 1 || item.PRs[0].Subject.Number != 265 {
			t.Fatalf("nested pulls = %s, want pull 265", labels(item.PRs))
		}
	})

	t.Run("the newest description wins over a stale one", func(t *testing.T) {
		// The pull was retitled away from its issue: what the agent last saw
		// is what the plugin links, not what an earlier run saw.
		runs := []Run{
			{
				ID: "run-old", Status: "success", Start: at(8, 0), End: ending(at(8, 5)),
				CostKnown: true,
				Subject:   pull(265, "feat(#264): search the word list", "feat/264-word-list-search"),
			},
			{
				ID: "run-new", Status: "success", Start: at(12, 0), End: ending(at(12, 5)),
				CostKnown: true,
				Subject:   pull(265, "chore: bump the dependencies", "renovate/deps"),
			},
		}

		groups := Build(testBase, runs)
		if len(groups) != 1 {
			t.Fatalf("Build returned %d repo groups, want 1: %+v", len(groups), groups)
		}
		items := groups[0].Items
		if len(items) != 1 {
			t.Fatalf("repo has %d items, want the pull on its own: %s", len(items), labels(items))
		}
		if items[0].Subject.Number != 265 || items[0].Subject.Kind != forge.KindPull {
			t.Errorf("top-level item = %s, want pull 265 at the top level", label(items[0]))
		}
	})
}

// TestBuildWithoutAForge: an install that was never pointed at a Forgejo has no
// URLs to hand out. The work is still grouped and totalled — it is the runs
// AInsel recorded — but every URL comes back empty, which the UI draws as plain
// text rather than a link to nowhere.
func TestBuildWithoutAForge(t *testing.T) {
	runs := []Run{
		{
			ID: "run-issue", Status: "success", Start: at(9, 0), End: ending(at(9, 5)),
			CostKnown: true, Subject: issue(264, "Search the word list"),
		},
		{
			ID: "run-pr", Status: "success", Start: at(10, 0), End: ending(at(10, 5)),
			CostKnown: true,
			Subject:   pull(265, "feat(#264): search the word list", "feat/264-word-list-search"),
		},
	}

	groups := Build("", runs)
	if len(groups) != 1 {
		t.Fatalf("Build returned %d repo groups, want 1: %+v", len(groups), groups)
	}
	g := groups[0]
	if g.Repo != testRepo {
		t.Errorf("repo = %q, want %q — the name is still worth showing", g.Repo, testRepo)
	}
	if g.URL != "" {
		t.Errorf("repo URL = %q, want empty", g.URL)
	}
	if len(g.Items) != 1 || len(g.Items[0].PRs) != 1 {
		t.Fatalf("items = %s, want issue 264 with pull 265 nested", labels(g.Items))
	}
	if got := g.Items[0].URL; got != "" {
		t.Errorf("issue URL = %q, want empty", got)
	}
	if got := g.Items[0].PRs[0].URL; got != "" {
		t.Errorf("pull URL = %q, want empty", got)
	}
}

// TestBuildWithNoRuns: no runs, no work to show. An empty answer rather than a
// group with an empty name, which the console would render as a heading over
// nothing.
func TestBuildWithNoRuns(t *testing.T) {
	if got := Build(testBase, nil); len(got) != 0 {
		t.Errorf("Build(nil) = %+v, want no groups", got)
	}
	if got := Build(testBase, []Run{}); len(got) != 0 {
		t.Errorf("Build(empty) = %+v, want no groups", got)
	}
}

// TestActive picks out the runs that are still work in progress, which is what
// makes an issue show as being worked on and what a trigger's "is anything
// running?" answer is built from. AInsel's statuses are queued, running, success,
// failure and timeout — there is no pending — so queued and running are kept and
// everything finished or given up on is dropped. Newest first, because the run an
// admin is waiting on is the one that started last.
func TestActive(t *testing.T) {
	runs := []Run{
		{ID: "queued-old", Status: "queued", Start: at(8, 0)},
		{ID: "succeeded", Status: "success", Start: at(9, 0), End: ending(at(9, 5))},
		{ID: "running-newest", Status: "running", Start: at(12, 0)},
		{ID: "failed", Status: "failure", Start: at(11, 0), End: ending(at(11, 5))},
		{ID: "running-older", Status: "running", Start: at(10, 0)},
		{ID: "timed-out", Status: "timeout", Start: at(13, 0), End: ending(at(13, 30))},
		{
			// Active sorts runs, it does not judge their subjects: dropping
			// a run about nothing is Build's job.
			ID: "queued-numberless", Status: "queued", Start: at(7, 0),
			Subject: forge.Subject{Repo: testRepo, Kind: forge.KindIssue},
		},
	}

	got := Active(runs)
	want := []string{"running-newest", "running-older", "queued-old", "queued-numberless"}
	if len(got) != len(want) {
		t.Fatalf("Active returned %s, want %v", runIDs(got), want)
	}
	for i, id := range want {
		if got[i].ID != id {
			t.Errorf("Active()[%d] = %q, want %q", i, got[i].ID, id)
		}
	}

	// The caller's slice keeps its own order: Active sorted a copy.
	if runs[0].ID != "queued-old" || runs[1].ID != "succeeded" {
		t.Errorf("Active reordered the input: %s", runIDs(runs))
	}
}

// TestActiveWithNothingRunning answers empty rather than nil-ish so a caller can
// read it as "nothing is going on" without a special case.
func TestActiveWithNothingRunning(t *testing.T) {
	runs := []Run{
		{ID: "succeeded", Status: "success", Start: at(9, 0), End: ending(at(9, 5))},
		{ID: "failed", Status: "failure", Start: at(10, 0), End: ending(at(10, 5))},
	}
	if got := Active(runs); len(got) != 0 {
		t.Errorf("Active(%s) = %s, want nothing", runIDs(runs), runIDs(got))
	}
}

// TestEmptyListsMarshalAsEmptyArrays: the console draws these lists with a map, and
// null is the one value a map cannot take — so an item with no runs, or no nested
// pulls, says so with [] rather than by leaving the key out or sending null.
func TestEmptyListsMarshalAsEmptyArrays(t *testing.T) {
	groups := Build(testBase, []Run{
		{
			ID: "run-pr", Status: "success", Start: at(13, 0), End: ending(at(13, 30)),
			CostKnown: true,
			Subject:   pull(265, "feat(#264): search the word list", "feat/264-word-list-search"),
		},
	})
	if len(groups) != 1 || len(groups[0].Items) != 1 || len(groups[0].Items[0].PRs) != 1 {
		t.Fatalf("Build = %+v, want one repo with one item holding one pull", groups)
	}

	parent := groups[0].Items[0]
	if b := marshal(t, parent); !strings.Contains(b, `"runs":[]`) {
		t.Errorf("placeholder issue JSON = %s, want an empty \"runs\" list", b)
	}
	if b := marshal(t, parent.PRs[0]); !strings.Contains(b, `"prs":[]`) {
		t.Errorf("pull request JSON = %s, want an empty \"prs\" list", b)
	}
}

// marshal renders a value for a JSON assertion.
func marshal(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal %T: %v", v, err)
	}
	return string(b)
}

// TestJSONFieldNames pins the names the console and the MCP tools read these
// fields by. The Go names are the brief's; the JSON ones are the API's, and a
// rename here silently breaks a UI that is deployed separately from this build.
func TestJSONFieldNames(t *testing.T) {
	item := Item{
		Subject:      issue(264, "Search the word list"),
		URL:          testBase + "/" + testRepo + "/issues/264",
		Runs:         []Run{{ID: "run-1", Start: at(9, 0), End: ending(at(9, 5))}},
		Duration:     5 * time.Minute,
		CostUSD:      0.5,
		CostKnown:    true,
		LastActivity: at(9, 5),
		PRs:          []Item{{Subject: pull(265, "feat(#264): x", "feat/264-x")}},
	}
	for _, key := range []string{"subject", "url", "runs", "duration", "costUsd", "costKnown", "lastActivity", "prs"} {
		if !hasKey(t, item, key) {
			t.Errorf("item JSON has no %q key", key)
		}
	}

	run := Run{
		ID: "run-1", Agent: "coder", Role: "implementer", Status: "success", Connector: "forgejo",
		Start: at(9, 0), End: ending(at(9, 5)), CostUSD: 0.5, CostKnown: true,
		Subject: issue(264, "Search the word list"),
	}
	for _, key := range []string{"id", "agent", "role", "status", "connector", "start", "end", "costUsd", "costKnown", "subject"} {
		if !hasKey(t, run, key) {
			t.Errorf("run JSON has no %q key", key)
		}
	}

	group := RepoGroup{
		Repo: testRepo, URL: testBase + "/" + testRepo,
		Items: []Item{item}, CostUSD: 0.5, CostKnown: true,
	}
	for _, key := range []string{"repo", "url", "items", "costUsd", "costKnown"} {
		if !hasKey(t, group, key) {
			t.Errorf("repo group JSON has no %q key", key)
		}
	}
}

// hasKey reports whether a value marshals to a JSON object carrying key.
func hasKey(t *testing.T, v any, key string) bool {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal([]byte(marshal(t, v)), &doc); err != nil {
		t.Fatalf("unmarshal %T: %v", v, err)
	}
	_, ok := doc[key]
	return ok
}

// labels names a slice of items for a failure message.
func labels(items []Item) string {
	parts := make([]string, len(items))
	for i, it := range items {
		parts[i] = label(it)
	}
	return strings.Join(parts, ", ")
}

// runIDs names a slice of runs for a failure message.
func runIDs(runs []Run) string {
	parts := make([]string, len(runs))
	for i, r := range runs {
		parts[i] = r.ID
	}
	return strings.Join(parts, ", ")
}
