package clienttinc

import (
	"strconv"
)

// Both paths come from Windows directory APIs, never from caller environment.
// Native tinc gets no inherited shell, script, loader, editor or routing knobs.
func nativeEnvironment(systemRoot, systemDirectory string, index uint32) []string {
	env := []string{"SystemRoot=" + systemRoot, "WINDIR=" + systemRoot, "PATH=" + systemDirectory}
	if index != 0 {
		env = append(env, "QUANTWAY_PHYSICAL_IFINDEX="+strconv.FormatUint(uint64(index), 10))
	}
	return env
}
