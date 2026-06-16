FROM golang:1.26-alpine AS builder
COPY core/ /build/core/
COPY media-scanner/ /build/media-scanner/
WORKDIR /build/media-scanner
RUN go mod download && CGO_ENABLED=0 go build -o /media-scanner ./cmd/module
FROM alpine:3.21
RUN adduser -D -h /data app
USER app
WORKDIR /app
COPY --from=builder /media-scanner .
ENTRYPOINT ["./media-scanner"]
