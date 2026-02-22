# --- Build stage ---
FROM golang:1.25-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /gotris-server ./cmd/server

# --- Runtime stage ---
FROM alpine:3.21

RUN apk add --no-cache ca-certificates
COPY --from=builder /gotris-server /gotris-server

EXPOSE 8080
CMD ["/gotris-server"]
