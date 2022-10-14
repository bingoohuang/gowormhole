package main

import (
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
	"github.com/bingoohuang/pb"
)

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

func receiveSubCmd(args ...string) {
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
	c := newConn(set.Arg(0), *length)
	defer iox.Close(c)

	// TODO append number to existing filenames?

	for {
		if err := receiving(c, *directory); err != nil {
			break
		}
	}
}

func receiving(c *wormhole.Wormhole, directory string) error {
	// First message is the header. 1k should be enough.
	buf := make([]byte, 1<<10)
	n, err := c.Read(buf)
	if err == io.EOF {
		return err
	}
	util.FatalfIf(err != nil, "could not read file header: %v", err)

	var h header
	err = json.Unmarshal(buf[:n], &h)
	util.FatalfIf(err != nil, "could not decode file header: %v", err)

	f, err := os.Create(filepath.Join(directory, filepath.Clean(h.Name)))
	util.FatalfIf(err != nil, "could not create output file %s: %v", h.Name, err)
	defer iox.Close(f)

	log.Printf("receiving %v... ", h.Name)

	reader := io.LimitReader(c, int64(h.Size))

	bar := pb.Full.Start64(int64(h.Size))   // start new bar
	barReader := bar.NewProxyReader(reader) // create proxy reader

	written, err := io.CopyBuffer(f, barReader, make([]byte, msgChunkSize))
	bar.Finish() // finish bar
	util.FatalfIf(err != nil, "could not receive file: %v", err)

	if written != int64(h.Size) {
		util.Fatalf("EOF before receiving all bytes: (%d/%d)", written, h.Size)
	}

	log.Printf("receive file %s done", h.Name)
	return nil
}

func sendSubCmd(args ...string) {
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
	c := newConn(*code, *length)
	defer iox.Close(c)

	for _, filename := range set.Args() {
		sendFile(c, filename)
	}
}

func sendFile(c *wormhole.Wormhole, filename string) {
	f, err := os.Open(filename)
	util.FatalfIf(err != nil, "could not open file %s: %v", filename, err)
	defer iox.Close(f)

	info, err := f.Stat()
	util.FatalfIf(err != nil, "could not stat file %s: %v", filename, err)

	baseFileName := filepath.Base(filepath.Clean(filename))
	h, err := json.Marshal(header{Name: baseFileName, Size: int(info.Size())})
	if err != nil {
		util.Fatalf("failed to marshal json: %v", err)
	}
	_, err = c.Write(h)
	if err != nil {
		util.Fatalf("could not send file header: %v", err)
	}
	log.Printf("sending %v... ", filename)

	bar := pb.Full.Start64(info.Size()) // start new bar
	barWriter := bar.NewProxyWriter(c)  // create proxy reader

	written, err := io.CopyBuffer(barWriter, f, make([]byte, msgChunkSize))
	bar.Finish() // finish bar
	util.FatalfIf(err != nil, "could not send file: %v", err)

	if written != info.Size() {
		util.Fatalf("EOF before sending all bytes: (%d/%d)", written, info.Size())
	}

	log.Printf("send file %s done", filename)
}
