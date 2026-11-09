# DOCX browser preview assets

The browser loads these files only when a `.docx` preview is opened:

- `docx-preview.min.js` from `docx-preview` 0.4.1, Apache-2.0. npm tarball integrity: `sha512-Dv4g+LeE0swHpPvNC0lAT0YsR5Zq5pAxw7d3vHQ8EChBLgIrqqe9VPL5nVt2HBJap8bEZbXUI0TJob7kBeNU9w==`.
- `jszip.min.js` from `jszip` 3.10.2, MIT OR GPL-3.0-or-later. npm tarball integrity: `sha512-3l+rb15IOWtUhU0H5MFqES/T6Kh7abYwjosBey/vD6hDt8zoEffkSC5Ws5SGtgVw3gBx2NEbhTeSW1+kWkpyTQ==`.

Both files are served from the embedded local web directory. No CDN or runtime package installation is used. The DOCX renderer creates browser HTML from the source document; this is not a PDF conversion and pagination/font appearance can vary by browser and installed fonts. Preview is limited to 32 MiB and disables embedded HTML alt-chunks, comments and tracked changes.

`.doc` is not supported. The original file remains available from the normal download route.
