# Build stage
FROM golang:1.24-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /gateway ./cmd/gateway

# Runtime stage
FROM alpine:3.21

RUN apk add --no-cache ca-certificates

COPY --from=builder /gateway /usr/local/bin/gateway

EXPOSE 8080

ENTRYPOINT ["gateway"]
