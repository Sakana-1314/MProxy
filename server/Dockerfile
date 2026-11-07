FROM golang:1.27-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/cdn-proxy .

FROM alpine:3.22

RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S -g 10001 mproxy \
    && adduser -S -D -H -u 10001 -G mproxy mproxy \
    && mkdir -p /data && chown mproxy:mproxy /data

ENV TZ=Asia/Shanghai

COPY --from=build /out/cdn-proxy /usr/local/bin/cdn-proxy

WORKDIR /app
USER mproxy
VOLUME ["/data"]
EXPOSE 8088

HEALTHCHECK --interval=30s --timeout=3s --start-period=10s \
    CMD wget -qO- http://127.0.0.1:8088/api/health || exit 1

ENTRYPOINT ["cdn-proxy"]
CMD ["-proxy-addr", ":8088", "-data-dir", "/data"]
