# ===== STAGE 1: BUILD BINARY =====
FROM golang:1.25-alpine AS builder
# hoặc nếu mày set go 1.26 thì:
# FROM golang:1.26-alpine AS builder

# Cài git (nếu dùng go modules từ github) + tzdata nếu cần timezone
RUN apk add --no-cache git tzdata

WORKDIR /app

# Copy go mod trước để cache
COPY go.mod go.sum ./
RUN go mod download

# Copy toàn bộ source vào
COPY . .

# Build binary (tùy tên)
RUN go build -o server ./api-service/cmd/api
# nếu project chỉ có main.go ở root thì:
# RUN go build -o server .

# ===== STAGE 2: RUNTIME =====
FROM alpine:latest

WORKDIR /app

# Copy binary từ stage build sang
COPY --from=builder /app/server /app/server

# Nếu dùng SQLite -> copy thư mục db / migrations nếu cần
# COPY ./migrations ./migrations
# COPY ./data ./data


# Copy file .env nếu muốn build cứng vào image (thường KHÔNG nên)
COPY .env .env
# COPY app.db app.db
# tốt hơn là dùng --env-file khi run container

# Expose port (sửa theo app của mày, vd 8080)
EXPOSE 5555

# Run
CMD ["/app/server"]
