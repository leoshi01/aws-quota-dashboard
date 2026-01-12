# Build stage
FROM golang:1.21-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /aws-quota-dashboard ./cmd/server

# Runtime stage
FROM alpine:3.19

RUN apk --no-cache add ca-certificates

WORKDIR /app

COPY --from=builder /aws-quota-dashboard .
COPY --from=builder /app/web ./web

ENV PORT=8080

EXPOSE 8080

CMD ["./aws-quota-dashboard"]
