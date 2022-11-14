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

func init() {
}

const (
	// msgChunkSize is the maximum size of a WebRTC DataChannel message.
	// 64k is okay for most modern browsers, 32 is conservative.
	msgChunkSize = 32 << 10
)

type header struct {
	Name string `json:"name,omitempty"`
	Size int    `json:"size,omitempty"`
	Type string `json:"type,omitempty"`
}

func sendSubCmd(sigserv string, args ...string) {
	set := flag.NewFlagSet(args[0], flag.ExitOnError)
	set.Usage = func() {
		_, _ = fmt.Fprintf(set.Output(), "send files\n\n")
		_, _ = fmt.Fprintf(set.Output(), "usage: %s %s [files]...\n\n", os.Args[0], args[0])
		_, _ = fmt.Fprintf(set.Output(), "flags:\n")
		set.PrintDefaults()
	}
	length := set.Int("length", 2, "length of generated secret")
	code := set.String("code", "", "use a wormhole code instead of generating one")
	_ = set.Parse(args[1:])

	if set.NArg() < 1 {
		set.Usage()
		os.Exit(2)
	}

	if err := sendFiles(&sendFileArg{
		Code:         *code,
		SecretLength: *length,
		Files:        set.Args(),
		Progress:     true,
		Sigserv:      sigserv,
	}); err != nil {
		log.Fatalf("sendFiles failed: %v", err)
	}
}

type sendFileArg struct {
	Code         string                `json:"code"`
	SecretLength int                   `json:"secretLength" default:"2"`
	Files        []string              `json:"files"`
	Progress     bool                  `json:"progress"`
	Sigserv      string                `json:"sigserv"`
	IceTimeouts  *wormhole.ICETimeouts `json:"LiceTimeouts"`

	pb util.ProgressBar
}

func sendFiles(arg *sendFileArg) error {
	c := newConn(context.TODO(), arg.Sigserv, arg.Code, arg.SecretLength, arg.IceTimeouts)
	arg.Code = c.Code
	defer iox.Close(c)

	return sendFilesByWormhole(c, arg)
}

func sendFilesByWormhole(c io.Writer, arg *sendFileArg) error {
	pb := util.CreateProgressBar(arg.pb, arg.Progress)
	for _, filename := range arg.Files {
		if err := sendFile(c, filename, pb); err != nil {
			return err
		}
	}

	return nil
}

func sendFile(c io.Writer, filename string, pb util.ProgressBar) error {
	f, err := os.Open(filename)
	if err != nil {
		return fmt.Errorf("open file %s failed: %w", filename, err)
	}
	defer iox.Close(f)

	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("stat file %s failed: %w", filename, err)
	}

	baseFileName := filepath.Base(filepath.Clean(filename))
	he := header{Name: baseFileName, Size: int(info.Size())}
	h, err := json.Marshal(he)
	if err != nil {
		return fmt.Errorf("marshal header %s failed: %w", baseFileName, err)
	}
	if _, err := c.Write(h); err != nil {
		return fmt.Errorf("write header %s failed: %w", h, err)
	}

	log.Printf("sending %s... ", filename)

	pb.Start(filename, he.Size)
	c = util.NewProxyWriter(c, pb)
	n, err := io.CopyBuffer(c, f, make([]byte, msgChunkSize))
	pb.Finish() // finish bar
	if err != nil {
		return fmt.Errorf("send file %s failed: %w", filename, err)
	}

	if n != info.Size() {
		return fmt.Errorf("EOF before sending all bytes: (%d/%d)", n, info.Size())
	}

	log.Printf("send file %s done", filename)
	return nil
}
