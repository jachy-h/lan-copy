/**
 * Build the prompt copied from the plugin page. Keep it self-contained: an
 * agent receiving it may only have the target service repository, not the
 * Lan Copy source tree.
 */
export function buildAgentPrompt(origin = 'http://127.0.0.1:8080') {
  return `你是一名负责把现有服务改造成 Lan Copy 插件的工程师。请先阅读目标服务仓库的启动方式、Dockerfile、健康检查和 HTTP 接口，然后完成改造与验证。不要修改 Lan Copy 主程序。

目标服务：<在这里填写目标服务仓库或服务名>
Lan Copy 地址：${origin}

## 目标

把目标服务打包成可通过“插件服务 → 镜像 → 上传离线镜像”导入的 Docker 镜像，并符合 Lan Copy 插件协议 1.0。导入后应能在 Lan Copy 页面中安装、启动、查看健康状态和日志。

## 必须实现的 HTTP 端点

服务监听容器内一个 TCP 端口（1024–65535），并实现：

1. GET /health
   返回 200：
   {"status":"ok","version":"1.0.0"}

2. GET /manifest
   返回与镜像 OCI 标签完全一致的 Manifest JSON（字段含义一致，不要求字节级相同）。

3. GET /version
   返回插件版本、构建版本和运行时信息。

4. GET /capabilities
   返回能力列表及接口描述。

业务接口由目标服务自行决定，不要为了 Lan Copy 改写现有业务 API。

## Manifest 模板

{
  "schemaVersion": 1,
  "id": "<plugin-id>",
  "name": "<显示名称>",
  "version": "1.0.0",
  "description": "<一句话描述>",
  "protocolVersion": "1.0",
  "runtime": "http",
  "category": "<可选分类>",
  "containerPort": 8080,
  "endpoints": {
    "health": "/health",
    "manifest": "/manifest",
    "version": "/version",
    "capabilities": "/capabilities"
  },
  "capabilities": ["<domain.action>"],
  "defaults": {
    "cpuLimit": 0.5,
    "memoryMB": 512,
    "pidsLimit": 128,
    "requestTimeoutSeconds": 30,
    "startupTimeoutSeconds": 60
  }
}

硬性规则：
- id 必须匹配 ^[a-z0-9][a-z0-9-]{0,63}$，全局唯一。
- protocolVersion 必须为 1.x（当前支持 1.0）。
- endpoints 必须是以 / 开头的绝对路径，不能包含 ..、查询串或片段。
- capabilities 每项形如 domain.action，不能重复。
- containerPort 必须在 1024–65535（模板中的 8080 按实际端口修改）。
- 不要在 Manifest 中声明宿主机路径、命令、Docker 参数、设备、网络权限或权限提升字段。

## 镜像要求

在 Dockerfile 中：

- 使用非 root USER，例如：
  USER 65532:65532
- 写入 Manifest 标签，值必须是合法 JSON，并与 GET /manifest 返回的 id、version、protocolVersion、containerPort、endpoints 一致：
  LABEL io.lan-copy.plugin.manifest='{"schemaVersion":1,...}'
- 监听 0.0.0.0:<containerPort>。
- 根文件系统会被 Lan Copy 挂载为只读；需要临时写入时只使用 /tmp（tmpfs）。需要持久化时只使用 Lan Copy 明确挂载的目录，不要假设能访问宿主机共享文件夹。
- 不得要求 privileged、host 网络、host PID、Docker Socket、设备直通、额外 capabilities 或宿主机目录挂载。
- 镜像架构必须与运行 Lan Copy 的 Docker Engine 一致（apple silicon 通常是 linux/arm64，Intel/AMD 通常是 linux/amd64）。
- 不要把模型、证书、Token 等敏感内容写进镜像标签或日志。

## 构建与导出

按目标服务的语言选择构建方式，最终产出 docker save 压缩包：

docker build -t <image-name>:<version> .
docker save <image-name>:<version> | gzip > <image-name>-<version>.tar.gz

如果使用 Go，建议静态编译：
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o plugin-server .
（amd64 主机改用 GOARCH=amd64）

构建后用以下命令自检：
docker run --rm -p 127.0.0.1:18080:<containerPort> <image-name>:<version>
curl -s http://127.0.0.1:18080/health
curl -s http://127.0.0.1:18080/manifest
docker inspect <image-name>:<version> --format '{{.Config.User}} {{index .Config.Labels "io.lan-copy.plugin.manifest"}}'
确认 User 非 root，标签存在且能解析为 JSON。

## 通过 API 导入与安装

所有接口仅本机可访问。导入：

curl -X POST \\
  -H 'X-Lan-Copy-Action: plugins-image' \\
  -F 'image=@<image-name>-<version>.tar.gz' \\
  ${origin}/api/plugins/images/upload

返回 {"taskId":"..."}，轮询：

curl -s ${origin}/api/plugins/tasks/<taskId>

安装并启动（imageId 来自任务结果或 GET /api/plugins/images）：

curl -X POST \\
  -H 'X-Lan-Copy-Action: plugins' \\
  -H 'Content-Type: application/json' \\
  -d '{"imageId":"<imageId>","config":{"cpuLimit":0.5,"memoryMB":512,"pidsLimit":128},"start":true}' \\
  ${origin}/api/plugins/install

启动已有插件：

curl -X POST -H 'X-Lan-Copy-Action: plugins' ${origin}/api/plugins/<plugin-id>/start

查看状态和日志：

curl -s ${origin}/api/plugins/<plugin-id>
curl -s ${origin}/api/plugins/<plugin-id>/status
curl -s "${origin}/api/plugins/<plugin-id>/logs?tail=200"

## 重要边界

- 导入镜像时，Lan Copy 会从镜像标签读取并校验 Manifest，同时创建插件记录；没有单独的“注册插件”接口，也不需要手写数据库记录。
- 上传新版本镜像会新增一个镜像记录。当前没有原地升级 API：已安装实例需要先卸载（默认保留镜像和数据），再选择新镜像安装并启动。
- 不要尝试挂载或读取 Docker Socket，也不要让插件直接访问 Lan Copy 的共享目录；文件通过业务 HTTP 接口传入。
- 插件容器以非 root、只读根文件系统、cap-drop ALL、no-new-privileges 运行，并有 CPU/内存/PID/日志限制。
- 如果目标服务已有 /health，请调整为兼容 {"status":"ok"}；已有业务接口保持不变。
- 完成后请给出：修改的文件、构建命令、导入/安装命令、健康检查结果，以及尚不支持的边界。

可参考 Lan Copy 仓库中的 scripts/plugin-example 作为最小实现示例。`
}
