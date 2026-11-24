import type { ChangeEvent } from "react";
import { Button, Field, Input, RegisterTable } from "@ainsel/plugin-sdk";
import type { Input as InputType } from "./model";
import { ROLE_COLOR } from "./model";
import { StepFooter } from "./Wizard";

interface AccountsProps {
  input: InputType;
  setInput: (input: InputType) => void;
  // Whether this walk edits an existing install (S0 "Edit") or creates a new one
  // (S0 "New Install"). The screen cannot infer it from the input: on a new
  // install the roles may already carry values, and on an edit the account work
  // is optional — an empty token means "keep what is there".
  editing: boolean;
  onNext: () => void;
  onBack: () => void;
}

interface AccountRow {
  role: string;
  color: string;
  bot: string;
  team: string;
}

const ACCOUNT_COLUMNS = [
  {
    key: "role",
    header: "Role",
    cell: (row: AccountRow) => (
      <span style={{ display: "inline-flex", alignItems: "center", gap: "0.5rem" }}>
        <span style={{ width: "0.5rem", height: "0.5rem", borderRadius: "999px", background: row.color, display: "inline-block" }} />
        {row.role}
      </span>
    ),
  },
  {
    key: "bot",
    header: "Bot user",
    cell: (row: AccountRow) => <code>{row.bot}</code>,
  },
  {
    key: "team",
    header: "Team",
    cell: (row: AccountRow) => <code>{row.team}</code>,
  },
];

export function Accounts({
  input,
  setInput,
  editing,
  onNext,
  onBack,
}: AccountsProps) {
  const roles = ["Refiner", "Developer", "Reviewer"] as const;
  const rows: AccountRow[] = roles.map((role) => {
    const key = role.toLowerCase() as keyof typeof ROLE_COLOR;
    return {
      role,
      color: ROLE_COLOR[key],
      bot: `${key}-${input.slug}`,
      team: `ainsel-${key}-${input.slug}`,
    };
  });

  return (
    <div className="forgejo-accounts" style={{ display: "flex", flexDirection: "column", gap: "1.25rem" }}>
      <p className="forgejo-accounts-info" style={{ margin: 0, fontSize: "0.875rem", color: "var(--ink-2)" }}>
        {editing
          ? `Bot accounts for this install already exist in Forgejo organization "${input.org}". They are created only with a new install.`
          : `The plugin will create three bot accounts in Forgejo organization "${input.org}".`}
      </p>

      <div className="forgejo-accounts-table" style={{ overflowX: "auto" }}>
        <RegisterTable
          rows={rows}
          columns={ACCOUNT_COLUMNS}
          rowKey={(row: AccountRow) => row.role}
          withRowNumbers={false}
        />
      </div>

      <div style={{ display: "grid", gridTemplateColumns: "repeat(auto-fit, minmax(260px, 1fr))", gap: "1rem 1.25rem" }}>
        <Field
          htmlFor="accounts-admin-token"
          label="Forgejo admin token"
          hint={
            editing
              ? "The account work runs only when a token is given. Without one, the existing bot accounts, teams and permissions are kept as they are."
              : "Used once to create the bot users and teams above. It is not stored."
          }
        >
          <Input
            id="accounts-admin-token"
            type="password"
            value={input.adminToken}
            onChange={(e: ChangeEvent<HTMLInputElement>) => setInput({ ...input, adminToken: e.target.value })}
            placeholder={
              editing
                ? "leave empty to keep"
                : "Personal access token with write:admin scope"
            }
          />
        </Field>
      </div>

      <StepFooter onBack={onBack}>
        {(editing || input.adminToken) && (
          <Button variant="primary" onClick={onNext} className="forgejo-next-btn">
            Continue
          </Button>
        )}
      </StepFooter>
    </div>
  );
}
