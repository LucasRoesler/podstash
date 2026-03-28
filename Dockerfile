FROM golang:1.25-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /podstash .

FROM alpine:3.21
RUN apk add --no-cache ca-certificates \
    && addgroup -S podstash && adduser -S podstash -G podstash
COPY --from=builder /podstash /podstash
USER podstash
ENV DATA_DIR=/data
ENV POLL_INTERVAL=60m
EXPOSE 8080
ENTRYPOINT ["/podstash"]
