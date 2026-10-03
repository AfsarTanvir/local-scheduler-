# Stage 1: compile a static binary with the full Go toolchain.
FROM golang:1.27-alpine AS build
WORKDIR /src
# Download dependencies first, so this layer is cached when only code changes.
COPY go.* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/local-scheduler ./cmd/local-scheduler

# Stage 2: small runtime image. Alpine (not scratch) so shell jobs have `sh`.
FROM alpine:3
# The data directory must exist and belong to the app user before VOLUME,
# so a new named volume starts with the right owner.
RUN adduser -D -u 10001 scheduler \
    && mkdir -p /app/data \
    && chown scheduler /app/data
WORKDIR /app
COPY --from=build /out/local-scheduler /usr/local/bin/local-scheduler
USER scheduler
ENV DB_PATH=/app/data/scheduler.db
# Jobs and run history live here. Mount a volume to keep them:
#   docker run -v scheduler-data:/app/data ...
VOLUME /app/data
EXPOSE 8080
ENTRYPOINT ["local-scheduler"]
