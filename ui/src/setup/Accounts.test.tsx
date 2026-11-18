// S3 — the Forgejo accounts step. Its one input is the admin token, and what an
// empty one means is the router's business, not this screen's: on a new install
// the token is required, on an edit it is optional and empty means "keep the
// accounts as they are" (spec §5[3]). So the screen is told which walk it is in.
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Accounts } from "./Accounts";
import { newInput } from "./model";

const TOKEN_HINT = "Personal access token with write:admin scope";

function renderAccounts(
  props: { editing: boolean; adminToken?: string; rolesFilled?: boolean } = {
    editing: false,
  },
) {
  const input = newInput();
  input.slug = "apps";
  input.org = "apps";
  input.adminToken = props.adminToken ?? "";
  if (props.rolesFilled) {
    // The state a walk into S4 leaves behind: an edit has it, and so can a new
    // install that was walked through and came back.
    input.roles.developer = {
      ...input.roles.developer,
      runtime: "registry.example.com/node:20",
      agentName: "forgejo-developer-apps",
    };
  }

  const onNext = vi.fn();
  render(
    <Accounts
      input={input}
      setInput={vi.fn()}
      editing={props.editing}
      onNext={onNext}
      onBack={vi.fn()}
    />,
  );
  return { onNext };
}

describe("Accounts (S3)", () => {
  afterEach(cleanup);
  it("asks for the admin token and blocks Next on a new install", () => {
    renderAccounts({ editing: false });

    expect(screen.getByPlaceholderText(TOKEN_HINT)).toBeTruthy();
    expect(screen.queryByText(/leave empty to keep/)).toBeNull();
    // Without the token there is no account work to preview, so there is no Next.
    expect(screen.queryByRole("button", { name: "Continue" })).toBeNull();
  });

  it("still asks for the token when the roles carry values", () => {
    // The screen used to guess "editing" from the roles being filled, which made
    // a new install that had been through S4 offer to keep a token it never had.
    renderAccounts({ editing: false, rolesFilled: true });

    expect(screen.getByPlaceholderText(TOKEN_HINT)).toBeTruthy();
    expect(screen.queryByPlaceholderText(/leave empty to keep/)).toBeNull();
    expect(screen.queryByRole("button", { name: "Continue" })).toBeNull();
  });

  it("offers to keep the stored token when editing", () => {
    renderAccounts({ editing: true });

    const token = screen.getByPlaceholderText(/leave empty to keep/);
    expect((token as HTMLInputElement).value).toBe("");
    expect(screen.queryByPlaceholderText(TOKEN_HINT)).toBeNull();
  });

  it("advances from an edit without a token", async () => {
    const user = userEvent.setup();
    const { onNext } = renderAccounts({ editing: true });

    // The account work is optional on an edit: the existing logins, teams and
    // permissions are kept, so Next is offered with an empty token.
    await user.click(screen.getByRole("button", { name: "Continue" }));

    expect(onNext).toHaveBeenCalledTimes(1);
  });
});
