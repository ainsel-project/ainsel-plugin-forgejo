// Stand-in for the console's copy of `@ainsel/plugin-sdk`, installed from npm
// for the types but never run here. What it stands in for, and how far, is
// worth naming:
//
// The real `usePluginContext` reads the context the console puts around a
// plugin page or slot, and the real primitives render the components the console
// registers with `registerHost` at startup. A component test has neither, so
// this module answers for both: it hands back a context whose `api` is a mock
// the test decides the answers of, and it renders the primitives as plain
// markup. Each mock takes exactly the props its console counterpart takes — a
// component passing a prop the console ignores (a `className` on `Notice`,
// say) fails here rather than in production — and reproduces the `role` the
// real `Notice` sets, because that one is observable to tests and screen
// readers alike. The mocks carry no classes of their own even where the real
// primitives do (`panel`, `notice-err`): those classes are the console's to
// name, and leaving them out keeps an assertion about the classes a component
// emits testing that component and nothing else.
//
// The `data-testid` and `data-variant` attributes below are the mock's own
// hooks for finding a rendered primitive and reading its variant; the real
// primitives carry neither.
import { vi } from "vitest";
import type { ChangeEvent, ReactNode } from "react";
import type { HttpClient, PluginContextValue } from "@ainsel/plugin-sdk";

// Mock API functions for work endpoints
export const get = vi.fn<
  (path: string, init?: { query?: Record<string, string> }) => Promise<unknown>
>();

export const post = vi.fn<
  (path: string, body?: unknown) => Promise<unknown>
>();

// The settings page writes through PUT — the console's client has it, so the
// stand-in does too.
export const put = vi.fn<(path: string, body?: unknown) => Promise<unknown>>();

// The context `usePluginContext` returns for plugin `forgejo`.
const api = { get, post, put } as unknown as HttpClient;

export function usePluginContext(): PluginContextValue {
  return {
    pluginId: "forgejo",
    user: { sub: "u-1", username: "acme-dev", isAdmin: false, groups: [] },
    hub: api,
    api,
    navigate: () => {},
    theme: "light",
  };
}

// The one place the mock is stricter than the console: the real primitives
// forward their props to the registered host component, which drops the ones it
// does not name — silently, in production. Here an unnamed prop throws, so a
// component passing one (a `className` on `Notice`, say) fails its test instead
// of quietly rendering nothing with it.
function checkProps(
  name: string,
  props: object,
  taken: readonly string[],
): void {
  const extra = Object.keys(props).filter((prop) => !taken.includes(prop));
  if (extra.length > 0) {
    throw new Error(`${name} takes no ${extra.join(", ")} — the console drops it`);
  }
}

// The other half of the same rule: a prop the console's primitive needs and the component
// does not pass is as wrong as one it passes by mistake. Without this the mock fails where
// the primitive would have failed — inside a `.map` over an undefined `tabs` — and the test
// says nothing about which prop was missing.
function requireProps(
  name: string,
  props: object,
  required: readonly string[],
): void {
  const missing = required.filter(
    (prop) => (props as Record<string, unknown>)[prop] === undefined,
  );
  if (missing.length > 0) {
    throw new Error(`${name} needs ${missing.join(", ")} — the console's primitive has no default for it`);
  }
}

type PanelProps = {
  title?: string;
  right?: ReactNode;
  children: ReactNode;
  className?: string;
};

export function Panel(props: PanelProps) {
  checkProps("Panel", props, ["title", "right", "children", "className"]);
  const { title, right, children, className } = props;
  if (className && !className.startsWith("forgejo-")) {
    throw new Error(`Panel className must start with 'forgejo-', got '${className}'`);
  }
  return (
    <section data-testid="panel" className={className}>
      {title ? <h3>{title}</h3> : null}
      {right ? <div>{right}</div> : null}
      {children}
    </section>
  );
}

// The props the real Notice takes — variant and children, nothing else — and
// the role it derives from the variant: a calm info is a status, a warn or an
// err announces itself as an alert.
const NOTICE_ROLE: Record<"info" | "warn" | "err", "status" | "alert"> = {
  info: "status",
  warn: "alert",
  err: "alert",
};

type NoticeProps = {
  variant?: "info" | "warn" | "err";
  children: ReactNode;
};

export function Notice(props: NoticeProps) {
  checkProps("Notice", props, ["variant", "children"]);
  const { variant = "info", children } = props;
  return (
    <div data-testid="notice" data-variant={variant} role={NOTICE_ROLE[variant]}>
      {children}
    </div>
  );
}

type TagProps = {
  children: ReactNode;
  variant?: "default" | "ok" | "warn" | "err" | "stale";
  solid?: boolean;
  className?: string;
};

export function Tag(props: TagProps) {
  checkProps("Tag", props, ["children", "variant", "solid", "className"]);
  const { children, variant = "default", className } = props;
  return (
    <span data-testid="tag" data-variant={variant} className={className}>
      {children}
    </span>
  );
}

// Button forwards its props to a real <button>, so `disabled` is one of the ones the console
// honours — a screen that means to stop a submit stops it by leaving the attribute out of
// neither side.
type ButtonProps = {
  children: ReactNode;
  onClick?: () => void;
  variant?: string;
  className?: string;
  disabled?: boolean;
};

export function Button(props: ButtonProps) {
  checkProps("Button", props, ["children", "onClick", "variant", "className", "disabled"]);
  const { children, onClick, className, disabled } = props;
  return (
    <button onClick={onClick} className={className} disabled={disabled} data-testid="button">
      {children}
    </button>
  );
}

// Field mirrors the console's primitive (frontend/src/primitives/Field.tsx): the label is
// tied to the control it labels by `htmlFor`, and there is no className for a field's own.
// A Field used without `htmlFor` renders a label that points at nothing — the control stays
// unannounced to a screen reader and clicking the label does nothing — and passed a
// className, the console drops it.
type FieldProps = {
  label: string;
  htmlFor: string;
  error?: string;
  hint?: ReactNode;
  children: ReactNode;
};

export function Field(props: FieldProps) {
  checkProps("Field", props, ["label", "htmlFor", "error", "hint", "children"]);
  requireProps("Field", props, ["label", "htmlFor"]);
  const { label, htmlFor, error, hint, children } = props;
  return (
    <div data-testid="field">
      <label htmlFor={htmlFor}>{label}</label>
      {children}
      {hint ? <div>{hint}</div> : null}
      {error ? <div>{error}</div> : null}
    </div>
  );
}

// The real Input is an <input> with its attributes passed through, so anything an input
// takes is fair; the list below is what the plugin's screens use. `id` is on it because a
// Field's label points at it.
type InputProps = {
  id?: string;
  value?: string;
  onChange?: (e: ChangeEvent<HTMLInputElement>) => void;
  placeholder?: string;
  type?: string;
  className?: string;
  readOnly?: boolean;
  "aria-label"?: string;
};

export function Input(props: InputProps) {
  checkProps("Input", props, ["id", "value", "onChange", "placeholder", "type", "className", "readOnly", "aria-label"]);
  const { id, value, onChange, placeholder, type = "text", className, readOnly } = props;
  return (
    <input
      id={id}
      type={type}
      value={value}
      onChange={onChange}
      placeholder={placeholder}
      className={className}
      readOnly={readOnly}
      aria-label={props["aria-label"]}
      data-testid="input"
    />
  );
}

// Select mirrors the console's primitive (frontend/src/primitives/Select.tsx):
// the choices come in as `options`, not <option> children, and onChange gets
// the chosen value, not an event. A native-<select>-shaped stand-in let the
// setup wizard ship against the wrong contract and crash in the console.
type SelectOption = { value: string; label: string };

type SelectProps = {
  id?: string;
  value: string;
  onChange: (next: string) => void;
  options: readonly SelectOption[];
  emptyLabel?: string;
  size?: "md" | "sm";
  disabled?: boolean;
  className?: string;
  "aria-label"?: string;
};

export function Select(props: SelectProps) {
  checkProps("Select", props, ["id", "value", "onChange", "options", "emptyLabel", "size", "disabled", "className", "aria-label"]);
  const { id, value, onChange, options, emptyLabel, disabled, className } = props;
  if (!Array.isArray(options)) {
    throw new Error("Select: options must be an array of { value, label }");
  }
  return (
    <select
      id={id}
      value={value}
      disabled={disabled}
      onChange={(e) => onChange(e.target.value)}
      className={className}
      aria-label={props["aria-label"]}
      data-testid="select"
    >
      {emptyLabel !== undefined ? <option value="">{emptyLabel}</option> : null}
      {options.map((opt) => (
        <option key={opt.value} value={opt.value}>
          {opt.label}
        </option>
      ))}
    </select>
  );
}

type TextareaProps = {
  id?: string;
  value?: string;
  onChange?: (e: ChangeEvent<HTMLTextAreaElement>) => void;
  placeholder?: string;
  className?: string;
};

export function Textarea(props: TextareaProps) {
  checkProps("Textarea", props, ["id", "value", "onChange", "placeholder", "className"]);
  const { id, value, onChange, placeholder, className } = props;
  return (
    <textarea
      id={id}
      value={value}
      onChange={onChange}
      placeholder={placeholder}
      className={className}
      data-testid="textarea"
    />
  );
}

// Tabs mirrors the console's primitive (frontend/src/primitives/Tabs.tsx), which renders the
// tab buttons and *nothing else* — the panel belongs to the caller, chosen by the value it
// passes in. A stand-in that took children let the roles screen ship with three forms
// stacked under three buttons that switched nothing: every field the admin saw belonged to
// the wrong role, and the panel for the role they were on was nowhere.
//
// `label` is what the console puts in the button as its children, so a node there renders rather
// than being dropped — that is how the roles tab carries a marker a screen reader can read. The
// console's own type asks for a string, which is what a plain tab still passes.
type Tab = { value: string; label: ReactNode };

type TabsProps = {
  value: string;
  onChange: (next: string) => void;
  tabs: readonly Tab[];
  "aria-label"?: string;
};

export function Tabs(props: TabsProps) {
  checkProps("Tabs", props, ["value", "onChange", "tabs", "aria-label"]);
  requireProps("Tabs", props, ["value", "onChange", "tabs"]);
  const { value, onChange, tabs } = props;
  if (!Array.isArray(tabs)) {
    throw new Error("Tabs: tabs must be an array of { value, label }");
  }
  return (
    <div role="tablist" aria-label={props["aria-label"]} data-testid="tabs">
      {tabs.map((tab) => (
        <button
          key={tab.value}
          type="button"
          role="tab"
          aria-selected={tab.value === value}
          onClick={() => onChange(tab.value)}
        >
          {tab.label}
        </button>
      ))}
    </div>
  );
}

type DotProps = {
  variant?: string;
  className?: string;
};

export function Dot(props: DotProps) {
  checkProps("Dot", props, ["variant", "className"]);
  return <span data-testid="dot" className={props.className} />;
}

type MarkdownProps = {
  children: string;
  className?: string;
};

export function Markdown(props: MarkdownProps) {
  checkProps("Markdown", props, ["children", "className"]);
  return (
    <div className={props.className} data-testid="markdown">
      {props.children}
    </div>
  );
}

// RegisterTable mirrors the console's primitive (frontend/src/primitives/RegisterTable.tsx):
// the rows and columns come in as data and the primitive builds the table. It took children
// here, which the real one ignores completely — a table built out of `<div>` children
// rendered a header row with no columns and no rows, so the preview of what an install is
// about to create was invisible in the console while passing every test here.
type Column<T> = {
  key: string;
  header: ReactNode;
  cell: (row: T) => ReactNode;
  width?: string | number;
  align?: "left" | "right" | "center";
  sortable?: boolean;
};

type RegisterTableProps<T> = {
  rows: readonly T[];
  columns: readonly Column<T>[];
  rowKey: (row: T) => string;
  emptyLabel?: ReactNode;
  rowClassName?: (row: T) => string | undefined;
  withRowNumbers?: boolean;
};

export function RegisterTable<T>(props: RegisterTableProps<T>) {
  checkProps("RegisterTable", props, [
    "rows",
    "columns",
    "rowKey",
    "emptyLabel",
    "rowClassName",
    "withRowNumbers",
  ]);
  requireProps("RegisterTable", props, ["rows", "columns", "rowKey"]);
  const { rows, columns, rowKey, emptyLabel, rowClassName, withRowNumbers = true } = props;
  if (rows.length === 0 && emptyLabel !== undefined) {
    return <div data-testid="register-table-empty">{emptyLabel}</div>;
  }
  return (
    <table data-testid="register-table">
      <thead>
        <tr>
          {withRowNumbers ? <th>#</th> : null}
          {columns.map((col) => (
            <th key={col.key}>{col.header}</th>
          ))}
        </tr>
      </thead>
      <tbody>
        {rows.map((row, idx) => (
          <tr key={rowKey(row)} className={rowClassName?.(row)}>
            {withRowNumbers ? <td>{String(idx + 1).padStart(2, "0")}</td> : null}
            {columns.map((col) => (
              <td key={col.key}>{col.cell(row)}</td>
            ))}
          </tr>
        ))}
      </tbody>
    </table>
  );
}
