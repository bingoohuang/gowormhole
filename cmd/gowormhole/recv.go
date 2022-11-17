package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"time"

	"github.com/bingoohuang/gg/pkg/iox"
	"github.com/bingoohuang/gowormhole/internal/util"
	"github.com/bingoohuang/gowormhole/wormhole"
	"github.com/creasty/defaults"
)

func receiveSubCmd(ctx context.Context, sigserv string, args ...string) {
	dir, code, passLength := parseFlags(args)
	if err := receiveRetry(ctx, &receiveFileArg{
		Code:         code,
		SecretLength: passLength,
		Dir:          dir,
		Progress:     true,
		Sigserv:      sigserv,
		RetryTimes:   1,
	}); err != nil && err != io.EOF {
		log.Fatalf("receiving failed: %v", err)
	}
}

type receiveFileArg struct {
	Code           string            `json:"code"`
	SecretLength   int               `json:"secretLength" default:"2"`
	Dir            string            `json:"dir" default:"."`
	Progress       bool              `json:"progress"`
	Sigserv        string            `json:"sigserv"`
	Timeouts       wormhole.Timeouts `json:"timeouts"`
	RetryTimes     int               `json:"retryTimes" default:"10"`
	ResultFile     string            `json:"resultFile"`
	ResultInterval time.Duration     `json:"resultInterval" default:"1s"`

	DriverName     string `json:"driverName" default:"sqlite"`
	DataSourceName string `json:"dataSourceName" default:"gowormhole.db"`

	pb util.ProgressBar
	db *sql.DB
}

func receiveRetry(ctx context.Context, arg *receiveFileArg) error {
	if err := defaults.Set(arg); err != nil {
		log.Printf("defaults.Set %+v failed: %v", arg, err)
	}

	var err error

	for i := 1; i <= arg.RetryTimes; i++ {
		if err = receiveOnce(ctx, arg); err == nil {
			return nil
		}

		log.Printf("receive failed: %v, retryTimes: %d", err, i)
	}

	return err
}

func receiveOnce(ctx context.Context, arg *receiveFileArg) error {
	c := newConn(context.TODO(), arg.Sigserv, arg.Code, arg.SecretLength, &arg.Timeouts)
	arg.Code = c.Code
	defer iox.Close(c)

	rw := util.TimeoutReadWriter(c, arg.Timeouts.RwTimeout)
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

	db := dbm.GetDB(ctx, arg.DriverName, arg.DataSourceName)
	defer dbm.Close(arg.DataSourceName, db)

	var rspFiles []*FileMetaRsp
	for _, f := range meta.Files {
		rsp, err := f.LookupFilePos(ctx, db, arg.Dir, meta)
		if err != nil {
			return fmt.Errorf("receiveByWormhole failed: %w", err)
		}

		rspFiles = append(rspFiles, rsp)
	}

	if _, err := sendJSON(c, SendFilesMetaRsp{
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

		log.Printf("receiving: %s... ", file.RecvFullName)

		if err := file.receiving(ctx, c, db, pb); err != nil {
			return err
		}

		metaFile, _ := createFileMetaReq(file.RecvFullName)
		if metaFile != nil {
			log.Printf("receiving: %s hash: %s ", file.RecvFullName, metaFile.Hash)
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

func (file *FileMetaRsp) receiving(ctx context.Context, c io.Reader, db *sql.DB, pb util.ProgressBar) error {
	if file.Pos == file.Size {
		pb.Start(file.RecvFullName, file.Size)
		pb.Add(file.Pos)
		pb.Finish()
		return nil
	}

	f, err := os.OpenFile(file.RecvFullName, os.O_CREATE|os.O_WRONLY, os.ModePerm)
	if err != nil {
		return fmt.Errorf("create output file %s failed: %w", file, err)
	}

	defer iox.Close(f)

	if file.Pos > 0 {
		if _, err := f.Seek(int64(file.Pos), io.SeekStart); err != nil {
			return fmt.Errorf("seek %s to offset %d failed: %w", file, file.Pos, err)
		}
	}

	pb.Start(file.RecvFullName, file.Size)
	pb.Add(file.Pos)
	p := newSaveN(ctx, db, file.Hash, file.Pos, pb)
	defer p.Finish()

	remainSize := int64(file.Size - file.Pos)
	written, err := io.CopyN(f, util.NewProxyReader(c, p), remainSize)
	if err != nil {
		return fmt.Errorf("create receive file %+v failed: %w", *file, err)
	}

	if written != remainSize {
		return fmt.Errorf("EOF before receiving all bytes: (%d/%d)", written, remainSize)
	}

	return nil
}
