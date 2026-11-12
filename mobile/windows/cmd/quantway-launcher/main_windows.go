//go:build windows

// Quantway's ordinary-user Windows launcher. The runtime is bundled and private
// to the installation; no shell, global Python, or user site packages are used.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

func main() {
	executable, err := os.Executable()
	if err != nil {
		fail(err)
		return
	}
	root := filepath.Dir(executable)
	python := filepath.Join(root, "runtime", "bin", "pythonw.exe")
	bootstrap := filepath.Join(root, "bootstrap.py")
	required := []string{python, bootstrap, filepath.Join(root, "ui", "quantway.py"), filepath.Join(root, "bin", "quantway-helper.exe"), filepath.Join(root, "bin", "quantway-net-helper.exe"), filepath.Join(root, "bin", "quantway-tinc-helper.exe"), filepath.Join(root, "bin", "tincd.exe"), filepath.Join(root, "bin", "tinc.exe"), filepath.Join(root, "bin", "wintun.dll")}
	for _, path := range required {
		info, e := os.Lstat(path)
		if e != nil || !info.Mode().IsRegular() {
			fail(fmt.Errorf("Неполная установка Quantway: %s", filepath.Base(path)))
			return
		}
	}
	if len(os.Args) > 1 && os.Args[1] == "--check-runtime" {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"complete": true, "install_dir": root, "runtime": python})
		return
	}
	release, primary, lockError := lockGUI()
	if lockError != nil {
		fail(lockError)
		return
	}
	if !primary {
		return
	}
	defer release()
	env := filteredEnvironment(os.Environ())
	runtime := filepath.Join(root, "runtime")
	systemRoot := os.Getenv("SystemRoot")
	values := map[string]string{
		"QUANTWAY_INSTALL_DIR": root, "QUANTWAY_ASSET_DIR": filepath.Join(root, "assets"), "QUANTWAY_HELPER": filepath.Join(root, "bin", "quantway-helper.exe"),
		"PYGI_DLL_PATH": filepath.Join(runtime, "bin"), "GI_TYPELIB_PATH": filepath.Join(runtime, "lib", "girepository-1.0"),
		"GSETTINGS_SCHEMA_DIR": filepath.Join(runtime, "share", "glib-2.0", "schemas"), "XDG_DATA_DIRS": filepath.Join(runtime, "share"),
		"PATH":        filepath.Join(runtime, "bin") + ";" + filepath.Join(root, "bin") + ";" + filepath.Join(systemRoot, "System32") + ";" + systemRoot,
		"GDK_BACKEND": "win32", "GSK_RENDERER": "cairo", "PYTHONUTF8": "1",
	}
	for name, value := range values {
		env = append(env, name+"="+value)
	}
	command := exec.Command(python, "-I", "-B", "-X", "utf8", bootstrap)
	command.Dir = root
	command.Env = env
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	// Startup errors are written by the bootstrap to the user's app-data log.
	if err := command.Run(); err != nil {
		fail(fmt.Errorf("Не удалось запустить Quantway. Журнал: %%LOCALAPPDATA%%\\Quantway\\logs\\startup.log\n%w", err))
	}
}

func filteredEnvironment(input []string) []string {
	output := make([]string, 0, len(input))
	for _, value := range input {
		name, _, _ := strings.Cut(value, "=")
		upper := strings.ToUpper(name)
		if strings.HasPrefix(upper, "PYTHON") || strings.HasPrefix(upper, "QUANTWAY_") || strings.HasPrefix(upper, "GTK_") || strings.HasPrefix(upper, "GDK_") || strings.HasPrefix(upper, "GSK_") || strings.HasPrefix(upper, "GI_") || strings.HasPrefix(upper, "GSETTINGS_") || upper == "PYGI_DLL_PATH" || upper == "XDG_DATA_DIRS" || upper == "PATH" {
			continue
		}
		output = append(output, value)
	}
	return output
}

func fail(err error) {
	if len(os.Args) > 1 && os.Args[1] == "--check-runtime" {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	message, _ := syscall.UTF16PtrFromString(err.Error())
	title, _ := syscall.UTF16PtrFromString("Quantway")
	syscall.NewLazyDLL("user32.dll").NewProc("MessageBoxW").Call(0, uintptr(unsafe.Pointer(message)), uintptr(unsafe.Pointer(title)), 0x10)
	os.Exit(1)
}

// The GUI mutex is per interactive session and has no privileged meaning.
func lockGUI() (func(), bool, error) {
	kernel := syscall.NewLazyDLL("kernel32.dll")
	name, _ := syscall.UTF16PtrFromString("Local\\Quantway.Desktop.GUI")
	handle, _, err := kernel.NewProc("CreateMutexW").Call(0, 1, uintptr(unsafe.Pointer(name)))
	if handle == 0 {
		return nil, false, fmt.Errorf("Не удалось открыть Quantway: %w", err)
	}
	closeHandle := func() { kernel.NewProc("CloseHandle").Call(handle) }
	if err == syscall.Errno(183) {
		closeHandle()
		user := syscall.NewLazyDLL("user32.dll")
		title, _ := syscall.UTF16PtrFromString("Quantway")
		window, _, _ := user.NewProc("FindWindowW").Call(0, uintptr(unsafe.Pointer(title)))
		if window != 0 {
			user.NewProc("ShowWindow").Call(window, 9)
			user.NewProc("SetForegroundWindow").Call(window)
		}
		return nil, false, nil
	}
	return func() { kernel.NewProc("ReleaseMutex").Call(handle); closeHandle() }, true, nil
}
