package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"

	"github.com/bingoohuang/gg/pkg/iox"
	"github.com/bingoohuang/gowormhole/internal/util"
	"github.com/bingoohuang/gowormhole/wormhole"
)

func receiveSubCmd(sigserv string, args ...string) {
	dir, code, passLength := parseFlags(args)
	if err := receive(&receiveFileArg{
		Code:         code,
		SecretLength: passLength,
		Dir:          dir,
		Progress:     true,
		Sigserv:      sigserv,
	}); err != nil && err != io.EOF {
		log.Fatalf("receiving failed: %v", err)
	}
}

type receiveFileArg struct {
	Code         string                `json:"code"`
	SecretLength int                   `json:"secretLength" default:"2"`
	Dir          string                `json:"dir" default:"."`
	Progress     bool                  `json:"progress"`
	Sigserv      string                `json:"sigserv"`
	IceTimeouts  *wormhole.ICETimeouts `json:"LiceTimeouts"`

	pb util.ProgressBar
}

func receive(arg *receiveFileArg) error {
	c := newConn(context.TODO(), arg.Sigserv, arg.Code, arg.SecretLength, arg.IceTimeouts)
	arg.Code = c.Code
	defer iox.Close(c)

	return receiveByWormhole(c, arg)
}

func receiveByWormhole(c io.Reader, arg *receiveFileArg) error {
	pb := util.CreateProgressBar(arg.pb, arg.Progress)
	for {
		if err := receiving(c, arg.Dir, pb); err != nil {
			return err
		}
	}
}

func parseFlags(args []string) (dir, code string, passLength int) {
	set := flag.NewFlagSet(args[0], flag.ExitOnError)
	set.Usage = func() {
		_, _ = fmt.Fprintf(set.Output(), "receive files\n\n")
		_, _ = fmt.Fprintf(set.Output(), "usage: %s %s [code]\n\n", os.Args[0], args[0])
		_, _ = fmt.Fprintf(set.Output(), "flags:\n")
		set.PrintDefaults()
	}
	length := set.Int("length", 2, "length of generated secret, if generating")
	directory := set.String("dir", ".", "directory to put downloaded files")
	_ = set.Parse(args[1:])

	if set.NArg() > 1 {
		set.Usage()
		os.Exit(2)
	}

	dir = *directory
	code = set.Arg(0)
	passLength = *length
	return
}

func receiving(c io.Reader, directory string, pb util.ProgressBar) error {
	// First message is the header. 1k should be enough.
	buf := make([]byte, 1<<10)
	n, err := c.Read(buf)
	if err == io.EOF {
		return io.EOF
	}

	if err != nil {
		return fmt.Errorf("read file header failed: %w", err)
	}

	var h header
	if err := json.Unmarshal(buf[:n], &h); err != nil {
		return fmt.Errorf("decode file header %s failed: %w", buf[:n], err)
	}

	name := filepath.Clean(h.Name)
	f, err := os.Create(filepath.Join(directory, name))
	if err != nil {
		return fmt.Errorf("create output file %s failed: %w", h.Name, err)
	}

	defer iox.Close(f)

	log.Printf("receiving %v... ", h.Name)

	reader := io.LimitReader(c, int64(h.Size))

	pb.Start(h.Name, h.Size)                 // start new bar
	reader = util.NewProxyReader(reader, pb) // create proxy reader

	written, err := io.CopyBuffer(f, reader, make([]byte, msgChunkSize))
	pb.Finish() // finish bar

	if err != nil {
		return fmt.Errorf("create receive file  failed%s: %w", h.Name, err)
	}

	if written != int64(h.Size) {
		return fmt.Errorf("EOF before receiving all bytes: (%d/%d)", written, h.Size)
	}

	log.Printf("receive file %s done", h.Name)
	return nil
}
