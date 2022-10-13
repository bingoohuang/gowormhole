// Command gowormhole moves files and other data over WebRTC.
package main

import (
	crand "crypto/rand"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/atotto/clipboard"
	"github.com/bingoohuang/gg/pkg/v"
	"github.com/bingoohuang/gowormhole/internal/util"
	"github.com/bingoohuang/gowormhole/wordlist"
	"github.com/bingoohuang/gowormhole/wormhole"
)

var subcmds = map[string]func(args ...string){
	"publicip":    publicIPSubCmd,
	"nat":         natSubCmd,
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

func usage() {
	util.Printf("gowormhole creates ephemeral pipes between computers.\n\n")
	util.Printf("usage:\n\n")
	util.Printf("  %s [flags] <command> [arguments]\n\n", os.Args[0])
	util.Printf("commands:\n")
	for key := range subcmds {
		util.Printf("  %s\n", key)
	}
	util.Printf("\nflags:\n")
	flag.PrintDefaults()
}

func main() {
	showVersion := flag.Bool("version", false, "show version and exit")
	flag.BoolVar(&verbose, "verbose", util.LookupEnvOrBool("WW_VERBOSE", verbose), "verbose logging")
	flag.StringVar(&sigserv, "signal", util.LookupEnvOr("WW_SIGSERV", sigserv), "signalling server to use")
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

func newConn(code string, length int) *wormhole.Wormhole {
	if code != "" {
		return joinWormhole(code)
	}

	return newWormhole(length)
}

func newWormhole(length int) *wormhole.Wormhole {
	pass := make([]byte, length)
	_, err := io.ReadFull(crand.Reader, pass)
	util.FatalfIf(err != nil, "could not generate password: %v", err)

	slotc := make(chan string)
	go func() {
		s := <-slotc
		slot, err := strconv.Atoi(s)
		util.FatalfIf(err != nil, "got invalid slot from signalling server: %v", s)
		word := wordlist.Encode(slot, pass)
		clipboard.WriteAll(word)
		util.PrintQRCode(sigserv, word)
	}()
	c, err := wormhole.New(string(pass), sigserv, slotc)
	util.FatalfIf(err == wormhole.ErrBadVersion,
		"%s%s%s",
		"the signalling server is running an incompatable version.\n",
		"try upgrading the client:\n\n",
		"    go get github.com/bingoohuang/gowormhole/cmd/gowormhole\n",
	)

	util.FatalfIf(err != nil, "could not dial: %v", err)
	util.Printf("connected: %s\n", util.If(c.IsRelay(), "relay", "direct"))
	return c
}

func joinWormhole(code string) *wormhole.Wormhole {
	slot, pass := wordlist.Decode(code)
	util.FatalfIf(pass == nil, "could not decode password")

	c, err := wormhole.Join(strconv.Itoa(slot), string(pass), sigserv)
	util.FatalfIf(err == wormhole.ErrBadVersion,
		`the signalling server is running in an incompatible version
try upgrading the client: go get github.com/bingoohuang/gowormhole/cmd/gowormhole`)

	util.FatalfIf(err != nil, "could not dial: %v", err)
	util.Printf("connected: %s\n", util.If(c.IsRelay(), "relay", "direct"))
	return c
}
