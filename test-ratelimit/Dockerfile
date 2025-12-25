FROM golang:1.24rc1-alpine AS builder

RUN apk add --no-cache git

WORKDIR /build
COPY . .
ENV GOTOOLCHAIN=go1.24rc1
RUN go build -o xray ./main

FROM alpine:latest
WORKDIR /app
COPY --from=builder /build/xray /app/xray
COPY test-ratelimit/config.json /app/config.json

EXPOSE 10808

CMD ["/app/xray", "run", "-c", "/app/config.json"]
