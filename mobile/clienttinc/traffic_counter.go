package clienttinc

import "math"

type trafficCounter struct {
	generation                       uint64
	lastRX, lastTX, totalRX, totalTX uint64
}

func (c *trafficCounter) observe(generation, rx, tx uint64) (uint64, uint64) {
	if generation != c.generation || rx < c.lastRX || tx < c.lastTX {
		c.lastRX, c.lastTX = 0, 0
	}
	c.totalRX = saturatingAdd(c.totalRX, rx-c.lastRX)
	c.totalTX = saturatingAdd(c.totalTX, tx-c.lastTX)
	c.generation, c.lastRX, c.lastTX = generation, rx, tx
	return c.totalRX, c.totalTX
}

func saturatingAdd(a, b uint64) uint64 {
	if math.MaxUint64-a < b {
		return math.MaxUint64
	}
	return a + b
}
