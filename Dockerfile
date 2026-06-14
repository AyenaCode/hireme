# syntax=docker/dockerfile:1

# ---- build stage ----------------------------------------------------------
FROM golang:1.26-alpine AS build

WORKDIR /src

# Cache dependencies separately from source for faster rebuilds.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Pure-Go SQLite (modernc.org/sqlite) lets us build a fully static binary with
# no CGO, so it runs on a minimal scratch/distroless base.
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w" \
    -trimpath \
    -o /out/jobalert ./cmd/jobalert

# Pre-create the data dir so a fresh volume inherits nonroot ownership.
RUN mkdir -p /data

# ---- runtime stage --------------------------------------------------------
# distroless static (nonroot): tiny, has CA certificates for HTTPS, no shell.
FROM gcr.io/distroless/static-debian12:nonroot

WORKDIR /app

# Writable location for the SQLite file; mount a volume here to persist state.
ENV DB_PATH=/data/jobs.db
# Liveness endpoint; the HEALTHCHECK below probes it via the binary itself.
ENV HEALTH_ADDR=:8080
VOLUME ["/data"]
EXPOSE 8080

COPY --from=build /out/jobalert /app/jobalert
COPY --from=build --chown=65532:65532 /data /data

# nonroot uid:gid is 65532; ensure it owns the data dir at runtime via volume.
USER nonroot:nonroot

# distroless has no shell/curl, so the binary self-probes via exec-form CMD.
# start-period covers the first poll cycle; the check just confirms the loop is
# alive (it does not go red on a JSearch/quota failure).
HEALTHCHECK --interval=5m --timeout=5s --start-period=30s \
    CMD ["/app/jobalert", "-healthcheck"]

ENTRYPOINT ["/app/jobalert"]
