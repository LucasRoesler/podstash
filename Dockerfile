# Builder runs on the host platform; Go cross-compiles for the target.
# No QEMU needed for the Go build itself.
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS builder
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -o /podstash .

FROM alpine:3.21
RUN apk add --no-cache ca-certificates \
    && addgroup -S podstash && adduser -S podstash -G podstash
COPY --from=builder /podstash /podstash
USER podstash
ENV DATA_DIR=/data
ENV POLL_INTERVAL=60m
EXPOSE 8080
ENTRYPOINT ["/podstash"]
