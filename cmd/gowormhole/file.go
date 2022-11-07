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
	"github.com/bingoohuang/gowormhole/wormhole"
	"github.com/bingoohuang/pb"
	"github.com/creasty/defaults"
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
	dir, code, passLength := parseFlags(args)
	if err := receive(receiveFileArg{
		Code:         code,
		SecretLength: passLength,
		Dir:          dir,
		Progress:     true,
	}); err != nil && err != io.EOF {
		log.Fatalf("receiving failed: %v", err)
	}
}

type receiveFileArg struct {
	Code         string `json:"code"`
	SecretLength int    `json:"secretLength" default:"2"`
	Dir          string `json:"dir"`
	Progress     bool   `json:"progress"`
}

func RecvFiles(argJSON string) string {
	var arg receiveFileArg
	if err := json.Unmarshal([]byte(argJSON), &arg); err != nil {
		log.Printf("Unmarshal %s failed: %v", argJSON, err)
		return fmt.Sprintf("unmarshal %s failed: %v", argJSON, err)
	}

	if err := defaults.Set(&arg); err != nil {
		log.Printf("defaults.Set failed: %v", err)
	}

	if err := receive(arg); err != nil {
		if err != io.EOF {
			log.Printf("receive failed: %v", err)
			return fmt.Sprintf("receive failed: %v", err)
		}
	}

	return ""
}

func receive(arg receiveFileArg) error {
	c := newConn(arg.Code, arg.SecretLength)
	defer iox.Close(c)

	for {
		if err := receiving(c, arg.Dir, arg.Progress); err != nil {
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

func receiving(c *wormhole.Wormhole, directory string, progress bool) error {
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

	var bar *pb.ProgressBar
	if progress {
		bar = pb.Full.Start64(int64(h.Size)) // start new bar
		reader = bar.NewProxyReader(reader)  // create proxy reader
	}

	written, err := io.CopyBuffer(f, reader, make([]byte, msgChunkSize))

	if progress {
		bar.Finish() // finish bar
	}

	if err != nil {
		return fmt.Errorf("create receive file  failed%s: %w", h.Name, err)
	}

	if written != int64(h.Size) {
		return fmt.Errorf("EOF before receiving all bytes: (%d/%d)", written, h.Size)
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

	if err := sendFiles(sendFileArg{
		Code:         *code,
		SecretLength: *length,
		Files:        set.Args(),
		Progress:     true,
	}); err != nil {
		log.Fatalf("sendFiles failed: %v", err)
	}
}

type sendFileArg struct {
	Code         string   `json:"code"`
	SecretLength int      `json:"secretLength" default:"2"`
	Files        []string `json:"files"`
	Progress     bool     `json:"progress"`
}

// SendFiles send files by wormhole
func SendFiles(sendFileArgJSON string) string {
	var arg sendFileArg
	if err := json.Unmarshal([]byte(sendFileArgJSON), &arg); err != nil {
		log.Printf("Unmarshal %s failed: %v", sendFileArgJSON, err)
		return fmt.Sprintf("Unmarshal %s failed: %v", sendFileArgJSON, err)
	}

	if err := defaults.Set(&arg); err != nil {
		log.Printf("defaults.Set failed: %v", err)
	}

	if err := sendFiles(arg); err != nil {
		log.Printf("sendFiles %s failed: %v", sendFileArgJSON, err)
		return fmt.Sprintf("sendFiles %s failed: %v", sendFileArgJSON, err)
	}

	return ""
}

func sendFiles(args sendFileArg) error {
	c := newConn(args.Code, args.SecretLength)
	defer iox.Close(c)

	for _, filename := range args.Files {
		if err := sendFile(c, filename, args.Progress); err != nil {
			return err
		}
	}

	return nil
}

func sendFile(c io.Writer, filename string, progress bool) error {
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
	_, err = c.Write(h)
	log.Printf("sending %v... ", filename)

	var bar *pb.ProgressBar
	if progress {
		bar = pb.Full.Start64(info.Size()) // start new bar
		c = bar.NewProxyWriter(c)          // create proxy reader
	}

	written, err := io.CopyBuffer(c, f, make([]byte, msgChunkSize))
	if progress {
		bar.Finish() // finish bar
	}

	if err != nil {
		return fmt.Errorf("send file %s failed: %w", filename, err)
	}

	if written != info.Size() {
		return fmt.Errorf("EOF before sending all bytes: (%d/%d)", written, info.Size())
	}

	log.Printf("send file %s done", filename)
	return nil
}
