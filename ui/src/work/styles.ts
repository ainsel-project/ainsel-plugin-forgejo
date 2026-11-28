// The three layouts the work page's pieces share. The bundle ships no stylesheet, so a
// layout is an inline object on the element that needs it, and these are the ones used
// by more than one.

export const PAGE_STYLE = { display: "flex", flexDirection: "column" as const, gap: 16 };

export const ICON_BUTTON = {
  display: "inline-flex",
  alignItems: "center",
  justifyContent: "center",
  width: 32,
  height: 32,
  padding: 0,
  border: "1px solid var(--rule-soft)",
  borderRadius: "var(--radius-sm)",
  background: "var(--paper)",
  color: "var(--ink-2)",
  cursor: "pointer",
};
export const COUNT_BADGE = {
  display: "inline-block",
  minWidth: 18,
  padding: "0 6px",
  marginLeft: 6,
  borderRadius: 9,
  background: "var(--paper-2)",
  color: "var(--ink-2)",
  fontSize: 11,
  lineHeight: "18px",
  textAlign: "center" as const,
};
