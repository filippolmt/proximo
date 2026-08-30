# syntax=docker/dockerfile:1
ARG GO_VERSION=1.26
ARG PROXIMO_REF=latest

FROM golang:${GO_VERSION}-alpine AS build
ARG PROXIMO_REF
ENV CGO_ENABLED=0
# git is required when `go install ...@<ref>` resolves through the VCS (e.g. a
# branch ref, or when GOPROXY is direct); it is not present in the alpine image.
RUN apk add --no-cache git
RUN go install github.com/filippolmt/proximo/cmd/inspector@${PROXIMO_REF}

FROM alpine:3.24
RUN apk add --no-cache ca-certificates
COPY --from=build /go/bin/inspector /usr/local/bin/inspector
ENTRYPOINT ["/usr/local/bin/inspector"]
