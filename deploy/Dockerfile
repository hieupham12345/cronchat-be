# ===== Production image (no .env baked in — secrets come from env_file at runtime) =====
# STAGE 1: build binary
FROM golang:1.25-alpine AS builder
RUN apk add --no-cache git tzdata
WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
# CGO disabled -> fully static binary, smallest runtime image
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o server ./api-service/cmd/api

# STAGE 2: runtime
FROM alpine:latest
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=builder /app/server /app/server

# data dirs are provided as volumes at runtime (see docker-compose.prod.yml)
EXPOSE 5555
CMD ["/app/server"]
