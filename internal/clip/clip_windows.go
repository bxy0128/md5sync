//go:build windows

// Package clip 提供 Windows 剪贴板读写，使命令行版可以像脚本版一样"复制路径直接跑"。
package clip

import (
	"errors"
	"syscall"
	"time"
	"unsafe"
)

// Supported 表示当前平台是否支持剪贴板读写。
const Supported = true

var (
	user32               = syscall.NewLazyDLL("user32.dll")
	kernel32             = syscall.NewLazyDLL("kernel32.dll")
	procOpenClipboard    = user32.NewProc("OpenClipboard")
	procCloseClipboard   = user32.NewProc("CloseClipboard")
	procEmptyClipboard   = user32.NewProc("EmptyClipboard")
	procGetClipboardData = user32.NewProc("GetClipboardData")
	procSetClipboardData = user32.NewProc("SetClipboardData")
	procGlobalAlloc      = kernel32.NewProc("GlobalAlloc")
	procGlobalLock       = kernel32.NewProc("GlobalLock")
	procGlobalUnlock     = kernel32.NewProc("GlobalUnlock")
)

const (
	cfUnicodeText = 13
	gmemMoveable  = 0x0002
)

func open() bool {
	for i := 0; i < 10; i++ {
		if r, _, _ := procOpenClipboard.Call(0); r != 0 {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

// toPointer 把 Win32 返回的 uintptr 句柄转成指针，避免 vet 误报。
func toPointer(u uintptr) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&u))
}

// Read 读取剪贴板纯文本。
func Read() (string, error) {
	if !open() {
		return "", errors.New("剪贴板被其他程序占用")
	}
	defer procCloseClipboard.Call()

	h, _, _ := procGetClipboardData.Call(cfUnicodeText)
	if h == 0 {
		return "", nil
	}
	p, _, _ := procGlobalLock.Call(h)
	if p == 0 {
		return "", errors.New("锁定剪贴板内存失败")
	}
	defer procGlobalUnlock.Call(h)

	ptr := toPointer(p)
	n := 0
	for {
		if *(*uint16)(unsafe.Add(ptr, n*2)) == 0 {
			break
		}
		n++
		if n > 1<<22 {
			break
		}
	}
	return syscall.UTF16ToString(unsafe.Slice((*uint16)(ptr), n)), nil
}

// Write 写入剪贴板纯文本。
func Write(s string) error {
	u, err := syscall.UTF16FromString(s)
	if err != nil {
		return err
	}
	h, _, _ := procGlobalAlloc.Call(gmemMoveable, uintptr(len(u)*2))
	if h == 0 {
		return errors.New("分配剪贴板内存失败")
	}
	p, _, _ := procGlobalLock.Call(h)
	if p == 0 {
		return errors.New("锁定剪贴板内存失败")
	}
	copy(unsafe.Slice((*uint16)(toPointer(p)), len(u)), u)
	procGlobalUnlock.Call(h)

	if !open() {
		return errors.New("剪贴板被其他程序占用")
	}
	defer procCloseClipboard.Call()
	procEmptyClipboard.Call()
	if r, _, _ := procSetClipboardData.Call(cfUnicodeText, h); r == 0 {
		return errors.New("写入剪贴板失败")
	}
	return nil
}
