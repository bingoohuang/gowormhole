package main

// #include <stdint.h>
import "C"

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"runtime/cgo"
	"sync"
	"sync/atomic"

	"github.com/bingoohuang/gg/pkg/iox"
	"github.com/bingoohuang/gg/pkg/v"
	"github.com/bingoohuang/gowormhole/wormhole"
	"github.com/creasty/defaults"
)

//export GetVersion
func GetVersion() string {
	ver, _ := json.Marshal(struct {
		GitCommit  string `json:"gitCommit"`
		BuildTime  string `json:"buildTime"`
		GoVersion  string `json:"goVersion"`
		AppVersion string `json:"appVersion"`
	}{
		GitCommit:  v.GitCommit,
		BuildTime:  v.BuildTime,
		GoVersion:  v.GoVersion,
		AppVersion: v.AppVersion,
	})

	return string(ver)
}

//export RecvFiles
func RecvFiles(argJSON string) (handle C.uintptr_t) {
	result := &FilesResult{}
	handle = C.uintptr_t(cgo.NewHandle(result))
	defer func() {
		if result.Err != nil {
			log.Printf("error occured: %+v", result.Err)
		}
	}()

	var arg receiveFileArg
	if err := json.Unmarshal([]byte(argJSON), &arg); err != nil {
		result.Err = fmt.Errorf("json.Unmarshal %s: %w", argJSON, err)
		return
	}

	if err := defaults.Set(&arg); err != nil {
		result.Err = fmt.Errorf("defaults.Set: %w", err)
		return
	}

	c := newConn(context.TODO(), arg.Sigserv, arg.Code, arg.SecretLength, arg.IceTimeouts)
	result.c = c
	arg.Code = c.Code
	defer iox.Close(c)

	result.wg = &sync.WaitGroup{}
	arg.pb = result

	result.wg.Add(1)
	go func() {
		defer result.wg.Done()
		defer iox.Close(c)

		if err := receiveByWormhole(c, &arg); err != nil {
			if err != io.EOF {
				result.Err = fmt.Errorf("receive: %w", err)
			}
		}
	}()

	return
}

// SendFiles 发送文件. 请求 JSON 字符串.
// e.g. {"code": "发送码", "files": ["1.jpg", "2.jpg"], "sync": false}
// code: 发送码，为空时，会生成新码
// files: 发送文件列表
// sync: 是否同步发送（当前调用阻塞，直到文件传输完成，或者发生错误）
//
//export SendFiles
func SendFiles(sendFileArgJSON string) (handle C.uintptr_t) {
	result := &FilesResult{}
	handle = C.uintptr_t(cgo.NewHandle(result))
	defer func() {
		if result.Err != nil {
			log.Printf("error occured: %+v", result.Err)
		}
	}()

	var arg sendFileArg
	if err := json.Unmarshal([]byte(sendFileArgJSON), &arg); err != nil {
		result.Err = fmt.Errorf("json.Unmarshal %s failed: %v", sendFileArgJSON, err)
		return
	}

	if err := defaults.Set(&arg); err != nil {
		result.Err = fmt.Errorf("defaults.Set failed: %v", err)
		return
	}

	result.wg = &sync.WaitGroup{}
	arg.pb = result

	c := newConn(context.TODO(), arg.Sigserv, arg.Code, arg.SecretLength, arg.IceTimeouts)
	result.c = c
	result.Code = c.Code

	result.wg.Add(1)
	go func() {
		defer result.wg.Done()
		defer iox.Close(c)

		if err := sendFilesByWormhole(c, &arg); err != nil {
			result.Err = fmt.Errorf("sendFiles %s failed: %v", sendFileArgJSON, err)
		}
	}()

	return
}

// GetFilesResult 获得文件传输结果，返回 JSON
// e.g. {"progresses":[{"code": "crop-nerd-xerox","error": null, "filename":"a.jpg", "size": 12345, "written": 1024, "finished": false}]}
// code: 传输码
// error: 错误信息
// filename: 文件名
// size: 文件大小
// sent: 已发送大小
// finished 是否已经传输完成
//
//export GetFilesResult
func GetFilesResult(handle C.uintptr_t) string {
	result := cgo.Handle(handle).Value().(*FilesResult)
	jsonProgressResult, _ := json.Marshal(result)
	return string(jsonProgressResult)
}

// WaitFilesHandle 等待传输处理结束
//
//export WaitFilesHandle
func WaitFilesHandle(handle C.uintptr_t) {
	result := cgo.Handle(handle).Value().(*FilesResult)
	result.wg.Wait()
}

type Progress struct {
	Progresses      []*FileProgress `json:"progresses"`
	currentProgress *FileProgress
}

type FileProgress struct {
	Filename string `json:"filename"`
	Size     int    `json:"size"`
	Written  int32  `json:"written"`
	Finished bool   `json:"finished"`
}

func (c *Progress) Start(filename string, n int) {
	c.currentProgress = &FileProgress{
		Filename: filename,
		Size:     n,
	}
	c.Progresses = append(c.Progresses, c.currentProgress)
}

func (c *Progress) Add(n int) {
	atomic.AddInt32(&c.currentProgress.Written, int32(n))
}

func (c *Progress) Finish() {
	c.currentProgress.Finished = true
	c.currentProgress = nil
}

type FilesResult struct {
	Code string `json:"code"`
	Err  error  `json:"error"`
	Progress

	wg *sync.WaitGroup
	c  *wormhole.Wormhole
}
