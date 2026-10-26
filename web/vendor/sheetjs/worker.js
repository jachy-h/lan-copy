importScripts("/vendor/sheetjs/xlsx.full.min.js");

let workbook;
const maxRows = 250;
const maxColumns = 50;
const parserRows = maxRows + 1;

self.onmessage = event => {
  const {id, type, buffer, sheetName} = event.data;
  try {
    if (type === "load") {
      workbook = XLSX.read(buffer, {
        type: "array",
        sheetRows: parserRows,
        cellHTML: false,
        bookVBA: false,
        bookFiles: false,
        WTF: false,
      });
      const names = workbook.SheetNames.slice(0, 20);
      const sheet = names.length ? sheetData(names[0]) : null;
      self.postMessage({id, names, hasMoreSheets: workbook.SheetNames.length > names.length, sheet});
    } else if (type === "sheet" && workbook?.Sheets[sheetName]) {
      self.postMessage({id, sheet: sheetData(sheetName)});
    }
  } catch (error) {
    self.postMessage({id, error: "工作簿无法解析，文件可能已损坏或格式不受支持。"});
  }
};

function sheetData(name) {
  const sheet = workbook.Sheets[name];
  if (!sheet?.["!ref"]) return {name, rows: [], columns: [], startRow: 1, truncatedRows: false, truncatedColumns: false};
  const fullRange = XLSX.utils.decode_range(sheet["!fullref"] || sheet["!ref"]);
  const columnCount = Math.max(0, fullRange.e.c - fullRange.s.c + 1);
  const columns = Array.from({length: Math.min(columnCount, maxColumns)}, (_, index) => XLSX.utils.encode_col(fullRange.s.c + index));
  const rows = XLSX.utils.sheet_to_json(sheet, {header: 1, raw: false, defval: "", blankrows: true})
    .slice(0, maxRows)
    .map(row => columns.map((_, index) => String(row[index] ?? "")));
  const rowCount = fullRange.e.r - fullRange.s.r + 1;
  return {
    name,
    rows,
    columns,
    startRow: fullRange.s.r + 1,
    truncatedRows: rowCount > maxRows,
    truncatedColumns: columnCount > maxColumns,
  };
}
