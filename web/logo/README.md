# Lan Copy Logo

本目录包含 Lan Copy 的品牌 logo 文件。

## 文件说明

| 文件名 | 尺寸 | 用途 |
|--------|------|------|
| `lan-copy-logo.svg` | 120x120 | 主 logo，用于品牌展示 |
| `favicon-16x16.svg` | 16x16 | 小尺寸图标，用于 favicon |
| `favicon-32x32.svg` | 32x32 | 中等尺寸图标，用于 favicon |
| `logo-192x192.svg` | 192x192 | 大尺寸图标，用于高分辨率显示 |

## 设计理念

Lan Copy 的 logo 设计体现了以下核心概念：

1. **局域网连接**：两个设备（电脑和手机）通过网络连接，象征局域网内的设备互联
2. **文件传输**：中间的文件图标表示文件传输功能
3. **简洁现代**：使用几何形状和蓝色调，体现现代软件的设计风格

## 颜色方案

- 主蓝色：`#3977f6` (主要品牌色)
- 深蓝色：`#2362df` (辅助色)
- 深色背景：`#14213d` (用于深色主题)
- 白色：`#ffffff` (用于前景元素)

## 使用方法

### Web 界面
在 `web/index.html` 中使用 SVG 格式的 logo：

```html
<img src="/logo/lan-copy-logo.svg" alt="Lan Copy Logo" width="120" height="120">
```

### Windows 应用程序
要为 Windows 可执行文件添加图标，需要将 SVG 转换为 .ico 格式，然后使用 Go 的资源嵌入工具。

### 文档和宣传材料
可以使用 `logo-192x192.svg` 作为高分辨率 logo。

## 转换为其他格式

如需将 SVG 转换为其他格式（PNG、ICO 等），可以使用以下工具：

- **在线转换**：SVG 转 PNG 在线工具
- **命令行工具**：ImageMagick (`convert logo.svg logo.png`)
- **设计软件**：Figma、Adobe Illustrator 等

## 注意事项

1. 保持 logo 的比例和颜色一致性
2. 在深色背景上使用时，确保足够的对比度
3. 最小使用尺寸：16x16 像素