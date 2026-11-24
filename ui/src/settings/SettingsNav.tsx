// The page's left rail. Each entry carries the count the admin would otherwise have
// to click to find: unsaved fields per role, configured instances. The counts come
// from the page, which owns the drafts and the list.

import { ROLES, sectionLabel } from "./model";
import type { RoleKey } from "./model";
import type { ForgejoInstance } from "../api";

export function SettingsNav({
  pane,
  currentRole,
  dirtyCount,
  instances,
  setPane,
  setCurrentRole,
}: {
  pane: "role" | "resources" | "instances";
  currentRole: RoleKey;
  dirtyCount: (role: string) => number;
  instances: ForgejoInstance[];
  setPane: (pane: "role" | "resources" | "instances") => void;
  setCurrentRole: (role: RoleKey) => void;
}) {
  return (
    <nav
      aria-label="Settings sections"
      style={{
        flex: "0 0 220px",
        maxWidth: "100%",
        boxSizing: "border-box",
        display: "flex",
        flexDirection: "column",
        gap: 2,
      }}
    >
      <span style={sectionLabel}>Agent roles</span>
      {ROLES.map((r) => {
        const on = pane === "role" && currentRole === r.key;
        const n = dirtyCount(r.key);
        return (
          <button
            key={r.key}
            type="button"
            aria-current={on ? "true" : undefined}
            onClick={() => {
              setPane("role");
              setCurrentRole(r.key);
            }}
            className={`forgejo-settings-nav-item${on ? " forgejo-settings-nav-item-on" : ""}`}
            style={{
              display: "flex",
              justifyContent: "space-between",
              alignItems: "center",
              gap: 8,
              width: "100%",
              padding: "8px 10px",
              border: 0,
              borderRadius: "var(--radius-sm)",
              fontSize: 14,
              cursor: "pointer",
              textAlign: "left",
            }}
          >
            <span style={{ display: "inline-flex", alignItems: "center", gap: 8 }}>
              <span
                aria-hidden="true"
                style={{
                  width: 8,
                  height: 8,
                  borderRadius: 4,
                  background: r.color,
                  display: "inline-block",
                  flex: "0 0 auto",
                }}
              />
              {r.label}
            </span>
            {n > 0 && (
              <span
                title="Fields edited away from the shipped default"
                style={{
                  minWidth: 18,
                  padding: "0 6px",
                  borderRadius: 9,
                  background: "var(--signal-haze)",
                  color: "var(--signal-ink)",
                  fontSize: 11,
                  fontWeight: 600,
                  lineHeight: "18px",
                  textAlign: "center",
                }}
              >
                {n}
              </span>
            )}
          </button>
        );
      })}
      <span style={{ ...sectionLabel, padding: "14px 10px 6px" }}>Plugin</span>
      <button
        type="button"
        aria-current={pane === "instances" ? "true" : undefined}
        onClick={() => setPane("instances")}
        className={`forgejo-settings-nav-item${pane === "instances" ? " forgejo-settings-nav-item-on" : ""}`}
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
          gap: 8,
          width: "100%",
          padding: "8px 10px",
          border: 0,
          borderRadius: "var(--radius-sm)",
          fontSize: 14,
          cursor: "pointer",
          textAlign: "left",
        }}
      >
        <span>Forgejo instances</span>
        {instances.length > 0 && (
          <span
            title="Configured Forgejo instances"
            style={{
              minWidth: 18,
              padding: "0 6px",
              borderRadius: 9,
              background: "var(--signal-haze)",
              color: "var(--signal-ink)",
              fontSize: 11,
              fontWeight: 600,
              lineHeight: "18px",
              textAlign: "center",
            }}
          >
            {instances.length}
          </span>
        )}
      </button>
      <button
        type="button"
        aria-current={pane === "resources" ? "true" : undefined}
        onClick={() => setPane("resources")}
        className={`forgejo-settings-nav-item${pane === "resources" ? " forgejo-settings-nav-item-on" : ""}`}
        style={{
          display: "flex",
          alignItems: "center",
          width: "100%",
          padding: "8px 10px",
          border: 0,
          borderRadius: "var(--radius-sm)",
          fontSize: 14,
          cursor: "pointer",
          textAlign: "left",
        }}
      >
        <span>Managed resources</span>
      </button>
    </nav>
  );
}
