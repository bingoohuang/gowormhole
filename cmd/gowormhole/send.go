package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"

	"github.com/bingoohuang/gg/pkg/codec"
	"github.com/bingoohuang/gg/pkg/defaults"
	"github.com/bingoohuang/gg/pkg/iox"
	"github.com/bingoohuang/gowormhole/internal/util"
)

func init() {
}

const (
	// msgChunkSize is the maximum size of a WebRTC DataChannel message.
	// 64k is okay for most modern browsers, 32 is conservative.
	msgChunkSize = 32 << 10
)

func createCodeCmd(ctx context.Context, args ...string) {
	set := flag.NewFlagSet(args[0], flag.ExitOnError)
	set.Usage = func() {
		_, _ = fmt.Fprintf(set.Output(), "create code\n\n")
		_, _ = fmt.Fprintf(set.Output(), "flags:\n")
		set.PrintDefaults()
	}
	length := set.Int("length", 2, "length of generated secret")
	pBearer := set.String("bearer", os.Getenv("BEARER"), "Bearer authentication")
	_ = set.Parse(args[1:])

	code, err := requestCode(CodeReq{
		Bearer:       *pBearer,
		SecretLength: *length,
		Sigserv:      Sigserv,
	})
	if err != nil {
		panic(err)
	}

	fmt.Printf("code: %s, slotNum: %d\n", code.Code, code.SlotNum)
}

func sendSubCmd(ctx context.Context, args ...string) {
	set := flag.NewFlagSet(args[0], flag.ExitOnError)
	set.Usage = func() {
		_, _ = fmt.Fprintf(set.Output(), "send files\n\n")
		_, _ = fmt.Fprintf(set.Output(), "usage: %s %s [files]...\n\n", os.Args[0], args[0])
		_, _ = fmt.Fprintf(set.Output(), "flags:\n")
		set.PrintDefaults()
	}
	length := set.Int("length", 2, "length of generated secret")
	code := set.String("code", "", "use a wormhole code instead of generating one")
	pBearer := set.String("bearer", os.Getenv("BEARER"), "Bearer authentication")

	_ = set.Parse(args[1:])

	if set.NArg() < 1 {
		set.Usage()
		os.Exit(2)
	}

	if err := sendFilesRetry(&sendFileArg{
		BaseArg: BaseArg{
			Bearer:       *pBearer,
			Code:         *code,
			SecretLength: *length,
			Progress:     true,
			Sigserv:      Sigserv,
			RetryTimes:   1,
		},
		Files: set.Args(),
	}); err != nil {
		log.Fatalf("sendFiles failed: %v", err)
	}
}

type CodeAware interface {
	GetCode() string
}

func (a *sendFileArg) GetCode() string    { return a.Code }
func (a *receiveFileArg) GetCode() string { return a.Code }

type sendFileArg struct {
	BaseArg `default:"{}"`
	Files   []string `json:"files"`
	Whoami  string   `json:"whoami"`
}

func sendFilesRetry(arg *sendFileArg) error {
	if err := defaults.Set(arg); err != nil {
		return fmt.Errorf("defaults.Set failed: %w", err)
	}

	var err error
	for i := 1; i <= arg.RetryTimes; i++ {
		if err = sendFilesOnce(arg); err == nil || errors.Is(err, ErrRetryUnsupported) {
			return err
		}

		log.Printf("send file failed: %v , retryTimes: %d", err, i)

	}
	return err
}

func sendFilesOnce(arg *sendFileArg) error {
	c, err := newConn(context.TODO(), arg.Sigserv, arg.Bearer, arg.Code, arg.SecretLength, &arg.Timeouts)
	if err != nil {
		return err
	}

	arg.Code = c.Code
	defer iox.Close(c)

	rw := util.TimeoutReadWriter(c, arg.Timeouts.RwTimeout.D())
	return sendFilesByWormhole(rw, arg)
}

func sendFilesByWormhole(c io.ReadWriter, arg *sendFileArg) error {
	meta, err := createSendFilesMeta(arg.Whoami, arg.Files)
	if err != nil {
		return fmt.Errorf("createSendFilesMeta failed: %w", err)
	}
	if err := sendJSON(c, meta); err != nil {
		return fmt.Errorf("sendJSON SendFilesMeta failed: %w", err)
	}

	if arg.recvMeta != nil {
		arg.recvMeta.SetSendFilesMeta(meta)
	}

	var rsp SendFilesMetaRsp
	if _, err := recvJSON(c, &rsp); err != nil {
		return fmt.Errorf("recvJSON failed: %w", err)
	}

	pb := util.CreateProgressBar(arg.pb, arg.Progress)

	for _, file := range rsp.Files {
		log.Printf("sending: %s.... ", codec.Json(file))
		if err := file.sendFile(c, pb); err != nil {
			return err
		}
	}

	return nil
}

func (file *FileMetaRsp) sendFile(c io.Writer, pb util.ProgressBar) error {
	if file.PosHash != "" {
		var localMeta FileMetaRsp
		if err := createFileMetaRsp(file.FullName, file.Pos, &localMeta); err != nil {
			log.Printf("createFileMeta failed: %v", err)
		}
		if localMeta.PosHash != file.PosHash {
			file.Pos = 0 // hash 不一致，重新从头开始传输
		}
	}

	if err := sendJSON(c, file); err != nil {
		return fmt.Errorf("sendJSON failed: %w", err)
	}

	if file.Pos >= file.Size { // 文件大小为0，或者文件已经传输完毕
		return nil
	}

	pb.Start(file.FullName, file.Size)
	defer pb.Finish()

	pb.Add(file.Pos)
	if file.Pos == file.Size {
		return nil
	}

	return file.sendFilePos(c, pb)
}

func (file *FileMetaRsp) sendFilePos(c io.Writer, pb util.ProgressBar) error {
	f, err := os.Open(file.FullName)
	if err != nil {
		return fmt.Errorf("open file %s failed: %w", file.FullName, err)
	}
	defer iox.Close(f)

	if offset := file.Pos; offset > 0 {
		if _, err = f.Seek(int64(offset), io.SeekStart); err != nil {
			return fmt.Errorf("seek %s failed: %w", codec.Json(file), err)
		}
	}

	c = util.NewProxyWriter(c, pb)
	remainSize := file.Size - file.Pos
	r := io.LimitReader(f, int64(remainSize))
	n, err := io.CopyBuffer(c, r, make([]byte, msgChunkSize))
	if err != nil {
		return fmt.Errorf("send file %s failed: %w", file.FullName, err)
	}

	if uint64(n) != remainSize {
		return fmt.Errorf("EOF before sending all bytes: (%d/%d)", n, remainSize)
	}

	return nil
}
