package main

import "C"

import (
	"encoding/json"
	"fmt"
	"github.com/bingoohuang/gg/pkg/v"
	"github.com/creasty/defaults"
	"io"
	"log"
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

//export SendFiles
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
