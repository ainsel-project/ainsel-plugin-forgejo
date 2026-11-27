# UI Layout

The plugin UI ships no stylesheet: layout is inline styles plus one scoped CSS block, all of it
owned by [`ui/src/layout.tsx`](../ui/src/layout.tsx). The module is deliberately
plugin-neutral — its classes are `ainsel-*`, not `forgejo-*` — so it can move into the plugin
SDK and the GitHub, memory and template plugins adopt it by bumping. Existing `forgejo-*`
classes stay on the elements that carry them; they are the console theme's hooks.

## FieldGrid — form fields

Every set of form fields (the setup wizard's steps) sits in a `FieldGrid`:

- Columns follow the grid's **own container width**, not the viewport: `<640px → 1,
  640–1080 → 2, ≥1080 → 3`, via the exported pure mapping `columnsForWidth`.
- Rows use `align-items: start`, and `input`/`select` inside the grid share one
  `min-height`, so a long hint never moves a control.
- The grid's children are the `Field`s themselves. Wrapping a `Field` in a layout `div`
  makes the wrapper the grid item and undoes the row alignment — never do it.
- When a conditional field is absent, the last row keeps a hole. That is intended: a hole
  is stable, a reflow is not.

## CardGrid — read-only content tiles

Read-only tiles (the Live board's KPI panels, the detail panel's summary cards, the
Settings page's compare diff) sit in a `CardGrid`, parameterised by `minTrackWidth` and
`maxColumns`: as many equal tracks of at least the track width as fit, clamped between 1
and `maxColumns` — again by container width, never by viewport.

## The rule

No view hand-rolls `auto-fit` grids any more: the column count must be a fixed function of
the container width, computed in JS (`vitest` covers the mappings and the grid structure).
`ui/src/layout.test.tsx` refuses any `auto-fit` grid left in the source.
