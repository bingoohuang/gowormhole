package main

import (
	"flag"
	"fmt"
	"time"

	"github.com/pion/webrtc/v3"
)

func main() {
	timemout := flag.Duration("timeout", 15*time.Second, "timeout")
	flag.Parse()

	PrintPublicIP(*timemout)
}

func PrintPublicIP(timeout time.Duration) {
	publicIP, err := DiscoverPublicIP(timeout)
	if err != nil {
		fmt.Println(err)
	} else {
		fmt.Println(publicIP)
	}
}

// DiscoverPublicIP discovers public IP address of executed device by STUN server
func DiscoverPublicIP(timeout time.Duration) (string, error) {
	c, err := webrtc.NewPeerConnection(webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{{
			URLs: []string{"stun:stun.l.google.com:19302"},
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
