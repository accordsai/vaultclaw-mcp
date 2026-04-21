FROM golang:1.24-bookworm AS build

WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /out/accords-mcp ./cmd/accords-mcp

FROM alpine:3.22

RUN addgroup -S -g 65532 accords && \
    adduser -S -D -H -u 65532 -G accords accords && \
    mkdir -p /var/lib/accords-mcp /var/lib/accords-mcp/.config /usr/local/bin && \
    chown -R 65532:65532 /var/lib/accords-mcp

ENV HOME=/var/lib/accords-mcp \
    XDG_CONFIG_HOME=/var/lib/accords-mcp/.config \
    ACCORDS_MCP_BUNDLED_COOKBOOKS_DIR=/opt/accords-mcp/cookbooks

COPY --from=build /out/accords-mcp /usr/local/bin/accords-mcp
COPY --from=build /src/cookbooks /opt/accords-mcp/cookbooks
RUN chmod 0755 /usr/local/bin/accords-mcp

USER 65532:65532
WORKDIR /var/lib/accords-mcp

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/accords-mcp"]
