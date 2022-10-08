package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	"github.com/pion/logging"
	"github.com/pion/stun"
	"github.com/pion/turn/v2"
)

/**
https://github.com/pion/turn/blob/master/examples/README.md

$ ./gowormwhole turn -public-ip 127.0.0.1
*/

func turnServerSubCmd(args ...string) {
	set := flag.NewFlagSet(args[0], flag.ExitOnError)
	set.Usage = func() {
		_, _ = fmt.Fprintf(set.Output(), "run the gowormhole TURN server\n\n")
		_, _ = fmt.Fprintf(set.Output(), "usage: %s %s\n\n", os.Args[0], args[0])
		_, _ = fmt.Fprintf(set.Output(), "flags:\n")
		set.PrintDefaults()
	}

	publicIP := set.String("public-ip", "127.0.0.1", "IP Address that TURN can be contacted by.")
	port := set.Int("port", 3478, "Listening port.")
	users := set.String("users", "scott=tiger", `List of username and password (e.g. "user=pass,user=pass")`)
	realm := set.String("realm", "pion.ly", `Realm (defaults to "pion.ly")`)
	portRange := set.String("portRange", "", `turn.RelayAddressGeneratorPortRange, like 50000-55000`)
	authSecret := flag.String("authSecret", "", "Shared secret for the Long Term Credential Mechanism")
	certFile := flag.String("cert", "server.crt", `Certificate (defaults to "server.crt")`)
	keyFile := flag.String("key", "server.key", `Key (defaults to "server.key")`)
	listeningOnTcp := set.Bool("tcp", false, `Listening on TCP`)
	inspectStunPackets := set.Bool("inspect", false, `Inspect incoming/outgoing STUN packets`)
	_ = set.Parse(args[1:])

	if len(*publicIP) == 0 {
		log.Fatalf("'public-ip' is required")
	} else if len(*users) == 0 {
		log.Fatalf("'users' is required")
	}

	var packetConnConfigs []turn.PacketConnConfig
	var listenerConfigs []turn.ListenerConfig
	if *listeningOnTcp {
		var tcpListener net.Listener
		if *certFile != "" && *keyFile != "" {
			cer, err := tls.LoadX509KeyPair(*certFile, *keyFile)
			if err != nil {
				log.Println(err)
				return
			}

			// Create a TLS listener to pass into pion/turn
			// pion/turn itself doesn't allocate any TLS listeners, but lets the user pass them in
			// this allows us to add logging, storage or modify inbound/outbound traffic
			tcpListener, err = tls.Listen("tcp4", "0.0.0.0:"+strconv.Itoa(*port), &tls.Config{
				MinVersion:   tls.VersionTLS12,
				Certificates: []tls.Certificate{cer},
			})
			if err != nil {
				log.Println(err)
				return
			}
		} else {
			var err error
			// Create a TCP listener to pass into pion/turn
			// pion/turn itself doesn't allocate any TCP listeners, but lets the user pass them in
			// this allows us to add logging, storage or modify inbound/outbound traffic
			tcpListener, err = net.Listen("tcp4", "0.0.0.0:"+strconv.Itoa(*port))
			if err != nil {
				log.Panicf("Failed to create TURN server listener: %s", err)
			}
		}
		// ListenerConfig is a list of Listeners and the configuration around them
		listenerConfigs = []turn.ListenerConfig{{
			Listener: tcpListener,
			RelayAddressGenerator: &turn.RelayAddressGeneratorStatic{
				RelayAddress: net.ParseIP(*publicIP),
				Address:      "0.0.0.0",
			},
		}}
	} else {
		// Create a UDP listener to pass into pion/turn
		// pion/turn itself doesn't allocate any UDP sockets, but lets the user pass them in
		// this allows us to add logging, storage or modify inbound/outbound traffic
		udpListener, err := net.ListenPacket("udp4", "0.0.0.0:"+strconv.Itoa(*port))
		if err != nil {
			log.Panicf("Failed to create TURN server listener: %s", err)
		}

		if *inspectStunPackets {
			udpListener = &stunLogger{PacketConn: udpListener}
		}

		var relayAddressGenerator turn.RelayAddressGenerator

		if *portRange == "" {
			relayAddressGenerator = &turn.RelayAddressGeneratorStatic{
				// Claim that we are listening on IP passed by user (This should be your Public IP)
				RelayAddress: net.ParseIP(*publicIP),
				// But actually be listening on every interface
				Address: "0.0.0.0",
			}
		} else {
			minPort, maxPort := SplitUint16(*portRange)
			relayAddressGenerator = &turn.RelayAddressGeneratorPortRange{
				// Claim that we are listening on IP passed by user (This should be your Public IP)
				RelayAddress: net.ParseIP(*publicIP),
				// But actually be listening on every interface
				Address: "0.0.0.0",
				MinPort: minPort,
				MaxPort: maxPort,
			}
		}

		packetConnConfigs = []turn.PacketConnConfig{{
			PacketConn:            udpListener,
			RelayAddressGenerator: relayAddressGenerator,
		}}
	}

	// Cache -users flag for easy lookup later
	// If passwords are stored they should be saved to your DB hashed using turn.GenerateAuthKey
	usersMap := map[string][]byte{}
	for _, kv := range regexp.MustCompile(`(\w+)=(\w+)`).FindAllStringSubmatch(*users, -1) {
		usersMap[kv[1]] = turn.GenerateAuthKey(kv[1], *realm, kv[2])
	}

	var authHandler turn.AuthHandler

	if *authSecret != "" {
		// NewLongTermAuthHandler takes a pion.LeveledLogger. This allows you to intercept messages
		// and process them yourself.
		logger := logging.NewDefaultLeveledLoggerForScope("longterm-creds", logging.LogLevelTrace, os.Stdout)
		// Set AuthHandler callback
		// This is called everytime a user tries to authenticate with the TURN server
		// Return the key for that user, or false when no user is found
		authHandler = turn.NewLongTermAuthHandler(*authSecret, logger)
	} else {
		authHandler = func(username, realm string, srcAddr net.Addr) ([]byte, bool) {
			key, ok := usersMap[username]
			return key, ok
		}
	}

	s, err := turn.NewServer(turn.ServerConfig{
		Realm: *realm,
		// Set AuthHandler callback
		// This is called everytime a user tries to authenticate with the TURN server
		// Return the key for that user, or false when no user is found
		AuthHandler: authHandler,
		// PacketConnConfigs is a list of UDP Listeners and the configuration around them
		PacketConnConfigs: packetConnConfigs,
		// ListenerConfig is a list of Listeners and the configuration around them
		ListenerConfigs: listenerConfigs,
	})
	if err != nil {
		log.Panic(err)
	}

	// Block until user sends SIGINT or SIGTERM
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	<-sigs

	if err = s.Close(); err != nil {
		log.Panic(err)
	}
}

func SplitUint16(portRange string) (uint16, uint16) {
	idx := strings.Index(portRange, "-")
	from, _ := strconv.ParseUint(portRange[:idx], 10, 16)
	to, _ := strconv.ParseUint(portRange[idx+1:], 10, 16)
	return uint16(from), uint16(to)
}

// stunLogger wraps a PacketConn and prints incoming/outgoing STUN packets
// This pattern could be used to capture/inspect/modify data as well
type stunLogger struct {
	net.PacketConn
}

func (s *stunLogger) WriteTo(p []byte, addr net.Addr) (n int, err error) {
	if n, err = s.PacketConn.WriteTo(p, addr); err == nil && stun.IsMessage(p) {
		msg := &stun.Message{Raw: p}
		if err = msg.Decode(); err != nil {
			return
		}

		fmt.Printf("Outbound STUN: %s \n", msg.String())
	}

	return
}

func (s *stunLogger) ReadFrom(p []byte) (n int, addr net.Addr, err error) {
	if n, addr, err = s.PacketConn.ReadFrom(p); err == nil && stun.IsMessage(p) {
		msg := &stun.Message{Raw: p}
		if err = msg.Decode(); err != nil {
			return
		}

		fmt.Printf("Inbound STUN: %s \n", msg.String())
	}

	return
}

// attributeAdder wraps a PacketConn and appends the SOFTWARE attribute to STUN packets
// This pattern could be used to capture/inspect/modify data as well
type attributeAdder struct {
	net.PacketConn
}

func (s *attributeAdder) WriteTo(p []byte, addr net.Addr) (n int, err error) {
	if stun.IsMessage(p) {
		m := &stun.Message{Raw: p}
		if err = m.Decode(); err != nil {
			return
		}

		if err = stun.NewSoftware("CustomTURNServer").AddTo(m); err != nil {
			return
		}

		m.Encode()
		p = m.Raw
	}

	return s.PacketConn.WriteTo(p, addr)
}
