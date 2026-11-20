package work

import (
	"sort"
	"time"

	"github.com/ainsel-project/ainsel-plugin-forgejo/internal/forge"
)

// The two statuses that mean a run has not finished. AInsel's invocation
// statuses are queued, running, success, failure and timeout — there is no
// pending — so these are the only two Active keeps.
const (
	StatusQueued  = "queued"
	StatusRunning = "running"
)

// Run is one AInsel run as the work list needs it: which agent and role did it,
// how it ended, what it cost, and the issue or pull request it was about. It is
// the hub's invocation reduced to what the work is on the forge — the run's own
// error text and task state stay with the invocation, because the work list
// answers "what is being done where", not "why did this run fail".
type Run struct {
	// ID is the AInsel invocation id, and the handle back to the run.
	ID string `json:"id"`

	// Agent is the id of the agent that ran (the hub's metadata.name, what an install
	// keys roles by), AgentName its display name — what a page shows — and Role the
	// persona it ran as. AgentName is the id again when the hub would not name it.
	Agent     string `json:"agent"`
	AgentName string `json:"agentName,omitempty"`
	Role      string `json:"role"`

	// Status is the invocation status: queued, running, success, failure or
	// timeout.
	Status string `json:"status"`

	// Connector is the connector whose event started the run — which Forgejo,
	// when an install talks to more than one.
	Connector string `json:"connector"`

	Start time.Time  `json:"start"`
	End   *time.Time `json:"end,omitempty"` // nil while the run has not finished

	// CostUSD is what the run cost, and CostKnown says whether that number was
	// measured. AInsel's token metrics only reach back seven days, so an older
	// run's cost is unknown rather than zero: the flag is what makes a UI draw
	// "—" instead of a figure nobody measured.
	CostUSD   float64 `json:"costUsd"`
	CostKnown bool    `json:"costKnown"`

	// Subject is the issue or pull request the run was about — the reason the
	// run appears in the work list at all.
	Subject forge.Subject `json:"subject"`
}

// Item is one issue or pull request the agents worked on: the runs about it, what
// they add up to, and the pull requests that belong under it.
//
// An item's totals are its own runs only. A pull request nested in an issue is not
// added into the issue's duration or cost — the two are separate work with
// separate runs, and the repository total is where everything is added together,
// once.
type Item struct {
	// Subject is the issue or pull request, described by the most recent run
	// that had anything to say about it.
	Subject forge.Subject `json:"subject"`

	// URL is its page on the forge, empty when the install has no Forgejo
	// address to build one from.
	URL string `json:"url"`

	// Runs are the runs about this subject, newest first.
	Runs []Run `json:"runs"`

	// Duration is the time the finished runs took, summed. A run that has not
	// ended has no finished duration to contribute.
	Duration time.Duration `json:"duration"`

	CostUSD   float64 `json:"costUsd"`
	CostKnown bool    `json:"costKnown"`

	// LastActivity is the newest thing that happened here: the latest of the
	// item's own runs, or — for an item that exists only to hold pull
	// requests — the latest activity of one of them, so a thread someone just
	// pushed to is not sorted as if nothing had happened to it.
	LastActivity time.Time `json:"lastActivity"`

	// PRs are the pull requests that reference this issue, newest first. It is
	// empty on a pull request, which nests one level at most: a link names an
	// issue, so a pull request is never filed under another one.
	PRs []Item `json:"prs"`
}

// RepoGroup is one repository's work: the issues and pull requests AInsel ran on,
// and the cost of every run in the repository counted exactly once — including the
// runs of pull requests nested under an issue.
type RepoGroup struct {
	// Repo is the repository's full name, "owner/repo".
	Repo string `json:"repo"`

	// URL is the repository's page on the forge.
	URL string `json:"url"`

	// Items are the top-level threads, newest activity first: every issue, and
	// every pull request that references no issue.
	Items []Item `json:"items"`

	CostUSD   float64 `json:"costUsd"`
	CostKnown bool    `json:"costKnown"`
}

// Build groups the runs AInsel recorded into the work they add up to: one item per
// issue or pull request, pull requests nested under the issue they reference,
// items gathered per repository and sorted by the most recent activity first.
//
// It reads only what is in the runs — no forge is contacted, which is how the
// plugin keeps to the work AInsel actually touched — so the answer is whatever the
// hub said the last time it was asked. A run whose subject has no number is about
// no page on the forge and is dropped; an empty base leaves every URL empty rather
// than inventing one.
//
// Every link in the answer is built from the one base — the shape for a caller
// with one Forgejo. A caller whose installs name their own addresses uses
// BuildWithBase, which asks per run instead.
func Build(base string, runs []Run) []RepoGroup {
	return BuildWithBase(runs, func(Run) string { return base })
}

// BuildWithBase is Build with the base resolved per run: baseFor names the
// Forgejo address a run's links are built from — the install whose connector
// received the event, so a plugin installed on several Forgejos links each
// repository on the one it lives on. A repository's group is built from the
// address of its first run: a repository lives on one forge, and the grouping
// key is its name, so the runs of a group share an address in every answer the
// plugin can actually build.
func BuildWithBase(runs []Run, baseFor func(Run) string) []RepoGroup {
	repos := map[string]*repoAcc{}
	for _, r := range runs {
		// Issue 0 and pull 0 are pages Forgejo does not serve, so a run
		// carrying one is about nothing a reader could open.
		if r.Subject.Number == 0 {
			continue
		}
		k := subjectKeyOf(r.Subject)
		acc := repos[k.repo]
		if acc == nil {
			acc = &repoAcc{runs: map[subjectKey][]Run{}}
			repos[k.repo] = acc
		}
		acc.runs[k] = append(acc.runs[k], r)
		acc.all = append(acc.all, r)
	}

	groups := make([]RepoGroup, 0, len(repos))
	for repo, acc := range repos {
		groups = append(groups, acc.build(baseFor, repo))
	}

	// The repository with the most recent work first, so an admin with several
	// sees the one that is moving. The repository name breaks a tie: two
	// identical answers, not two lists in two orders.
	sort.Slice(groups, func(i, j int) bool {
		a, b := newestItemActivity(groups[i].Items), newestItemActivity(groups[j].Items)
		if !a.Equal(b) {
			return a.After(b)
		}
		return groups[i].Repo < groups[j].Repo
	})
	return groups
}

// Active returns the runs that are still work in progress — queued or running —
// newest first, which is the order a reader wants them in: the run that started
// last is the one being waited on. It copies and sorts rather than reordering the
// caller's slice, and it filters on status alone: whether a run's subject is
// showable is Build's decision, not this one's.
func Active(runs []Run) []Run {
	out := make([]Run, 0, len(runs))
	for _, r := range runs {
		if r.Status == StatusQueued || r.Status == StatusRunning {
			out = append(out, r)
		}
	}
	// Start, not end: an active run has no end, and one that carries a
	// leftover end anyway is still queued or running as far as AInsel says.
	sort.SliceStable(out, func(i, j int) bool { return out[i].Start.After(out[j].Start) })
	return out
}

// subjectKey is what a work item is: a repository, a number inside it, and what
// kind of thing that number names. The kind belongs to the key because Forgejo
// numbers issues and pull requests separately — issue 5 and pull 5 are two threads
// that happen to share a number, not one thread seen twice.
type subjectKey struct {
	repo   string
	number int
	kind   forge.Kind
}

func subjectKeyOf(s forge.Subject) subjectKey {
	return subjectKey{repo: s.Repo, number: s.Number, kind: s.Kind}
}

// repoAcc collects one repository's runs while they are being grouped: the runs per
// subject, and all of them together so the repository total can add each run once
// whatever nesting the items end up in.
type repoAcc struct {
	runs map[subjectKey][]Run
	all  []Run
}

// build turns one repository's collected runs into a RepoGroup. The forge
// address is the one baseFor names for the repository's first run — the runs of
// one repository are runs of one forge.
func (a *repoAcc) build(baseFor func(Run) string, repo string) RepoGroup {
	base := ""
	if len(a.all) > 0 {
		base = baseFor(a.all[0])
	}
	items := make(map[subjectKey]Item, len(a.runs))
	for k, runs := range a.runs {
		items[k] = newItem(base, k, runs)
	}

	// The pull requests are collected before any of them is filed under an
	// issue, because the filing pass adds items of its own and a map read while
	// it is written may or may not hand back what was just added.
	var pulls []Item
	for k, it := range items {
		if k.kind == forge.KindPull {
			pulls = append(pulls, it)
		}
	}

	// nested marks the pulls that found their issue, so they are not listed a
	// second time at the top level.
	nested := make(map[subjectKey]bool, len(pulls))
	for _, pr := range pulls {
		number, ok := LinkedIssue(pr.Subject.Title, pr.Subject.Branch, pr.Subject.Body)
		if !ok {
			continue
		}
		key := subjectKey{repo: repo, number: number, kind: forge.KindIssue}
		parent, ok := items[key]
		if !ok {
			// The referenced issue has no runs: nobody ran anything on
			// it, only on the pull that references it. It still gets an
			// item — with a page to link to and no runs of its own —
			// because the alternative is showing the pull as work that
			// belongs to no issue.
			parent = newItem(base, key, nil)
		}
		parent.PRs = append(parent.PRs, pr)
		items[key] = parent
		nested[subjectKeyOf(pr.Subject)] = true
	}

	list := make([]Item, 0, len(items))
	for k, it := range items {
		if nested[k] {
			continue
		}
		it.LastActivity = threadActivity(it)
		sortItems(it.PRs)
		list = append(list, it)
	}
	sortItems(list)

	return RepoGroup{
		Repo:  repo,
		URL:   forge.RepoURL(base, repo),
		Items: list,
		// Every run in the repository, counted once: nesting a pull under an
		// issue must not make its cost appear twice, and the runs of a pull
		// that nests must not disappear from the repository's total.
		CostUSD:   totalCost(a.all),
		CostKnown: allCostKnown(a.all),
	}
}

// newItem makes one item out of the runs about one subject. The runs decide the
// totals; the key decides what the item is, so an item always names the
// repository, number and kind it was grouped under.
func newItem(base string, k subjectKey, runs []Run) Item {
	s := latestSubject(runs)
	s.Repo, s.Number, s.Kind = k.repo, k.number, k.kind

	it := Item{
		Subject:      s,
		Runs:         sortedRuns(runs),
		PRs:          []Item{},
		Duration:     finishedDuration(runs),
		CostUSD:      totalCost(runs),
		CostKnown:    allCostKnown(runs),
		LastActivity: newestRunActivity(runs),
	}
	it.URL = forge.SubjectURL(base, it.Subject)
	return it
}

// latestSubject is the newest run's account of what a subject is: the title,
// branch and body as the last run that saw them reported them. A pull request is
// retitled and retargeted as work goes on, and the newest description is the one
// worth linking from. A run whose event carried nothing does not get to say the
// subject has no title; the run before it still answers. It returns the zero
// Subject when no run described one.
func latestSubject(runs []Run) forge.Subject {
	var best forge.Subject
	var bestAt time.Time
	found := false
	for _, r := range runs {
		if r.Subject.Title == "" && r.Subject.Branch == "" && r.Subject.Body == "" {
			continue
		}
		at := runActivity(r)
		if !found || at.After(bestAt) {
			best, bestAt, found = r.Subject, at, true
		}
	}
	return best
}

// sortedRuns is the item's runs in the order they are read: newest activity
// first. The order is stable, so two runs at the same instant stay in the order
// the hub listed them rather than swapping between calls. The copy is never nil:
// the console maps over these lists, and an empty one is a shorter answer than a
// missing one.
func sortedRuns(runs []Run) []Run {
	out := make([]Run, len(runs))
	copy(out, runs)
	sort.SliceStable(out, func(i, j int) bool {
		return runActivity(out[i]).After(runActivity(out[j]))
	})
	return out
}

// finishedDuration is how long the finished runs took, added together. A run with
// no end is still going and has no finished duration; a run that reports an end
// before its start is bad data and adds nothing rather than subtracting.
func finishedDuration(runs []Run) time.Duration {
	var total time.Duration
	for _, r := range runs {
		if r.End == nil {
			continue
		}
		if d := r.End.Sub(r.Start); d > 0 {
			total += d
		}
	}
	return total
}

// totalCost is the cost of the runs whose cost was measured. An unmeasured run
// adds nothing: its CostUSD is not a zero, it is an absence, and adding absences
// to a bill is how a cheap-looking week turns out to be an unknown one.
func totalCost(runs []Run) float64 {
	var total float64
	for _, r := range runs {
		if !r.CostKnown {
			continue
		}
		total += r.CostUSD
	}
	return total
}

// allCostKnown reports whether every run's cost was measured, which is the only
// way an item's or a repository's cost can be called known: one unmeasured run
// makes the sum unknown. A subject with no runs has nothing unmeasured about it,
// so an item created only to hold a pull request reports its empty cost as known.
func allCostKnown(runs []Run) bool {
	for _, r := range runs {
		if !r.CostKnown {
			return false
		}
	}
	return true
}

// runActivity is when a run counts as having happened: it ended, or, if it has not,
// it started.
func runActivity(r Run) time.Time {
	if r.End != nil {
		return *r.End
	}
	return r.Start
}

// newestRunActivity is the most recent activity among a set of runs, and the zero
// time among none — which is what leaves an item built to hold a pull request
// without activity of its own.
func newestRunActivity(runs []Run) time.Time {
	var newest time.Time
	for _, r := range runs {
		if at := runActivity(r); at.After(newest) {
			newest = at
		}
	}
	return newest
}

// newestItemActivity is when the most recently active of a set of items last saw a
// run — the moment its list, and the repository holding it, is sorted by.
func newestItemActivity(items []Item) time.Time {
	var newest time.Time
	for _, it := range items {
		if it.LastActivity.After(newest) {
			newest = it.LastActivity
		}
	}
	return newest
}

// threadActivity is an item's activity as a reader experiences it: its own runs,
// or the newest of the pull requests nested in it when it has none of its own. It
// is only about ordering — the item's duration and cost stay its own runs' — and it
// is what keeps a thread that was just pushed to from sorting as though nothing
// happened.
func threadActivity(it Item) time.Time {
	newest := it.LastActivity
	if nested := newestItemActivity(it.PRs); nested.After(newest) {
		newest = nested
	}
	return newest
}

// sortItems puts the newest work first. The tie-breaks make the order total: two
// items that last saw a run at the same instant are ordered by number, larger
// first — in a repository the larger number is the newer thread — and then by kind,
// which is what separates an issue from a pull request that share a number.
func sortItems(items []Item) {
	sort.Slice(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if !a.LastActivity.Equal(b.LastActivity) {
			return a.LastActivity.After(b.LastActivity)
		}
		if a.Subject.Number != b.Subject.Number {
			return a.Subject.Number > b.Subject.Number
		}
		return a.Subject.Kind < b.Subject.Kind
	})
}
