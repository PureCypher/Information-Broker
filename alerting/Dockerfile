FROM golang:1.21-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o alertmanager ./cmd/alertmanager

FROM alpine:latest
RUN apk --no-cache add ca-certificates tzdata
WORKDIR /root/

COPY --from=builder /app/alertmanager .
COPY --from=builder /app/config.yaml .

EXPOSE 9093

CMD ["./alertmanager"]