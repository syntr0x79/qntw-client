#include <unistd.h>

/* Packaged executable fixture; never execute code from writable app storage. */
int main(void) {
	for (;;) pause();
}
