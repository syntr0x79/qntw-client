package main

import (
	"golang.org/x/sys/windows"
	"os"
)

// Preserve a non-inheritable protocol pipe, then remove the original stdout
// handle so even a library that captured it before main cannot corrupt JSON.
func configureJSONOutput() error {
	var duplicate windows.Handle
	process := windows.CurrentProcess()
	if err := windows.DuplicateHandle(process, windows.Handle(os.Stdout.Fd()), process,
		&duplicate, 0, false, windows.DUPLICATE_SAME_ACCESS); err != nil {
		return err
	}
	if err := windows.SetStdHandle(windows.STD_OUTPUT_HANDLE, windows.Handle(os.Stderr.Fd())); err != nil {
		windows.CloseHandle(duplicate)
		return err
	}
	original := os.Stdout
	responseOut = os.NewFile(uintptr(duplicate), "json-response")
	os.Stdout = os.Stderr
	return original.Close()
}
