//go:build !linux && !windows && !darwin

package main

import "bufio"

func tincConnect(_ request, _ *bufio.Reader) {
	fail(true, "Подключение tinc доступно только в Linux.")
}
