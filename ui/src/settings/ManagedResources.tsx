// The resources the plugin manifest installs at every install. They are read-only
// here by design: membership changes only when the plugin upgrades, and the version
// tag says which manifest the list came from.

import { RegisterTable, Tag } from "@ainsel/plugin-sdk";
import { cardStyle, MONO, RESOURCES } from "./model";
import type { Resource } from "./model";

export function ManagedResources({ version }: { version: string }) {
  return (
    <section style={{ ...cardStyle, overflow: "hidden" }}>
      <div style={{ padding: "16px 20px" }}>
        <h2 style={{ margin: 0, fontSize: 18, fontWeight: 600 }}>Managed resources</h2>
        <p style={{ margin: "4px 0 0", fontSize: 13, color: "var(--ink-2)", maxWidth: 640 }}>
          Every install receives these. They come from the plugin manifest and change only when the plugin is upgraded,
          so they cannot be edited here.
        </p>
        <p style={{ margin: "8px 0 0", fontSize: 13, color: "var(--ink-2)", maxWidth: 640 }}>
          Each install also gets its forge MCP server, <code>forgejo-mcp-&lt;install&gt;</code>, pointed at by the MCP
          fields below. The sets are the plugin manifest&apos;s — membership changes only when the plugin upgrades.
        </p>
      </div>
      <div style={{ overflowX: "auto" }}>
        <div style={{ minWidth: 640 }}>
          <RegisterTable
            withRowNumbers={false}
            rows={RESOURCES}
            rowKey={(r: Resource) => r.name}
            columns={[
              {
                key: "name",
                header: "Resource",
                cell: (r: Resource) => (
                  <span style={{ display: "inline-flex", alignItems: "center", gap: 8 }}>
                    <svg
                      width="14"
                      height="14"
                      viewBox="0 0 24 24"
                      fill="none"
                      stroke="currentColor"
                      strokeWidth={2}
                      strokeLinecap="round"
                      strokeLinejoin="round"
                      role="img"
                      aria-label="Locked"
                      style={{ color: "var(--ink-3)", flex: "0 0 auto" }}
                    >
                      <rect x="4" y="11" width="16" height="10" rx="2" />
                      <path d="M8 11V7a4 4 0 0 1 8 0v4" />
                    </svg>
                    <span className="forgejo-settings-mono" style={{ fontFamily: MONO, color: "var(--ink)" }}>
                      {r.name}
                    </span>
                  </span>
                ),
              },
              { key: "kind", header: "Kind", cell: (r: Resource) => r.kind },
              { key: "scope", header: "Scope", cell: (r: Resource) => r.scope },
              { key: "source", header: "Source", cell: () => <Tag>{`Plugin v${version}`}</Tag> },
            ]}
          />
        </div>
      </div>
    </section>
  );
}
