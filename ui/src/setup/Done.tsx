import { Button, Notice, Panel, RegisterTable, Tag } from "@ainsel/plugin-sdk";
import { CREATED_KINDS } from "./createdKinds";

interface Install {
  slug: string;
  baseUrl: string;
  org: string;
  webhookVerifiedAt?: string;
}

interface Report {
  created: { kind: string; name: string }[];
}

interface DoneProps {
  install: Install;
  // What the apply made, so the admin can see the walk did what they asked — the installs
  // list alone says only that a row exists.
  report: Report;
  // Whether S7 saw a delivery before the admin continued. False is the normal state of a
  // fresh install, not a problem: the reconcile loop marks it verified on the first event.
  verified: boolean;
  onClose: () => void;
}

// What the apply made, one row per kind that was made. A kind it did not make has no row: the
// table says what exists, not what could have.
interface CreatedRow {
  kind: string;
  label: string;
  count: number;
}

const CREATED_COLUMNS = [
  { key: "kind", header: "Kind", cell: (row: CreatedRow) => row.label },
  {
    key: "count",
    header: "Count",
    cell: (row: CreatedRow) => <span style={{ fontVariantNumeric: "tabular-nums" }}>{row.count}</span>,
  },
];

export function Done({ install, report, verified, onClose }: DoneProps) {
  // The org page, not an org-level "new issue" page: Forgejo has no such page, and the link
  // is where the admin goes to open the issue that verifies the webhook.
  const orgUrl = `${install.baseUrl}/${install.org}`;

  const created: CreatedRow[] = CREATED_KINDS.flatMap(({ kind, label }) => {
    const count = report.created.filter((op) => op.kind === kind).length;
    return count === 0 ? [] : [{ kind, label, count }];
  });

  return (
    <Panel className="forgejo-done" title="Setup complete">
      <div style={{ display: "flex", flexDirection: "column", gap: "1.25rem" }}>
        <Notice variant="info">
          <span className="forgejo-done-headline">
            <Tag variant="ok">✓ Installed</Tag>{" "}
            {`"${install.slug}" is set up for ${install.org} on ${install.baseUrl}.`}
          </span>
        </Notice>

        <div className="forgejo-done-created" style={{ display: "flex", flexDirection: "column", gap: "0.5rem" }}>
          <h5 style={{ margin: 0, fontSize: "0.8125rem", fontWeight: 600, color: "var(--ink-2)" }}>Created</h5>
          <div style={{ overflowX: "auto" }}>
            <RegisterTable
              rows={created}
              columns={CREATED_COLUMNS}
              rowKey={(row: CreatedRow) => row.kind}
              emptyLabel="Nothing was created."
              withRowNumbers={false}
            />
          </div>
        </div>

        {verified ? (
          <Tag variant="ok">✓ Webhook verified</Tag>
        ) : (
          <Notice variant="info">
            The webhook is set up. It is marked verified when the first event arrives: open an
            issue, or comment on one, in any repository of {install.org}.
          </Notice>
        )}

        <div className="forgejo-next-steps" style={{ display: "flex", flexDirection: "column", gap: "0.5rem" }}>
          <h5 style={{ margin: 0, fontSize: "0.8125rem", fontWeight: 600, color: "var(--ink-2)" }}>Next steps</h5>
          <ol style={{ margin: 0, paddingLeft: "1.25rem", fontSize: "0.875rem", color: "var(--ink-2)", display: "flex", flexDirection: "column", gap: "0.375rem" }}>
            <li>
              <a href={orgUrl} target="_blank" rel="noopener noreferrer">
                Open {install.org} in Forgejo
              </a>
              {" "}and open an issue, or comment on one, to see the agents respond.
            </li>
            <li>Check the AInsel dashboard for the agents&apos; responses.</li>
            <li>Close this screen to return to the installs list, where you can edit the install.</li>
          </ol>
        </div>

        <div>
          <Button variant="primary" onClick={onClose} className="forgejo-close-btn">
            Close
          </Button>
        </div>
      </div>
    </Panel>
  );
}
