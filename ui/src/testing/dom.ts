// A Field's label names the control it belongs to by id. A label pointing at nothing is
// invisible in a screenshot — the text still sits above the box — but the control has no
// accessible name for a screen reader working through the form, and clicking the label does
// nothing. It is also invisible to a test that finds a control by its label. So it is worth
// checking a screen whole, after the click that renders it, rather than field by field.
export function assertEveryLabelPointsAtItsControl(
  screenName: string,
  container: HTMLElement,
): void {
  const labels = Array.from(container.querySelectorAll("label[for]"));
  const dangling = labels.filter((label) => {
    const id = label.getAttribute("for");
    return !id || !container.querySelector(`#${CSS.escape(id)}`);
  });
  if (dangling.length > 0) {
    const names = dangling.map((label) => `"${label.textContent}"`).join(", ");
    throw new Error(`${screenName}: label points at no control: ${names}`);
  }
}
