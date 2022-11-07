// Package gowormhole  provides some helpful utilities.
package gowormhole

import (
	"embed"
	"io/fs"
	"log"
)

//go:embed web
var web embed.FS

// Web is the web embed fs.FS for the web folder.
var Web = func() fs.FS {
	sub, err := fs.Sub(web, "web")
	if err != nil {
		log.Fatal(err)
	}
	return sub
}()

const (
	// DefaultStunPort is the default stun server's port.
	DefaultStunPort = 3478
	// DefaultTurnPort is the default turn server's port.
	DefaultTurnPort = 3478
)
