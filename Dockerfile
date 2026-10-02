# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG TARGETOS
ARG TARGETARCH
# default.pgo in cmd/skua is picked up automatically when present.
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/skua ./cmd/skua

# Pinned by digest: a rebuild of an old commit produces the same image,
# and the tag lookup is off the path.
# gcr.io/distroless/static-debian12:nonroot as of 2026-08-26.
FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
# Links the GHCR package to the repository, which is what lets the
# workflow's GITHUB_TOKEN push to it.
LABEL org.opencontainers.image.source=https://github.com/6586x57890143/skua
COPY --from=builder /out/skua /skua
USER nonroot:nonroot
ENTRYPOINT ["/skua"]
