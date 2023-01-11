package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/OneOfOne/xxhash"
	"github.com/bingoohuang/gg/pkg/goip"
	"github.com/bingoohuang/gg/pkg/iox"
)

type FileMetaRsp struct {
	FileMetaReq
	Pos          uint64 `json:"pos"`
	PosHash      string `json:"posHash"`
	RecvFullName string `json:"recvFullName"`
}

type FileMetaReq struct {
	CleanName string `json:"cleanName"`
	FullName  string `json:"fullName"`
	Size      uint64 `json:"size"`
	Hash      string `json:"hash"`
}

type SendFilesMeta struct {
	Whoami   string         `json:"whoami"`
	Hostname string         `json:"hostname"`
	Ips      string         `json:"ips"`
	Files    []*FileMetaReq `json:"files"`
}

type SendFilesMetaRsp struct {
	Files []*FileMetaRsp `json:"files"`
}

var ips = func() string {
	_, ips := goip.MainIP()
	return strings.Join(ips, ",")
}()

var hostname = func() string {
	h, _ := os.Hostname()
	return h
}

func createSendFilesMeta(whoami string, files []string) (*SendFilesMeta, error) {
	meta := &SendFilesMeta{Whoami: whoami, Hostname: hostname(), Ips: ips}
	for _, file := range files {
		fileMeta, err := createFileMetaReq(file)
		if err != nil {
			return nil, err
		}

		meta.Files = append(meta.Files, fileMeta)
	}

	return meta, nil
}

func createFileMetaReq(filename string) (*FileMetaReq, error) {
	f, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("open file %s failed: %w", filename, err)
	}
	defer iox.Close(f)

	stat, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat file %s failed: %w", filename, err)
	}

	h := xxhash.New64()
	if _, err := io.Copy(h, f); err != nil {
		return nil, fmt.Errorf("create file hash failed: %w", err)
	}

	return &FileMetaReq{
		CleanName: filepath.Base(filepath.Clean(filename)),
		FullName:  filename,
		Hash:      fmt.Sprintf("%d", h.Sum64()),
		Size:      uint64(stat.Size()),
	}, nil
}

func createFileMetaRsp(filename string, pos uint64, rsp *FileMetaRsp) error {
	rsp.RecvFullName = filename
	f, err := os.Open(filename)
	if err != nil {
		if os.IsNotExist(err) {
			rsp.Pos = 0
			rsp.PosHash = ""
			return nil
		}
		return fmt.Errorf("open file %s failed: %w", filename, err)
	}
	defer iox.Close(f)

	h := xxhash.New64()
	var n int64
	if pos > 0 {
		n, err = io.CopyN(h, f, int64(pos))
	} else {
		n, err = io.Copy(h, f)
	}
	if err != nil {
		return fmt.Errorf("create file hash failed: %w", err)
	}

	rsp.Pos = uint64(n)
	rsp.PosHash = fmt.Sprintf("%d", h.Sum64())

	return nil
}

type Recv struct {
	Hash     string
	Size     uint64
	Pos      uint64
	Expired  time.Time
	Updated  time.Time
	Name     string
	Full     string
	Hostname string
	Ips      string
	Whoami   string
	Cost     string
}

func sendJSON(c io.Writer, v interface{}) error {
	j, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("json.Marshal failed: %w", err)
	}

	if _, err := c.Write(j); err != nil {
		return fmt.Errorf("written JSON %s failed: %w", j, err)
	}

	return err
}

func recvJSON(c io.Reader, v interface{}) ([]byte, error) {
	// First message is the header. 1M should be enough.
	buf := make([]byte, 1024*1024)
	n, err := c.Read(buf)
	if err != nil {
		if err == io.EOF {
			return nil, io.EOF
		}

		return nil, fmt.Errorf("read file header failed: %w", err)
	}

	if err := json.Unmarshal(buf[:n], v); err != nil {
		return nil, fmt.Errorf("json.Unmarshal %s failed: %w", buf[:n], err)
	}

	return buf[:n], nil
}

func (file *FileMetaReq) LookupFilePos(dir string) (*FileMetaRsp, error) {
	rsp := &FileMetaRsp{
		FileMetaReq: *file,
	}

	full := path.Join(dir, file.CleanName)
	if err := createFileMetaRsp(full, 0, rsp); err != nil {
		log.Printf("createFileMeta failed: %v", err)
	}

	return rsp, nil
}
