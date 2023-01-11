package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"time"

	"github.com/bingoohuang/gg/pkg/codec"
	"github.com/bingoohuang/gg/pkg/defaults"
	"github.com/bingoohuang/gg/pkg/iox"
	"github.com/bingoohuang/gowormhole/internal/util"
	"github.com/bingoohuang/gowormhole/wormhole"
)

func receiveSubCmd(ctx context.Context, args ...string) {
	dir, code, bearer, passLength := parseFlags(args)
	if err := receiveRetry(ctx, &receiveFileArg{
		BaseArg: BaseArg{
			Bearer:       bearer,
			Code:         code,
			SecretLength: passLength,
			Progress:     true,
			Sigserv:      Sigserv,
			RetryTimes:   1,
		},
		Dir: dir,
	}); err != nil && err != io.EOF {
		log.Fatalf("receiving failed: %v", err)
	}
}

type BaseArg struct {
	Bearer         string            `json:"bearer"`
	Code           string            `json:"code"`
	SecretLength   int               `json:"secretLength" default:"2"`
	Progress       bool              `json:"progress"`
	Sigserv        string            `json:"sigserv"`
	Timeouts       wormhole.Timeouts `json:"timeouts"`
	RetryTimes     int               `json:"retryTimes" default:"10"`
	ResultFile     string            `json:"resultFile"`
	ResultInterval time.Duration     `json:"resultInterval" default:"1s"`

	pb util.ProgressBar

	recvMeta SendFilesMetaSetter
}

type SendFilesMetaSetter interface {
	SetSendFilesMeta(*SendFilesMeta)
}

type receiveFileArg struct {
	BaseArg `default:"{}"`
	Dir     string `json:"dir" default:"."`
}

func receiveRetry(ctx context.Context, arg *receiveFileArg) error {
	if err := defaults.Set(arg); err != nil {
		return fmt.Errorf("defaults.Set failed: %w", err)
	}

	var err error

	for i := 1; i <= arg.RetryTimes; i++ {
		if err = receiveOnce(ctx, arg); err == nil || errors.Is(err, ErrRetryUnsupported) {
			return err
		}

		log.Printf("receive failed: %v, retryTimes: %d", err, i)
	}

	return err
}

func receiveOnce(ctx context.Context, arg *receiveFileArg) error {
	c, err := newConn(ctx, arg.Sigserv, arg.Bearer, arg.Code, arg.SecretLength, &arg.Timeouts)
	if err != nil {
		return err
	}

	arg.Code = c.Code
	defer iox.Close(c)

	rw := util.TimeoutReadWriter(c, arg.Timeouts.RwTimeout.D())
	return receiveByWormhole(ctx, rw, arg)
}

func receiveByWormhole(ctx context.Context, c io.ReadWriter, arg *receiveFileArg) error {
	if err := InjectError("RECV_START"); err != nil {
		return err
	}

	var meta SendFilesMeta
	if metaJSON, err := recvJSON(c, &meta); err != nil {
		return fmt.Errorf("recvJSON SendFilesMeta failed: %w", err)
	} else {
		log.Printf("receiveByWormhole %s", metaJSON)
	}

	if arg.recvMeta != nil {
		arg.recvMeta.SetSendFilesMeta(&meta)
	}

	var rspFiles []*FileMetaRsp
	for _, f := range meta.Files {
		rsp, err := f.LookupFilePos(arg.Dir)
		if err != nil {
			return fmt.Errorf("receiveByWormhole failed: %w", err)
		}

		rspFiles = append(rspFiles, rsp)
	}

	if err := sendJSON(c, SendFilesMetaRsp{
		Files: rspFiles,
	}); err != nil {
		return fmt.Errorf("sendJSON SendFilesMetaResponse failed: %w", err)
	}

	pb := util.CreateProgressBar(arg.pb, arg.Progress)
	for {
		var file FileMetaRsp
		if fileJSON, err := recvJSON(c, &file); err != nil {
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("recvJSON SendFilesMeta failed: %w", err)
		} else {
			log.Printf("receive file %s", fileJSON)
		}

		if err := file.receiving(ctx, c, pb); err != nil {
			return err
		}

		if metaFile, _ := createFileMetaReq(file.RecvFullName); metaFile != nil {
			log.Printf("check received file: %s hash: %s ", file.RecvFullName, metaFile.Hash)
		}
	}
}

func parseFlags(args []string) (dir, code, bearer string, passLength int) {
	set := flag.NewFlagSet(args[0], flag.ExitOnError)
	set.Usage = func() {
		_, _ = fmt.Fprintf(set.Output(), "receive files\n\n")
		_, _ = fmt.Fprintf(set.Output(), "usage: %s %s [code]\n\n", os.Args[0], args[0])
		_, _ = fmt.Fprintf(set.Output(), "flags:\n")
		set.PrintDefaults()
	}
	length := set.Int("length", 2, "length of generated secret, if generating")
	directory := set.String("dir", ".", "directory to put downloaded files")
	pBearer := set.String("bearer", os.Getenv("BEARER"), "Bearer authentication")
	_ = set.Parse(args[1:])

	if set.NArg() > 1 {
		set.Usage()
		os.Exit(2)
	}

	dir = *directory
	code = set.Arg(0)
	passLength = *length
	bearer = *pBearer
	return
}

func (file *FileMetaRsp) receiving(ctx context.Context, c io.Reader, pb util.ProgressBar) error {
	f, err := os.OpenFile(file.RecvFullName, os.O_CREATE|os.O_WRONLY, os.ModePerm)
	if err != nil {
		return fmt.Errorf("create output file %s failed: %w", codec.Json(file), err)
	}

	defer iox.Close(f)

	pb.Start(file.RecvFullName, file.Size)
	pb.Add(file.Pos)
	defer pb.Finish()

	if file.Pos >= file.Size {
		return nil
	}

	if file.Pos > 0 {
		if _, err := f.Seek(int64(file.Pos), io.SeekStart); err != nil {
			return fmt.Errorf("seek %s  failed: %w", codec.Json(file), err)
		}
	}

	remainSize := int64(file.Size - file.Pos)
	written, err := io.CopyN(f, util.NewProxyReader(c, pb), remainSize)
	if err != nil {
		return fmt.Errorf("create receive file %+v failed: %w", *file, err)
	}

	if written != remainSize {
		return fmt.Errorf("EOF before receiving all bytes: (%d/%d)", written, remainSize)
	}

	return nil
}
