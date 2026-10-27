package printing

import (
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"runtime"
	"syscall"
	"unsafe"
)

var (
	openPrinter        = spool.NewProc("OpenPrinterW")
	closePrinter       = spool.NewProc("ClosePrinter")
	documentProperties = spool.NewProc("DocumentPropertiesW")
	stretchDIBits      = gdi.NewProc("StretchDIBits")
	setStretchMode     = gdi.NewProc("SetStretchBltMode")
	setBrushOrigin     = gdi.NewProc("SetBrushOrgEx")
)

type deviceMetrics struct{ width, height, left, top, printWidth, printHeight, dpiX, dpiY int }

func (m deviceMetrics) layout() Layout {
	mmX, mmY := 25.4/float64(m.dpiX), 25.4/float64(m.dpiY)
	return Layout{Width: float64(m.width) * mmX, Height: float64(m.height) * mmY, Left: float64(m.left) * mmX, Top: float64(m.top) * mmY, PrintableWidth: float64(m.printWidth) * mmX, PrintableHeight: float64(m.printHeight) * mmY, Key: layoutKey(m.width, m.height, m.left, m.top, m.printWidth, m.printHeight, m.dpiX, m.dpiY)}
}

func readMetrics(dc uintptr) (deviceMetrics, error) {
	cap := func(index uintptr) int { v, _, _ := getDeviceCaps.Call(dc, index); return int(int32(v)) }
	m := deviceMetrics{width: cap(110), height: cap(111), left: cap(112), top: cap(113), printWidth: cap(8), printHeight: cap(10), dpiX: cap(88), dpiY: cap(90)}
	if m.width <= 0 || m.height <= 0 || m.dpiX <= 0 || m.dpiY <= 0 || !m.layout().valid() {
		return m, fmt.Errorf("无法读取打印机纸张与可打印区域")
	}
	return m, nil
}

// createDocumentDC obtains the driver's full DEVMODE (including private data),
// normalizes supported copy/duplex fields and validates the changes via the driver.
// This never shows a driver dialog or changes the system's default settings.
func createDocumentDC(printer string) (uintptr, error) {
	name, err := syscall.UTF16PtrFromString(printer)
	if err != nil {
		return 0, err
	}
	var handle uintptr
	ok, _, callErr := openPrinter.Call(uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(&handle)), 0)
	if ok == 0 {
		return 0, fmt.Errorf("无法打开打印机：%v", callErr)
	}
	defer closePrinter.Call(handle)
	size, _, callErr := documentProperties.Call(0, handle, uintptr(unsafe.Pointer(name)), 0, 0, 0)
	if int32(size) < 96 || size > 1<<20 {
		return 0, fmt.Errorf("无法读取打印机设置：%v", callErr)
	}
	mode := make([]byte, int(size))
	result, _, callErr := documentProperties.Call(0, handle, uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(&mode[0])), 0, 2) // DM_OUT_BUFFER
	if int32(result) != 1 {
		return 0, fmt.Errorf("无法读取打印机设置：%v", callErr)
	}
	if binary.LittleEndian.Uint16(mode[68:70]) < 96 {
		return 0, fmt.Errorf("打印机设置结构无效")
	}
	fields := binary.LittleEndian.Uint32(mode[72:76])
	if fields&0x100 != 0 {
		binary.LittleEndian.PutUint16(mode[86:88], 1)
	} // DM_COPIES
	if fields&0x1000 != 0 {
		binary.LittleEndian.PutUint16(mode[94:96], 1)
	} // DM_DUPLEX: simplex
	result, _, callErr = documentProperties.Call(0, handle, uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(&mode[0])), uintptr(unsafe.Pointer(&mode[0])), 2|8)
	if int32(result) != 1 {
		return 0, fmt.Errorf("打印机不接受单份单面设置：%v", callErr)
	}
	fields = binary.LittleEndian.Uint32(mode[72:76])
	if (fields&0x100 != 0 && binary.LittleEndian.Uint16(mode[86:88]) != 1) || (fields&0x1000 != 0 && binary.LittleEndian.Uint16(mode[94:96]) != 1) {
		return 0, fmt.Errorf("打印机不接受单份单面设置")
	}
	driver, _ := syscall.UTF16PtrFromString("WINSPOOL")
	dc, _, callErr := createDC.Call(uintptr(unsafe.Pointer(driver)), uintptr(unsafe.Pointer(name)), 0, uintptr(unsafe.Pointer(&mode[0])))
	runtime.KeepAlive(mode)
	if dc == 0 {
		return 0, fmt.Errorf("无法连接打印机：%v", callErr)
	}
	return dc, nil
}

func (nativeBackend) Layout(printer string) (Layout, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	dc, err := createDocumentDC(printer)
	if err != nil {
		return Layout{}, err
	}
	defer deleteDC.Call(dc)
	metrics, err := readMetrics(dc)
	if err != nil {
		return Layout{}, err
	}
	return metrics.layout(), nil
}

// bitmapHeader is BITMAPINFOHEADER with a negative height for top-down BGRA.
type bitmapHeader struct {
	Size                         uint32
	Width, Height                int32
	Planes, BitCount             uint16
	Compression, SizeImage       uint32
	XPelsPerMeter, YPelsPerMeter int32
	ClrUsed, ClrImportant        uint32
}

func paintPage(dc uintptr, name string, metrics deviceMetrics) error {
	file, err := os.Open(name)
	if err != nil {
		return err
	}
	picture, err := png.Decode(file)
	file.Close()
	if err != nil {
		return err
	}
	bounds := picture.Bounds()
	rgba := image.NewRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	draw.Draw(rgba, rgba.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.Draw(rgba, rgba.Bounds(), picture, bounds.Min, draw.Over)
	// DIB RGB quads are B,G,R,unused; the alpha channel was flattened above.
	for i := 0; i < len(rgba.Pix); i += 4 {
		rgba.Pix[i], rgba.Pix[i+2] = rgba.Pix[i+2], rgba.Pix[i]
	}
	header := bitmapHeader{Size: 40, Width: int32(bounds.Dx()), Height: -int32(bounds.Dy()), Planes: 1, BitCount: 32, SizeImage: uint32(len(rgba.Pix))}
	setStretchMode.Call(dc, 4) // HALFTONE; reset the brush origin as required.
	setBrushOrigin.Call(dc, 0, 0, 0)
	result, _, callErr := stretchDIBits.Call(dc, 0, 0, uintptr(metrics.printWidth), uintptr(metrics.printHeight), 0, 0, uintptr(bounds.Dx()), uintptr(bounds.Dy()), uintptr(unsafe.Pointer(&rgba.Pix[0])), uintptr(unsafe.Pointer(&header)), 0, 0x00CC0020)
	runtime.KeepAlive(rgba)
	if int32(result) <= 0 {
		return fmt.Errorf("绘制打印页面失败：%v", callErr)
	}
	return nil
}

func (nativeBackend) PrintDocument(printer string, document Document) (int, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	dc, err := createDocumentDC(printer)
	if err != nil {
		return 0, err
	}
	defer deleteDC.Call(dc)
	metrics, err := readMetrics(dc)
	if err != nil {
		return 0, err
	}
	if metrics.layout().Key != document.Layout.Key {
		return 0, ErrLayoutChanged
	}
	title, err := syscall.UTF16PtrFromString("Lan Copy - " + document.Name)
	if err != nil {
		return 0, err
	}
	doc := docInfo{Name: title}
	doc.Size = int32(unsafe.Sizeof(doc))
	job, _, callErr := startDoc.Call(dc, uintptr(unsafe.Pointer(&doc)))
	if int32(job) <= 0 {
		return 0, fmt.Errorf("无法创建文档打印任务：%v", callErr)
	}
	finished := false
	defer func() {
		if !finished {
			abortDoc.Call(dc)
		}
	}()
	for _, page := range document.Pages {
		if result, _, err := startPage.Call(dc); int32(result) <= 0 {
			return 0, fmt.Errorf("无法开始打印页面：%v", err)
		}
		if err := paintPage(dc, page, metrics); err != nil {
			return 0, err
		}
		if result, _, err := endPage.Call(dc); int32(result) <= 0 {
			return 0, fmt.Errorf("无法结束打印页面：%v", err)
		}
	}
	if result, _, err := endDoc.Call(dc); int32(result) <= 0 {
		return 0, fmt.Errorf("无法提交文档打印任务：%v", err)
	}
	finished = true
	return int(job), nil
}
