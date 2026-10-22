(() => {
  const limit = 10000;
  function collector(progress = () => {}) {
    const entries = [];
    function add(entry) {
      if (entry.path.split("/").length > 64) throw new Error("文件夹层级过深，最多支持 64 层");
      if (entries.length >= limit) throw new Error("文件和目录合计超过 10000 项，请分批上传");
      entries.push(entry);
      if (entries.length === 1 || entries.length % 50 === 0) progress(entries.length);
    }
    return {entries, add};
  }
  async function fromEntries(roots, progress) {
    const {entries, add} = collector(progress);
    async function visit(entry, prefix = "") {
      const relativePath = prefix + entry.name;
      if (entry.isDirectory) {
        add({path: relativePath, directory: true});
        const reader = entry.createReader();
        // Chromium returns directory children in batches (often at most 100).
        for (;;) {
          const children = await new Promise((resolve, reject) => reader.readEntries(resolve, reject));
          if (!children.length) break;
          for (const child of children) await visit(child, relativePath + "/");
        }
      } else if (entry.isFile) {
        const file = await new Promise((resolve, reject) => entry.file(resolve, reject));
        add({path: relativePath, directory: false, file});
      } else {
        throw new Error(`无法读取“${relativePath}”，请检查文件权限`);
      }
    }
    for (const root of roots) await visit(root);
    return entries;
  }
  function fromFiles(files, requireRelative = false, progress) {
    const {entries, add} = collector(progress);
    const directories = new Set();
    for (const file of files) {
      if (requireRelative && !file.webkitRelativePath) throw new Error("此浏览器未提供文件夹路径，请使用电脑浏览器拖拽文件夹上传");
      const relativePath = file.webkitRelativePath || file.name;
      const components = relativePath.split("/");
      for (let index = 1; index < components.length; index++) {
        const directory = components.slice(0, index).join("/");
        if (!directories.has(directory)) { directories.add(directory); add({path: directory, directory: true}); }
      }
      add({path: relativePath, directory: false, file});
    }
    return entries;
  }
  window.LanCopyUploadTree = {fromEntries, fromFiles};
})();
