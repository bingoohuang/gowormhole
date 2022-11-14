package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/bingoohuang/gowormhole"
	"github.com/bingoohuang/gowormhole/internal/util"
	"github.com/pion/webrtc/v3"
)

func publicIPSubCmd(sigserv string, args ...string) {
	set := flag.NewFlagSet(args[0], flag.ExitOnError)
	set.Usage = func() {
		_, _ = fmt.Fprintf(set.Output(), "nat behavior discovery\n\n")
		_, _ = fmt.Fprintf(set.Output(), "usage: %s %s ...\n\n", os.Args[0], args[0])
		_, _ = fmt.Fprintf(set.Output(), "flags:\n")
		set.PrintDefaults()
	}

	stunAddrPtr := set.String("stun", "stun.voip.blackberry.com", "STUN server address, e.g. stun:stun.l.google.com:19302")
	timeoutPtr := set.Duration("timeout", 3*time.Second, "timeout to wait for STUN server's response")
	_ = set.Parse(args[1:])

	PrintPublicIP(*stunAddrPtr, *timeoutPtr)
}

func PrintPublicIP(stunAddr string, timeout time.Duration) {
	publicIP, err := DiscoverPublicIP(stunAddr, timeout)
	if err != nil {
		fmt.Println(err)
	} else {
		fmt.Println(publicIP)
	}
}

// DiscoverPublicIP discovers public IP address of executed device by STUN server
func DiscoverPublicIP(stunAddr string, timeout time.Duration) (string, error) {
	c, err := webrtc.NewPeerConnection(webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{{
			URLs: []string{
				util.Prefix("stun:", util.AppendPort(stunAddr, gowormhole.DefaultStunPort)),
			},
		}},
	})
	if err != nil {
		return "", err
	}

	ch := make(chan string)

	c.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c != nil && c.Typ == webrtc.ICECandidateTypeSrflx { // receive public ip address
			ch <- c.Address
		}
	})

	if _, err := c.CreateDataChannel("", nil); err != nil {
		return "", err
	}

	offer, err := c.CreateOffer(nil)
	if err != nil {
		return "", err
	}

	if err := c.SetLocalDescription(offer); err != nil {
		return "", err
	}

	select {
	case <-time.After(timeout):
		return "", fmt.Errorf("timeout")
	case IP := <-ch:
		return IP, nil
	}
}
