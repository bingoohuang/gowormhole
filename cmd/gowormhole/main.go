// Command gowormhole moves files and other data over WebRTC.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"

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
	"recv":        receiveSubCmd,
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
	util.Printf("flags:\n")
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

func newConn(ctx context.Context, code string, length int) *wormhole.Wormhole {
	slotKey, pass := "", ""
	if code == "" {
		pass = string(util.RandPass(length))
	} else {
		slot, pass1 := wordlist.Decode(code)
		util.FatalfIf(pass1 == nil, "could not decode password")
		slotKey = strconv.Itoa(slot)
		pass = string(pass1)
	}

	c, err := wormhole.Setup(ctx, slotKey, pass, sigserv)
	util.FatalfIf(err == wormhole.ErrBadVersion,
		"%s%s%s",
		"the signalling server is running an incompatable version.\n",
		"try upgrading the client:\n\n",
		"    go get github.com/bingoohuang/gowormhole/cmd/gowormhole\n",
	)

	util.FatalfIf(err != nil, "could not dial: %v", err)
	log.Printf("connected: %s", util.If(c.IsRelay(), "relay", "direct"))
	return c
}
