//go:build wails && windows

package main

import (
	"os"
	"syscall"
	"unsafe"
)

// relaunchElevated restarts this program with administrator rights when it was
// started without them. Scheduled tasks running as SYSTEM, the firewall and
// ProgramData all need them, and a normal shortcut does not ask for them, so
// registering the sign-in service was refused with "Access is denied".
//
// It returns true when an elevated copy was started and this one should exit.
// If the person declines the prompt, the app carries on without rights, and
// the steps that need them report that.
func relaunchElevated() bool {
	if isElevated() {
		return false
	}
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	verb, _ := syscall.UTF16PtrFromString("runas")
	file, _ := syscall.UTF16PtrFromString(exe)
	shell32 := syscall.NewLazyDLL("shell32.dll")
	shellExecute := shell32.NewProc("ShellExecuteW")
	const swNormal = 1
	ret, _, _ := shellExecute.Call(0,
		uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(file)), 0, 0, swNormal)
	return ret > 32
}

// isElevated asks Windows whether this process token is elevated.
func isElevated() bool {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	advapi32 := syscall.NewLazyDLL("advapi32.dll")
	getCurrent := kernel32.NewProc("GetCurrentProcess")
	openToken := advapi32.NewProc("OpenProcessToken")
	getInfo := advapi32.NewProc("GetTokenInformation")

	proc, _, _ := getCurrent.Call()
	const tokenQuery = 0x0008
	var token syscall.Handle
	if r, _, _ := openToken.Call(proc, tokenQuery, uintptr(unsafe.Pointer(&token))); r == 0 {
		return false
	}
	defer syscall.CloseHandle(token)

	const tokenElevation = 20 // TOKEN_INFORMATION_CLASS: TokenElevation
	var elevation, size uint32
	r, _, _ := getInfo.Call(uintptr(token), tokenElevation,
		uintptr(unsafe.Pointer(&elevation)), unsafe.Sizeof(elevation),
		uintptr(unsafe.Pointer(&size)))
	return r != 0 && elevation != 0
}
