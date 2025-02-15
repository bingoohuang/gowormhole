package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"strings"
	"time"

	"github.com/bingoohuang/gowormhole"
	"github.com/pion/logging"
	"github.com/pion/turn/v2"
)

// https://github.com/pion/turn/blob/master/examples/turn-client/udp/main.go
func turnClientSubCmd(ctx context.Context, args ...string) {
	set := flag.NewFlagSet(args[0], flag.ExitOnError)
	set.Usage = func() {
		_, _ = fmt.Fprintf(set.Output(), "run the gowormhole TURN client\n\n")
		_, _ = fmt.Fprintf(set.Output(), "usage: %s %s\n\n", os.Args[0], args[0])
		_, _ = fmt.Fprintf(set.Output(), "flags:\n")
		set.PrintDefaults()
	}

	host := set.String("host", "127.0.0.1", "TURN Server name.")
	port := set.Int("port", gowormhole.DefaultTurnPort, "Listening port.")
	user := set.String("user", "scott:tiger", `A pair of username and password (e.g. "user:pass")`)
	realm := set.String("realm", "d5k.ly", `Realm (defaults to "d5k.ly")`)
	ping := set.Bool("ping", false, "Run ping test")
	tcp := set.Bool("tcp", false, "Run ping test")
	_ = set.Parse(args[1:])
	if len(*host) == 0 {
		log.Fatalf("'host' is required")
	}
	if len(*user) == 0 {
		log.Fatalf("'user' is required")
	}

	cred := strings.SplitN(*user, ":", 2)
	var conn net.PacketConn
	var err error

	if *tcp {
		// Dial TURN Server
		turnServerAddr := fmt.Sprintf("%s:%d", *host, *port)
		c, err := net.Dial("tcp", turnServerAddr)
		if err != nil {
			panic(err)
		}
		conn = turn.NewSTUNConn(c)
	} else {
		// TURN client won't create a local listening socket by itself.
		conn, err = net.ListenPacket("udp4", "0.0.0.0:0")
	}

	if err != nil {
		panic(err)
	}

	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			panic(closeErr)
		}
	}()

	turnServerAddr := fmt.Sprintf("%s:%d", *host, *port)

	cfg := &turn.ClientConfig{
		STUNServerAddr: turnServerAddr,
		TURNServerAddr: turnServerAddr,
		Conn:           conn,
		Username:       cred[0],
		Password:       cred[1],
		Realm:          *realm,
		LoggerFactory:  logging.NewDefaultLoggerFactory(),
	}

	client, err := turn.NewClient(cfg)
	if err != nil {
		panic(err)
	}
	defer client.Close()

	// Start listening on the conn provided.
	err = client.Listen()
	if err != nil {
		panic(err)
	}

	// Allocate a relay socket on the TURN server. On success, it
	// will return a net.PacketConn which represents the remote
	// socket.
	relayConn, err := client.Allocate()
	if err != nil {
		panic(err)
	}
	defer func() {
		if closeErr := relayConn.Close(); closeErr != nil {
			panic(closeErr)
		}
	}()

	// The relayConn's local address is actually the transport
	// address assigned on the TURN server.
	log.Printf("relayed-address=%s", relayConn.LocalAddr().String())

	// If you provided `-ping`, perform a ping test agaist the
	// relayConn we have just allocated.
	if *ping {
		err = doPingTest(client, relayConn)
		if err != nil {
			panic(err)
		}
	}
}

func doPingTest(client *turn.Client, relayConn net.PacketConn) error {
	// Send BindingRequest to learn our external IP
	mappedAddr, err := client.SendBindingRequest()
	if err != nil {
		return err
	}

	// Set up pinger socket (pingerConn)
	pingerConn, err := net.ListenPacket("udp4", "0.0.0.0:0")
	if err != nil {
		panic(err)
	}
	defer func() {
		if closeErr := pingerConn.Close(); closeErr != nil {
			panic(closeErr)
		}
	}()

	// Punch a UDP hole for the relayConn by sending a data to the mappedAddr.
	// This will trigger a TURN client to generate a permission request to the
	// TURN server. After this, packets from the IP address will be accepted by
	// the TURN server.
	_, err = relayConn.WriteTo([]byte("Hello"), mappedAddr)
	if err != nil {
		return err
	}

	// Start read-loop on pingerConn
	go func() {
		buf := make([]byte, 1600)
		for {
			n, from, pingerErr := pingerConn.ReadFrom(buf)
			if pingerErr != nil {
				break
			}

			msg := string(buf[:n])
			if sentAt, pingerErr := time.Parse(time.RFC3339Nano, msg); pingerErr == nil {
				rtt := time.Since(sentAt)
				log.Printf("%d bytes from from %s time=%d ms\n", n, from.String(), int(rtt.Seconds()*1000))
			}
		}
	}()

	// Start read-loop on relayConn
	go func() {
		buf := make([]byte, 1600)
		for {
			n, from, readerErr := relayConn.ReadFrom(buf)
			if readerErr != nil {
				break
			}

			// Echo back
			if _, readerErr = relayConn.WriteTo(buf[:n], from); readerErr != nil {
				break
			}
		}
	}()

	time.Sleep(500 * time.Millisecond)

	// Send 10 packets from relayConn to the echo server
	for i := 0; i < 10; i++ {
		msg := time.Now().Format(time.RFC3339Nano)
		_, err = pingerConn.WriteTo([]byte(msg), relayConn.LocalAddr())
		if err != nil {
			return err
		}

		// For simplicity, this example does not wait for the pong (reply).
		// Instead, sleep 1 second.
		time.Sleep(time.Second)
	}

	return nil
}
