# Lan Copy 插件服务协议与使用说明

本文档面向插件镜像作者和 Lan Copy 管理员，描述插件 HTTP 协议、镜像构建、安装、运行安全边界、API 与排错方式。

## 1. 范围

Lan Copy 通过 Docker Engine API 在本机运行插件容器，并提供：

- 插件镜像上传（`docker save` 的 tar/tar.gz）
- 插件安装、启动、停止、重启、卸载
- 健康检查、日志、资源用量、调用指标
- 统一 Go HTTP Client（业务代码不拼接容器地址）

不包含：Kubernetes、远程 Docker、Compose 编排、多节点调度、插件市场、自动升级、插件间通信、Registry 拉取。

## 2. 总体架构

```text
Vue /plugins
   │ 局域网可访问 + 写操作动作头 + 同源
   ▼
internal/httpserver
   │
   ▼
internal/plugin（Manager / Registry / Manifest / Client）
   │                         │
   │                         └── <dataDir>/plugins/state.json
   ▼
Runtime 接口
   │
   ▼
internal/plugin/dockerruntime
   │
   ▼
Docker / Podman Engine API（unix socket / Windows named pipe / DOCKER_HOST / CONTAINER_HOST）
```

`internal/plugin` 不导入 Docker 类型；所有 Docker SDK 调用都在 `internal/plugin/dockerruntime`。Podman 通过其 Docker 兼容 API 复用同一适配层，不引入第二套客户端或命令行调用。

### 2.1 运行时检测

插件管理启动时按以下顺序探测本机端点，连接成功即选定，不执行 `docker` / `podman` 命令：

| 顺序 | 端点 |
| --- | --- |
| 1 | `DOCKER_HOST`（显式指定，优先级最高） |
| 2 | `CONTAINER_HOST`（显式指定） |
| 3 | `/var/run/docker.sock`、`/run/docker.sock` |
| 4 | `/run/podman/podman.sock`、`$XDG_RUNTIME_DIR/podman/podman.sock`、`/run/user/<uid>/podman/podman.sock` |
| 5 | `$HOME/.local/share/containers/podman/machine/podman.sock` 及其下一层 `*/podman.sock`（`podman machine`） |
| 6 | Windows `//./pipe/docker_engine`、`//./pipe/podman-machine-default` |

- `DOCKER_HOST` 和 `CONTAINER_HOST` 都存在时只使用前者；显式端点不可达会直接报不可用，不会静默改用另一个引擎。
- 引擎类型来自 `/version`：出现 `Podman Engine` 组件即判为 Podman，否则为 Docker。页面与实例记录显示实际类型。
- 两者都不可用时主程序照常启动，插件页显示“容器运行时不可用”并列出已尝试的端点；日志中另有每个端点的原始错误。
- 不可用横幅提供“启动 Podman”按钮（仅本机存在 podman 且平台支持时）：
  - Linux 执行 `systemctl --user start podman.socket`（root 为 `systemctl start podman.socket`）。
  - Windows / macOS 执行 `podman machine start`。
  - 后台任务执行；成功后自动重新探测并刷新页面，无需重启 Lan Copy。
  - 该接口对能访问插件页的设备开放，仅依赖同源 + 动作头；请只在可信局域网使用。
- 检测总超时 4 秒，单个端点 1.5 秒；已安装实例保存的是安装时的地址与主机映射，切换引擎后需要卸载重装。

Podman 的 Docker 兼容 API 默认可能未启动。Lan Copy 在自动探测失败且检测到已安装 Podman 时，会自动执行一次启动命令，再重新探测：

- 非 root：`systemctl --user start podman.socket`
- root：`systemctl start podman.socket`（rootful）
- 只 `start`，不改开机自启；设置 `LAN_COPY_DISABLE_PODMAN_AUTOSTART=1` 可关闭。
- macOS/Windows 的 `podman machine` 不会自动启动。

自动启动失败时（无 systemd 用户会话、容器内、无 systemd 发行版等），再手动启用：

```bash
# rootless（普通用户）
systemctl --user enable --now podman.socket
export DOCKER_HOST=unix://$XDG_RUNTIME_DIR/podman/podman.sock

# rootful（root）
sudo systemctl enable --now podman.socket
export DOCKER_HOST=unix:///run/podman/podman.sock
```

`podman machine` 的 socket 位置由 `podman machine inspect` 的 `ConnectionInfo.PodmanSocket.Path` 给出；Lan Copy 会自动尝试常见位置，找不到时可用 `DOCKER_HOST` 显式指定。Lan Copy 自身运行在容器里时，还必须把该 socket 挂载进容器。

## 3. 插件 HTTP 协议

插件必须监听容器内一个 TCP 端口，并提供以下接口：

| 方法 | 默认路径 | 响应 |
| --- | --- | --- |
| GET | `/health` | `200` + `{"status":"ok","version":"1.0.0"}` |
| GET | `/manifest` | 与镜像标签中的 Manifest 完全一致 |
| GET | `/version` | 版本、构建版本、运行时信息 |
| GET | `/capabilities` | 能力列表及描述 |
| POST | `/events` | 接收生命周期事件；未声明 `events` 时不会收到请求 |

业务接口由插件自行定义，Lan Copy 不做协议限制。

示例：

```json
{
  "schemaVersion": 1,
  "id": "invoice-ocr",
  "name": "Invoice OCR",
  "version": "1.0.0",
  "description": "Chinese VAT invoice recognition",
  "protocolVersion": "1.0",
  "runtime": "http",
  "category": "finance",
  "containerPort": 8080,
  "endpoints": {
    "health": "/health",
    "manifest": "/manifest",
    "version": "/version",
    "capabilities": "/capabilities",
    "events": "/events"
  },
  "capabilities": ["invoice.recognize"],
  "events": ["file.uploaded", "folder.*", "text.*"],
  "ui": { "path": "/" },
  "uploadHook": { "path": "/hooks/upload", "timeoutSeconds": 5 },
  "defaults": {
    "cpuLimit": 0.5,
    "memoryMB": 512,
    "pidsLimit": 128,
    "requestTimeoutSeconds": 30,
    "startupTimeoutSeconds": 60
  }
}
```

### 协议规则

1. 插件 ID 全局唯一，格式为 `^[a-z0-9][a-z0-9-]{0,63}$`。
2. `protocolVersion` 首期只支持主版本 `1`（如 `1.0`）。
3. Manifest 使用严格 JSON，未知字段一律拒绝。
4. 启动后 Lan Copy 会再次读取 `/manifest`，插件 ID 必须与镜像标签、安装记录一致；版本不一致会记录警告。
5. Manifest 只能描述能力，不能声明宿主机路径、Docker 参数、命令、设备或网络权限。
6. 镜像必须声明非 root `USER`，否则拒绝导入。
7. `events` 与 `endpoints.events` 可选；声明后按第 6 节推送生命周期事件，旧镜像无需修改。
8. `ui` 可选；声明后 Lan Copy 为插件提供局域网可访问的 Web 界面入口，见 3.1。
9. `uploadHook` 可选；声明后上传成功时同步调用并可用返回页面弹窗，见 6.6。

### 3.1 插件 Web 界面

Manifest 增加可选字段，声明插件自带浏览器界面：

```json
{
  "ui": { "path": "/" }
}
```

- `ui` 省略表示插件没有 Web 界面；`ui.path` 是界面在容器内的挂载路径，默认 `/`，必须是以 `/` 开头的绝对路径，不能包含 `..`、查询串或片段。
- Lan Copy 固定提供 `http://<lan-copy>/plugin-app/<插件ID>/`，把请求转发到运行中且健康的实例；插件不能自定义对外路径。
- 入口对局域网默认开放。代理请求不带 `X-Lan-Copy-Action` 动作头，插件必须自行处理写操作的 CSRF 与鉴权。
- 实例停止、启动中或不健康时返回 `503`（带 `Retry-After`）；插件未声明 `ui` 或已卸载时返回 `404`。
- 支持普通 HTTP、文件上传下载、SSE 和 WebSocket；不缓冲响应，也不套用插件调用 API 的请求超时。
- 实例重启后宿主端口可能变化，Lan Copy 每次请求都会重新解析实例地址，无需重新配置。

插件必须按子路径部署，不能假设自己挂在站点根路径：

1. 读取请求头 `X-Forwarded-Prefix`（例如 `/plugin-app/invoice-ocr`），用它生成资源地址、`<base href>`、前端路由 base 和 `fetch` 地址。
2. 不要生成 `/assets/...` 这类根绝对路径；浏览器会把它解析到 Lan Copy 根路径，导致 404。
3. `Set-Cookie` 的 `Path` 使用该前缀，避免与其他插件或 Lan Copy 的路径冲突。
4. 可以使用根绝对路径重定向，Lan Copy 会把 `Location` 改写为带前缀的地址；页面内链接仍应自行带前缀。
5. 代理会附带 `X-Forwarded-For`、`X-Forwarded-Host`、`X-Forwarded-Proto` 和 `X-Forwarded-Prefix`。

安全提示：代理页面与 Lan Copy 同源，页面脚本可以携带动作头调用插件管理 API。请只安装可信镜像；需要更强隔离时，可让 Lan Copy 只监听本机地址，或在外层反向代理增加认证。

带 `ui` 的 Manifest 需要 Lan Copy v0.11.0 及以上；旧版解析 Manifest 时会拒绝未知字段。

## 4. 镜像构建规范

### 4.1 Manifest 标签

Manifest 写在 OCI 镜像标签中：

```dockerfile
LABEL io.lan-copy.plugin.manifest='{"schemaVersion":1,...}'
```

Dockerfile 示例（Go 静态二进制，无基础镜像依赖）：

```dockerfile
FROM golang:1.24 AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /plugin-server .

FROM scratch
COPY --from=build /plugin-server /plugin-server
USER 65532:65532
EXPOSE 8080
LABEL io.lan-copy.plugin.manifest='{"schemaVersion":1,"id":"demo","name":"Demo","version":"1.0.0","protocolVersion":"1.0","runtime":"http","containerPort":8080,"endpoints":{"health":"/health","manifest":"/manifest","version":"/version","capabilities":"/capabilities"},"capabilities":["demo.echo"]}'
ENTRYPOINT ["/plugin-server"]
```

要求：

- 非 root 用户运行。
- 不使用 `VOLUME` 隐式声明持久化；持久化目录由 Lan Copy 明确挂载。
- 不把数据写入镜像内只读路径；需要临时文件时使用 `/tmp`（tmpfs）。
- 容器内监听 `0.0.0.0`，由 Lan Copy 决定只发布到 `127.0.0.1` 或加入专用网络。
- 镜像架构必须与 Docker Engine 架构一致。

### 4.2 构建与导出

```bash
docker build -t registry.example/plugin/invoice-ocr:1.0.0 .
docker save registry.example/plugin/invoice-ocr:1.0.0 | gzip > invoice-ocr-1.0.0.tar.gz
```

仓库内 `scripts/plugin-example/` 提供了一个可直接构建的最小示例插件，详见其 README。

## 5. 安装与生命周期

### 5.1 上传镜像

- 位置：`设置 → 插件 → 镜像 → 上传离线镜像`
- 接口：`POST /api/plugins/images/upload`，multipart 字段 `image`
- 限制：`.tar`、`.tar.gz`、`.tgz`，默认最大 2 GiB
- 上传只导入镜像并创建记录，不安装、不启动
- 后端校验文件类型、魔数、SHA256、镜像架构、非 root 用户、Manifest

### 5.2 安装

1. 选择已导入镜像。
2. 查看 Manifest、能力和安全说明。
3. 配置环境变量、CPU/内存/PID、重启策略、持久化目录、请求/启动超时。
4. 选择是否立即启动。

安装创建容器但不自动运行（除非勾选立即启动）。启动时等待 `/health` 通过并复核 `/manifest`。

### 5.3 状态

`not_installed | installing | stopped | starting | running | unhealthy | stopping | failed | uninstalling`

容器实际状态来自 Docker Inspect，不以数据库记录为准。Lan Copy 启动后会异步同步已有容器；崩溃残留的托管容器会在下次启动接管，孤儿容器只报告、不自动删除。

### 5.4 配置变更

运行中的插件不能修改配置。停止后修改，下一次启动会删除旧容器并按新配置重建；镜像、插件记录和持久化数据保留。

### 5.5 卸载

- 默认：删除容器和实例，保留镜像、插件元信息与数据目录；插件回到 `not_installed`，可直接重新安装。
- `deleteImages`：同时删除镜像记录和 Docker 镜像；没有剩余镜像时删除插件记录。
- `deleteData`：删除插件挂载的宿主机持久化目录，必须额外传 `confirmDataDeletion: true`，界面要求输入确认文本。

## 6. 生命周期事件

Lan Copy 在文件、文件夹和文本操作**成功提交后**，向声明了订阅的插件异步推送事件。推送不阻塞上传、删除等响应；失败只记录状态，不回滚业务操作。

### 6.1 订阅声明

Manifest 增加可选字段：

```json
{
  "endpoints": {
    "health": "/health",
    "manifest": "/manifest",
    "version": "/version",
    "capabilities": "/capabilities",
    "events": "/events"
  },
  "events": ["file.uploaded", "folder.*", "text.*"]
}
```

- `events` 最多 32 项，支持精确类型（`file.uploaded`）、单层通配（`file.*`、`folder.*`、`text.*`）和全局通配 `*`。
- `endpoints.events` 缺省为 `/events`，必须是以 `/` 开头的绝对路径。
- 省略 `events` 或使用空数组表示不订阅；旧插件不会收到任何事件。
- 插件启动时 `/manifest` 需返回相同订阅和端点，便于复核身份。

### 6.2 请求格式与签名

```http
POST /events
Content-Type: application/json
X-Lan-Copy-Event: folder.created
X-Lan-Copy-Event-Id: evt-01J...
X-Lan-Copy-Timestamp: 1763435045
X-Lan-Copy-Signature: v1=<hex>
```

```json
{
  "schemaVersion": 1,
  "id": "evt-01J...",
  "type": "folder.created",
  "occurredAt": "2026-11-18T03:04:05Z",
  "data": { "path": "docs/2026", "name": "2026" }
}
```

签名：

```text
HMAC-SHA256(key = LAN_COPY_EVENT_TOKEN, message = timestamp + "." + 原始请求体)
```

- 事件密钥由 Lan Copy 的插件密钥和实例 ID 派生，不写入 `state.json`，通过保留环境变量 `LAN_COPY_EVENT_TOKEN` 注入容器；配置中不能覆盖，实例重装后密钥轮换。
- 建议校验时间戳在 5 分钟内，并使用常量时间比较。
- 插件应返回 2xx。事件 ID 全局唯一，请按 `id` 幂等处理，重试可能产生重复投递。

### 6.3 事件目录

| 事件 | 触发时机 | 主要字段 |
| --- | --- | --- |
| `file.uploaded` | 上传事务提交成功 | `destination`、`files[{path,size}]`、`directories[]`、`counts`、`truncated` |
| `file.deleted` | 删除共享文件成功 | `path`、`name`、`size` |
| `file.renamed` | 文件原地重命名成功 | `path`、`name`、`previousPath`、`previousName` |
| `file.moved` | 文件移动成功 | `path`、`name`、`previousPath`、`previousName` |
| `folder.created` | 新建文件夹成功 | `path`、`name` |
| `folder.deleted` | 删除文件夹成功 | `path`、`name`、`recursive` |
| `folder.renamed` | 文件夹原地重命名成功 | `path`、`name`、`previousPath`、`previousName` |
| `folder.moved` | 文件夹移动成功 | `path`、`name`、`previousPath`、`previousName` |
| `text.created` | 新建文本或图文碎片成功 | `id`、`kind`、`title`、`size`、`expiresAt` |
| `text.deleted` | 删除碎片成功 | `id`、`kind`、`title`、`size` |
| `text.saved` | 碎片转存为共享文件成功 | `id`、`files[{path,name}]` |
| `system.ping` | 仅由“发送测试事件”触发 | `requestedBy`、`at` |

路径均为共享根目录内的相对路径。`file.uploaded` 的列表最多 100 项，超出时 `truncated=true`，`counts` 始终是实际数量。

### 6.4 投递语义

- 每插件一个 FIFO 队列，容量 128；队列满时丢弃最旧事件。
- 单次超时 5 秒，最多 3 次尝试（1 秒、2 秒退避）；网络错误、超时、`429`、`408` 和 `5xx` 重试，其余 `4xx` 立即丢弃。
- 连续失败 5 次后暂停投递 60 秒。
- 只有状态 `running` 且健康 `healthy` 的实例会收到事件；停止、启动中或不健康时事件被丢弃。
- 事件和投递记录只驻留内存，Lan Copy 重启后清空；不提供补发或重放。
- 升级后已有实例如果是在旧版本创建的，需要重启一次以获得 `LAN_COPY_EVENT_TOKEN`。

### 6.5 调试

在 **设置 → 插件 → 对接** 查看事件目录、订阅、已投递/失败/丢弃计数和最近 50 条投递记录。运行中且健康的插件可点击 **发送测试事件**：

```bash
curl -X POST -H 'X-Lan-Copy-Action: plugins' \
  http://127.0.0.1:8080/api/plugins/demo-plugin/events/test

curl -s http://127.0.0.1:8080/api/plugins/demo-plugin/events
```

### 6.6 上传 Hook（同步页面回调）

生命周期事件适合后台处理，但不能把“这次上传需要用户确认”的页面带回给上传者。需要在上传成功后弹窗的插件，可以声明上传 Hook：

```json
{
  "ui": { "path": "/" },
  "uploadHook": {
    "path": "/hooks/upload",
    "timeoutSeconds": 5
  }
}
```

- `uploadHook` 可选；声明后 Lan Copy 在 `/api/upload-tree` 提交成功后调用一次。
- `path` 必须是以 `/` 开头的绝对路径，不含 `..`、查询串、片段，长度 ≤256。
- `timeoutSeconds` 范围 1–30，默认 5。Lan Copy 不会让上传响应无限等待 Hook。
- 只有 `running` 且 `healthy` 的实例会收到 Hook。
- 目录、扩展名、大小等业务过滤由插件自行完成；不需要处理时返回 `204`。
- Hook 不重试；网络错误、超时、非 2xx、响应格式错误都只记录日志，**上传仍然成功**。
- 复用 `LAN_COPY_EVENT_TOKEN` 和与生命周期事件相同的 HMAC-SHA256 签名。
- 页面只能位于插件自己的 `/plugin-app/<插件ID>/` 下；Lan Copy 拒绝外部 URL、`//`、`..`、片段和重复前缀。
- 声明 `uploadHook` 需要 Lan Copy v0.13.0 及以上；旧版会因严格 Manifest 解析拒绝安装。

请求：

```http
POST /hooks/upload
Content-Type: application/json
X-Lan-Copy-Hook: file.uploaded
X-Lan-Copy-Hook-Id: hook-01J...
X-Lan-Copy-Timestamp: 1763435045
X-Lan-Copy-Signature: v1=<hex>
```

请求体与 `file.uploaded` 事件信封一致：

```json
{
  "schemaVersion": 1,
  "id": "hook-01J...",
  "type": "file.uploaded",
  "occurredAt": "2026-11-23T03:04:05Z",
  "data": {
    "destination": "发票",
    "files": [{ "path": "发票/a.pdf", "size": 182344 }],
    "directories": [],
    "counts": { "files": 1, "directories": 0 },
    "truncated": false
  }
}
```

签名：

```text
HMAC-SHA256(key = LAN_COPY_EVENT_TOKEN, message = timestamp + "." + 原始请求体)
```

响应（2xx）：

```json
{
  "schemaVersion": 1,
  "dialogs": [
    { "title": "确认发票信息", "path": "/review/01J..." }
  ]
}
```

- `204`、空响应体或 `dialogs: []` 表示这次上传不需要弹窗。
- 单插件最多 5 个弹窗，单次上传总计最多 10 个，超出截断。
- `title` 最长 100 字符；`path` 最长 1024，允许查询串。
- 响应体上限 64 KiB。

Lan Copy 把返回页面放进当前文件页的弹窗 iframe：

```text
/plugin-app/<插件ID><path>
```

插件页面要求：

1. 读取 `X-Forwarded-Prefix`，按 `/plugin-app/<插件ID>` 生成 API 地址。
2. 允许被同源 iframe 嵌入，例如响应当前页面时使用 `frame-ancestors 'self'`，不要设置 `X-Frame-Options: DENY`。
3. 页面完成后可通知父页面：

```js
window.parent.postMessage({
  type: 'lan-copy:plugin-dialog:complete',
  refresh: true,
  message: '已保存'
}, window.location.origin)
```

4. 只关闭弹窗、不完成业务时发送：

```js
window.parent.postMessage({ type: 'lan-copy:plugin-dialog:close' }, window.location.origin)
```

5. 页面与 Lan Copy 同源，拥有与主页面相同的 API 能力；只安装可信镜像，不要把弹窗当作安全隔离。

**示例：发票审核**。Hook 只做目录/类型/大小校验，立即返回审核页并创建会话；OCR 在插件后台执行，页面轮询结果，用户修改后确认，插件再用 `LAN_COPY_API_TOKEN` 调用智能表格 API 写入。不要在 Hook 内同步执行耗时 OCR。

## 7. 安全边界

| 项 | 固定行为 |
| --- | --- |
| 用户 | 必须非 root |
| 根文件系统 | 只读 |
| 临时目录 | tmpfs `/tmp`，128 MiB，`noexec,nosuid,nodev` |
| 特权 | 禁止 |
| Capabilities | `cap-drop ALL` |
| 提权 | `no-new-privileges:true` |
| 命名空间 | 不使用 host PID/网络/IPC/UTS |
| Docker / Podman Socket | 禁止挂载 |
| 设备 | 禁止 |
| 网络 | 只加入专用网络；默认端口只发布到 `127.0.0.1` |
| CPU/内存/PID | 必须有上限 |
| 内存交换 | `MemorySwap = Memory` |
| 日志 | json-file（Podman 映射为 k8s-file），大小和数量受限 |
| 重启策略 | 默认 `no`，可选 `on-failure[:N]` |
| 生命周期事件 | 每实例独立派生密钥，不落盘，仅注入插件容器；请求带 HMAC-SHA256 签名 |
| 上传 Hook | 与生命周期事件共用实例密钥和签名；同步调用、有超时、不重试；失败不影响上传 |
| 上传 Hook 页面 | 只接受插件相对路径，统一拼接 `/plugin-app/<id>`；拒绝任意外部 URL |
| 表格插件 API | 每实例独立 Bearer 令牌，仅授权 `/api/plugin-api/tables*`；令牌不写入状态文件 |
| 插件容器网络 | `loopback` 模式只增加固定主机映射：Docker 为 `host.docker.internal:host-gateway`，Linux 原生 Podman 为 `host.containers.internal:host-gateway`；Windows/macOS 的 podman machine 不增加映射，改用宿主机局域网 IPv4（见第 8.1 节），拒绝任意 ExtraHosts |
| 管理接口访问范围 | 局域网可达；写操作要求动作头与同源，但无账号认证。任何能访问服务的设备都可请求启动本机 Podman，请只部署在可信局域网或加防火墙/反向代理认证 |
| 插件 Web 界面 | 只代理到本程序管理的运行中实例，不接受任意 URL；入口默认对局域网开放，页面与 Lan Copy 同源 |

持久化目录：

- 只允许 bind mount。
- 宿主机路径必须是绝对路径，解析软链接后位于允许根目录内。
- 默认允许根：`<应用数据目录>/plugins/data`。
- 拒绝 `docker.sock`、`podman.sock`、`/var/run`、`/run`、`/proc`、`/sys`、`/dev` 等位置。
- 容器路径不能是 `/`、系统目录或 `/tmp`。

## 8. 网络模式

| 模式 | 使用场景 | 行为 |
| --- | --- | --- |
| `loopback`（默认） | Lan Copy 运行在宿主机（现有发布形态） | 容器端口发布到 `127.0.0.1:<随机端口>`，Client 使用该地址 |
| `container` | Lan Copy 也运行在 Docker 中 | 不发布宿主端口，Client 使用 `http://<容器名>:<端口>` |

启动参数：

```bash
lan-copy -plugin-network-mode=loopback -plugin-network=plugin-network
```

### 8.1 插件 API 地址

插件通过 `LAN_COPY_API_URL` 访问 Lan Copy。地址优先级从高到低：

| 优先级 | 场景 | 注入地址 | ExtraHosts |
| --- | --- | --- | --- |
| 1 | 启动参数 `-plugin-api-url` 非空 | 用户指定值（去掉尾部 `/`） | 仍按引擎/宿主规则，不因显式地址新增映射 |
| 2 | `container` 模式 | `http://lan-copy:<监听端口>` | 无 |
| 3 | Docker（任意宿主） | `http://host.docker.internal:<监听端口>` | `host.docker.internal:host-gateway` |
| 4 | Podman + Linux | `http://host.containers.internal:<监听端口>` | `host.containers.internal:host-gateway` |
| 5 | Podman + Windows/macOS（podman machine） | `http://<宿主局域网 IPv4>:<监听端口>`；无可用地址时不注入 | 无 |

Podman 在 Windows/macOS 上运行于 `podman machine` 的 Linux 虚拟机，容器里的 `host.containers.internal` 指向虚拟机而不是宿主机，因此在 machine 场景改用宿主局域网地址。Linux 原生 Podman（含 rootless）继续使用 `host.containers.internal`。

局域网 IPv4 的选择规则固定且可解释：

1. 只使用 IPv4，排除回环、`169.254.0.0/16` 链路本地、未指定和组播地址。
2. 排除已关闭、回环、点对点接口和常见虚拟网卡（`docker`、`veth`、`br`、`vmnet`、`vboxnet`、`vEthernet`/`WSL`、`utun`/`tun`/`tap`、`tailscale`、`wg`、`bridge` 等）。
3. 依次优先 `192.168.0.0/16`、`10.0.0.0/8`、`172.16.0.0/12`、其他 IPv4；同一档按 IP 数值和网卡名稳定排序。
4. 没有可用地址时不注入 `LAN_COPY_API_URL`，并在日志和插件页提示用 `-plugin-api-url` 指定宿主机可达地址；不会回退到只在虚拟机内可达的 `host.containers.internal`。

创建容器时会打印实际注入的地址与理由，例如：

```text
插件 API 地址: http://192.168.1.20:8080（Podman machine（windows）中 host.containers.internal 指向虚拟机，改用宿主网卡 Wi-Fi 的局域网 IPv4 192.168.1.20）
```

插件页顶部的运行时状态卡也会只读展示 `apiBaseUrl` 与 `apiBaseUrlReason`（对应 `GET /api/plugins/runtime`），便于现场排查。

地址只在创建容器时注入。**重启插件会重建容器并重新注入**；卸载后重新安装同样生效。普通启动会沿用已有容器，不会改变其中的环境变量。若宿主机只监听 `127.0.0.1`，或防火墙未放行监听端口，局域网地址仍不可达，请改用 `-listen :<端口>` 或 `-plugin-api-url` 指定可达地址。

容器部署示例（仅把 Docker 或 Podman Socket 交给 Lan Copy 主服务，绝不交给插件）：

```bash
docker network create plugin-network
docker run -d --name lan-copy \
  --network plugin-network \
  -p 8080:8080 \
  -v "$PWD/lan-copy-data:/data" \
  -v /var/run/docker.sock:/var/run/docker.sock \
  lan-copy -plugin-network-mode=container
```

注意：容器内的 Lan Copy 必须挂载对应运行时 Socket 才能管理插件（Podman 为 `/run/podman/podman.sock`，容器内路径需与 `DOCKER_HOST` 一致）；插件容器不会获得该 Socket。

## 9. API

所有 `/api/plugins*` 接口对能访问 Lan Copy 的设备开放。写操作要求：

```http
X-Lan-Copy-Action: plugins
```

镜像上传使用 `X-Lan-Copy-Action: plugins-image`。浏览器调用还必须同源。

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/api/plugins/runtime` | 运行时类型（Docker/Podman）、版本、架构、网络模式、数据根、孤儿容器；含只读的 `apiBaseUrl` 与 `apiBaseUrlReason`；不可用时含 `startAvailable` |
| POST | `/api/plugins/runtime/start` | 启动本机 Podman（Linux systemd 或 Windows/macOS `podman machine`），返回任务 |
| GET | `/api/plugins` | 插件列表 |
| POST | `/api/plugins/install` | 安装，返回任务 |
| GET | `/api/plugins/{id}` | 详情 |
| DELETE | `/api/plugins/{id}` | 卸载，返回任务 |
| POST | `/api/plugins/{id}/start` | 启动，返回任务 |
| POST | `/api/plugins/{id}/stop` | 停止，返回任务 |
| POST | `/api/plugins/{id}/restart` | 重启，返回任务 |
| GET | `/api/plugins/{id}/status` | 实际状态、资源用量、健康记录、调用指标 |
| GET | `/api/plugins/{id}/health` | 最近健康检查 |
| GET | `/api/plugins/{id}/logs?tail=200` | 纯文本日志，最多 512 KiB |
| GET | `/api/plugins/{id}/config` | 配置，secret 已脱敏 |
| PUT | `/api/plugins/{id}/config` | 修改配置（必须停止） |
| GET | `/api/plugins/{id}/manifest` | Manifest |
| GET | `/api/plugins/{id}/capabilities` | 能力 |
| GET | `/api/plugins/images` | 镜像列表 |
| POST | `/api/plugins/images/upload` | 上传镜像，返回任务 |
| GET | `/api/plugins/images/{id}` | 镜像详情 |
| DELETE | `/api/plugins/images/{id}` | 删除镜像（使用中返回 409） |
| GET | `/api/plugins/tasks` | 最近任务 |
| GET | `/api/plugins/tasks/{id}` | 任务状态 |
| GET | `/api/tables` | 智能表格 CSV 列表 |
| POST | `/api/tables/mark` | 标记或取消 CSV 的智能表格标记，要求 `X-Lan-Copy-Action: tables` |
| POST | `/api/tables/query` | 查询、筛选、排序、分页 |
| POST | `/api/tables/create` | 新建空表，要求 `X-Lan-Copy-Action: tables` |
| POST | `/api/tables/lock-and-update` | 原子加锁更新，要求 `X-Lan-Copy-Action: tables` |
| GET | `/api/plugin-api/tables` | 插件侧表格列表，Bearer 令牌 |
| POST | `/api/plugin-api/tables/query` | 插件侧查询，Bearer 令牌 |
| POST | `/api/plugin-api/tables/lock-and-update` | 插件侧原子更新，Bearer 令牌 |
| GET | `/api/plugins/event-catalog` | 生命周期事件目录及示例数据 |
| GET | `/api/plugins/{id}/events` | 订阅、事件端点、投递统计和最近 50 条记录 |
| POST | `/api/plugins/{id}/events/test` | 发送 `system.ping` 测试事件，返回 `202` 与事件 ID |

示例：

```bash
curl -X POST -H 'X-Lan-Copy-Action: plugins-image' \
  -F 'image=@invoice-ocr-1.0.0.tar.gz' \
  http://127.0.0.1:8080/api/plugins/images/upload

curl -X POST -H 'X-Lan-Copy-Action: plugins' -H 'Content-Type: application/json' \
  -d '{"imageId":"img-...","config":{"memoryMB":512,"cpuLimit":0.5},"start":true}' \
  http://127.0.0.1:8080/api/plugins/install
```

异步任务返回 `202` 与 `{"taskId":"..."}`，轮询任务接口直到 `succeeded` 或 `failed`。参数校验在请求阶段返回 4xx；已安装冲突、Docker 不可用、启动失败等异步错误在任务的 `error` 字段返回。

## 10. 配置字段

| 字段 | 范围 |
| --- | --- |
| `env` | 最多 128 个；名称 `[A-Za-z_][A-Za-z0-9_]*`，值 ≤ 4096 字节 |
| `cpuLimit` | 0.1–8.0 |
| `memoryMB` | 64–16384 |
| `pidsLimit` | 16–4096 |
| `restartPolicy` | `no`、`on-failure`、`on-failure:1..10` |
| `mounts` | 最多 8 个，必须位于允许根目录 |
| `healthCheck.path` | 以 `/` 开头，不允许 `..`、查询串 |
| `healthCheck.intervalSeconds` | 5–3600 |
| `healthCheck.timeoutSeconds` | 1–60 |
| `healthCheck.retries` | 1–10 |
| `healthCheck.startupTimeoutSeconds` | 10–300 |
| `requestTimeoutSeconds` | 1–300 |
| `logMaxSizeMB` / `logMaxFiles` | 1–50 / 1–5 |

服务端会拒绝保留变量名 `DOCKER_HOST`、`LD_PRELOAD`、`LD_LIBRARY_PATH`、`LAN_COPY_EVENT_TOKEN`、`LAN_COPY_API_URL`、`LAN_COPY_API_TOKEN`。

### 敏感环境变量

- `secret: true` 的值使用 AES-256-GCM 加密保存。
- 密钥来自 `LAN_COPY_PLUGIN_SECRET_KEY`（base64 32 字节），未设置时使用 `<dataDir>/plugins/secret.key`（0600）。
- API 和任务结果中 secret 值始终为空；更新时留空表示保留原值。
- 密钥丢失时相关插件拒绝启动，不会以空值继续。
- 同用户权限的本地进程仍可读取密钥，该加密用于避免明文落盘和备份泄露，不等同于系统密钥链。

## 11. Go Plugin Client

业务代码通过插件 ID 调用，不构造容器地址：

```go
client := pluginManager.Client()

var result struct {
    Message string `json:"message"`
}
request := plugin.Request{
    Method: http.MethodPost,
    Path:   "/v1/recognize",
    JSON:   input,
    Timeout: 20 * time.Second,
}
_, err := client.Do(ctx, "invoice-ocr", request, &result)
switch {
case errors.Is(err, plugin.ErrPluginNotInstalled):
case errors.Is(err, plugin.ErrPluginNotRunning):
case errors.Is(err, plugin.ErrPluginUnhealthy):
case errors.Is(err, plugin.ErrPluginTimeout):
case errors.Is(err, plugin.ErrPluginUnavailable):
case errors.Is(err, plugin.ErrPluginRequestFailed):
}
```

能力：

- GET/POST/PUT/DELETE、JSON、multipart 流式上传、流式下载
- 默认 30 秒超时，最大 300 秒
- 默认响应上限 8 MiB，下载上限 64 MiB
- 不跨主机跟随重定向
- 默认不自动重试
- 所有错误可 `errors.Is` 分类

## 12. 持久化与恢复

```text
<应用数据目录>/plugins/
  state.json      # 插件、镜像、实例、任务、健康记录，0600
  state.backup    # 上一份有效快照
  secret.key      # 0600
  tmp/            # 镜像上传临时文件，任务结束清理
  data/           # 默认允许的持久化根目录
```

- 单文件原子写入，避免跨实体半写。
- 启动时任务最多保留 100 条，健康记录每个实例最多 50 条。
- 中断任务在重启后标记失败。
- 容器状态以 Docker 为准；DB 与容器不一致时启动同步会修复。

## 13. 测试

```bash
go test ./...
go vet ./...

# 真实 Docker 集成测试（需要本机 Docker 和示例镜像包）
cd scripts/plugin-example
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o plugin-server .
docker build -t lan-copy-demo-plugin:1.0 .
docker save lan-copy-demo-plugin:1.0 | gzip > /tmp/lan-copy-demo-plugin.tar.gz
rm plugin-server
cd ../..
LAN_COPY_DOCKER_TEST=1 LAN_COPY_PLUGIN_TEST_IMAGE=/tmp/lan-copy-demo-plugin.tar.gz \
  go test ./internal/plugin -run TestDockerIntegration -v
```

前端：

```bash
cd frontend
npm run check
npm run check-assets
```

## 14. 排错

| 现象 | 检查 |
| --- | --- |
| 页面显示容器运行时不可用 | Docker/Podman 是否运行；`DOCKER_HOST` / `CONTAINER_HOST` 是否正确；当前用户能否访问 socket/npipe |
| 导入失败：无 Manifest | 镜像是否有 `io.lan-copy.plugin.manifest` 标签 |
| 导入失败：root | 镜像是否声明非 root `USER` |
| 导入失败：架构不匹配 | 镜像架构是否与容器运行时一致 |
| 启动失败：健康检查超时 | `/health` 是否返回 200 和 `{"status":"ok"}`；启动超时是否足够 |
| 启动失败：protocol mismatch | `/manifest` 的 `id`/`protocolVersion` 是否与镜像标签一致 |
| 调用返回 `ErrPluginUnhealthy` | 查看详情健康记录和容器日志 |
| 调用超时 | 调整 `requestTimeoutSeconds`，检查插件负载 |
| 日志为空 | 容器是否启动过；插件是否输出到 stdout/stderr |
| 容器运行但 Lan Copy 无法访问 | 网络模式是否与部署方式匹配（宿主机用 `loopback`，容器内用 `container`） |
| 收不到生命周期事件 | Manifest 是否声明 `events`；实例是否 `running` 且 `healthy`；页面“对接”近期记录是否显示丢弃 |
| 事件签名校验失败 | 是否读取 `LAN_COPY_EVENT_TOKEN`；是否对原始请求体计算 HMAC；时间戳是否在 5 分钟内 |
| 升级后原有插件收不到事件 | 重启一次插件，让容器获得 `LAN_COPY_EVENT_TOKEN` |
| 插件界面返回 404 | Manifest 是否声明 `ui`；插件 ID 是否正确；实例是否已安装 |
| 插件界面返回 503 | 实例是否 `running` 且健康状态为 `healthy`；查看健康记录和容器日志 |
| 插件界面资源 404 或白屏 | 插件是否读取 `X-Forwarded-Prefix` 生成资源地址，而不是写死根路径 `/assets/...` |
| 插件界面重定向到 Lan Copy 根路径 | 插件是否生成根绝对路径链接；`Location` 会被改写，但页面内的相对链接需要插件自行带前缀 |

## 15. Agent 集成与当前 API 能力

插件服务页右上角的 **复制 Agent Prompt** 会生成一段可直接交给编码 Agent 的说明，包含协议端点、Manifest 模板、镜像标签、非 root/只读根等安全要求、构建与 API 导入步骤。Prompt 使用当前页面地址作为示例 API 根地址。

当前 API 能力边界：

| 能力 | 是否支持 | 方式 |
| --- | --- | --- |
| 注册插件元信息 | 间接支持 | 没有独立注册接口；导入带 `io.lan-copy.plugin.manifest` 标签的镜像时自动创建/更新插件记录 |
| 上传镜像 | 支持 | `POST /api/plugins/images/upload`，异步任务 |
| 新增/保留镜像版本 | 支持 | 再次上传同一插件的新版本会新增镜像记录；已安装版本不被改写 |
| 安装并创建容器 | 支持 | `POST /api/plugins/install`，`start` 可同时启动 |
| 启动已有容器 | 支持 | `POST /api/plugins/{id}/start` |
| 原地切换运行实例的镜像 | 暂不支持 | 先卸载（默认保留镜像和数据），再从新镜像安装并启动 |
| 修改已有镜像记录 | 不支持（设计如此） | 镜像记录不可变；上传新版本生成新记录 |
| 删除镜像 | 支持 | `DELETE /api/plugins/images/{id}`，使用中返回 409 |
| 生命周期事件 | 支持 | Manifest `events` + `POST /events`；页面“对接”可查看投递状态并发送测试事件 |
| 插件 Web 界面 | 支持 | Manifest `ui` + `GET /plugin-app/{id}/` 反向代理；插件按 `X-Forwarded-Prefix` 生成子路径地址 |

所有接口对能访问 Lan Copy 的设备开放；导入使用 `X-Lan-Copy-Action: plugins-image`，其余写操作使用 `plugins`，浏览器调用还必须同源。动作头用于 CSRF 防护，不是身份认证；任何能访问服务的设备都可以管理插件，请只安装可信镜像。异步操作返回 `202` 和 `taskId`。


## 16. 智能表格插件 API

共享目录中的 `.csv` 文件可以直接作为智能表格查询和更新。浏览器内的“表格”入口是每个 CSV 的可选标记，保存在应用数据目录的 `table-marks.json`；插件 API 不要求先标记，仍按显式 `path` 访问任意合法 CSV。插件通过安装时注入的环境变量访问：

```text
LAN_COPY_API_URL=http://host.docker.internal:8080
LAN_COPY_API_TOKEN=<每实例令牌>
```

- Docker 在 `loopback` 模式默认使用 `http://host.docker.internal:<监听端口>`，并增加固定 `host.docker.internal:host-gateway` 映射。
- Linux 原生 Podman 使用 `http://host.containers.internal:<监听端口>`，并增加固定 `host.containers.internal:host-gateway` 映射。
- Windows/macOS 的 podman machine 使用 `http://<宿主局域网 IPv4>:<监听端口>`，不增加宿主机映射；没有可用 IPv4 时不注入 `LAN_COPY_API_URL` 并提示用 `-plugin-api-url`。选择规则、日志与排查见第 8.1 节。
- `container` 模式默认使用 `http://lan-copy:<监听端口>`；启动参数 `-plugin-api-url` 始终具有最高优先级。
- 如果 Lan Copy 只监听 `127.0.0.1`，或宿主机防火墙未放行监听端口，插件可能无法访问；请改用可被容器访问的监听地址，或用 `-plugin-api-url` 指向可达地址。
- 地址在创建容器时注入。重启插件（会重建容器）或重新安装后生效；普通启动沿用已有容器。
- 令牌只授权 `/api/plugin-api/tables*`，不能访问插件管理、普通文件、更新或关停接口。停止的实例令牌失效。

### 16.1 表格规则

- 只支持共享根目录内 `.csv` 普通文件，第一行是列名；拒绝符号链接和越界路径。
- 单文件 ≤ 8 MiB、≤ 50,000 行、≤ 200 列，单元格 ≤ 64 KiB。
- UTF-8，支持 BOM、CRLF 和 RFC 4180 引号；写入会规范化引号和换行。
- 版本是文件内容的 SHA-256；查询返回 `version`，更新必须提交当前版本。

### 16.2 查询

```http
POST /api/plugin-api/tables/query
Authorization: Bearer <LAN_COPY_API_TOKEN>
Content-Type: application/json
```

```json
{
  "path": "data/orders.csv",
  "q": "pending",
  "filters": [{ "column": "status", "op": "eq", "value": "pending" }],
  "sort": { "column": "updatedAt", "order": "desc" },
  "limit": 100,
  "offset": 0
}
```

筛选操作：`eq`、`neq`、`contains`、`prefix`、`gt`、`gte`、`lt`、`lte`、`empty`、`not_empty`。`contains` 不区分大小写；比较优先按数字，否则按字符串。`limit` 最大 500。

### 16.3 lock_and_update

```http
POST /api/plugin-api/tables/lock-and-update
Authorization: Bearer <LAN_COPY_API_TOKEN>
Content-Type: application/json
```

```json
{
  "path": "data/orders.csv",
  "expectedVersion": "sha256:...",
  "keyColumn": "id",
  "operations": [
    { "kind": "insert", "values": { "id": "A-2", "status": "new" } },
    { "kind": "update", "key": "A-1", "changes": { "status": "done" } },
    { "kind": "delete", "key": "A-3" }
  ]
}
```

- 一次请求内完成加锁、读取、版本校验、全部操作校验、写回和解锁；任一操作失败都不改文件。
- `update` 和 `delete` 必须提供唯一且非空的 `keyColumn`；不能通过 `changes` 修改主键列。
- 单次最多 1,000 个操作；未知列、重复键、键不存在返回明确的 4xx 或 409。
- 成功返回：

```json
{
  "path": "data/orders.csv",
  "version": "sha256:...",
  "applied": { "inserted": 1, "updated": 1, "deleted": 1 }
}
```

- 版本冲突返回 409，并返回当前 `version`；重新查询后再重试。
- 事件投递和表格更新都发生在文件锁之外/内的一致边界，表格更新不会绕过共享目录路径校验。

### 16.4 页面

在内网空间的文件行菜单把 CSV 标记为智能表格后，文件行显示“表格”标记，点击或双击打开单文件内联视图，支持全文搜索（输入即搜并高亮匹配）、排序、分页、列顺序调整、列隐藏、行新增/编辑/删除和版本冲突提示。内联视图写操作同样使用 `lock-and-update`，并要求 `X-Lan-Copy-Action: tables`；标记本身不改变 CSV 内容或版本。

列顺序与列隐藏是浏览器展示偏好，保存在 CSV 同目录的 `<完整表格文件名>.hidden.metadata.json` 中。它不改变 CSV 物理列顺序、`version` 或插件 API 响应；`/api/plugin-api/tables*` 不读取也不返回该偏好。Lan Copy 的文件列表、搜索、下载、预览和缩略图都会隐藏这个元数据文件。插件无需适配，也不应依赖该文件的内容或存在性。浏览器列偏好接口自 v0.14.0 起提供，插件协议与最低插件版本不变。
