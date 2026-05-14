# syntax=docker/dockerfile:1.6
#
# Multi-stage build for the loadtest CLI.
#
# Stage 1 — builder: full Go toolchain, fetches deps, compiles a static
# binary with stripped symbols.
#
# Stage 2 — runtime: alpine + ca-certificates only. /scenarios and /mock are
# expected to be bind-mounted by the caller.

FROM golang:1.22-alpine AS builder

WORKDIR /src

# Cache module downloads in their own layer.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
ARG COMMIT=unknown
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT}" \
    -o /out/loadtest \
    ./cmd/loadtest

# ---------------------------------------------------------------------------

FROM alpine:3.19

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app
COPY --from=builder /out/loadtest /usr/local/bin/loadtest
COPY scenarios /scenarios
COPY mock /mock

# Bind-mount your own configs over these defaults if you prefer:
#   docker run -v $(pwd)/scenarios:/scenarios loadtest run --config /scenarios/...
VOLUME ["/scenarios", "/mock", "/results"]

ENTRYPOINT ["loadtest"]
CMD ["--help"]
