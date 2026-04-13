FROM golang:1.24-bookworm AS build

WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /out/accords-mcp ./cmd/accords-mcp

FROM alpine:3.22

RUN addgroup -S -g 65532 accords && \
    adduser -S -D -H -u 65532 -G accords accords && \
    mkdir -p /var/lib/accords-mcp /usr/local/bin && \
    chown -R 65532:65532 /var/lib/accords-mcp

COPY --from=build /out/accords-mcp /usr/local/bin/accords-mcp
RUN chmod 0755 /usr/local/bin/accords-mcp

USER 65532:65532
WORKDIR /var/lib/accords-mcp

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/accords-mcp"]
