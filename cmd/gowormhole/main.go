// Command gowormhole moves files and other data over WebRTC.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"

	"github.com/bingoohuang/gg/pkg/ss"
	"github.com/bingoohuang/gg/pkg/v"
	"github.com/bingoohuang/gowormhole/internal/util"
	"github.com/bingoohuang/gowormhole/wordlist"
	"github.com/bingoohuang/gowormhole/wormhole"
)

var subcmds = map[string]func(sigserv string, args ...string){
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

const DefaultSigserv = "http://gowormhole.d5k.co"

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
	verbose := flag.Bool("verbose", util.LookupEnvOrBool("GW_VERBOSE", true), "verbose logging")
	sigserv := flag.String("signal", util.LookupEnvOr("GW_SIGSERV", ""), "signalling server to use")
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

	wormhole.Verbose = *verbose
	cmd, ok := subcmds[flag.Arg(0)]
	if !ok {
		flag.Usage()
		os.Exit(2)
	}
	cmd(*sigserv, flag.Args()...)
}

func newConn(ctx context.Context, sigserv string, code string, length int, iceTimeouts *wormhole.ICETimeouts) *wormhole.Wormhole {
	slotKey, pass := "", ""
	if code == "" {
		pass = string(util.RandPass(length))
	} else {
		slot, pass1 := wordlist.Decode(code)
		util.FatalfIf(pass1 == nil, "could not decode password")
		slotKey = strconv.Itoa(slot)
		pass = string(pass1)
	}

	c, err := wormhole.Setup(ctx, slotKey, pass, ss.Or(sigserv, DefaultSigserv), iceTimeouts)
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
