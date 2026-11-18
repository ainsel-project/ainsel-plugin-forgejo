package setup

import (
	"slices"
	"sort"
)

// Label fields the trigger set excludes on.
const (
	// labelNameField is the field the refiner's exclusion filters: a dotted path
	// AInsel resolves through the issue's array of label objects to their names,
	// which is what a not-contains there has to compare against.
	labelNameField = "issue.labels.name"

	// legacyLabelField is the spelling the live dev triggers carry. Forgejo sends
	// issue.labels as an array of label objects and AInsel's matcher stringifies
	// each element, so a not-contains there never matched a label and excluded
	// nothing — the generator emits labelNameField instead, but Missing still
	// counts the legacy spelling as covering it so setup recognises what is
	// already there instead of duplicating it.
	legacyLabelField = "issue.labels"
)

// Filter is one condition a trigger's events must pass, in the shape AInsel's
// trigger API takes: the field is a dotted path into the webhook payload
// (resolved by AInsel's filter engine), the op is one of its operations — eq,
// neq, contains, not-contains and the set ops among them. Values is the operand
// list for the set ops only; the equality and substring ops here use Value.
type Filter struct {
	Field  string   `json:"field"`
	Op     string   `json:"op"`
	Value  string   `json:"value"`
	Values []string `json:"values,omitempty"`
}

// Trigger is the trigger set's unit, in the shape POST /api/v1/triggers takes:
// agentRef is the agent's ID (metadata.name, e.g. "a-reviewer") and connectorRef the connector's id,
// and the JSON tags are the API's field names. The trigger name is display only — Missing matches triggers without looking at it.
// ID is populated by the hub API when listing triggers.
type Trigger struct {
	ID           string   `json:"id,omitempty"`
	Name         string   `json:"name"`
	AgentRef     string   `json:"agentRef"`
	ConnectorRef string   `json:"connectorRef"`
	Filters      []Filter `json:"filters"`
}

// Triggers generates the trigger set an install needs — exactly the six of the
// spec — named forgejo-<role>-<what>-<slug>:
//
//   - the refiner sees every new and reopened issue of the org unless it
//     carries the exclude label, and answers mentions of its bot;
//   - the reviewer sees every new pull request, and answers mentions of its bot;
//   - the developer answers mentions of its bot.
//
// The mention triggers for refiner and reviewer filter their authors, because a
// bot answering another bot's comment is a conversation nobody asked for; the
// developer's does not, because the other two hand work to it by mentioning it.
//
// Six and only six: the CI-sender trigger the old wizard could add is gone with
// the field it read — a CI sender on Forgejo is an account like any other, and
// it gets a mention of its own if it is meant to reach an agent.
func Triggers(in Install) []Trigger {
	exclude := in.ExcludeLabel
	if exclude == "" {
		exclude = DefaultExcludeLabel
	}
	return []Trigger{
		issueActionTrigger(in, RoleRefiner, "opened", exclude),
		issueActionTrigger(in, RoleRefiner, "reopened", exclude),
		mentionTrigger(in, RoleRefiner),
		pullOpenedTrigger(in),
		mentionTrigger(in, RoleReviewer),
		mentionTrigger(in, RoleDeveloper),
	}
}

// Missing returns the wanted triggers no existing trigger covers — by
// (agentRef, connectorRef, filter set), names ignored: the setup flow re-runs
// against a live instance, and an instance that already has the right trigger
// under an older name has it. Duplicates in want are reported as often as they
// are uncovered; the order is want's order.
func Missing(want, existing []Trigger) []Trigger {
	out := make([]Trigger, 0, len(want))
	used := make([]bool, len(existing))
	for _, w := range want {
		found := false
		for i, e := range existing {
			if !used[i] && sameTrigger(w, e) {
				used[i] = true
				found = true
				break
			}
		}
		if !found {
			out = append(out, w)
		}
	}
	return out
}

// issueActionTrigger is the refiner's view of an issue being opened or
// reopened, unless the human excluded it by label: issue.labels.name resolves
// through the array of the issue's label objects to their names, which is what a
// not-contains there has to compare against — one matching name is enough to hold
// the trigger off the issue.
func issueActionTrigger(in Install, role Role, action, exclude string) Trigger {
	return Trigger{
		Name:         triggerName(role, "issue-"+action, in.Slug),
		AgentRef:     in.Roles[role].AgentID,
		ConnectorRef: in.ConnectorID,
		Filters: []Filter{
			{Field: "type", Op: "eq", Value: "issues"},
			{Field: "action", Op: "eq", Value: action},
			{Field: labelNameField, Op: "not-contains", Value: exclude},
		},
	}
}

// mentionTrigger is a role answering comments that address its bot. The bot
// filters name every bot in the install, itself included: a comment is for one
// bot, and the one being mentioned answers it. A role whose bot is not named
// contributes no filter to exclude — there is nothing to exclude.
func mentionTrigger(in Install, role Role) Trigger {
	filters := []Filter{
		{Field: "type", Op: "eq", Value: "issue_comment"},
		{Field: "comment.body", Op: "contains", Value: "@" + in.Roles[role].BotLogin},
	}
	if role != RoleDeveloper {
		for _, r := range []Role{RoleRefiner, RoleDeveloper, RoleReviewer} {
			if bot := in.Roles[r].BotLogin; bot != "" {
				filters = append(filters, Filter{
					Field: "comment.user.login", Op: "neq", Value: bot,
				})
			}
		}
	}
	return Trigger{
		Name:         triggerName(role, "mention", in.Slug),
		AgentRef:     in.Roles[role].AgentID,
		ConnectorRef: in.ConnectorID,
		Filters:      filters,
	}
}

// pullOpenedTrigger is the reviewer's first look at new work: every pull
// request as it opens.
func pullOpenedTrigger(in Install) Trigger {
	return Trigger{
		Name:         triggerName(RoleReviewer, "pr-opened", in.Slug),
		AgentRef:     in.Roles[RoleReviewer].AgentID,
		ConnectorRef: in.ConnectorID,
		Filters: []Filter{
			{Field: "type", Op: "eq", Value: "pull_request"},
			{Field: "action", Op: "eq", Value: "opened"},
		},
	}
}

// triggerName names one trigger the way the install names everything it owns:
// forgejo-<role>-<what>-<slug> (R3).
func triggerName(role Role, what, slug string) string {
	return "forgejo-" + string(role) + "-" + what + "-" + slug
}

// sameTrigger is Missing's equality: the refs must match exactly, and the
// filters must match as a set — the order a trigger's filters happen to be
// stored in says nothing about what it selects. The one field spelling that is
// forgiven is the label exclusion, see canonicalFilters.
func sameTrigger(a, b Trigger) bool {
	return a.AgentRef == b.AgentRef && a.ConnectorRef == b.ConnectorRef &&
		sameFilters(canonicalFilters(a.Filters), canonicalFilters(b.Filters))
}

// canonicalFilters maps the legacy issue.labels exclusion onto issue.labels.name,
// so an existing trigger that still carries the old spelling counts as the same
// condition as the one the generator emits (see legacyLabelField). It copies
// rather than edits in place: a caller's filter set stays as it read it.
func canonicalFilters(fs []Filter) []Filter {
	out := slices.Clone(fs)
	for i := range out {
		if out[i].Field == legacyLabelField {
			out[i].Field = labelNameField
		}
	}
	return out
}

// sameFilters compares two filter sets by sorting copies and matching element
// by element, rather than by any one canonical spelling: a Filter holds a
// slice, so it is not even ==-comparable, and inventing a key string for it
// only invents a separator a value could contain.
func sameFilters(a, b []Filter) bool {
	if len(a) != len(b) {
		return false
	}
	sa, sb := slices.Clone(a), slices.Clone(b)
	sortFilters(sa)
	sortFilters(sb)
	for i := range sa {
		if sa[i].Field != sb[i].Field || sa[i].Op != sb[i].Op ||
			sa[i].Value != sb[i].Value || !slices.Equal(sa[i].Values, sb[i].Values) {
			return false
		}
	}
	return true
}

// sortFilters puts a filter set in its one sorted order. The order is total —
// field, then op, then value, then the operand list — so two equal sets sort
// to the same sequence.
func sortFilters(fs []Filter) {
	sort.Slice(fs, func(i, j int) bool {
		a, b := fs[i], fs[j]
		if a.Field != b.Field {
			return a.Field < b.Field
		}
		if a.Op != b.Op {
			return a.Op < b.Op
		}
		if a.Value != b.Value {
			return a.Value < b.Value
		}
		return slices.Compare(a.Values, b.Values) < 0
	})
}
