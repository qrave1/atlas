FROM golang:1.27-alpine3.24 AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -o build .

FROM alpine:3.24

WORKDIR /app

RUN apk add --no-cache ca-certificates ffmpeg

COPY --from=builder /app/build .

CMD ["./build"]
