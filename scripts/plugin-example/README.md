# Demo plugin image

This directory builds the minimal HTTP plugin used for manual acceptance and
Docker integration checks. It is not part of the Lan Copy binary.

```bash
cd scripts/plugin-example
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o plugin-server .
docker build -t lan-copy-demo-plugin:1.0 .
docker save lan-copy-demo-plugin:1.0 | gzip > /tmp/lan-copy-demo-plugin.tar.gz
```

On an amd64 Docker host use `GOARCH=amd64`. Do not commit the built
`plugin-server` binary.

The image runs as UID/GID `65532:65532`, exposes port 8080 and declares the
plugin manifest in the `io.lan-copy.plugin.manifest` OCI label. Endpoints:

- `GET /health`
- `GET /manifest`
- `GET /version`
- `GET /capabilities`
- `GET /` (browser interface; reads `X-Forwarded-Prefix`)
- `POST /echo`
- `GET /download`
- `POST /upload` (multipart field `file`)
- `POST /events` (signed lifecycle events)
- `POST /table-query` and `POST /table-update` (proxy to Lan Copy smart-table API)

The manifest subscribes to `file.uploaded`, `folder.*` and `text.*`. It also
declares `ui`, so after the plugin is running and healthy the plugins page shows
**打开界面**, which opens `/plugin-app/demo-plugin/` on Lan Copy. The page reads
`X-Forwarded-Prefix` and calls the plugin through the same prefix; open the same
URL from a phone on the LAN to verify the proxy.

After installing and starting the plugin, open **设置 → 插件 → 对接** and use
**发送测试事件** to verify the signature check; the received event is printed to
the container log (`GET /api/plugins/demo-plugin/logs`).

After importing the archive in **设置 → 插件 → 镜像**, install it from the same
page and start it. The default loopback mode publishes a random
`127.0.0.1:<port>` address.
