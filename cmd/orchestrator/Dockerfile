# Build stage
FROM golang:1.24-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /orchestrator ./cmd/orchestrator

# Runtime stage
FROM alpine:3.21

RUN apk add --no-cache ca-certificates

COPY --from=builder /orchestrator /usr/local/bin/orchestrator

EXPOSE 9090

ENTRYPOINT ["orchestrator"]
