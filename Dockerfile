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
VOLUME ["/data"]

COPY --from=build /out/jobalert /app/jobalert
COPY --from=build --chown=65532:65532 /data /data

# nonroot uid:gid is 65532; ensure it owns the data dir at runtime via volume.
USER nonroot:nonroot

ENTRYPOINT ["/app/jobalert"]
