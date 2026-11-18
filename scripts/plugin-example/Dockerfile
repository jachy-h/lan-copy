# Build a minimal plugin image without a base image:
#   cd scripts/plugin-example
#   CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o plugin-server .
#   docker build -t lan-copy-demo-plugin:1.0 .
#   docker save lan-copy-demo-plugin:1.0 | gzip > /tmp/lan-copy-demo-plugin.tar.gz
#
# Use GOARCH=amd64 for amd64 hosts. The image must match the Docker Engine
# architecture; Lan Copy rejects a mismatched upload.
FROM scratch
COPY plugin-server /plugin-server
USER 65532:65532
EXPOSE 8080
LABEL io.lan-copy.plugin.manifest='{"schemaVersion":1,"id":"demo-plugin","name":"Demo Plugin","version":"1.0.0","description":"Minimal Lan Copy plugin protocol example","protocolVersion":"1.0","runtime":"http","category":"example","containerPort":8080,"endpoints":{"health":"/health","manifest":"/manifest","version":"/version","capabilities":"/capabilities","events":"/events"},"capabilities":["demo.echo","demo.files"],"events":["file.uploaded","folder.*","text.*"],"defaults":{"cpuLimit":0.2,"memoryMB":64,"pidsLimit":64,"requestTimeoutSeconds":15,"startupTimeoutSeconds":30}}'
ENTRYPOINT ["/plugin-server"]
