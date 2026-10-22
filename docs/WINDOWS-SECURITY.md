# Windows 安全提示与签名发布

## 先确认拦截类型

三种提示需要不同处理，不能通过修改程序统一消除：

| 提示 | 含义与处理 |
| --- | --- |
| “Windows 已保护你的电脑”、未知发布者 | SmartScreen 对下载文件的信誉检查。正式发布时使用受信任的 Authenticode 代码签名证书，保持发布者身份一致。签名不保证新版本立即获得信誉。 |
| 病毒和威胁防护报告威胁名称，文件被隔离或删除 | Defender Antivirus 检测。保留威胁名称、文件 SHA256、Go 版本、Defender 引擎和安全情报版本；由开发者提交实际发布的 exe 复核。仅修改编译参数或签名不能保证消除检测。 |
| 防火墙已阻止此应用的部分功能 | 程序监听局域网 HTTP 端口时的网络权限提示。使用局域网传输需要用户或管理员允许其在可信的专用网络上通信；代码签名不会代替防火墙授权。 |

不要关闭 Defender、SmartScreen 或通过添加杀毒排除项来解决发布问题。

## 重复启动时文件消失的排查

“已关闭阻止可能不需要的应用设置，你的设备可能受到攻击”表示 PUA 防护设置关闭，并不是针对 `lan-copy.exe` 的恶意软件检测或删除记录。仅凭此提示不能认定 Defender 删除了程序。

首先查看“Windows 安全中心 → 病毒和威胁防护 → 保护历史记录”，寻找受影响项目包含 `lan-copy.exe`（或旧名称）的记录，记录检测时间、威胁名称、受影响路径和操作结果。也可在 PowerShell 中只读查询：

```powershell
Get-MpThreatDetection | Select-Object InitialDetectionTime, LastThreatStatusChangeTime, ThreatID, ActionSuccess, Resources | Format-List
Get-MpComputerStatus | Select-Object AMEngineVersion, AntivirusSignatureVersion, AntivirusSignatureLastUpdated, RealTimeProtectionEnabled
```

若没有对应记录，继续检查浏览器下载记录或其他安全软件的隔离记录；不要把一般设置提醒当作删除证据。若文件仍存在，补充其 SHA256 和 Authenticode 状态；若已被移除，保留原始压缩包和版本号以便核对实际文件。

代码检查发现：此前重复启动探测受“独占控制台”限制，某些启动方式会报端口占用；本次已移除此限制，并增加服务身份校验与短暂启动重试。此前本机文件打开使用 `rundll32.exe`，现已统一使用 `ShellExecuteW`；重复启动打开浏览器原本就使用 `ShellExecuteW`。未发现重复启动路径中存在自删除、修改 Defender 设置或计划任务的逻辑。这些代码调整不能证明或保证消除杀毒检测。

默认 `make windows` / `build.bat build` 仍未签名；未签名制品与新发布文件的信誉、静态或行为检测都需要结合实际检测记录调查，不能仅凭启动两次推断具体原因。正式发布继续使用下述签名流程；有确切检测记录时，将原始被检文件提交微软复核。

## 正式发布带签名的 Windows 包

`build.bat build` 和 `make windows` 仍生成**未签名开发制品**。仓库不附带签名证书，也不能在源码中生成一个能被其他电脑自动信任的证书。

在 Windows 发布机上准备：

1. Go 和 Windows SDK，将 `signtool.exe` 加入 PATH（例如使用 SDK 的开发者终端）。
2. 受信任 CA 签发的代码签名证书及对应私钥，安装到当前用户的个人证书存储区 `Cert:\CurrentUser\My`。硬件令牌需要供应商驱动。自签名证书不适合公开发布。
3. 在 PowerShell 中指定证书的 SHA-1 指纹（用于选择证书，实际文件签名算法为 SHA256），运行：

```powershell
$env:LAN_COPY_SIGN_CERT_SHA1 = '你的证书的40位十六进制指纹'
.\build.bat release
```

脚本先执行 Windows 原生测试与静态检查，再默认构建 amd64，签名并添加 RFC 3161 时间戳，验证签名、发布者与时间戳，最后打包并计算校验和。任何构建、签名或校验失败都会终止，不会将本次失败制品打包发布。输出位于 `release/windows-signed/`，与未签名制品分开保存。该目录可能保留上次成功构建的文件，因此必须确认命令成功后才可上传。

可以直接指定 SDK 工具路径、时间戳服务或目标架构：

```powershell
.\scripts\release-windows.ps1 -SignTool 'C:\path\to\signtool.exe' -TimestampUrl 'http://timestamp.digicert.com' -Architectures amd64
```

发布 `release/windows-signed/` 中的 ZIP 和 SHA256SUMS。不要再运行 `make windows` 替换它们，也不要在签名后修改 exe。

## 验证与误报复核

在干净的 Windows 机器上下载实际发布的压缩包，解压后检查：

```powershell
Get-AuthenticodeSignature '.\lan-copy.exe' | Format-List Status, StatusMessage, SignerCertificate, TimeStamperCertificate
Get-FileHash -Algorithm SHA256 '.\lan-copy.exe'
```

还需实际验证 Defender 扫描、SmartScreen 启动提示和专用网络传输。签名有效与 Defender 不拦截是不同的检查；macOS 交叉编译无法验证 Windows 的实时防护结果。

如仍有误报，由开发者通过 [Microsoft 文件复核入口](https://www.microsoft.com/en-us/wdsi/filesubmission) 提交**实际被拦截的发布文件**，选择对应的 Defender 产品并附上检测信息，记录提交编号。本脚本不会自动上传文件。

参考：[微软 SmartScreen 发布者指南](https://learn.microsoft.com/windows/apps/package-and-deploy/smartscreen-reputation)、[软件开发者误报 FAQ](https://learn.microsoft.com/en-us/defender-xdr/developer-faq)、[SignTool 文档](https://learn.microsoft.com/en-us/windows/win32/seccrypto/signtool)。

## English

`build.bat build` and `make windows` produce unsigned development binaries. For public Windows releases, install a trusted code-signing certificate with its private key in `Cert:\CurrentUser\My`, put Windows SDK SignTool on PATH, set `LAN_COPY_SIGN_CERT_SHA1` to the certificate thumbprint, and run `build.bat release` on Windows. The script tests, builds both architectures, signs with SHA256 and an RFC 3161 timestamp, verifies signatures, and packages signed binaries with checksums in `release/windows-signed/`. Upload only after a successful command; previous successful artifacts may remain after a failed attempt.

Signing does not guarantee immediate SmartScreen reputation or prevent antivirus false positives. Submit the exact detected release binary to Microsoft's file submission portal for review. Firewall prompts require permission for LAN access on a trusted private network. Do not disable Windows security protections to distribute this application.
