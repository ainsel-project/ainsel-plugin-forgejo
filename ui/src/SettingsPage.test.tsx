// The admin's settings page: the registry as cards per role, the drafts that
// collect into one batch save, the immediate reset, the "Update available" box
// that carries the winner rule, the "Reconcile now" pass over the installs, and
// the managed resources. The fixtures are the API's own shape — every call
// answers with the whole registry — because the page renders what the answer
// says rather than a form of its own making.
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { SettingsPage } from "./SettingsPage";
import { get, post, put } from "./testing/sdk";
import type { Setting } from "./api";

const ROLES = ["refiner", "developer", "reviewer"];

const FIELD_VALUES: Record<string, string> = {
  personaText: "the shipped persona",
  skillText: "the shipped skill",
  llmProvider: "ollama-cloud",
  llmModel: "llama3.3",
  // The custom pair: the URL is an ordinary text row; the key's answer is
  // always empty because the plugin keeps no key to answer with — its home is the
  // hub, on the role's agents. So is every key row: never a value, never edited.
  llmCustomUrl: "",
  llmCustomKey: "",
  // The vendor keys: the same contract.
  llmOpenAIKey: "",
  // The OpenAI auth method: a dropdown row, and the shipped default is the
  // method the hub itself answers for an agent that never named one.
  llmOpenAIAuthMethod: "APIKey",
  llmAnthropicKey: "",
  llmGoogleKey: "",
  mcpUrl: "https://forge-mcp.example.com:3000",
  mcpTokenFromEnv: "FORGEJO_TOKEN",
};

const LABELS: Record<string, string> = {
  personaText: "Persona",
  skillText: "Skill section",
  llmProvider: "Provider",
  llmModel: "Model",
  llmCustomUrl: "Custom provider URL",
  llmCustomKey: "Custom provider key",
  llmOpenAIKey: "OpenAI key",
  llmOpenAIAuthMethod: "OpenAI auth method",
  llmAnthropicKey: "Anthropic key",
  llmGoogleKey: "Google Gemini key",
  mcpUrl: "Forge MCP server URL",
  mcpTokenFromEnv: "Token environment variable",
};

const RUNNING_VERSION = "0.1.0";

// The provider list the registry serves on every llmProvider row — the same
// list the backend's checkEdit enforces (internal/settings.LLMProviders).
const PROVIDER_OPTIONS = [
  { value: "ollama-cloud", label: "Ollama Cloud" },
  { value: "opencode", label: "OpenCode" },
  { value: "alibaba-cloud", label: "Alibaba Token Plan" },
  { value: "openai", label: "OpenAI" },
  { value: "anthropic", label: "Anthropic" },
  { value: "google", label: "Google Gemini" },
  { value: "custom", label: "Custom" },
];

// The auth-method list the registry serves on every llmOpenAIAuthMethod row — the
// two values internal/settings.LLMAuthMethods ships, API key first because it is
// the default.
const AUTH_METHOD_OPTIONS = [
  { value: "APIKey", label: "API key" },
  { value: "ChatGPTSignIn", label: "Sign in with ChatGPT" },
];

// The whole registry as the GET answers it: 3 roles × 12 fields. `edits` patches
// one row by its "role.field" key — a dirty row, an older defaultVersion, and so on.
// `extra` adds rows for fields the page has no card for, the way a newer plugin's
// registry would answer.
function makeSettings(
  edits: Record<string, Partial<Setting>> = {},
  extra: Setting[] = [],
): Setting[] {
  const rows: Setting[] = [];
  for (const role of ROLES) {
    for (const [field, value] of Object.entries(FIELD_VALUES)) {
      rows.push({
        role,
        field,
        value,
        dirty: false,
        default: value,
        defaultVersion: RUNNING_VERSION,
        // The provider row and the OpenAI auth-method row are the two rows the
        // answer serves as a dropdown.
        ...(field === "llmProvider" ? { options: PROVIDER_OPTIONS } : {}),
        ...(field === "llmOpenAIAuthMethod" ? { options: AUTH_METHOD_OPTIONS } : {}),
        ...(edits[`${role}.${field}`] ?? {}),
      });
    }
  }
  return [...rows, ...extra];
}

// The registry as it stands after one successful edit of (role, field): the row
// carries what was saved, and a save is what makes a row dirty.
function afterEdit(current: Setting[], role: string, field: string, value: string): Setting[] {
  return current.map((s) =>
    s.role === role && s.field === field
      ? { ...s, value, dirty: true, defaultVersion: RUNNING_VERSION }
      : s,
  );
}

// The registry after a batch: every edit in the request, applied in order.
function afterBatch(current: Setting[], edits: { role: string; field: string; value?: string }[]) {
  return edits.reduce(
    (acc, e) => afterEdit(acc, e.role, e.field, e.value ?? ""),
    current,
  );
}

function mockSettingsGet(edits: Record<string, Partial<Setting>> = {}, extra: Setting[] = []) {
  get.mockImplementation(async (path: string) => {
    if (path === "/settings") return { settings: makeSettings(edits, extra) };
    // The instance list rides the same load: the page reads it beside the
    // registry, because the wizard's Instance step selects over it.
    if (path === "/instances") return { instances: [] };
    throw new Error(`unexpected GET ${path}`);
  });
}

// Each field is one row, found by its label and then by its own buttons: a
// button is never found by name alone, since every row has one of the same name.
function rowOf(label: string): HTMLElement {
  return screen.getByLabelText(label).closest("[data-field]") as HTMLElement;
}
function rowButton(label: string, name: RegExp | string): HTMLButtonElement {
  return within(rowOf(label)).getByRole("button", { name }) as HTMLButtonElement;
}
function saveBar(): HTMLElement {
  return screen.getByRole("region", { name: "Unsaved changes" });
}

async function renderLoaded() {
  render(<SettingsPage />);
  await screen.findByRole("button", { name: "Reconcile now" });
}

// The role nav's button for a role. Its name carries the dirty count when there is one.
function roleNav(name: "Refiner" | "Developer" | "Reviewer"): HTMLButtonElement {
  return screen.getByRole("button", { name: new RegExp(`^${name}`) }) as HTMLButtonElement;
}

beforeEach(() => {
  get.mockReset();
  post.mockReset();
  put.mockReset();
});

afterEach(() => {
  cleanup();
});

describe("SettingsPage (the admin's cards)", () => {
  it("groups the refiner's fields into cards, each showing its value", async () => {
    mockSettingsGet();
    await renderLoaded();

    for (const group of ["Model", "Forge connection", "Instructions"]) {
      expect(screen.getByRole("heading", { name: group })).toBeTruthy();
    }
    for (const [field, value] of Object.entries(FIELD_VALUES)) {
      const control = screen.getByLabelText(LABELS[field]) as HTMLInputElement | HTMLTextAreaElement;
      expect(control.value, `${field} shows its value`).toBe(value);
    }
    // Nothing the registry did not answer with gets a card.
    expect(screen.queryByRole("heading", { name: "Other" })).toBeNull();
  });

  it("renders the provider row as the dropdown the registry serves, other rows as before", async () => {
    mockSettingsGet();
    await renderLoaded();

    const select = screen.getByLabelText("Provider") as HTMLSelectElement;
    expect(select.tagName).toBe("SELECT");
    expect(Array.from(select.options).map((o) => o.value)).toEqual(
      PROVIDER_OPTIONS.map((o) => o.value),
    );
    expect(select.value).toBe("ollama-cloud");
    // The row next to it stays a free-text input: the dropdown is the answer's
    // call, not a per-field rule in the page.
    expect(screen.getByLabelText("Model").tagName).toBe("INPUT");
  });

  it("keeps an off-list current value in the dropdown, labeled as such", async () => {
    // The shape the registry serves for a row holding a provider the list does
    // not name: the list plus the current value, labeled as the exception.
    mockSettingsGet({
      "refiner.llmProvider": {
        value: "legacy-cloud",
        dirty: true,
        options: [
          ...PROVIDER_OPTIONS,
          { value: "legacy-cloud", label: "legacy-cloud (current, not in list)" },
        ],
      },
    });
    await renderLoaded();

    const select = screen.getByLabelText("Provider") as HTMLSelectElement;
    expect(select.value).toBe("legacy-cloud");
    const values = Array.from(select.options).map((o) => o.value);
    expect(values).toEqual([...PROVIDER_OPTIONS.map((o) => o.value), "legacy-cloud"]);
  });

  it("lists Custom among the provider choices", async () => {
    // The dropdown's list comes from the registry answer, and the registry now
    // names `custom` — the setting that was missing when the issue was filed.
    mockSettingsGet();
    await renderLoaded();

    const select = screen.getByLabelText("Provider") as HTMLSelectElement;
    const values = Array.from(select.options).map((o) => o.value);
    expect(values).toContain("custom");
  });

  it("renders the OpenAI auth-method row as the dropdown the registry serves", async () => {
    // The second dropdown the registry answers with. The API key leads the list
    // because it is the shipped default and the hub's own answer for an agent
    // that never named a method.
    mockSettingsGet();
    await renderLoaded();

    const select = screen.getByLabelText("OpenAI auth method") as HTMLSelectElement;
    expect(select.tagName).toBe("SELECT");
    expect(Array.from(select.options).map((o) => o.value)).toEqual(
      AUTH_METHOD_OPTIONS.map((o) => o.value),
    );
    expect(select.value).toBe("APIKey");
  });

  it("sends the auth method as an ordinary row save", async () => {
    mockSettingsGet();
    put.mockImplementation(async (_path: string, body: any) => ({
      settings: afterBatch(makeSettings(), body.settings),
    }));
    await renderLoaded();

    fireEvent.change(screen.getByLabelText("OpenAI auth method"), {
      target: { value: "ChatGPTSignIn" },
    });
    fireEvent.click(within(saveBar()).getByRole("button", { name: "Save changes" }));

    await waitFor(() => expect(put).toHaveBeenCalledTimes(1));
    expect(put).toHaveBeenCalledWith("/settings", {
      settings: [{ role: "refiner", field: "llmOpenAIAuthMethod", value: "ChatGPTSignIn" }],
    });
  });

  it("names the auth-method row when the backend refuses a sign-in batch that carries a key", async () => {
    // The pair rule is the backend's — the hub answers an openAI block with both a
    // key and the sign-in with a 400. The page's job is to put the refusal on the
    // row the admin has to change, keeping the draft.
    mockSettingsGet();
    put.mockImplementation(async () => {
      const err: any = new Error("bad request");
      err.status = 400;
      err.body = {
        fields: [
          {
            role: "refiner",
            field: "llmOpenAIAuthMethod",
            error: "ChatGPT sign-in sends no API key (leave llmOpenAIKey empty to keep what the agents hold, or keep APIKey)",
          },
        ],
      };
      throw err;
    });
    await renderLoaded();

    fireEvent.change(screen.getByLabelText("OpenAI key"), { target: { value: "sk-both" } });
    fireEvent.change(screen.getByLabelText("OpenAI auth method"), {
      target: { value: "ChatGPTSignIn" },
    });
    fireEvent.click(within(saveBar()).getByRole("button", { name: "Save changes" }));

    expect(await screen.findByText(/ChatGPT sign-in sends no API key/)).toBeTruthy();
    expect((screen.getByLabelText("OpenAI auth method") as HTMLSelectElement).value).toBe(
      "ChatGPTSignIn",
    );
  });

  it("renders the custom pair in the Model card: the URL as text, the key as a write-only password", async () => {
    mockSettingsGet();
    await renderLoaded();

    const url = screen.getByLabelText("Custom provider URL") as HTMLInputElement;
    expect(url.type).toBe("text");
    expect(url.value).toBe(""); // nothing ships a custom endpoint

    // The key row: a password input that starts empty whatever the agents hold —
    // the plugin keeps no key to answer with — and reads as an untouched field.
    const key = screen.getByLabelText("Custom provider key") as HTMLInputElement;
    expect(key.type).toBe("password");
    expect(key.value).toBe("");
    expect(within(rowOf("Custom provider key")).getByText("Default")).toBeTruthy();
  });

  it("renders each vendor provider key as a write-only password row", async () => {
    // The three vendor keys share the custom key's contract: the plugin holds no
    // key at all, so every row starts empty and none of them reads as edited —
    // there is nothing here that an admin could have edited.
    mockSettingsGet();
    await renderLoaded();

    for (const label of ["OpenAI key", "Anthropic key", "Google Gemini key"]) {
      const key = screen.getByLabelText(label) as HTMLInputElement;
      expect(key.type).toBe("password");
      expect(key.value).toBe("");
      expect(within(rowOf(label)).getByText("Default")).toBeTruthy();
    }
  });

  it("sends a vendor key with the provider that uses it", async () => {
    // Moving a role onto OpenAI and typing its key is one batch: the provider
    // and the key save together, and the backend writes the key to the hub's
    // openAI block for the role's agent.
    mockSettingsGet();
    put.mockImplementation(async (_path: string, body: any) => ({
      settings: afterBatch(makeSettings(), body.settings),
    }));
    await renderLoaded();

    fireEvent.change(screen.getByLabelText("Provider"), { target: { value: "openai" } });
    fireEvent.change(screen.getByLabelText("OpenAI key"), { target: { value: "sk-openai" } });
    fireEvent.click(within(saveBar()).getByRole("button", { name: "Save changes" }));

    await waitFor(() => expect(put).toHaveBeenCalledTimes(1));
    expect(put).toHaveBeenCalledWith("/settings", {
      settings: [
        { role: "refiner", field: "llmProvider", value: "openai" },
        { role: "refiner", field: "llmOpenAIKey", value: "sk-openai" },
      ],
    });
  });

  it("never shows a key row as edited, whatever an older answer carried", async () => {
    // The plugin keeps no key, so no key row is edited — not now, and not from a
    // stale answer that still carries the dirty flag an older version set. The
    // row's edited tag used to mean "a key is stored here"; nothing here stores
    // a key any more, and a tag that could never be cleared would read as one.
    mockSettingsGet({ "refiner.llmCustomKey": { value: "", dirty: true } });
    await renderLoaded();

    const key = screen.getByLabelText("Custom provider key") as HTMLInputElement;
    expect(key.value).toBe("");
    expect(within(rowOf("Custom provider key")).getByText("Default")).toBeTruthy();
  });

  it("sends provider, URL and key together as one batch when the role moves to custom", async () => {
    // The batch contract: the backend checks the custom rule against the
    // post-batch values, so the three edits belong in one save — and the page
    // already collects every typed draft into one PUT.
    mockSettingsGet();
    put.mockImplementation(async (_path: string, body: any) => ({
      settings: afterBatch(makeSettings(), body.settings),
    }));
    await renderLoaded();

    fireEvent.change(screen.getByLabelText("Provider"), { target: { value: "custom" } });
    fireEvent.change(screen.getByLabelText("Custom provider URL"), {
      target: { value: "https://llm.example.com/v1" },
    });
    fireEvent.change(screen.getByLabelText("Custom provider key"), { target: { value: "sk-secret" } });
    fireEvent.click(within(saveBar()).getByRole("button", { name: "Save changes" }));

    await waitFor(() => expect(put).toHaveBeenCalledTimes(1));
    expect(put).toHaveBeenCalledWith("/settings", {
      settings: [
        { role: "refiner", field: "llmProvider", value: "custom" },
        { role: "refiner", field: "llmCustomUrl", value: "https://llm.example.com/v1" },
        { role: "refiner", field: "llmCustomKey", value: "sk-secret" },
      ],
    });
  });

  it("sends no key at all when the key field is left empty", async () => {
    // The admin edits the URL of a role already on custom and types no key: the
    // save does not name the key field, because an empty key says nothing — the
    // agents keep the key they hold. There is no reset to clear it either; a new
    // key typed over the old one is the only write a key row makes.
    mockSettingsGet({
      "refiner.llmProvider": { value: "custom", dirty: true, options: PROVIDER_OPTIONS },
      "refiner.llmCustomUrl": { value: "https://old.example.com/v1", dirty: true },
      "refiner.llmCustomKey": { value: "", dirty: true },
    });
    put.mockImplementation(async (_path: string, body: any) => ({
      settings: afterBatch(makeSettings(), body.settings),
    }));
    await renderLoaded();

    fireEvent.change(screen.getByLabelText("Custom provider URL"), {
      target: { value: "https://new.example.com/v1" },
    });
    fireEvent.click(within(saveBar()).getByRole("button", { name: "Save changes" }));

    await waitFor(() => expect(put).toHaveBeenCalledTimes(1));
    const sent = (put.mock.calls[0][1] as any).settings;
    expect(sent).toEqual([
      { role: "refiner", field: "llmCustomUrl", value: "https://new.example.com/v1" },
    ]);
  });

  it("offers no reset on a key row, because there is nothing here to reset", async () => {
    // A key row is never dirty, so it never carries the reset a dirty row shows:
    // the hub has no way to clear an agent's key, and a reset that promised to
    // would promise a write the hub cannot take.
    mockSettingsGet();
    await renderLoaded();

    expect(within(rowOf("Custom provider key")).queryByRole("button", { name: /Reset/ })).toBeNull();
    expect(put).not.toHaveBeenCalled();
  });

  it("names the key row when a saved key did not reach the agents", async () => {
    // The save itself landed — the settings are the PUT's promise — but the key
    // is a write onto the role's agents, and one of them refused it. Nothing
    // retries it: the plugin kept no copy to retry from, so the field says so.
    mockSettingsGet();
    put.mockImplementation(async (_path: string, body: any) => ({
      settings: afterBatch(makeSettings(), body.settings),
      fields: [
        {
          role: "refiner",
          field: "llmCustomKey",
          error: "the role runs on ollama-cloud, so a custom key has no agent to hold it",
        },
      ],
    }));
    await renderLoaded();

    fireEvent.change(screen.getByLabelText("Custom provider key"), { target: { value: "sk-secret" } });
    fireEvent.click(within(saveBar()).getByRole("button", { name: "Save changes" }));

    expect(await screen.findByText(/no agent to hold it/)).toBeTruthy();
    expect(put).toHaveBeenCalledTimes(1);
  });

  it("names the URL field when the backend refuses a custom provider without one", async () => {
    // The refusal is the backend's rule; the page's job is to put it on the row
    // the admin has to fill in, keeping the draft.
    mockSettingsGet();
    put.mockImplementation(async () => {
      const err: any = new Error("bad request");
      err.status = 400;
      err.body = {
        fields: [
          {
            role: "refiner",
            field: "llmProvider",
            error: "custom provider needs a URL (set llmCustomUrl in the same save)",
          },
        ],
      };
      throw err;
    });
    await renderLoaded();

    fireEvent.change(screen.getByLabelText("Provider"), { target: { value: "custom" } });
    fireEvent.click(within(saveBar()).getByRole("button", { name: "Save changes" }));

    expect(await screen.findByText(/custom provider needs a URL/)).toBeTruthy();
    expect((screen.getByLabelText("Provider") as HTMLSelectElement).value).toBe("custom");
  });

  it("renders a field the registry adds beyond the cards under Other, and saves it", async () => {
    // A field a newer plugin answers with (say, agent concurrency). The page has
    // no card for it, so it must still render and still save.
    const extra: Setting[] = ROLES.map((role) => ({
      role,
      field: "agentConcurrency",
      value: "2",
      dirty: false,
      default: "2",
      defaultVersion: RUNNING_VERSION,
    }));
    mockSettingsGet({}, extra);
    put.mockImplementation(async (_path: string, body: any) =>
      ({ settings: afterBatch(makeSettings({}, extra), body.settings) }),
    );
    await renderLoaded();

    expect(screen.getByRole("heading", { name: "Other" })).toBeTruthy();
    const other = screen.getByLabelText("agentConcurrency") as HTMLInputElement;
    expect(other.value).toBe("2");

    fireEvent.change(other, { target: { value: "4" } });
    fireEvent.click(screen.getByRole("button", { name: "Save changes" }));

    await waitFor(() => {
      expect(put).toHaveBeenCalledWith("/settings", {
        settings: [{ role: "refiner", field: "agentConcurrency", value: "4" }],
      });
    });
  });

  it("offers the three roles in the side nav, each showing its own values", async () => {
    mockSettingsGet({ "developer.llmModel": { value: "gpt-5", default: "gpt-5" } });
    await renderLoaded();

    expect((screen.getByLabelText("Model") as HTMLInputElement).value).toBe("llama3.3");

    fireEvent.click(roleNav("Developer"));
    await waitFor(() => {
      expect((screen.getByLabelText("Model") as HTMLInputElement).value).toBe("gpt-5");
    });
    expect(screen.getByRole("heading", { level: 2, name: "Developer" })).toBeTruthy();
  });

  it("counts each role's edited fields on its nav button, and shows no count for a clean role", async () => {
    mockSettingsGet({
      "developer.llmModel": { value: "gpt-5", dirty: true },
      "developer.personaText": { value: "my persona", dirty: true },
    });
    await renderLoaded();

    expect(roleNav("Developer").textContent).toContain("2");
    expect(roleNav("Refiner").textContent).toBe("Refiner");
    expect(roleNav("Reviewer").textContent).toBe("Reviewer");
  });

  it("tags an untouched field Default and an edited one Overridden", async () => {
    mockSettingsGet({ "refiner.llmModel": { value: "qwen3", dirty: true } });
    await renderLoaded();

    expect(within(rowOf("Model")).getByText("Overridden")).toBeTruthy();
    expect(within(rowOf("Provider")).getByText("Default")).toBeTruthy();
  });

  it("puts the Overridden default line and its reset on an edited field, truncated", async () => {
    const longDefault = "You are the Refiner. Restate the goal of a new issue, list acceptance criteria.";
    mockSettingsGet({
      "refiner.personaText": { value: "my persona", dirty: true, default: longDefault },
    });
    await renderLoaded();

    const row = rowOf("Persona");
    expect(row.textContent).toContain(longDefault.slice(0, 40) + "…");
    expect(row.textContent).not.toContain(longDefault);
    expect(rowButton("Persona", "Reset to default")).toBeTruthy();
  });

  it("shows no reset on a field that still holds its default", async () => {
    mockSettingsGet();
    await renderLoaded();

    expect(screen.queryByRole("button", { name: "Reset to default" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Adopt new default" })).toBeNull();
  });
});

describe("SettingsPage (batch save)", () => {
  it("collects typed changes as drafts and sends them all in one PUT", async () => {
    const current = makeSettings();
    mockSettingsGet();
    put.mockImplementation(async (_path: string, body: any) => ({
      settings: afterBatch(current, body.settings),
    }));
    await renderLoaded();

    fireEvent.change(screen.getByLabelText("Model"), { target: { value: "llama3.1" } });
    fireEvent.change(screen.getByLabelText("Provider"), { target: { value: "opencode" } });
    fireEvent.change(screen.getByLabelText("Persona"), { target: { value: "new persona" } });

    // Nothing is written until the bar's button is pressed.
    expect(put).not.toHaveBeenCalled();
    expect(within(saveBar()).getByText(/3 unsaved changes/)).toBeTruthy();
    expect(within(rowOf("Model")).getByText("Unsaved")).toBeTruthy();

    fireEvent.click(within(saveBar()).getByRole("button", { name: "Save changes" }));

    await waitFor(() => {
      expect(put).toHaveBeenCalledTimes(1);
    });
    expect(put).toHaveBeenCalledWith("/settings", {
      settings: [
        { role: "refiner", field: "llmModel", value: "llama3.1" },
        { role: "refiner", field: "llmProvider", value: "opencode" },
        { role: "refiner", field: "personaText", value: "new persona" },
      ],
    });
    // Saved: the bar goes, the drafts retire, the rows now read as edited.
    await waitFor(() => {
      expect(screen.queryByRole("region", { name: "Unsaved changes" })).toBeNull();
    });
    expect(within(rowOf("Model")).getByText("Overridden")).toBeTruthy();
    expect(within(rowOf("Model")).queryByText("Unsaved")).toBeNull();
  });

  it("sends a batch that spans roles in the same request", async () => {
    mockSettingsGet();
    put.mockImplementation(async (_path: string, body: any) => ({
      settings: afterBatch(makeSettings(), body.settings),
    }));
    await renderLoaded();

    fireEvent.change(screen.getByLabelText("Model"), { target: { value: "llama3.1" } });
    fireEvent.click(roleNav("Reviewer"));
    fireEvent.change(screen.getByLabelText("Model"), { target: { value: "gpt-5" } });

    expect(within(saveBar()).getByText(/2 unsaved changes/)).toBeTruthy();
    fireEvent.click(within(saveBar()).getByRole("button", { name: "Save changes" }));

    await waitFor(() => expect(put).toHaveBeenCalledTimes(1));
    expect(put).toHaveBeenCalledWith("/settings", {
      settings: [
        { role: "refiner", field: "llmModel", value: "llama3.1" },
        { role: "reviewer", field: "llmModel", value: "gpt-5" },
      ],
    });
  });

  it("shows the bar only while a draft exists, and drops a draft typed back to the saved value", async () => {
    mockSettingsGet();
    await renderLoaded();

    expect(screen.queryByRole("region", { name: "Unsaved changes" })).toBeNull();

    fireEvent.change(screen.getByLabelText("Model"), { target: { value: "llama3.1" } });
    expect(within(saveBar()).getByText(/1 unsaved change\b/)).toBeTruthy();

    fireEvent.change(screen.getByLabelText("Model"), { target: { value: "llama3.3" } });
    expect(screen.queryByRole("region", { name: "Unsaved changes" })).toBeNull();
  });

  it("discards every draft at once without a request", async () => {
    mockSettingsGet();
    await renderLoaded();

    fireEvent.change(screen.getByLabelText("Model"), { target: { value: "llama3.1" } });
    fireEvent.change(screen.getByLabelText("Persona"), { target: { value: "new persona" } });
    fireEvent.click(within(saveBar()).getByRole("button", { name: "Discard" }));

    expect(screen.queryByRole("region", { name: "Unsaved changes" })).toBeNull();
    expect((screen.getByLabelText("Model") as HTMLInputElement).value).toBe("llama3.3");
    expect((screen.getByLabelText("Persona") as HTMLTextAreaElement).value).toBe("the shipped persona");
    expect(put).not.toHaveBeenCalled();
  });

  it("keeps the drafts and names each refused field when the batch is refused", async () => {
    // The PUT lands whole or not at all; a refusal comes back with each bad field
    // named (the console's client puts the parsed body on the error).
    mockSettingsGet();
    put.mockImplementation(async () => {
      const err: any = new Error("bad request");
      err.status = 400;
      err.body = {
        fields: [
          { role: "refiner", field: "llmModel", error: "model must not be empty" },
          { role: "refiner", field: "mcpUrl", error: "not a URL" },
        ],
      };
      throw err;
    });
    await renderLoaded();

    fireEvent.change(screen.getByLabelText("Model"), { target: { value: "" } });
    fireEvent.change(screen.getByLabelText("Forge MCP server URL"), { target: { value: "nope" } });
    fireEvent.click(within(saveBar()).getByRole("button", { name: "Save changes" }));

    expect(await screen.findByText("model must not be empty")).toBeTruthy();
    expect(within(rowOf("Forge MCP server URL")).getByText("not a URL")).toBeTruthy();
    // The typed drafts are still where the admin left them, and the bar still counts them.
    expect((screen.getByLabelText("Model") as HTMLInputElement).value).toBe("");
    expect(within(saveBar()).getByText(/2 unsaved changes/)).toBeTruthy();
  });

  it("shows a refused batch that names no field as a notice, keeping the drafts", async () => {
    mockSettingsGet();
    put.mockImplementation(async () => {
      throw new Error("the hub is not answering");
    });
    await renderLoaded();

    fireEvent.change(screen.getByLabelText("Model"), { target: { value: "llama3.1" } });
    fireEvent.click(within(saveBar()).getByRole("button", { name: "Save changes" }));

    await screen.findByText("the hub is not answering");
    expect((screen.getByLabelText("Model") as HTMLInputElement).value).toBe("llama3.1");
    expect(screen.getByRole("region", { name: "Unsaved changes" })).toBeTruthy();
  });

  it("keeps the bar's buttons out of reach while the batch is in flight", async () => {
    mockSettingsGet();
    let resolvePut: (value: { settings: Setting[] }) => void = () => {};
    put.mockImplementation(
      () =>
        new Promise<{ settings: Setting[] }>((resolve) => {
          resolvePut = resolve;
        }),
    );
    await renderLoaded();

    fireEvent.change(screen.getByLabelText("Model"), { target: { value: "llama3.1" } });
    fireEvent.click(within(saveBar()).getByRole("button", { name: "Save changes" }));

    // One click in, the buttons are out of reach for the duration of the call —
    // a second save would ask the backend to write the same drafts twice.
    await waitFor(() => {
      const save = within(saveBar()).getByRole("button", { name: "Saving…" }) as HTMLButtonElement;
      expect(save.disabled).toBe(true);
      expect((within(saveBar()).getByRole("button", { name: "Discard" }) as HTMLButtonElement).disabled).toBe(true);
    });

    resolvePut({ settings: makeSettings({ "refiner.llmModel": { value: "llama3.1", dirty: true } }) });
    await waitFor(() => {
      expect(screen.queryByRole("region", { name: "Unsaved changes" })).toBeNull();
    });
  });
});

describe("SettingsPage (reset and the winner rule)", () => {
  it("resets an edited field back to its default straight away, with no batch", async () => {
    mockSettingsGet({
      "refiner.llmModel": { value: "edited-away", dirty: true, default: "llama3.3" },
    });
    put.mockImplementation(async (path: string, body: any) => {
      expect(path).toBe("/settings");
      expect(body).toEqual({ settings: [{ role: "refiner", field: "llmModel", reset: true }] });
      return { settings: makeSettings() }; // the reset re-adopts the default
    });
    await renderLoaded();

    fireEvent.click(rowButton("Model", "Reset to default"));

    await waitFor(() => {
      expect((screen.getByLabelText("Model") as HTMLInputElement).value).toBe("llama3.3");
    });
    expect(put).toHaveBeenCalledTimes(1);
  });

  it("warns about a stale edit and shows what the new version ships", async () => {
    // The admin edited away from v0.0.9's default; the plugin now runs v0.1.0, whose
    // default differs. The edit stands — it is never silently lost — and the box
    // says there is an update.
    mockSettingsGet({
      "refiner.personaText": {
        value: "my older persona",
        dirty: true,
        default: "the shipped persona",
        defaultVersion: "0.0.9",
      },
    });
    await renderLoaded();

    const box = screen.getByRole("group", { name: "Persona: update available" });
    expect(box.textContent).toContain("Update available.");
    expect(box.textContent).toContain(
      "Plugin v0.1.0 ships a new default for this field. Your edit, made on v0.0.9, is kept until you adopt it.",
    );
    // The stale edit is still shown in the field, not replaced by the default.
    expect((screen.getByLabelText("Persona") as HTMLTextAreaElement).value).toBe("my older persona");
    // The stale box replaces the one-line default: there is nothing to reset to
    // that the box does not already offer.
    expect(screen.queryByRole("button", { name: "Reset to default" })).toBeNull();
  });

  it("compares the edit with the shipped default side by side, and hides it again", async () => {
    mockSettingsGet({
      "refiner.personaText": {
        value: "my older persona",
        dirty: true,
        default: "the shipped persona",
        defaultVersion: "0.0.9",
      },
    });
    await renderLoaded();

    const box = screen.getByRole("group", { name: "Persona: update available" });
    expect(within(box).queryByText("Your version")).toBeNull();

    fireEvent.click(within(box).getByRole("button", { name: "Compare" }));
    expect(within(box).getByText("Your version")).toBeTruthy();
    expect(within(box).getByText("Default in v0.1.0")).toBeTruthy();
    expect(box.textContent).toContain("my older persona");
    expect(box.textContent).toContain("the shipped persona");

    fireEvent.click(within(box).getByRole("button", { name: "Hide comparison" }));
    expect(within(box).queryByText("Your version")).toBeNull();
  });

  it("adopts the new default from the stale box, which is the same reset", async () => {
    mockSettingsGet({
      "refiner.personaText": {
        value: "my older persona",
        dirty: true,
        default: "the shipped persona",
        defaultVersion: "0.0.9",
      },
    });
    put.mockImplementation(async (_path: string, body: any) => {
      expect(body).toEqual({ settings: [{ role: "refiner", field: "personaText", reset: true }] });
      return { settings: makeSettings() };
    });
    await renderLoaded();

    const box = screen.getByRole("group", { name: "Persona: update available" });
    fireEvent.click(within(box).getByRole("button", { name: "Adopt new default" }));

    await waitFor(() => {
      expect((screen.getByLabelText("Persona") as HTMLTextAreaElement).value).toBe("the shipped persona");
    });
    expect(screen.queryByRole("group", { name: "Persona: update available" })).toBeNull();
  });

  it("shows no upgrade hint for an edit made away from the running version's default", async () => {
    mockSettingsGet({
      "refiner.personaText": { value: "my persona", dirty: true, defaultVersion: RUNNING_VERSION },
    });
    await renderLoaded();

    expect(screen.queryByRole("group", { name: /update available/ })).toBeNull();
    // The field is still tagged as overridden — the tag and the box are different things.
    expect(within(rowOf("Persona")).getByText("Overridden")).toBeTruthy();
  });

  it("shows no hint at all for a field nobody edited", async () => {
    mockSettingsGet();
    await renderLoaded();

    expect(screen.queryByText("Overridden")).toBeNull();
    expect(screen.queryByRole("group", { name: /update available/ })).toBeNull();
  });

  it("says a per-field reset that fails, on the field, and keeps the edit", async () => {
    mockSettingsGet({
      "refiner.llmModel": { value: "edited-away", dirty: true, default: "llama3.3" },
    });
    put.mockImplementation(async () => {
      throw new Error("reset refused");
    });
    await renderLoaded();

    fireEvent.click(rowButton("Model", "Reset to default"));

    expect(await within(rowOf("Model")).findByText("reset refused")).toBeTruthy();
    expect((screen.getByLabelText("Model") as HTMLInputElement).value).toBe("edited-away");
  });
});

describe("SettingsPage (Reconcile now)", () => {
  it("says what a reconcile pass fixed, and dismisses it", async () => {
    mockSettingsGet();
    post.mockImplementation(async (path: string) => {
      if (path === "/settings/reconcile") {
        return {
          updated: [
            {
              install: "apps",
              role: "developer",
              kind: "agent",
              field: "llmModel",
              from: "old-model",
              to: "llama3.3",
            },
          ],
          refused: [],
        };
      }
      throw new Error(`unexpected POST ${path}`);
    });
    await renderLoaded();

    fireEvent.click(screen.getByRole("button", { name: "Reconcile now" }));

    const notice = (await screen.findByText("Reconciled: 1 updated, 0 refused")).closest(
      '[data-testid="notice"]',
    ) as HTMLElement;
    expect(notice.textContent).toContain("apps/developer: agent.llmModel");
    expect(notice.textContent).toContain("old-model");
    expect(notice.textContent).toContain("llama3.3");

    fireEvent.click(within(notice).getByRole("button", { name: "Dismiss" }));
    expect(screen.queryByText("Reconciled: 1 updated, 0 refused")).toBeNull();
  });

  it("lists what the hub refused, next to what it fixed", async () => {
    mockSettingsGet();
    post.mockImplementation(async (path: string) => {
      if (path === "/settings/reconcile") {
        return {
          updated: [],
          refused: [
            {
              install: "apps",
              role: "refiner",
              kind: "persona",
              field: "personaText",
              error: "hub is not answering",
            },
          ],
        };
      }
      throw new Error(`unexpected POST ${path}`);
    });
    await renderLoaded();

    fireEvent.click(screen.getByRole("button", { name: "Reconcile now" }));

    const notice = (await screen.findByText("Reconciled: 0 updated, 1 refused")).closest(
      '[data-testid="notice"]',
    ) as HTMLElement;
    expect(notice.textContent).toContain("apps/refiner: persona.personaText");
    expect(notice.textContent).toContain("hub is not answering");
  });

  it("says so when a reconcile pass finds nothing to fix", async () => {
    mockSettingsGet();
    post.mockImplementation(async (path: string) => {
      if (path === "/settings/reconcile") return { updated: [], refused: [] };
      throw new Error(`unexpected POST ${path}`);
    });
    await renderLoaded();

    fireEvent.click(screen.getByRole("button", { name: "Reconcile now" }));

    await screen.findByText("Checked: nothing to fix.");
  });

  it("warns when the LLM check finds a provider that cannot serve", async () => {
    // The issue's symptom: the button must not say "nothing to fix" when the
    // configured provider is unreachable — it names the role, the pair and the reason.
    mockSettingsGet();
    post.mockImplementation(async (path: string) => {
      if (path === "/settings/reconcile") {
        return {
          updated: [],
          refused: [],
          llmChecks: [
            { role: "refiner", provider: "ollama-cloud", model: "llama3.3", state: "ok" },
            {
              role: "developer",
              provider: "spurious",
              model: "gone-model",
              state: "unreachable",
              reason: "provider refused: connection reset",
            },
          ],
        };
      }
      throw new Error(`unexpected POST ${path}`);
    });
    await renderLoaded();

    fireEvent.click(screen.getByRole("button", { name: "Reconcile now" }));

    const notice = (await screen.findByText(/gone-model @spurious/)).closest(
      '[data-testid="notice"]',
    ) as HTMLElement;
    expect(notice.dataset.variant).toBe("warn");
    expect(notice.textContent).toContain("developer");
    expect(notice.textContent).toContain("unreachable");
    expect(notice.textContent).toContain("provider refused: connection reset");
    expect(screen.queryByText("Checked: nothing to fix.")).toBeNull();
  });

  it("warns when the LLM check could not be asked at all", async () => {
    // "Cannot check" is its own state: an old hub that would not answer must never
    // read as health.
    mockSettingsGet();
    post.mockImplementation(async (path: string) => {
      if (path === "/settings/reconcile") {
        return {
          updated: [],
          refused: [],
          llmChecks: [
            {
              role: "refiner",
              provider: "ollama-cloud",
              model: "llama3.3",
              state: "unknown",
              reason: "cannot check: the hub would not answer the check",
            },
          ],
        };
      }
      throw new Error(`unexpected POST ${path}`);
    });
    await renderLoaded();

    fireEvent.click(screen.getByRole("button", { name: "Reconcile now" }));

    const notice = (
      await screen.findByText(/cannot check: the hub would not answer/)
    ).closest('[data-testid="notice"]') as HTMLElement;
    expect(notice.dataset.variant).toBe("warn");
    expect(notice.textContent).toContain("unknown");
    expect(screen.queryByText("Checked: nothing to fix.")).toBeNull();
  });

  it("still says nothing to fix when every check passed", async () => {
    // Reachable providers are clean: ok lines are not flags, and a fully clean
    // summary — drift-free and all-ok — still reads as the confirmation it is.
    mockSettingsGet();
    post.mockImplementation(async (path: string) => {
      if (path === "/settings/reconcile") {
        return {
          updated: [],
          refused: [],
          llmChecks: [
            { role: "refiner", provider: "ollama-cloud", model: "llama3.3", state: "ok" },
            { role: "developer", provider: "ollama-cloud", model: "llama3.3", state: "ok" },
          ],
        };
      }
      throw new Error(`unexpected POST ${path}`);
    });
    await renderLoaded();

    fireEvent.click(screen.getByRole("button", { name: "Reconcile now" }));

    await screen.findByText("Checked: nothing to fix.");
  });

  it("shows the drift answer and the LLM answer together", async () => {
    mockSettingsGet();
    post.mockImplementation(async (path: string) => {
      if (path === "/settings/reconcile") {
        return {
          updated: [
            {
              install: "apps",
              role: "reviewer",
              kind: "agent",
              field: "llm",
              from: "old @elsewhere",
              to: "llama3.3 @ollama-cloud",
            },
          ],
          refused: [],
          llmChecks: [
            { role: "reviewer", provider: "ollama-cloud", model: "llama3.3", state: "ok" },
          ],
        };
      }
      throw new Error(`unexpected POST ${path}`);
    });
    await renderLoaded();

    fireEvent.click(screen.getByRole("button", { name: "Reconcile now" }));

    const notice = (await screen.findByText(/Reconciled: 1 updated, 0 refused/)).closest(
      '[data-testid="notice"]',
    ) as HTMLElement;
    expect(notice.textContent).toContain("apps/reviewer: agent.llm");
    expect(notice.textContent).toContain("reviewer: llama3.3 @ollama-cloud — ok");
  });

  it("counts a flagged LLM check in the header's checked tag", async () => {
    // "In sync" must not stand for a page whose provider cannot serve: the flagged
    // count carries the non-ok checks too.
    mockSettingsGet();
    post.mockImplementation(async (path: string) => {
      if (path === "/settings/reconcile") {
        return {
          updated: [],
          refused: [],
          llmChecks: [
            {
              role: "refiner",
              provider: "spurious",
              model: "gone-model",
              state: "unreachable",
              reason: "no route",
            },
          ],
        };
      }
      throw new Error(`unexpected POST ${path}`);
    });
    await renderLoaded();

    fireEvent.click(screen.getByRole("button", { name: "Reconcile now" }));

    await screen.findByText(/^1 flagged · checked /);
    expect(screen.queryByText(/In sync · checked/)).toBeNull();
  });

  it("shows when the installs were last seen in sync, only once a pass has run here", async () => {
    mockSettingsGet();
    post.mockImplementation(async () => ({ updated: [], refused: [] }));
    await renderLoaded();

    expect(screen.queryByText(/In sync · checked/)).toBeNull();

    fireEvent.click(screen.getByRole("button", { name: "Reconcile now" }));

    await screen.findByText(/^In sync · checked /);
  });

  it("tells the admin when the reconcile pass itself cannot run", async () => {
    mockSettingsGet();
    post.mockImplementation(async () => {
      throw new Error("the hub is not answering");
    });
    await renderLoaded();

    fireEvent.click(screen.getByRole("button", { name: "Reconcile now" }));

    await screen.findByText("the hub is not answering");
  });
});

describe("SettingsPage (access)", () => {
  it("says only admins when the GET says only admins", async () => {
    get.mockImplementation(async () => {
      const err: any = new Error("forbidden");
      err.status = 403;
      throw err;
    });
    render(<SettingsPage />);

    await screen.findByText("Only admins can access the settings.");
  });
});

describe("SettingsPage (managed resources)", () => {
  it("lists the fixed membership as provided by the plugin, read-only", async () => {
    mockSettingsGet();
    await renderLoaded();

    fireEvent.click(screen.getByRole("button", { name: "Managed resources" }));

    expect(await screen.findByRole("heading", { name: "Managed resources" })).toBeTruthy();
    const table = screen.getByTestId("register-table");
    expect(within(table).getByText("forgejo-forgejo")).toBeTruthy();
    expect(within(table).getByText("plugin-forgejo")).toBeTruthy();
    expect(within(table).getByText("forgejo-mcp-<install>")).toBeTruthy();
    expect(within(table).getAllByText(`Plugin v${RUNNING_VERSION}`)).toHaveLength(3);
    expect(within(table).getByText("Per install")).toBeTruthy();
    expect(within(table).getAllByText("All installs")).toHaveLength(2);
    // The per-install forge MCP server is named too, pointed at by the MCP fields.
    expect(document.body.textContent).toContain("pointed at by the MCP fields below");
  });

  it("hides the role fields while the resources are shown, and brings them back", async () => {
    mockSettingsGet();
    await renderLoaded();

    fireEvent.click(screen.getByRole("button", { name: "Managed resources" }));
    expect(screen.queryByLabelText("Model")).toBeNull();

    fireEvent.click(roleNav("Refiner"));
    expect(screen.getByLabelText("Model")).toBeTruthy();
  });
});
