<template>
    <dialog id="previewDialog" class="preview-dialog" aria-labelledby="previewTitle">
      <div class="preview-dialog-toolbar"><h2 id="previewTitle">文件预览</h2><div><a id="previewDownload" class="secondary hidden">下载文件</a><button id="previewClose" class="secondary" type="button">关闭</button></div></div>
      <p id="previewStatus" class="preview-status" role="status">正在读取文件…</p>
      <div id="textTools" class="preview-text-tools hidden"><label for="textEncoding">文本编码</label><select id="textEncoding"><option value="utf-8">UTF-8</option><option value="gb18030">GB18030 / GBK</option><option value="utf-16le">UTF-16 LE</option><option value="utf-16be">UTF-16 BE</option></select></div>
      <section id="previewContent" class="preview-content" aria-label="文件内容"></section>
      <p class="preview-footnote">文件通过局域网直接读取 · 预览失败时可下载原文件</p>
    </dialog>
</template>
