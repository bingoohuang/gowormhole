package util

import (
	"flag"
	"fmt"
	"net/url"
	"os"
	"strconv"

	"rsc.io/qr"
)

func PrintQRCode(baseURL, code string) {
	Printf("%s\n", code)
	u, err := url.Parse(baseURL)
	if err != nil {
		return
	}
	u.Fragment = code
	qrcode, err := qr.Encode(u.String(), qr.L)
	if err != nil {
		return
	}
	for x := 0; x < qrcode.Size; x++ {
		Printf("█")
	}
	Printf("████████\n")
	for x := 0; x < qrcode.Size; x++ {
		Printf("█")
	}
	Printf("████████\n")
	for y := 0; y < qrcode.Size; y += 2 {
		Printf("████")
		for x := 0; x < qrcode.Size; x++ {
			switch {
			case qrcode.Black(x, y) && qrcode.Black(x, y+1):
				Printf(" ")
			case qrcode.Black(x, y):
				Printf("▄")
			case qrcode.Black(x, y+1):
				Printf("▀")
			default:
				Printf("█")
			}
		}
		Printf("████\n")
	}
	for x := 0; x < qrcode.Size; x++ {
		Printf("█")
	}
	Printf("████████\n")
	for x := 0; x < qrcode.Size; x++ {
		Printf("█")
	}
	Printf("████████\n")
	Printf("%s\n", u.String())
}

func LookupEnvOrBool(key string, defaultVal bool) bool {
	if v, ok := os.LookupEnv(key); ok {
		val, err := strconv.ParseBool(v)
		FatalfIf(err != nil, "Cannot parse envvar: %s: %v", v, err)

		return val
	}
	return defaultVal
}

func LookupEnvOr(key string, defaultVal string) string {
	if val, ok := os.LookupEnv(key); ok {
		return val
	}
	return defaultVal
}

var stderr = flag.CommandLine.Output()

func FatalfIf(condition bool, format string, v ...interface{}) {
	if condition {
		Fatalf(format, v...)
	}
}

func Fatalf(format string, v ...interface{}) {
	Printf(format+"\n", v...)
	os.Exit(1)
}

func Printf(format string, a ...any) {
	_, _ = fmt.Fprintf(stderr, format, a...)
}

func If[T any](condition bool, a, b T) T {
	if condition {
		return a
	}

	return b
}
