package printing

import (
	"fmt"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

type nativeBackend struct{}

func New(dataDir string) (*Service, error) { return NewWithBackend(nativeBackend{}, dataDir) }

var (
	spool             = syscall.NewLazyDLL("winspool.drv")
	enumPrinters      = spool.NewProc("EnumPrintersW")
	getDefaultPrinter = spool.NewProc("GetDefaultPrinterW")
	gdi               = syscall.NewLazyDLL("gdi32.dll")
	createDC          = gdi.NewProc("CreateDCW")
	deleteDC          = gdi.NewProc("DeleteDC")
	startDoc          = gdi.NewProc("StartDocW")
	startPage         = gdi.NewProc("StartPage")
	endPage           = gdi.NewProc("EndPage")
	endDoc            = gdi.NewProc("EndDoc")
	abortDoc          = gdi.NewProc("AbortDoc")
	getDeviceCaps     = gdi.NewProc("GetDeviceCaps")
	textOut           = gdi.NewProc("TextOutW")
	createFont        = gdi.NewProc("CreateFontW")
	selectObject      = gdi.NewProc("SelectObject")
	deleteObject      = gdi.NewProc("DeleteObject")
)

// PRINTER_INFO_2W; pointer fields retain the Windows ABI on 32/64-bit builds.
type printerInfo2 struct {
	Server, Name, Share, Port, Driver, Comment, Location                                  *uint16
	DevMode                                                                               unsafe.Pointer
	Separator, Processor, DataType, Parameters                                            *uint16
	Security                                                                              unsafe.Pointer
	Attributes, Priority, DefaultPriority, StartTime, UntilTime, Status, Jobs, AveragePPM uint32
}

func wideString(p *uint16) string {
	if p == nil {
		return ""
	}
	var chars []uint16
	for i := uintptr(0); i < 32768; i++ {
		v := *(*uint16)(unsafe.Add(unsafe.Pointer(p), i*2))
		if v == 0 {
			break
		}
		chars = append(chars, v)
	}
	return syscall.UTF16ToString(chars)
}

func defaultPrinterName() string {
	var size uint32
	getDefaultPrinter.Call(0, uintptr(unsafe.Pointer(&size)))
	if size == 0 || size > 32768 {
		return ""
	}
	buf := make([]uint16, size)
	ok, _, _ := getDefaultPrinter.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)))
	if ok == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf)
}

func (nativeBackend) Printers() ([]Printer, error) {
	const flags = 2 | 4 // PRINTER_ENUM_LOCAL | PRINTER_ENUM_CONNECTIONS
	var size, count uint32
	ok, _, callErr := enumPrinters.Call(flags, 0, 2, 0, 0, uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&count)))
	if ok == 0 && callErr != syscall.ERROR_INSUFFICIENT_BUFFER {
		return nil, fmt.Errorf("读取 Windows 打印机失败：%v", callErr)
	}
	if size == 0 {
		return []Printer{}, nil
	}
	// Retry if printers change between the sizing and data calls.
	for attempt := 0; attempt < 3; attempt++ {
		if size > 16<<20 {
			return nil, fmt.Errorf("Windows 打印机列表过大")
		}
		buf := make([]byte, size)
		ok, _, callErr = enumPrinters.Call(flags, 0, 2, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&count)))
		if ok == 0 {
			if callErr == syscall.ERROR_INSUFFICIENT_BUFFER {
				continue
			}
			return nil, fmt.Errorf("读取 Windows 打印机失败：%v", callErr)
		}
		if uintptr(count)*unsafe.Sizeof(printerInfo2{}) > uintptr(len(buf)) {
			return nil, fmt.Errorf("Windows 打印机列表无效")
		}
		defaultName := defaultPrinterName()
		printers := []Printer{}
		for _, info := range unsafe.Slice((*printerInfo2)(unsafe.Pointer(&buf[0])), int(count)) {
			name, port, driver := wideString(info.Name), strings.ToUpper(wideString(info.Port)), strings.ToLower(wideString(info.Driver))
			if name == "" || info.Attributes&0x4000 != 0 || strings.Contains(port, "PORTPROMPT:") || strings.Contains(port, "FILE:") || strings.Contains(port, "NUL:") || strings.Contains(driver, "microsoft print to pdf") || strings.Contains(driver, "microsoft xps") || strings.Contains(driver, "onenote") {
				continue
			}
			printers = append(printers, Printer{Name: name, Default: name == defaultName})
		}
		runtime.KeepAlive(buf)
		sort.Slice(printers, func(i, j int) bool { return printers[i].Name < printers[j].Name })
		return printers, nil
	}
	return nil, fmt.Errorf("打印机列表发生变化，请刷新重试")
}

type docInfo struct {
	Size                   int32
	Name, Output, DataType *uint16
	Type                   uint32
}

func (nativeBackend) PrintTest(printer string) (int, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	dc, err := createDocumentDC(printer)
	if err != nil {
		return 0, err
	}
	defer deleteDC.Call(dc)
	title, _ := syscall.UTF16PtrFromString("Lan Copy - Test Page")
	doc := docInfo{Name: title}
	doc.Size = int32(unsafe.Sizeof(doc))
	job, _, callErr := startDoc.Call(dc, uintptr(unsafe.Pointer(&doc)))
	if int32(job) <= 0 {
		return 0, fmt.Errorf("无法创建打印任务：%v", callErr)
	}
	finished := false
	defer func() {
		if !finished {
			abortDoc.Call(dc)
		}
	}()
	if result, _, err := startPage.Call(dc); int32(result) <= 0 {
		return 0, fmt.Errorf("无法创建测试页：%v", err)
	}
	dpiX, _, _ := getDeviceCaps.Call(dc, 88) // LOGPIXELSX
	dpiY, _, _ := getDeviceCaps.Call(dc, 90) // LOGPIXELSY
	if dpiX == 0 || dpiY == 0 {
		return 0, fmt.Errorf("无法读取打印机分辨率")
	}
	fontName, _ := syscall.UTF16PtrFromString("Microsoft YaHei")
	height := -int32(dpiY * 12 / 72)
	font, _, err := createFont.Call(uintptr(height), 0, 0, 0, 400, 0, 0, 0, 1, 0, 0, 0, 0, uintptr(unsafe.Pointer(fontName)))
	if font == 0 {
		return 0, fmt.Errorf("无法创建测试页字体：%v", err)
	}
	old, _, _ := selectObject.Call(dc, font)
	defer func() { selectObject.Call(dc, old); deleteObject.Call(font) }()
	lines := []string{
		"LAN COPY / PRINT TEST", "",
		"Printer: " + printer,
		"Time: " + time.Now().Format("2006-01-02 15:04:05 -07:00"), "",
		"This test page was sent from Lan Copy.",
		"If you can read this page, the print connection is working.", "",
		"ABCDEFGHIJKLMNOPQRSTUVWXYZ", "abcdefghijklmnopqrstuvwxyz 0123456789",
	}
	for i, line := range lines {
		chars, err := syscall.UTF16FromString(line)
		if err != nil {
			return 0, err
		}
		if len(chars) == 1 {
			continue
		}
		result, _, err := textOut.Call(dc, dpiX/3, dpiY/3+uintptr(i)*dpiY/3, uintptr(unsafe.Pointer(&chars[0])), uintptr(len(chars)-1))
		if result == 0 {
			return 0, fmt.Errorf("绘制测试页失败：%v", err)
		}
	}
	if result, _, err := endPage.Call(dc); int32(result) <= 0 {
		return 0, fmt.Errorf("提交测试页失败：%v", err)
	}
	if result, _, err := endDoc.Call(dc); int32(result) <= 0 {
		return 0, fmt.Errorf("提交打印任务失败：%v", err)
	}
	finished = true
	return int(job), nil
}
