// Command gowormhole moves files and other data over WebRTC.
package main

import (
	crand "crypto/rand"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"

	"github.com/bingoohuang/gg/pkg/v"

	"github.com/bingoohuang/gowormhole/wordlist"
	"github.com/bingoohuang/gowormhole/wormhole"
	"rsc.io/qr"
)

var subcmds = map[string]func(args ...string){
	"send":        sendSubCmd,
	"receive":     receiveSubCmd,
	"pipe":        pipeSubCmd,
	"server":      signallingServerCmd,
	"turn":        turnServerSubCmd,
	"turn-client": turnClientSubCmd,
}

var (
	verbose = true
	sigserv = "http://gowormhole.d5k.co"
)

var stderr = flag.CommandLine.Output()

func usage() {
	fmt.Fprintf(stderr, "gowormhole creates ephemeral pipes between computers.\n\n")
	fmt.Fprintf(stderr, "usage:\n\n")
	fmt.Fprintf(stderr, "  %s [flags] <command> [arguments]\n\n", os.Args[0])
	fmt.Fprintf(stderr, "commands:\n")
	for key := range subcmds {
		fmt.Fprintf(stderr, "  %s\n", key)
	}
	fmt.Fprintf(stderr, "\nflags:\n")
	flag.PrintDefaults()
}

func main() {
	showVersion := flag.Bool("version", false, "show version and exit")
	flag.BoolVar(&verbose, "verbose", LookupEnvOrBool("WW_VERBOSE", verbose), "verbose logging")
	flag.StringVar(&sigserv, "signal", LookupEnvOrString("WW_SIGSERV", sigserv), "signalling server to use")
	flag.Usage = usage
	flag.Parse()
	if *showVersion {
		fmt.Println(v.Version())
		os.Exit(0)
	}

	if flag.NArg() < 1 {
		usage()
		os.Exit(2)
	}
	if verbose {
		wormhole.Verbose = true
	}
	cmd, ok := subcmds[flag.Arg(0)]
	if !ok {
		flag.Usage()
		os.Exit(2)
	}
	cmd(flag.Args()...)
}

func fatalf(format string, v ...interface{}) {
	fmt.Fprintf(stderr, format+"\n", v...)
	os.Exit(1)
}

func newConn(code string, length int) *wormhole.Wormhole {
	if code != "" {
		// Join wormhole.
		slot, pass := wordlist.Decode(code)
		if pass == nil {
			fatalf("could not decode password")
		}
		c, err := wormhole.Join(strconv.Itoa(slot), string(pass), sigserv)
		if err == wormhole.ErrBadVersion {
			fatalf(
				"%s%s%s",
				"the signalling server is running an incompatible version.\n",
				"try upgrading the client:\n\n",
				"    go get github.com/bingoohuang/gowormhole/cmd/gowormhole\n",
			)
		}
		if err != nil {
			fatalf("could not dial: %v", err)
		}
		if c.IsRelay() {
			fmt.Fprintf(stderr, "connected: relay\n")
		} else {
			fmt.Fprintf(stderr, "connected: direct\n")
		}
		return c
	}
	// New wormhole.
	pass := make([]byte, length)
	if _, err := io.ReadFull(crand.Reader, pass); err != nil {
		fatalf("could not generate password: %v", err)
	}
	slotc := make(chan string)
	go func() {
		s := <-slotc
		slot, err := strconv.Atoi(s)
		if err != nil {
			fatalf("got invalid slot from signalling server: %v", s)
		}
		printcode(wordlist.Encode(slot, pass))
	}()
	c, err := wormhole.New(string(pass), sigserv, slotc)
	if err == wormhole.ErrBadVersion {
		fatalf(
			"%s%s%s",
			"the signalling server is running an incompatable version.\n",
			"try upgrading the client:\n\n",
			"    go get github.com/bingoohuang/gowormhole/cmd/gowormhole\n",
		)
	}
	if err != nil {
		fatalf("could not dial: %v", err)
	}
	if c.IsRelay() {
		fmt.Fprintf(stderr, "connected: relay\n")
	} else {
		fmt.Fprintf(stderr, "connected: direct\n")
	}
	return c
}

func printcode(code string) {
	fmt.Fprintf(stderr, "%s\n", code)
	u, err := url.Parse(sigserv)
	if err != nil {
		return
	}
	u.Fragment = code
	qrcode, err := qr.Encode(u.String(), qr.L)
	if err != nil {
		return
	}
	for x := 0; x < qrcode.Size; x++ {
		fmt.Fprintf(stderr, "█")
	}
	fmt.Fprintf(stderr, "████████\n")
	for x := 0; x < qrcode.Size; x++ {
		fmt.Fprintf(stderr, "█")
	}
	fmt.Fprintf(stderr, "████████\n")
	for y := 0; y < qrcode.Size; y += 2 {
		fmt.Fprintf(stderr, "████")
		for x := 0; x < qrcode.Size; x++ {
			switch {
			case qrcode.Black(x, y) && qrcode.Black(x, y+1):
				fmt.Fprintf(stderr, " ")
			case qrcode.Black(x, y):
				fmt.Fprintf(stderr, "▄")
			case qrcode.Black(x, y+1):
				fmt.Fprintf(stderr, "▀")
			default:
				fmt.Fprintf(stderr, "█")
			}
		}
		fmt.Fprintf(stderr, "████\n")
	}
	for x := 0; x < qrcode.Size; x++ {
		fmt.Fprintf(stderr, "█")
	}
	fmt.Fprintf(stderr, "████████\n")
	for x := 0; x < qrcode.Size; x++ {
		fmt.Fprintf(stderr, "█")
	}
	fmt.Fprintf(stderr, "████████\n")
	fmt.Fprintf(stderr, "%s\n", u.String())
}

func LookupEnvOrBool(key string, defaultVal bool) bool {
	if v, ok := os.LookupEnv(key); ok {
		val, err := strconv.ParseBool(v)
		if err != nil {
			fatalf("Cannot parse envvar: %s: %v", v, err)
		}
		return val
	}
	return defaultVal
}

func LookupEnvOrString(key string, defaultVal string) string {
	if val, ok := os.LookupEnv(key); ok {
		return val
	}
	return defaultVal
}
