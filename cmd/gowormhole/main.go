// Command gowormhole moves files and other data over WebRTC.
package main

import (
	"context"
	"errors"
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

var subcmds = map[string]func(ctx context.Context, sigserv string, args ...string){
	"publicip":    publicIPSubCmd,
	"nat":         natSubCmd,
	"send":        sendSubCmd,
	"receive":     receiveSubCmd,
	"recv":        receiveSubCmd,
	"pipe":        pipeSubCmd,
	"server":      signallingServerCmd,
	"http":        httpCmd,
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
	verbose := flag.Bool("verbose", util.GetEnvBool("VERBOSE", true), "verbose logging")
	sigserv := flag.String("signal", os.Getenv("SIGSERV"), "signalling server to use")
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
	cmd(context.TODO(), *sigserv, flag.Args()...)
}

var ErrRetryUnsupported = errors.New("retry Unsupported")

func newConn(ctx context.Context, sigserv, bearer, code string, length int, timeouts *wormhole.Timeouts) (*wormhole.Wormhole, error) {
	slotKey, pass := "", ""
	if code == "" {
		pass = string(util.RandPass(length))
	} else {
		slot, pass1 := wordlist.Decode(code)
		if pass1 == nil {
			return nil, fmt.Errorf("bad code, could not decode password: %w", ErrRetryUnsupported)
		}
		slotKey = strconv.Itoa(slot)
		pass = string(pass1)
	}

	c, err := wormhole.Setup(ctx, slotKey, pass, ss.Or(sigserv, DefaultSigserv), bearer, timeouts)
	if err != nil {
		return nil, fmt.Errorf("could not dial: %w", err)
	}

	log.Printf("connected: %s", util.If(c.IsRelay(), "relay", "direct"))
	return c, nil
}
