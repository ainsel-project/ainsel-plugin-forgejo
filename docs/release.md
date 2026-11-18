# Release

Publishing is one action: merge the release PR release-please keeps open. `.github/workflows/release.yml`
builds the image, signs it keyless, pushes image and chart, and prints the
digest you deploy with. `.github/workflows/ci.yml` runs the same gates on every
push to `main` and every PR (vet, Go tests, UI build + tests, chart render
tests, image build — built, never pushed).

## Cutting a release

Releases run the same way as in [ainsel-project/ainsel](https://github.com/ainsel-project/ainsel): [release-please](https://github.com/googleapis/release-please) reads the conventional commits on `main` and keeps a release PR open (`.github/workflows/release-please.yml`).

1. Land changes on `main` through PRs with conventional titles (`feat:`, `fix:`, `feat!:` …) — they decide the next version and become the changelog.
2. Review the release PR. Its diff is the dry run: `CHANGELOG.md`, and the version in `ainsel-plugin.json` and `chart/Chart.yaml`'s `version`/`appVersion`, all bumped together (`release-please-config.json` → `extra-files`).
3. Merge it. release-please creates the `vX.Y.Z` tag and the GitHub Release, then starts `release.yml` **on that tag** (`gh workflow run release.yml --ref vX.Y.Z`).

Publishing runs on the tag, never on `main`, because the tag ref is the signing identity installers trust (`release.yml@refs/tags/v…`, below). A tag the workflow token creates fires no push trigger, which is why the hand-off is an explicit dispatch — and why release-please runs with the workflow token, not a PAT: a PAT-pushed tag would also fire `release.yml`'s tag trigger and publish twice.

The `v` prefix is dropped, so the image tag, the chart version and the chart's `appVersion` all read `1.2.3`. `release.yml` refuses a dispatch on anything but a `v` tag, and a release whose `ainsel-plugin.json` does not name the tag's version — the version the hub shows and the plugin's API and MCP endpoint report.

Pushing a tag by hand (`git tag vX.Y.Z && git push origin vX.Y.Z`) still runs `release.yml`, for a re-publish or an emergency; bump `ainsel-plugin.json` first or the gate stops it.

## What release.yml does, in order

1. **Build and push the image** to `ghcr.io/ainsel-project/ainsel-plugin-forgejo`
   with tags `<version>`, `<major>.<minor>`, `<major>`, via buildx.
2. **Sign it** — `cosign sign --yes "$IMAGE@$DIGEST"`. *Keyless*: the runner's
   GitHub OIDC identity is the signing identity (`id-token: write`), so there
   is no private key to keep or rotate. The signature carries the workflow
   identity — this repo + `release.yml@refs/tags/v…` — as a certificate.
3. **Package the chart** with `--version`/`--app-version` from the tag.
4. **Push the chart** to `oci://ghcr.io/ainsel-project/charts/forgejo`: `helm push` appends
   the chart's own name (`chart/Chart.yaml`) to the `CHART_OCI` registry path.
5. **Print the release record** in the job summary: image at
   `ghcr.io/ainsel-project/ainsel-plugin-forgejo@DIGEST`, chart location, and a
   ready-to-paste `helm install` command with `--set image.digest=…`.

## The digest is the deliverable

The digest signed in step 2 is the build output of step 1 — the same string the
summary prints. Deploy by it, never by tag:

```bash
helm install forgejo \
  oci://ghcr.io/ainsel-project/charts/forgejo \
  --version 1.2.3 \
  --set image.repository=ghcr.io/ainsel-project/ainsel-plugin-forgejo \
  --set image.digest=sha256:…
```

Tags are for humans; AInsel trusts digests — the signature covers exactly what
runs.

## Verifying a signature

Keyless verification checks the certificate against the workflow identity, not
a key you hold:

```bash
cosign verify \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-identity-regexp '^https://github.com/ainsel-project/ainsel-plugin-forgejo/\.github/workflows/release\.yml@refs/tags/v' \
  ghcr.io/ainsel-project/ainsel-plugin-forgejo@<digest>
```

## Who trusts it

The `cosign verify` above is one side; AInsel's side is configured, not
hardcoded. The operator verifies the same keyless identity against a list of
trusted issuers and subject patterns in AInsel's own values — and
`plugins.requireSignature` defaults to **true**, so a signed release is accepted
and an unsigned one is rejected:

```yaml
plugins:
  signing:
    keyless:
      - issuer: https://token.actions.githubusercontent.com
        subjectRegExp: ^https://github\.com/ainsel-project/ainsel-plugin-forgejo/\.github/workflows/release\.yml@refs/tags/v.*$
```

That regexp is the identity from step 2: ainsel-project/ainsel-plugin-forgejo,
this workflow, a tag starting `v`. Keep it narrow — an issuer plus a loose
pattern trusts every workflow in every repo that can reach that issuer.
`plugins.signing.keys` is the alternative for a key you hold.

`plugins.requireSignature: false` turns the check off entirely, which is why it
is not a dev setting: with the default `true` and no identity named here, nothing
verifies and no plugin becomes Ready — the fix is the block above, not the switch
below it.
