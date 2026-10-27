package printing

import (
	"testing"
	"unsafe"
)

// These sizes/offsets are the Windows ABI, not Go serialization formats.
func TestNativePrintingABI(t *testing.T) {
	if unsafe.Sizeof(bitmapHeader{}) != 40 {
		t.Fatalf("BITMAPINFOHEADER: %d", unsafe.Sizeof(bitmapHeader{}))
	}
	ptrSize := unsafe.Sizeof(uintptr(0))
	wantDoc, wantPrinter := uintptr(20), uintptr(84)
	if ptrSize == 8 {
		wantDoc, wantPrinter = 40, 136
	}
	if unsafe.Sizeof(docInfo{}) != wantDoc || unsafe.Sizeof(printerInfo2{}) != wantPrinter {
		t.Fatalf("DOCINFOW=%d PRINTER_INFO_2W=%d", unsafe.Sizeof(docInfo{}), unsafe.Sizeof(printerInfo2{}))
	}
	metrics := deviceMetrics{width: 4961, height: 7016, left: 118, top: 118, printWidth: 4725, printHeight: 6780, dpiX: 600, dpiY: 600}
	layout := metrics.layout()
	if !layout.valid() || layout.Width < 209 || layout.Width > 211 || layout.Height < 296 || layout.Height > 298 {
		t.Fatalf("layout=%+v", layout)
	}
	changed := metrics
	changed.left++
	if changed.layout().Key == layout.Key {
		t.Fatal("layout key ignores device margins")
	}
}
