FROM --platform=$BUILDPLATFORM golang:1.25-alpine3.21 AS builder
ARG TARGETOS
ARG TARGETARCH
WORKDIR /app
COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -o /app/fsb -ldflags="-w -s" ./cmd/fsb

FROM scratch
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /app/fsb /app/fsb
COPY --from=builder /app/watch.html /app/watch.html
COPY --from=builder /app/favicon.png /app/favicon.png
EXPOSE 8080
ENTRYPOINT ["/app/fsb", "run"]
