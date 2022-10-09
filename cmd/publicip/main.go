package main

import (
	"fmt"
	"time"

	"github.com/pion/webrtc/v3"
)

func main() {
	PrintPublicIP()
}

func PrintPublicIP() {
	publicIP, err := DiscoverPublicIP()
	if err != nil {
		fmt.Println(err)
	} else {
		fmt.Println(publicIP)
	}
}

// DiscoverPublicIP discovers public IP address of executed device by STUN server
func DiscoverPublicIP() (string, error) {
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
	case <-time.After(5 * time.Second):
		return "", fmt.Errorf("timeout")
	case IP := <-ch:
		return IP, nil
	}
}
