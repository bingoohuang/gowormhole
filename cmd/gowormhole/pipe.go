package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/bingoohuang/gowormhole/internal/util"
)

func pipeSubCmd(sigserv string, args ...string) {
	set := flag.NewFlagSet(args[0], flag.ExitOnError)
	set.Usage = func() {
		_, _ = fmt.Fprintf(set.Output(), "netcat-like pipe\n\n")
		_, _ = fmt.Fprintf(set.Output(), "usage: %s %s [code]\n\n", os.Args[0], args[0])
		_, _ = fmt.Fprintf(set.Output(), "flags:\n")
		set.PrintDefaults()
	}
	length := set.Int("length", 2, "length of generated secret, if generating")
	_ = set.Parse(args[1:])

	if set.NArg() > 1 {
		set.Usage()
		os.Exit(2)
	}
	c := newConn(context.TODO(), sigserv, set.Arg(0), *length, nil)

	done := make(chan struct{})
	// The recieve end of the pipe.
	go func() {
		_, err := io.CopyBuffer(os.Stdout, c, make([]byte, msgChunkSize))
		util.FatalfIf(err != nil, "could not write to stdout: %v", err)

		done <- struct{}{}
	}()
	// The send end of the pipe.
	go func() {
		_, err := io.CopyBuffer(c, os.Stdin, make([]byte, msgChunkSize))
		util.FatalfIf(err != nil, "could not write to channel: %v", err)

		done <- struct{}{}
	}()
	<-done
	_ = c.Close()
}
