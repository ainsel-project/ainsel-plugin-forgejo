// The small drawings of the work table: the hand-off bar and its label, the issue/pull
// request mark, and the pager's chevron. Each names itself to assistive tech in words —
// the bar as one image whose label spells out all three stages — and each is drawn in
// theme tokens so it moves with the console's theme.

import { STAGES, STAGE_TOKEN, STAGE_WORD, handOffLabel } from "./model";
import type { StageState } from "./model";

// HandOff is the three-stage bar and its label. The bar is one image for assistive tech with the
// stage names and states spelled out, and the label says where the thread is in words.
export function HandOff({ states }: { states: StageState[] }) {
  const description = STAGES.map((stage, i) => `${stage.name}: ${STAGE_WORD[states[i]]}`).join(", ");
  return (
    <span style={{ display: "flex", flexDirection: "column", gap: 6 }}>
      <span role="img" aria-label={description} title={description} style={{ display: "flex", gap: 3 }}>
        {STAGES.map((stage, i) => (
          <span
            key={stage.role}
            aria-hidden="true"
            style={{
              display: "inline-block",
              width: 26,
              height: 6,
              borderRadius: 3,
              background: STAGE_TOKEN[states[i]],
            }}
          />
        ))}
      </span>
      <span style={{ fontSize: 12, color: "var(--ink-2)" }}>{handOffLabel(states)}</span>
    </span>
  );
}

// KindIcon marks a thread as an issue or a pull request, with the name spelled out for screen
// readers. The icons are drawn in theme tokens so they move with the console's theme.
export function KindIcon({ isPr }: { isPr: boolean }) {
  if (isPr) {
    return (
      <svg
        width="16"
        height="16"
        viewBox="0 0 24 24"
        fill="none"
        stroke="var(--signal)"
        strokeWidth="2"
        strokeLinecap="round"
        strokeLinejoin="round"
        role="img"
        aria-label="Pull request"
        style={{ flex: "0 0 auto", marginTop: 1 }}
      >
        <circle cx="18" cy="18" r="3" />
        <circle cx="6" cy="6" r="3" />
        <path d="M13 6h3a2 2 0 0 1 2 2v7" />
        <path d="M6 9v12" />
      </svg>
    );
  }
  return (
    <svg
      width="16"
      height="16"
      viewBox="0 0 24 24"
      fill="none"
      stroke="var(--ok)"
      strokeWidth="2"
      role="img"
      aria-label="Issue"
      style={{ flex: "0 0 auto", marginTop: 1 }}
    >
      <circle cx="12" cy="12" r="9" />
      <circle cx="12" cy="12" r="1.5" fill="var(--ok)" />
    </svg>
  );
}

export function ChevronIcon({ direction }: { direction: "left" | "right" }) {
  return (
    <svg
      width="14"
      height="14"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      aria-hidden="true"
    >
      <path d={direction === "left" ? "M15 18l-6-6 6-6" : "M9 18l6-6-6-6"} />
    </svg>
  );
}
