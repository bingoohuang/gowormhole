package main

// #include <stdint.h>
import "C"

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/bingoohuang/gg/pkg/v"
	"github.com/creasty/defaults"
)

// GetVersion 获得版本号信息，返回 JSON 字符串
// e.g. {"gitCommit": "master-96c5683@2022-11-14T13:18:13+08:00", "buildTime": "2022-11-15T20:12:20+0800", "goVersion": "go1.19.2_darwin/amd64", "appVersion": ""appVersion""}
//
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

// SendFiles 发送文件. 请求 JSON 字符串.
// e.g. {"code": "发送码", "files": ["1.jpg", "2.jpg"]}
// code: 发送码，为空时，会生成新码
// files: 发送文件列表
// sigserv: 信令服务器地址，默认 http://gowormhole.d5k.co
// iceTimeouts: 超时时间，默认 {"disconnectedTimeout": "5s", "failedTimeout": "10s", "keepAliveInterval": "2s"}
//   - disconnectedTimeout is the duration without network activity before a Agent is considered disconnected. Default is 5 Seconds
//   - failedTimeout is the duration without network activity before a Agent is considered failed after disconnected. Default is 25 Seconds
//   - keepAliveInterval is how often the ICE Agent sends extra traffic if there is no activity, if media is flowing no traffic will be sent. Default is 2 seconds
//
// retryTimes: 重试次数，默认 10
// whoami: 我是谁，标记当前客户端信息
// resultFile: 输出结果 JSON 文件名，默认不输出，需要访问传输进度，请设置次文件，例如: some.json，然后定时从此文件中读取进度结果
// resultInterval: 刷新进度间隔，默认1s.

// 输出 JSON 文件内容示例：
// {"code": "", "error":"", "progresses":[{"filename":"a.jpg", "size": 12345, "written": 1024, "finished": false}]}
// code: 传输码
// error: 错误信息
// filename: 文件名
// size: 文件大小
// sent: 已发送大小
// finished 是否已经传输完成

//export SendFiles
func SendFiles(sendFileArgJSON string) (resultJSON string) {
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
	arg.pb = result

	if err := sendFilesRetry(&arg); err != nil {
		result.Err = fmt.Errorf("sendFiles %s failed: %v", sendFileArgJSON, err)
	}

	return
}

// RecvFiles 接收文件. 请求 JSON 字符串.
// e.g. {"code": "发送码", "dir": "."}
// code: 发送码，为空时，会生成新码
// dir: 接收文件存放目录
// sigserv: 信令服务器地址，默认 http://gowormhole.d5k.co
// iceTimeouts: 超时时间，默认 {"disconnectedTimeout": "5s", "failedTimeout": "10s", "keepAliveInterval": "2s"}
//   - disconnectedTimeout is the duration without network activity before a Agent is considered disconnected. Default is 5 Seconds
//   - failedTimeout is the duration without network activity before a Agent is considered failed after disconnected. Default is 25 Seconds
//   - keepAliveInterval is how often the ICE Agent sends extra traffic if there is no activity, if media is flowing no traffic will be sent. Default is 2 seconds
//
// retryTimes: 重试次数，默认 10
// resultFile: 输出结果 JSON 文件名，默认不输出，需要访问传输进度，请设置次文件，例如: some.json，然后定时从此文件中读取进度结果
// resultInterval: 刷新进度间隔，默认1s.

// 输出 JSON 文件内容示例：
// {"code": "", "error":"", "progresses":[{"filename":"a.jpg", "size": 12345, "written": 1024, "finished": false}]}
// code: 传输码
// error: 错误信息
// filename: 文件名
// size: 文件大小
// sent: 已接收大小
// finished 是否已经传输完成

//export RecvFiles
func RecvFiles(argJSON string) (resultJSON string) {
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
	arg.pb = result

	if err := receiveRetry(context.TODO(), &arg); err != nil {
		result.Err = fmt.Errorf("receive: %w", err)
	}

	return
}

func (c *FilesResult) Start(filename string, n uint64) {
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
	c.writeJSON()
	c.currentProgress.Finished = true
	c.currentProgress = nil
}

type FilesResult struct {
	Code            string          `json:"code"`
	Err             error           `json:"error"`
	Progresses      []*FileProgress `json:"progresses"`
	currentProgress *FileProgress

	startTime time.Time
	interval  time.Duration
	jsonFile  string
}

type FileProgress struct {
	Filename string `json:"filename"`
	Size     uint64 `json:"size"`
	Written  uint64 `json:"written"`
	Finished bool   `json:"finished"`
}
