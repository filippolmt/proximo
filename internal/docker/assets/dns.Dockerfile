# syntax=docker/dockerfile:1
ARG GO_VERSION=1.26
ARG PROXIMO_REF=latest

FROM golang:${GO_VERSION}-alpine AS build
ARG PROXIMO_REF
ENV CGO_ENABLED=0
RUN go install github.com/filippolmt/proximo/cmd/dnsserver@${PROXIMO_REF}

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=build /go/bin/dnsserver /usr/local/bin/dnsserver
EXPOSE 5353/udp
ENTRYPOINT ["/usr/local/bin/dnsserver"]
