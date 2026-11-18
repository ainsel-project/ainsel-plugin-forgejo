# syntax=docker/dockerfile:1

# Stage 1: build the UI bundle. The Go binary embeds ui/dist (see assets.go), so
# the bundle has to exist before the Go build — and it has to be built here
# rather than assumed present, because a fresh clone ships ui/dist empty.
FROM node:24-bookworm AS ui

# Corepack picks the pnpm version from the package.json in the *working
# directory* — the repo root has none, so running from /src would make it fall
# back to the latest pnpm and fail against ui/package.json's packageManager
# pin (pnpm@9.15.9). Work inside the ui directory so the pin is honoured. The
# download prompt is turned off so a first build never blocks waiting for a tty.
WORKDIR /src/ui
ENV COREPACK_ENABLE_DOWNLOAD_PROMPT=0

# Manifest and lockfile first, so dependency install is cached across source edits.
COPY ui/package.json ui/pnpm-lock.yaml ./
RUN corepack enable && pnpm install --frozen-lockfile

COPY ui/ ./
RUN pnpm build

# Stage 2: build the plugin binary with the bundle embedded. CGO off and a static
# binary so it can run on the distroless base.
FROM golang:1.26 AS build

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
