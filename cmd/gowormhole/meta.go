package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/OneOfOne/xxhash"
	"github.com/bingoohuang/gg/pkg/codec"
	"github.com/bingoohuang/gg/pkg/goip"
	"github.com/bingoohuang/gg/pkg/iox"
	"github.com/bingoohuang/gg/pkg/sqx"
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

func createSendFilesMeta(whoami string, files []string) (*SendFilesMeta, error) {
	hostname, _ := os.Hostname()
	_, ips := goip.MainIP()
	meta := &SendFilesMeta{
		Whoami:   whoami,
		Hostname: hostname,
		Ips:      strings.Join(ips, ","),
	}
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

func updateRecvTable(ctx context.Context, db *sql.DB, hash string, pos uint64, cost string) error {
	r, err := db.ExecContext(ctx, updateTableRecvSQL, pos, time.Now(), cost, hash)
	if err != nil {
		return fmt.Errorf("update table failed: %w", err)
	}

	if rowsAffected, _ := r.RowsAffected(); rowsAffected != 1 {
		return fmt.Errorf("update table failed because rowsAffected = 0")
	}

	return nil
}

func (file *FileMetaReq) LookupDB(ctx context.Context, db *sql.DB, dir string, meta SendFilesMeta) (*Recv, error) {
	var recv Recv
	sq := sqx.SQL{Q: hashQuerySQL, Ctx: ctx, Vars: sqx.Vars(file.Hash), NoLog: true}
	if err := sq.Query(db, &recv); err == nil {
		log.Printf("lookup db %s", codec.Json(recv))
		return &recv, nil
	}

	r := Recv{
		Hash:     file.Hash,
		Size:     file.Size,
		Pos:      0,
		Expired:  time.Now().Add(24 * time.Hour),
		Updated:  time.Now(),
		Name:     file.CleanName,
		Full:     path.Join(dir, file.CleanName),
		Hostname: meta.Hostname,
		Ips:      meta.Ips,
		Whoami:   meta.Whoami,
	}
	if _, err := db.ExecContext(ctx, insertTableRecvSQL,
		r.Hash, r.Size, r.Pos, r.Expired, r.Updated, r.Name, r.Full, r.Hostname, r.Ips, r.Whoami); err != nil {
		return nil, fmt.Errorf("insert recv failed: %w", err)
	}
	return &r, nil
}

type dbManager struct {
	dbs map[string]*sql.DB
	sync.RWMutex
}

var dbm = &dbManager{dbs: make(map[string]*sql.DB)}

func (d *dbManager) GetDB(ctx context.Context, driverName, dataSourceName string) *sql.DB {
	d.RLock()
	db, ok := d.dbs[dataSourceName]
	d.RUnlock()
	if ok {
		return db
	}

	d.Lock()
	defer d.Unlock()

	db, err := sql.Open(driverName, dataSourceName)
	if err != nil {
		log.Fatalf("open db (driverName: %s, dataSourceName: %s) failed: %v", driverName, dataSourceName, err)
	}

	if _, err := db.ExecContext(ctx, createTableRecvSQL); err != nil {
		log.Printf("createTableRecvSQL failed: %v", err)
	}

	d.dbs[dataSourceName] = db
	return db
}

func (d *dbManager) Close(dataSourceName string, db *sql.DB) error {
	d.Lock()
	defer d.Unlock()

	err := db.Close()
	delete(d.dbs, dataSourceName)

	return err
}

func sendJSON(c io.Writer, v interface{}) ([]byte, error) {
	j, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("json.Marshal failed: %w", err)
	}

	if _, err := c.Write(j); err != nil {
		return nil, fmt.Errorf("written failed: %w", err)
	}

	return j, err
}

func recvJSON(c io.Reader, v interface{}) ([]byte, error) {
	// First message is the header. 10k should be enough.
	buf := make([]byte, 1<<11)
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

func (file *FileMetaReq) LookupFilePos(ctx context.Context, db *sql.DB, dir string, meta SendFilesMeta) (*FileMetaRsp, error) {
	recv, err := file.LookupDB(ctx, db, dir, meta)
	if err != nil {
		return nil, fmt.Errorf("lookup failed: %w", err)
	}

	rsp := &FileMetaRsp{
		FileMetaReq: *file,
	}

	if err := createFileMetaRsp(recv.Full, 0, rsp); err != nil {
		log.Printf("createFileMeta failed: %v", err)
	}

	if rsp.Pos != recv.Pos {
		if err := updateRecvTable(ctx, db, recv.Hash, rsp.Pos, recv.Cost); err != nil {
			log.Printf("update recv pos failed: %v", err)
		}
	}

	return rsp, nil
}
