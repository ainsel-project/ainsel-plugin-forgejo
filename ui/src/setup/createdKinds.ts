// The vocabulary of what an install creates, shared by the done screen (S8) and the
// installs table (S0) so the two screens can never drift apart in what they call a
// resource. The kinds are the ones the backend plan reports (`internal/setup/plan.go`:
// user, team, agent, image, persona, trigger, cronTrigger, mcpServer, connector, hook).
//
// `label` is the wording the done screen lists each kind under.
import type { Install } from "../api";

export interface CreatedKind {
  kind: string;
  label: string;
}

// In the order a setup makes them, which is the order both screens list them in.
export const CREATED_KINDS: CreatedKind[] = [
  { kind: "user", label: "Bot accounts" },
  { kind: "team", label: "Teams" },
  { kind: "agent", label: "Agents" },
  { kind: "image", label: "Agent images" },
  { kind: "persona", label: "Personas" },
  { kind: "trigger", label: "Triggers" },
  { kind: "cronTrigger", label: "PR patrol" },
  { kind: "mcpServer", label: "MCP server" },
  { kind: "connector", label: "Connector" },
  { kind: "hook", label: "Webhook" },
];

// The event families the install row's coverage records, in reading order, with the names the
// installs list shows them under. Reviews count as part of the pull_request family (design
// ruling V5), so a review shows under Pull requests too.
export const COVERAGE_FAMILIES: { key: string; label: string }[] = [
  { key: "issues", label: "Issues" },
  { key: "issue_comment", label: "Comments" },
  { key: "pull_request", label: "Pull requests" },
];

// What the install row says it created, counted off the ids the row holds. The row is
// the source of truth for what exists now: a row saved half way through the wizard, or
// left by a failed apply, legitimately has pieces missing, so each count stands on its
// own and a missing one is information, not an error.
//
// Triggers are the one convention: their ids are not on the row (re-apply manages them
// by their slug-derived names), and apply creates all six event triggers plus the
// developer's patrol cron trigger as one batch — so three complete roles plus a
// connector is shown as six events and one patrol. Display only; never use it as logic.
// A row that has not got there reads as "—" rather than as a wrong number.
export function deriveResults(install: Install): Record<string, number | null> {
  const roles = Object.values(install.roles ?? {});
  const filled = (v: unknown) => v !== undefined && v !== null && v !== "";
  const completeRoles = roles.filter((r) => filled(r.agentId)).length;
  return {
    user: roles.filter((r) => filled(r.botLogin)).length,
    team: roles.filter((r) => r.teamId !== undefined && r.teamId !== 0).length,
    agent: roles.filter((r) => filled(r.agentId)).length,
    image: roles.filter((r) => filled(r.imageId)).length,
    persona: roles.filter((r) => filled(r.personaId)).length,
    trigger: completeRoles === 3 && filled(install.connectorId) ? 6 : null,
    cronTrigger: completeRoles === 3 && filled(install.connectorId) ? 1 : null,
    mcpServer: filled(install.mcpServer) ? 1 : 0,
    connector: filled(install.connectorId) ? 1 : 0,
    hook: install.hookId !== undefined && install.hookId !== 0 ? 1 : 0,
  };
}
