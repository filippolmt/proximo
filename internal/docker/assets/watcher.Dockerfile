# syntax=docker/dockerfile:1
ARG GO_VERSION=1.26
ARG PROXIMO_REF=latest

FROM golang:${GO_VERSION}-alpine AS build
ARG PROXIMO_REF
ENV CGO_ENABLED=0
RUN go install github.com/filippolmt/proximo/cmd/watcher@${PROXIMO_REF}

FROM alpine:3.23
RUN apk add --no-cache ca-certificates
COPY --from=build /go/bin/watcher /usr/local/bin/watcher
ENTRYPOINT ["/usr/local/bin/watcher"]
