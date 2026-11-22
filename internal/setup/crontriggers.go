package setup

// CronTrigger is one scheduled prompt: a hub-managed resource that fires a fixed
// prompt at an agent on a cron schedule, with no connector and no filters — the
// hub synthesises the event itself and delivers the prompt verbatim. The JSON tags
// are the hub cron-triggers API's field names (the shape
// services/hub/internal/api/handlers_cron_triggers.go answers with), so a list
// decodes straight into it and a create posts it as it stands, mirroring Trigger's
// ruling R4. Name is the display name — the hub's ids are generated, so the
// install recognises its own cron triggers by name, and deletes them by the id the
// list carried.
type CronTrigger struct {
	ID       string `json:"id,omitempty"`
	Name     string `json:"name"`
	AgentRef string `json:"agentRef"`
	Schedule string `json:"schedule"`
	Prompt   string `json:"prompt"`
	Enabled  bool   `json:"enabled"`
}

// CronPatrolSchedule is the PR patrol's cadence: every 30 minutes, so a CI
// failure, a review comment or a merge conflict on an open pull is acted on
// within half an hour of appearing. An admin may retune it on the hub's Schedules
// UI — CronMissing matches by name, so the reconcile loop never fights an edited
// schedule.
const CronPatrolSchedule = "*/30 * * * *"

// CronPatrolPrompt is the patrol's whole brief. The hub fires it verbatim with no
// event context, so it names the job, the two-repositories-per-run cap that keeps
// one sweep bounded however many repositories the install spans, the three things
// to look for, and the two rules that keep a 48-fires-a-day loop from becoming
// spam: read before posting, and say nothing when there is nothing to say.
const CronPatrolPrompt = "Scheduled PR patrol. List the open pull requests authored by your own bot account " +
	"(list_repo_pull_requests with your own author, across the install's repositories). Check at most two repositories per run: " +
	"when more than two hold open pulls of yours, work the two whose pulls were updated most recently and leave the rest to the next run. " +
	"For each pull you check, look for three things: " +
	"(1) review comments or a changes-requested review posted after your last comment — apply the feedback and push, per your persona; " +
	"(2) the pull's CI verdict (get_pull_request_status) — on a failing verdict open the workflow run it points at, read the job logs, and either fix and push or post one comment explaining why it is not a code issue; when no workflow run reports on this commit, say so once and never report it as passing; " +
	"(3) a merge conflict with the base branch — rebase or merge, resolve, and push. " +
	"Before posting anything, read the pull's existing comments: if you already reported the same problem and nothing has changed since, post nothing. " +
	"If no pull needs action, end the turn silently — no comment, no mention."

// CronTriggers generates the cron triggers an install owns — exactly one today:
// the developer's scheduled PR patrol, named forgejo-developer-pr-patrol-<slug>
// the way the event triggers are named (R3). Like them it is not stored on the
// install row; it is recognised by name.
//
// It reads the row's patrol, though, because this function is what both the re-apply
// loop and the apply's create path build from: a row written before the patrol was a
// choice says nothing and gets the shipped cadence, and one switched off gets no
// trigger at all. That is the whole of why a patrol an admin turned off in the wizard
// is not re-filed by the next pass — and, with the diff's row baseline, why one they
// retuned on the hub's Schedules UI is not fought either.
func CronTriggers(in Install) []CronTrigger {
	want := PatrolWanted(in.Roles[RoleDeveloper].Patrol)
	if !want.Enabled {
		return nil
	}
	schedule := want.Schedule
	if schedule == "" {
		schedule = CronPatrolSchedule
	}
	return []CronTrigger{
		{
			Name:     CronPatrolName(in.Slug),
			AgentRef: in.Roles[RoleDeveloper].AgentID,
			Schedule: schedule,
			Prompt:   CronPatrolPrompt,
			Enabled:  true,
		},
	}
}

// CronTriggerUpdate is the write one scheduled prompt that already exists takes: the
// fields the install means to move, with a nil meaning "say nothing" rather than "clear".
// The hub's update takes the name, the agent and the prompt on every write, so a caller
// that means to retune a cadence sends the row's own three back — which is also what keeps
// a prompt an admin edited on the hub's Schedules UI from being restored to the shipped one
// by a change to nothing of the sort.
type CronTriggerUpdate struct {
	Name     string
	AgentRef string
	Prompt   string
	Schedule *string
	Enabled  *bool
}

// CronPatrolName names the install's PR-patrol cron trigger,
// forgejo-developer-pr-patrol-<slug>: the event triggers' pattern with "pr-patrol"
// in the <what> slot, so it carries the slug like everything else the install owns
// and the removal flow finds it by the same prefix-and-suffix match.
func CronPatrolName(slug string) string {
	return triggerName(RoleDeveloper, "pr-patrol", slug)
}

// CronMissing returns the wanted cron triggers no existing one covers — by name
// alone. The event triggers compare by (agentRef, connectorRef, filter set)
// because their names are display only; a cron trigger's name is its identity to
// this install: it has no connector to scope by, no filters to compare, and the
// hub gives every create a fresh generated id. Matching by name is what makes the
// loop idempotent — and what lets an admin retune the schedule or the prompt
// without the next pass calling that drift.
func CronMissing(want, existing []CronTrigger) []CronTrigger {
	out := make([]CronTrigger, 0, len(want))
	have := make(map[string]bool, len(existing))
	for _, e := range existing {
		have[e.Name] = true
	}
	for _, w := range want {
		if !have[w.Name] {
			out = append(out, w)
		}
	}
	return out
}
