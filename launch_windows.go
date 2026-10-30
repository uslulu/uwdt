//go:build windows

package main

import (
	"golang.org/x/sys/windows"
	"os"
	"strings"
	"syscall"
	"unsafe"
)

// A second click can show the existing elevated window without starting another
// elevated process. Only the first launch needs permission to manage the tunnel.
func prepareWindowsLaunch() bool {
	user32 := windows.NewLazySystemDLL("user32.dll")
	title, _ := windows.UTF16PtrFromString("UWDT")
	hwnd, _, _ := user32.NewProc("FindWindowW").Call(0, uintptr(unsafe.Pointer(title)))
	if hwnd != 0 {
		user32.NewProc("ShowWindowAsync").Call(hwnd, 9) // SW_RESTORE
		user32.NewProc("SetForegroundWindow").Call(hwnd)
		return false
	}
	if windows.GetCurrentProcessToken().IsElevated() {
		return true
	}
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	args := make([]string, 0, len(os.Args)-1)
	for _, arg := range os.Args[1:] {
		args = append(args, syscall.EscapeArg(arg))
	}
	verb, _ := windows.UTF16PtrFromString("runas")
	file, _ := windows.UTF16PtrFromString(exe)
	params, _ := windows.UTF16PtrFromString(strings.Join(args, " "))
	_ = windows.ShellExecute(0, verb, file, params, nil, 1)
	return false
}
