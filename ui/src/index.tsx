// The console imports exactly this module (manifest `ui.entry`, served at
// /api/v1/plugins/forgejo/ui/index.js). A slot names its component in the
// manifest — `ui.slots` maps a slot id to an exported name, so "ForgejoAgentPanel"
// is what fills the `agent.panel` slot — and since manifest API 1.1 a page names its
// component too, through `ui.pages[].component`, which is how "LivePage" fills the
// page "/now". React and the SDK stay external; this file must export components
// only, nothing with side effects.
export { LivePage } from "./LivePage";
export { WorkPage } from "./WorkPage";
export { SettingsPage } from "./SettingsPage";
export { SetupPage } from "./setup/SetupPage";
export { ForgejoInvocationPanel } from "./ForgejoInvocationPanel";
export { ForgejoAgentPanel } from "./ForgejoAgentPanel";
