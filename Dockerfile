FROM golang:1.26-alpine@sha256:3ad57304ad93bbec8548a0437ad9e06a455660655d9af011d58b993f6f615648 AS builder

ARG VERSION=dev

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o asura ./cmd/asura

FROM alpine:3.21@sha256:48b0309ca019d89d40f670aa1bc06e426dc0931948452e8491e3d65087abc07d

RUN apk add --no-cache ca-certificates tzdata && \
    adduser -D -H -s /sbin/nologin asura

WORKDIR /app
COPY --from=builder /build/asura .

USER asura

EXPOSE 8090

ENTRYPOINT ["./asura"]
CMD ["-config", "/app/config.yaml"]
