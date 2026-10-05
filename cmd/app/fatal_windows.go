package main

import (
	"os"
	"syscall"
	"unsafe"
)

// fatal shows err in a message box, since a windowed app has no console.
func fatal(err error) {
	user32 := syscall.NewLazyDLL("user32.dll")
	box := user32.NewProc("MessageBoxW")
	text, _ := syscall.UTF16PtrFromString(err.Error())
	title, _ := syscall.UTF16PtrFromString(appName)
	const mbIconError = 0x10
	box.Call(0, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(title)), mbIconError)
	os.Exit(1)
}
