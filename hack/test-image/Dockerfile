# The image `make test` and `make check` run Go in: the build image plus the
# docker CLI and Compose, so TestComposeAcceptsTheMaterializedFile runs locally
# as it does in CI instead of skipping. `compose config` validates the file
# client-side: no daemon, socket or mount is needed.
ARG GO_IMAGE=golang:1.27-alpine
FROM ${GO_IMAGE}
RUN apk add --no-cache docker-cli docker-cli-compose
