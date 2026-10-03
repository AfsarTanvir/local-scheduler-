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
RUN adduser -D -u 10001 scheduler
COPY --from=build /out/local-scheduler /usr/local/bin/local-scheduler
USER scheduler
EXPOSE 8080
ENTRYPOINT ["local-scheduler"]
