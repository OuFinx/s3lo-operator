# NOTE: requires github.com/finx/s3lo to be a published Go module
# For local dev with replace directive, build with: go build -o s3lo-proxy ./cmd/s3lo-proxy
FROM golang:1.22-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o s3lo-proxy ./cmd/s3lo-proxy

FROM alpine:3.19
RUN apk --no-cache add ca-certificates
COPY --from=builder /app/s3lo-proxy /usr/local/bin/s3lo-proxy
ENTRYPOINT ["s3lo-proxy"]
