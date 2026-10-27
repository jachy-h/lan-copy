// Minimal boundary used by the preview engine; the library stays locally vendored.
export interface PDFRenderTask { promise: Promise<void>; cancel(): void }
export interface PDFViewport { width: number; height: number }
export interface PDFPage {
  cleanup(): void
  getViewport(options: { scale: number }): PDFViewport
  render(options: { canvasContext: CanvasRenderingContext2D; viewport: PDFViewport; transform: number[]; intent?: 'display' | 'print' }): PDFRenderTask
}
export interface PDFDocument { numPages: number; getPage(index: number): Promise<PDFPage> }
export interface PDFLoadingTask { promise: Promise<PDFDocument>; destroy(): Promise<void> }
export interface PDFModule {
  GlobalWorkerOptions: { workerSrc: string }
  getDocument(options: { url?: string; data?: Uint8Array; cMapUrl: string; cMapPacked: boolean; standardFontDataUrl: string; wasmUrl: string; isEvalSupported: boolean; disableAutoFetch: boolean; disableStream: boolean }): PDFLoadingTask
}
export async function loadPDF(): Promise<PDFModule> {
  const url = '/vendor/pdfjs/legacy/build/pdf.min.mjs'
  return import(/* @vite-ignore */ url) as Promise<PDFModule>
}
