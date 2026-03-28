# Builder runs on the host platform; Go cross-compiles for the target.
# No QEMU needed anywhere — the runtime image is scratch (no package manager).
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS builder
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -o /podstash .

FROM scratch
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /etc/passwd /etc/passwd
COPY --from=builder /podstash /podstash
USER nobody
ENV DATA_DIR=/data
ENV POLL_INTERVAL=60m
EXPOSE 8080
ENTRYPOINT ["/podstash"]
