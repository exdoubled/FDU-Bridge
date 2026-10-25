package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
)

func probeTemp(dir string) error {
	path := filepath.Join(dir, "nsis-write-test.tmp")
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	// Use the same access, sharing and delete-on-close flags as NSIS's solid cache.
	const temporary = 0x100
	const deleteOnClose = 0x04000000
	handle, err := syscall.CreateFile(name, syscall.GENERIC_READ|syscall.GENERIC_WRITE, 0, nil,
		syscall.CREATE_ALWAYS, temporary|deleteOnClose, 0)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(handle), path)
	_, writeErr := f.WriteString("test")
	_, seekErr := f.Seek(0, io.SeekStart)
	var data [4]byte
	_, readErr := io.ReadFull(f, data[:])
	return errors.Join(writeErr, seekErr, readErr, f.Close())
}

func tempCandidates(self string) []string {
	paths := []string{os.TempDir()}
	var folder [260]uint16
	proc := syscall.NewLazyDLL("shell32.dll").NewProc("SHGetFolderPathW")
	// CSIDL_LOCAL_APPDATA comes from Windows, independently of TEMP/TMP variables.
	result, _, _ := proc.Call(0, 0x1c, 0, 0, uintptr(unsafe.Pointer(&folder[0])))
	if result == 0 {
		paths = append(paths, syscall.UTF16ToString(folder[:]))
	}
	return append(paths, filepath.Dir(self))
}

func rawCommandLine() string {
	return syscall.UTF16ToString((*[32768]uint16)(unsafe.Pointer(syscall.GetCommandLine()))[:])
}

func showError(message string) {
	text, _ := syscall.UTF16PtrFromString(message)
	title, _ := syscall.UTF16PtrFromString("FDU Connect 安装程序")
	proc := syscall.NewLazyDLL("user32.dll").NewProc("MessageBoxW")
	_, _, _ = proc.Call(0, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(title)), 0x10)
}
