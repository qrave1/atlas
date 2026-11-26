FROM golang:1.27-alpine3.24 AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o build ./cmd/atlas

FROM alpine:3.24
WORKDIR /app
RUN apk add --no-cache ca-certificates ffmpeg \
    && adduser -D -u 10001 atlas \
    && mkdir -p /app/data /app/google /app/secrets \
    && chown -R atlas:atlas /app/data /app/google /app/secrets
COPY --from=builder /app/build .
USER 10001:10001
CMD ["./build", "run"]
