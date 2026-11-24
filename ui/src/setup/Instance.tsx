import type { ChangeEvent } from "react";
import { Button, Field, Input, Notice, Select } from "@ainsel/plugin-sdk";
import type { ForgejoInstance } from "../api";
import type { Input as InputType } from "./model";
import { FIELD_GRID_STYLE, StepFooter } from "./Wizard";

interface Group {
  id: string;
  name: string;
  role: string;
}

interface InstanceProps {
  input: InputType;
  setInput: (input: InputType) => void;
  groups: Group[];
  pluginGroups: string[];
  // The Forgejo instances the admin configured on the Settings page. The step
  // selects over them; it no longer offers a free-text address, because a typed
  // one is a typo that becomes an install pointed at the wrong host.
  instances: ForgejoInstance[];
  // The install's own address, on an edit. It is what the step offers as "keep
  // it" when that address is not on the configured list — the edit form's one
  // legal way to leave the address alone.
  currentBaseUrl?: string;
  onNext: () => void;
  onBack: () => void;
}

// SettingsLink points the admin at the page that owns the list. The console
// serves the plugin's pages under its own mount, so this is a plain link rather
// than a route the wizard owns.
function SettingsLink() {
  return (
    <a className="forgejo-settings-link" href="settings" style={{ color: "inherit", textDecoration: "underline" }}>
      Settings → Forgejo instances
    </a>
  );
}

export function Instance({
  input,
  setInput,
  groups,
  pluginGroups,
  instances,
  currentBaseUrl,
  onNext,
  onBack,
}: InstanceProps) {
  const isGroupBlocked = input.groupId && !pluginGroups.includes(input.groupId);

  // An edit whose install lives on an address nobody configured is not stuck:
  // leaving the select untouched keeps that address, which is what the plan's
  // blank-baseUrl rule means. It is flagged here rather than hidden, because
  // the address is about to be the thing an admin wonders about.
  const keepCurrent = !!currentBaseUrl && !instances.some((i) => i.baseUrl === currentBaseUrl);
  const hasInstances = instances.length > 0;

  // Nothing to pick and no install to keep: the step cannot go on. This is the
  // fresh-install case on a plugin whose admin has not configured a Forgejo yet.
  // On a create — no install address to keep — Continue also waits until the admin
  // actually picks one: clicking through with the select untouched would plan the
  // install against the plugin's env address and store it with no address of its own.
  const canContinue = (hasInstances || !!currentBaseUrl) && (!!currentBaseUrl || !!input.baseUrl);

  return (
    <div className="forgejo-instance" style={{ display: "flex", flexDirection: "column", gap: "1.25rem" }}>
      {isGroupBlocked && (
        <Notice variant="err">
          This group is not granted to the plugin. An admin grants it in the AInsel console under Plugins → Forgejo →
          Group Access (or with set_plugin_group_access).
        </Notice>
      )}

      {!hasInstances && (
        <Notice variant="err">
          No Forgejo instance is configured, so there is nothing to install against. Add one on <SettingsLink />.
        </Notice>
      )}

      {keepCurrent && (
        <Notice variant="warn">
          This install points at <code>{currentBaseUrl}</code>, which is not a configured instance. Leaving the choice
          untouched keeps that address; picking a listed one moves the install.
        </Notice>
      )}

      <div style={FIELD_GRID_STYLE}>
        <Field htmlFor="instance-slug" label="Install name" hint="Lower case, used in every resource name.">
          <Input
            id="instance-slug"
            value={input.slug}
            onChange={(e: ChangeEvent<HTMLInputElement>) => setInput({ ...input, slug: e.target.value })}
            placeholder="e.g., apps"
          />
        </Field>

        <Field htmlFor="instance-group" label="Owning group" hint="Who can see and run this install's agents.">
          <Select
            id="instance-group"
            value={input.groupId}
            onChange={(id: string) => setInput({ ...input, groupId: id })}
            options={groups.map((g) => ({ value: g.id, label: `${g.name} (${g.role})` }))}
            emptyLabel="Select a group"
          />
        </Field>

        {hasInstances && (
          <Field
            htmlFor="instance-base-url"
            label="Forgejo instance"
            hint={
              instances.length === 1
                ? "The only instance configured on the plugin's Settings page."
                : "One of the addresses configured on the plugin's Settings page."
            }
          >
            <Select
              id="instance-base-url"
              value={input.baseUrl}
              onChange={(url: string) => setInput({ ...input, baseUrl: url })}
              options={instances.map((i) => ({ value: i.baseUrl, label: `${i.name} — ${i.baseUrl}` }))}
              emptyLabel={keepCurrent ? `Keep ${currentBaseUrl}` : "Select an instance"}
            />
          </Field>
        )}

        <Field htmlFor="instance-org" label="Organization" hint="Agents act on every repository in it.">
          <Input
            id="instance-org"
            value={input.org}
            onChange={(e: ChangeEvent<HTMLInputElement>) => setInput({ ...input, org: e.target.value })}
            placeholder="e.g., example-org"
          />
        </Field>
      </div>

      <StepFooter onBack={onBack}>
        {!isGroupBlocked && canContinue && (
          <Button variant="primary" onClick={onNext} className="forgejo-next-btn">
            Continue
          </Button>
        )}
      </StepFooter>
    </div>
  );
}
