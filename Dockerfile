# syntax=docker/dockerfile:1

# Stage 1: build the UI bundle. The Go binary embeds ui/dist (see assets.go), so
# the bundle has to exist before the Go build — and it has to be built here
# rather than assumed present, because a fresh clone ships ui/dist empty.
FROM node:26-bookworm AS ui

# Node 24 shipped corepack, which picked the pnpm version from the package.json
# in the *working directory*; Node 26 no longer bundles corepack in the image.
# So the pin (pnpm@9.15.9) is read straight out of ui/package.json and that
# exact pnpm is installed with npm — one source of truth, and no interactive
# download prompt to disable because there is no corepack to prompt.
WORKDIR /src/ui

# Manifest and lockfile first, so dependency install is cached across source edits.
COPY ui/package.json ui/pnpm-lock.yaml ./
RUN PNPM_VERSION=$(node -p "require('./package.json').packageManager.split('@')[1]") \
 && npm install --global "pnpm@${PNPM_VERSION}" \
 && pnpm install --frozen-lockfile

COPY ui/ ./
RUN pnpm build

# Stage 2: build the plugin binary with the bundle embedded. CGO off and a static
# binary so it can run on the distroless base.
FROM golang:1.27 AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
COPY --from=ui /src/ui/dist ./ui/dist

RUN CGO_ENABLED=0 go build -o /plugin ./cmd/plugin

# Stage 3: the runtime image. distroless static carries no shell and no package
# manager, nonroot is uid 65532, and the binary writes nothing, so the container
# can run with a read-only root filesystem (as the Helm chart configures it).
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /plugin /plugin

EXPOSE 8080
USER nonroot

ENTRYPOINT ["/plugin"]
