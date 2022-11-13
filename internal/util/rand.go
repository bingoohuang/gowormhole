package util

import (
	crand "crypto/rand"
	"io"
	"math/rand"
	"time"
)

func init() {
	rand.Seed(time.Now().UnixNano()) // for slot allocation
}

func RandIntn(n int) int {
	return rand.Intn(n)
}

func RandPass(n int) []byte {
	pass := make([]byte, n)
	_, _ = io.ReadFull(crand.Reader, pass)
	return pass
}

func RandFull(buf []byte) {
	_, _ = io.ReadFull(crand.Reader, buf)
}
