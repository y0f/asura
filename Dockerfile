FROM golang:1.26-alpine AS builder

ARG VERSION=dev

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o asura ./cmd/asura

FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata && \
    adduser -D -H -s /sbin/nologin asura

WORKDIR /app
COPY --from=builder /build/asura .

USER asura

EXPOSE 8090

ENTRYPOINT ["./asura"]
CMD ["-config", "/app/config.yaml"]
