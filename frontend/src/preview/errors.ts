export function previewFailure(stage: string, error: unknown) {
  const name = error instanceof Error ? error.name : 'Error'
  const detail = error instanceof Error ? `${name}: ${error.message}` : String(error)
  let reason = '请刷新重试；如果仍然失败，可下载原文件。'
  if (name === 'PasswordException') reason = '此 PDF 需要密码，请下载后使用支持密码的阅读器打开。'
  else if (name === 'InvalidPDFException') reason = '文件不是有效的 PDF，或内容不完整，请重新上传或下载检查。'
  else if (/HTTP (401|403)\b/.test(detail)) reason = '服务拒绝了访问，请检查文件访问权限或服务端访问限制。'
  else if (/HTTP 404\b/.test(detail)) reason = '文件已被移动或删除，请返回文件列表刷新后重试。'
  else if (/HTTP 415\b/.test(detail)) reason = '此文件类型不支持浏览器预览，请下载原文件。'
  else if (/HTTP 5\d\d\b/.test(detail)) reason = '文件服务返回异常，请检查电脑上的服务状态后重试。'
  else if (/worker/i.test(detail)) reason = 'PDF 后台解析组件启动失败，可能被浏览器限制或组件未完整加载。请刷新重试或更新浏览器。'
  else if (/SyntaxError|is not a function|is not defined|not supported/i.test(detail)) reason = '浏览器缺少预览所需的能力，或组件加载不完整。请刷新重试；仍失败时请更新浏览器或下载文件。'
  else if (/fetch|network|HTTP|load.*module|import.*module/i.test(detail)) reason = '无法获取文件或预览组件。请确认手机与电脑仍连接同一局域网，且服务正在运行。'
  return { summary: `读取失败（${stage}）：${reason}`, detail }
}
