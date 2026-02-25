# syntax=docker/dockerfile:1

# ---- Build stage ----
FROM --platform=$BUILDPLATFORM golang:1.24-alpine AS builder

ARG TARGETOS=linux
ARG TARGETARCH=amd64

WORKDIR /workspace

# Cache dependencies first
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY cmd/ cmd/
COPY pkg/ pkg/

# Build the scheduler binary
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build \
    -ldflags="-s -w" \
    -o /workspace/kubevirt-scheduler \
    ./cmd/scheduler

# ---- Final stage ----
# Use distroless for a minimal, secure image
FROM gcr.io/distroless/static:nonroot

COPY --from=builder /workspace/kubevirt-scheduler /kubevirt-scheduler

USER nonroot:nonroot

ENTRYPOINT ["/kubevirt-scheduler"]
