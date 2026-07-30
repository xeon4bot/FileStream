FROM --platform=$BUILDPLATFORM golang:1.25-alpine3.21 AS builder
ARG TARGETOS
ARG TARGETARCH
WORKDIR /app

# Cache Go modules to prevent Go proxy download stream errors
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -o /app/fsb -ldflags="-w -s" ./cmd/fsb

FROM alpine:3.21
RUN apk add --no-cache ca-certificates ffmpeg tzdata
COPY --from=builder /app/fsb /app/fsb
EXPOSE 8080
ENTRYPOINT ["/app/fsb", "run"]

