package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/bingoohuang/gg/pkg/defaults"
	"github.com/bingoohuang/gg/pkg/iox"
	"github.com/bingoohuang/gg/pkg/netx/freeport"
	"github.com/bingoohuang/gg/pkg/ss"
	"github.com/bingoohuang/godaemon"
	"github.com/bingoohuang/golog"
	"github.com/bingoohuang/gowormhole/internal/util"
	"github.com/bingoohuang/gowormhole/wordlist"
	"github.com/bingoohuang/jj"
	"github.com/go-resty/resty/v2"
)

func httpCmd(ctx context.Context, sigserv string, args ...string) {
	f := flag.NewFlagSet(args[0], flag.ExitOnError)
	f.Usage = func() {
		_, _ = fmt.Fprintf(f.Output(), "run the gowormhole http server\n\n")
		_, _ = fmt.Fprintf(f.Output(), "usage: %s %s\n\n", os.Args[0], args[0])
		_, _ = fmt.Fprintf(f.Output(), "flags:\n")
		f.PrintDefaults()
	}
	httpAddr := f.String("addr", "", "http listen address, default :31415")
	pDaemon := f.Bool("daemon", false, "Daemonized")
	_ = f.Parse(args[1:])

	godaemon.Daemonize(*pDaemon)
	golog.Setup()

	http.HandleFunc("/", httpService)
	addr := *httpAddr
	if addr == "" {
		addr = fmt.Sprintf(":%d", freeport.PortStart(31415))
	}

	log.Printf("Listening on %s", addr)

	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Printf("listen error: %v", err)
	}

	log.Printf("exiting...")
}

func httpService(w http.ResponseWriter, r *http.Request) {
	body := iox.ReadString(r.Body)
	if !jj.Valid(body) {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	if bearer := jj.Get(body, "bearer"); bearer.String() == "" {
		if envBearer := os.Getenv("BEARER"); envBearer != "" {
			if newBody, err := jj.Set(body, "bearer", envBearer); err != nil {
				log.Printf("set bearer to env $BEARER failed: %v", err)
			} else {
				body = newBody
			}
		}
	}

	files := jj.Get(body, "files")
	if files.Type == jj.JSON {
		responseJSON(w, sendFiles(body))
		return
	}

	dir := jj.Get(body, "dir")
	if dir.Type == jj.String {
		responseJSON(w, recvFiles(body))
		return
	}

	op := jj.Get(body, "operation")
	if op.Type == jj.String {
		switch op.String() {
		case "createCode":
			responseJSON(w, createCode(body))
			return
		}
	}

	w.WriteHeader(http.StatusBadRequest)
	return
}

func responseJSON(w http.ResponseWriter, resultJSON string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Write([]byte(resultJSON))
}

func sendFiles(sendFileArgJSON string) (resultJSON string) {
	result := &FilesResult{}
	defer func() {
		if result.Err != nil {
			log.Printf("error occured: %+v", result.Err)
		}
		j, _ := json.Marshal(result)
		log.Printf("sendFiles result: %s", j)
		resultJSON = string(j)
	}()

	var arg sendFileArg
	if err := json.Unmarshal([]byte(sendFileArgJSON), &arg); err != nil {
		result.Err = fmt.Errorf("json.Unmarshal %s failed: %v", sendFileArgJSON, err)
		return
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

// Create a Resty Client
var rest = resty.New()

func createCode(argJSON string) (resultJSON string) {
	var req struct {
		Bearer       string `json:"bearer"`
		SecretLength int    `json:"secretLength" default:"2"`
		Sigserv      string `json:"sigserv"`
	}
	var result struct {
		Code string `json:"code"`
		Err  error  `json:"error,omitempty"`
	}

	defer func() {
		if result.Err != nil {
			log.Printf("error occured: %+v", result.Err)
		}

		j, _ := json.Marshal(result)
		log.Printf("createCode result: %s", j)
		resultJSON = string(j)
	}()

	if err := json.Unmarshal([]byte(argJSON), &req); err != nil {
		result.Err = fmt.Errorf("json.Unmarshal %s: %w", argJSON, err)
		return
	}

	defaults.Set(&req)

	var reserveResult reserveSlotResult
	_, err := rest.R().
		SetHeader("GoWormhole", "reserve_slot_key").
		SetHeader("Authorization", "bearer "+req.Bearer).
		SetResult(&reserveResult).
		Get(ss.Or(req.Sigserv, DefaultSigserv))
	if err != nil {
		result.Err = fmt.Errorf("reserve slot failed: %w", err)
		return
	}

	pass := string(util.RandPass(req.SecretLength))
	slotNum, _ := strconv.Atoi(reserveResult.Key)
	result.Code = wordlist.Encode(slotNum, []byte(pass))
	return
}

func recvFiles(argJSON string) (resultJSON string) {
	result := &FilesResult{}
	defer func() {
		if result.Err != nil {
			log.Printf("error occured: %+v", result.Err)
		}

		j, _ := json.Marshal(result)
		log.Printf("recvFiles result: %s", j)
		resultJSON = string(j)
	}()

	var arg receiveFileArg
	if err := json.Unmarshal([]byte(argJSON), &arg); err != nil {
		result.Err = fmt.Errorf("json.Unmarshal %s: %w", argJSON, err)
		return
	}

	result.jsonFile = arg.ResultFile
	result.interval = arg.ResultInterval
	result.arg = &arg
	arg.pb = result
	arg.recvMeta = result

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
	*SendFilesMeta
}

func (c *FilesResult) SetSendFilesMeta(meta *SendFilesMeta) {
	c.SendFilesMeta = meta
}

type FileProgress struct {
	Filename string `json:"filename"`
	Size     uint64 `json:"size"`
	Written  uint64 `json:"written"`
	Finished bool   `json:"finished"`
}
