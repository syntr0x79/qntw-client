//go:build !linux && !windows && !darwin

package main

import "bufio"

func connect(_ request, _ *bufio.Reader) {
	fail(true, "Подключение доступно только в Linux.")
}
