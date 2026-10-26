# Spreadsheet preview assets

`xlsx.full.min.js` is the browser build from the official SheetJS Community Edition 0.20.3 release, Apache-2.0. Source tarball: `https://cdn.sheetjs.com/xlsx-0.20.3/xlsx-0.20.3.tgz`; npm tarball integrity: `sha512-oLDq3jw7AcLqKWH2AhCpVTZl8mf6X2YReP+Neh0SJUzV/BdZYjth94tG5toiMB1PPrYtxOCfaoUCkvtuH+3AJA==`.

The local worker parses `.xls` and `.xlsx` files off the UI thread. Each file is limited to 32 MiB; each sheet preview shows at most 250 rows and 50 columns, and at most 20 sheet tabs are exposed. Formula cells display their cached values; macros are not executed. No runtime CDN request is made.

The `worker.js` adapter is project code. The upstream license is included as `LICENSE`.
