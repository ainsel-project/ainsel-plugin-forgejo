// The one place `@ainsel/plugin-sdk` is mocked (vitest `setupFiles`, so it
// applies to every test file before it imports a component). The real package
// is installed for its types and stays external in the build; at test time the
// console is not there to register host primitives or fill a context, so
// ./sdk.tsx answers for it — see that file for what it stands in for.
import { vi } from "vitest";

vi.mock("@ainsel/plugin-sdk", () => import("./sdk"));
