# syntax=docker/dockerfile:1
#
# The published stack image: ONE multi-arch image holding all three in-stack
# binaries. Compose picks one per service with an `entrypoint:` — see
# docs/adr/0002-stack-services-ship-as-one-published-image.md for why one image
# rather than three, and why the tag is the CLI's own version.
#
# It is also the image `PROXIMO_SRC` builds from a local checkout (the dev
# override in internal/docker/stack.go points `build:` here), so the published
# path and the contributor path cannot drift.
ARG GO_VERSION=1.27

# Cross-compile from the build platform: Go needs no emulation, so a two-arch
# manifest costs one native build leg per arch instead of a QEMU run.
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS build
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=none
ARG DATE=unknown
ENV CGO_ENABLED=0
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Same -X targets the CLI's own build uses, so the in-stack binaries report a
# build identity even though they are no longer produced by `go install @<ref>`.
RUN GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath \
      -ldflags "-s -w \
        -X github.com/filippolmt/proximo/internal/version.Version=${VERSION} \
        -X github.com/filippolmt/proximo/internal/version.Commit=${COMMIT} \
        -X github.com/filippolmt/proximo/internal/version.Date=${DATE}" \
      -o /out/ ./cmd/dnsserver ./cmd/watcher ./cmd/inspector

FROM alpine:3.24
RUN apk add --no-cache ca-certificates
COPY --from=build /out/ /usr/local/bin/
EXPOSE 5353/udp
# No default entrypoint on purpose: compose names the binary per service.
