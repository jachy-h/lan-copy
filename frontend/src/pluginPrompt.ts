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

把目标服务打包成可通过“设置 → 插件 → 镜像 → 上传离线镜像”导入的 Docker 镜像，并符合 Lan Copy 插件协议 1.0。导入后应能在 Lan Copy 页面中安装、启动、查看健康状态和日志。

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

所有接口对能访问 Lan Copy 的设备开放；写操作需要 X-Lan-Copy-Action 动作头且浏览器调用必须同源。导入：

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

## 插件 Web 界面（可选）

如果目标服务已有浏览器界面，希望在局域网通过 Lan Copy 打开，请在 Manifest 增加：

"ui": { "path": "/" }

1. path 是界面在容器内的挂载路径，默认 /，必须以 / 开头，不能包含 ..、查询串或片段；省略 ui 表示没有界面。
2. Lan Copy 固定入口为 ${origin}/plugin-app/<plugin-id>/，把请求反向代理到容器端口，只在实例运行且健康时可访问。
3. 插件必须读取请求头 X-Forwarded-Prefix（例如 /plugin-app/<plugin-id>），用它生成 HTML 资源地址、<base href>、前端路由 base 和 Cookie Path；不要写死 /assets/... 或 /api/...。
4. 代理支持 SSE、上传下载和 WebSocket；页面与 Lan Copy 同源，写接口请自行校验 CSRF。

安装并启动后验证：

curl -s ${origin}/plugin-app/<plugin-id>/

## 生命周期事件（可选）

如果目标服务需要在上传、文件夹/文件变化或文本变化时收到通知，请同时完成以下改造：

1. Manifest 增加订阅与端点，例如：
   "events": ["file.uploaded", "folder.*", "text.*"],
   "endpoints": { ..., "events": "/events" }
   省略 events 表示不订阅，旧插件无需改动。

2. 实现 POST /events，读取 application/json 请求体。事件信封包含 schemaVersion、id、type、occurredAt 和 data；路径都是共享根目录内的相对路径。

3. 从环境变量 LAN_COPY_EVENT_TOKEN 读取事件密钥，校验请求头：
   X-Lan-Copy-Timestamp 与 X-Lan-Copy-Signature: v1=<hex>
   签名 = HMAC-SHA256(timestamp + "." + 原始请求体)，建议校验 5 分钟时间窗并用常量时间比较。

4. 事件类型：file.uploaded、file.deleted、file.renamed、file.moved；folder.created、folder.deleted、folder.renamed、folder.moved；text.created、text.deleted、text.saved。所有事件均在操作成功后异步推送。

5. 返回 2xx 表示成功；网络错误、超时、429 和 5xx 最多重试 3 次，其余 4xx 立即丢弃。请按事件 id 幂等处理，不要依赖固定顺序之外的假设。

安装并启动后，用以下接口验证：
curl -X POST -H 'X-Lan-Copy-Action: plugins' ${origin}/api/plugins/<plugin-id>/events/test
curl -s ${origin}/api/plugins/<plugin-id>/events

## 上传 Hook（可选，Lan Copy v0.13.0+）

如果目标服务需要在上传成功后弹出自己的审核页，请在 Manifest 增加：

\`\`\`json
"uploadHook": {
  "path": "/hooks/upload",
  "timeoutSeconds": 5
}
\`\`\`

1. Manifest 还必须声明 \`ui\`；返回的页面只能位于 \`/plugin-app/<plugin-id>/\` 下，不能返回外部 URL。
2. Lan Copy 在上传提交成功后同步调用 \`POST /hooks/upload\`，请求头：
   - \`X-Lan-Copy-Hook: file.uploaded\`
   - \`X-Lan-Copy-Hook-Id\`
   - \`X-Lan-Copy-Timestamp\`
   - \`X-Lan-Copy-Signature: v1=<hex>\`
3. 请求体与 \`file.uploaded\` 事件信封相同；签名算法仍是 HMAC-SHA256(LAN_COPY_EVENT_TOKEN, timestamp + "." + 原始请求体)。
4. Hook 应快速返回，不要在 Hook 内做耗时 OCR；耗时工作放到返回页面的后台任务。
5. \`204\` 表示不需要弹窗。需要弹窗时返回：

\`\`\`json
{
  "schemaVersion": 1,
  "dialogs": [
    { "title": "确认信息", "path": "/review/<session-id>" }
  ]
}
\`\`\`

6. Hook 失败或超时不会让上传失败，也不重试。不要依赖 Hook 一定送达。
7. 页面完成后可向父窗口发送：

\`\`\`js
window.parent.postMessage({
  type: 'lan-copy:plugin-dialog:complete',
  refresh: true,
  message: '已保存'
}, window.location.origin)
\`\`\`

页面必须允许被同源 iframe 嵌入（CSP \`frame-ancestors 'self'\`），并读取 \`X-Forwarded-Prefix\` 生成 API 地址。

## 智能表格 API（可选）

如果插件需要读写共享目录中的 CSV 表格，Lan Copy 会在安装时注入：

LAN_COPY_API_URL
LAN_COPY_API_TOKEN

使用 Bearer 令牌调用：

- POST $LAN_COPY_API_URL/api/plugin-api/tables/query
- POST $LAN_COPY_API_URL/api/plugin-api/tables/lock-and-update

lock-and-update 必须在 expectedVersion 中提交查询返回的当前 version，并提供唯一且非空的 keyColumn；版本冲突返回 409，重新查询后再重试。

## 重要边界

- 导入镜像时，Lan Copy 会从镜像标签读取并校验 Manifest，同时创建插件记录；没有单独的“注册插件”接口，也不需要手写数据库记录。
- 上传新版本镜像会新增一个镜像记录。当前没有原地升级 API：已安装实例需要先卸载（默认保留镜像和数据），再选择新镜像安装并启动。
- 不要尝试挂载或读取 Docker Socket，也不要让插件直接访问 Lan Copy 的共享目录；文件通过业务 HTTP 接口传入。
- 插件容器以非 root、只读根文件系统、cap-drop ALL、no-new-privileges 运行，并有 CPU/内存/PID/日志限制。
- 如果目标服务已有 /health，请调整为兼容 {"status":"ok"}；已有业务接口保持不变。
- 完成后请给出：修改的文件、构建命令、导入/安装命令、健康检查结果，以及尚不支持的边界。

可参考 Lan Copy 仓库中的 scripts/plugin-example 作为最小实现示例。`
}
