# Changelog

## [0.3.1](https://github.com/ainsel-project/ainsel-plugin-forgejo/compare/v0.3.0...v0.3.1) (2026-11-28)


### Bug Fixes

* **#18:** shared FieldGrid/CardGrid align fields on a fixed grid ([#21](https://github.com/ainsel-project/ainsel-plugin-forgejo/issues/21)) ([cf5b23d](https://github.com/ainsel-project/ainsel-plugin-forgejo/commit/cf5b23d60da69f41091f8a447c9ea36d1ed00eb6))
* **#20:** single-source every version string through release-please ([#23](https://github.com/ainsel-project/ainsel-plugin-forgejo/issues/23)) ([7232c5a](https://github.com/ainsel-project/ainsel-plugin-forgejo/commit/7232c5a1c7399126e983ad3bbbea518fc237c698))

## [0.3.0](https://github.com/ainsel-project/ainsel-plugin-forgejo/compare/v0.2.0...v0.3.0) (2026-11-22)


### Features

* make the PR patrol cadence one global setting, 10 minutes to 24 hours ([#16](https://github.com/ainsel-project/ainsel-plugin-forgejo/issues/16)) ([7b4b7b1](https://github.com/ainsel-project/ainsel-plugin-forgejo/commit/7b4b7b1761a99d7e5d5974ee4f1b2f4c1eb1ce62))

## [0.2.0](https://github.com/ainsel-project/ainsel-plugin-forgejo/compare/v0.1.0...v0.2.0) (2026-11-22)


### Features

* **api:** answer Check now with the drift pass and the LLM checks ([b4d33c7](https://github.com/ainsel-project/ainsel-plugin-forgejo/commit/b4d33c7c1d646694c0aa166c63a16d05665ce48b))
* **api:** write a saved key onto the agents, and refuse one per field ([c1e0400](https://github.com/ainsel-project/ainsel-plugin-forgejo/commit/c1e0400d96445112c630a0d557382490707e5936))
* choose the patrol's cadence in the roles step ([7938f59](https://github.com/ainsel-project/ainsel-plugin-forgejo/commit/7938f592ca14eb4ae212197c4cfa108c6b973bfe)), closes [#13](https://github.com/ainsel-project/ainsel-plugin-forgejo/issues/13)
* **docs-site:** publish docs/ on GitHub Pages ([9378b3a](https://github.com/ainsel-project/ainsel-plugin-forgejo/commit/9378b3a7e8193c4c6161a06bb20a2ca5396d9f70))
* **hub:** add CheckLLM surface for the LLM reachability check ([d9cef79](https://github.com/ainsel-project/ainsel-plugin-forgejo/commit/d9cef7906ce89f85845cf3c379ba841f3f1ab072))
* make Check now actually check the LLM configuration ([126ea99](https://github.com/ainsel-project/ainsel-plugin-forgejo/commit/126ea99531f70554ea255a0cd14907d4c8983489))
* make the developer's PR patrol cadence a wizard choice ([ec6e574](https://github.com/ainsel-project/ainsel-plugin-forgejo/commit/ec6e57490d0486ad5fb9c6e3022681a444961d7f)), closes [#13](https://github.com/ainsel-project/ainsel-plugin-forgejo/issues/13)
* **plugin:** forward a stored key to the agents at startup, then drop the row ([e4031c9](https://github.com/ainsel-project/ainsel-plugin-forgejo/commit/e4031c963edd429e59b8287ccd0f0fcdf2aba64d))
* publish the docs as a GitHub Pages site ([e8224af](https://github.com/ainsel-project/ainsel-plugin-forgejo/commit/e8224afd151e010d56cc711005fea0b1b268aea2))
* **reconcile:** add on-demand CheckLLMs over the settings' pairs ([322d759](https://github.com/ainsel-project/ainsel-plugin-forgejo/commit/322d7594fab86f57a97bdcb0f0f6f55136dd1855))
* **reconcile:** push a provider key onto the role's installed agents ([3650cdd](https://github.com/ainsel-project/ainsel-plugin-forgejo/commit/3650cdd85139c187c619d611f4c13fe2a05e53e6))
* **settings:** keep no provider key — the agents hold them now ([1cc4a8a](https://github.com/ainsel-project/ainsel-plugin-forgejo/commit/1cc4a8acc91b3a8348d735915d7244c8d3405a8d))
* **setup:** let a wizard key stand on its own beside the settings reading ([6b9d149](https://github.com/ainsel-project/ainsel-plugin-forgejo/commit/6b9d149c3574a0ea749c1182a02bbb3c4b0204ff))
* **ui:** ask for the optional provider key on the wizard's roles step ([75d61fe](https://github.com/ainsel-project/ainsel-plugin-forgejo/commit/75d61fe205d0315ddfdfd7a96dd39daeb15a6ea2))
* **ui:** render the per-role LLM checks in the Check now answer ([5b90193](https://github.com/ainsel-project/ainsel-plugin-forgejo/commit/5b901934d9898baddf4ee1c7147d2703f828c846))
* **ui:** say what a key row does now, and show where a key did not land ([cbcc44d](https://github.com/ainsel-project/ainsel-plugin-forgejo/commit/cbcc44de58979eb2facf900d9260412582457166))


### Bug Fixes

* **ci:** give the chart push step its GHCR token ([07668c5](https://github.com/ainsel-project/ainsel-plugin-forgejo/commit/07668c54f740eff64c9a4e3f63475c1964311f0f))
* **ci:** give the chart push step its GHCR token ([1b72a62](https://github.com/ainsel-project/ainsel-plugin-forgejo/commit/1b72a6239b3b40f97168fcfbd975bab05c8e22f3))
* **docs-site:** draw diagrams in the theme the page was opened in ([0bc5a82](https://github.com/ainsel-project/ainsel-plugin-forgejo/commit/0bc5a82c2ea596d1050dce453d59fdf3cfc177cc))
* **docs-site:** rewrite links in prose, and leave the ones in code as typed ([5bfe404](https://github.com/ainsel-project/ainsel-plugin-forgejo/commit/5bfe404f14e202ce8399349961df9a368c6742c5))
* **docs-site:** stop the dev server serving files outside the docs directory ([308e9b7](https://github.com/ainsel-project/ainsel-plugin-forgejo/commit/308e9b76ccbe16b7c6b70c90a413c0f74920acbb))
* **settings:** keep no provider key — the hub's agents hold them ([d31892c](https://github.com/ainsel-project/ainsel-plugin-forgejo/commit/d31892c58c074320e50d543fdbdd9abe1ae16932))
* **ui:** show agents by their names, not their hub ids ([c8c6a8e](https://github.com/ainsel-project/ainsel-plugin-forgejo/commit/c8c6a8e3752e2cc5c2d792258e39abd9a2b88229))
* **ui:** show agents by their names, not their hub ids ([74ae188](https://github.com/ainsel-project/ainsel-plugin-forgejo/commit/74ae1886539999c30f8fab790416df8a9d8afc7f))

## 0.1.0 (2026-11-18)


### Features

* AInsel plugin for Forgejo ([fd5171a](https://github.com/ainsel-project/ainsel-plugin-forgejo/commit/fd5171a8c525ae93addc1a1b336966902d435313))
