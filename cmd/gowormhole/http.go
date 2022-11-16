package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/bingoohuang/gg/pkg/iox"
	"github.com/bingoohuang/godaemon"
	"github.com/bingoohuang/golog"
	"github.com/bingoohuang/jj"
	"github.com/creasty/defaults"
)

func httpCmd(ctx context.Context, sigserv string, args ...string) {
	f := flag.NewFlagSet(args[0], flag.ExitOnError)
	f.Usage = func() {
		_, _ = fmt.Fprintf(f.Output(), "run the gowormhole http server\n\n")
		_, _ = fmt.Fprintf(f.Output(), "usage: %s %s\n\n", os.Args[0], args[0])
		_, _ = fmt.Fprintf(f.Output(), "flags:\n")
		f.PrintDefaults()
	}
	httpAddr := f.String("addr", ":31415", "http listen address")
	pDaemon := f.Bool("daemon", false, "Daemonized")
	_ = f.Parse(args[1:])

	godaemon.Daemonize(*pDaemon)
	golog.Setup()

	http.HandleFunc("/", httpService)
	http.ListenAndServe(*httpAddr, nil)
}

func httpService(w http.ResponseWriter, r *http.Request) {
	body := iox.ReadString(r.Body)
	if !jj.Valid(body) {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	files := jj.Get(body, "files")
	if files.Type == jj.JSON {
		resultJSON := sendFiles(body)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Write([]byte(resultJSON))
		return
	}

	dir := jj.Get(body, "dir")
	if dir.Type == jj.String {
		resultJSON := recvFiles(body)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Write([]byte(resultJSON))
		return
	}

	w.WriteHeader(http.StatusBadRequest)
	return
}

func sendFiles(sendFileArgJSON string) (resultJSON string) {
	result := &FilesResult{}
	defer func() {
		if result.Err != nil {
			log.Printf("error occured: %+v", result.Err)
		}
		j, _ := json.Marshal(result)
		resultJSON = string(j)
	}()

	var arg sendFileArg
	if err := json.Unmarshal([]byte(sendFileArgJSON), &arg); err != nil {
		result.Err = fmt.Errorf("json.Unmarshal %s failed: %v", sendFileArgJSON, err)
		return
	}

	if err := defaults.Set(&arg); err != nil {
		log.Printf("defaults.Set: %w", err)
	}

	result.jsonFile = arg.ResultFile
	result.interval = arg.ResultInterval
	result.arg = &arg
	arg.pb = result

	if err := sendFilesRetry(&arg); err != nil {
		result.Err = fmt.Errorf("sendFiles %s failed: %v", sendFileArgJSON, err)
	}

	return
}

func recvFiles(argJSON string) (resultJSON string) {
	result := &FilesResult{}
	defer func() {
		if result.Err != nil {
			log.Printf("error occured: %+v", result.Err)
		}

		j, _ := json.Marshal(result)
		resultJSON = string(j)
	}()

	var arg receiveFileArg
	if err := json.Unmarshal([]byte(argJSON), &arg); err != nil {
		result.Err = fmt.Errorf("json.Unmarshal %s: %w", argJSON, err)
		return
	}

	if err := defaults.Set(&arg); err != nil {
		log.Printf("defaults.Set: %w", err)
	}

	result.jsonFile = arg.ResultFile
	result.interval = arg.ResultInterval
	result.arg = &arg
	arg.pb = result

	if err := receiveRetry(context.TODO(), &arg); err != nil {
		result.Err = fmt.Errorf("receive: %w", err)
	}

	return
}

func (c *FilesResult) Start(filename string, n uint64) {
	c.Code = c.arg.GetCode()
	c.startTime = time.Now()
	c.currentProgress = &FileProgress{
		Filename: filename,
		Size:     n,
	}
	c.Progresses = append(c.Progresses, c.currentProgress)
	c.writeJSON()
}

func (c *FilesResult) writeJSON() {
	if c.jsonFile != "" {
		c.startTime = time.Now()
		j, _ := json.Marshal(c)
		if err := os.WriteFile(c.jsonFile, j, os.ModePerm); err != nil {
			log.Printf("write result json error: %v", err)
		}
	}
}

func (c *FilesResult) Add(n uint64) {
	c.currentProgress.Written += n
	if time.Since(c.startTime) >= c.interval {
		c.writeJSON()
	}
}

func (c *FilesResult) Finish() {
	c.currentProgress.Finished = true
	c.currentProgress = nil
	c.writeJSON()
}

type FilesResult struct {
	Code            string          `json:"code"`
	Err             error           `json:"error"`
	Progresses      []*FileProgress `json:"progresses"`
	currentProgress *FileProgress

	startTime time.Time
	interval  time.Duration
	jsonFile  string
	arg       CodeAware
}

type FileProgress struct {
	Filename string `json:"filename"`
	Size     uint64 `json:"size"`
	Written  uint64 `json:"written"`
	Finished bool   `json:"finished"`
}
