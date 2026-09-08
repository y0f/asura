FROM golang:1.27-alpine@sha256:cf6fca6641884b8433441b2b0652976f975e1d0fdd26d177eaaf8596087f3125 AS builder

ARG VERSION=dev

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o asura ./cmd/asura

FROM alpine:3.24@sha256:28bd5fe8b56d1bd048e5babf5b10710ebe0bae67db86916198a6eec434943f8b

RUN apk add --no-cache ca-certificates tzdata && \
    adduser -D -H -s /sbin/nologin asura

WORKDIR /app
COPY --from=builder /build/asura .

USER asura

EXPOSE 8090

ENTRYPOINT ["./asura"]
CMD ["-config", "/app/config.yaml"]
