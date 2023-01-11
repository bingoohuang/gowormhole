package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/bingoohuang/gowormhole"
	"github.com/bingoohuang/gowormhole/internal/util"
	"github.com/pion/logging"
	"github.com/pion/stun"
)

func natSubCmd(ctx context.Context, args ...string) {
	set := flag.NewFlagSet(args[0], flag.ExitOnError)
	set.Usage = func() {
		_, _ = fmt.Fprintf(set.Output(), "nat behavior discovery\n\n")
		_, _ = fmt.Fprintf(set.Output(), "usage: %s %s ...\n\n", os.Args[0], args[0])
		_, _ = fmt.Fprintf(set.Output(), "flags:\n")
		set.PrintDefaults()
	}

	stunServer := set.String("stun", "stun.voip.blackberry.com", "STUN server address")
	timeout := set.Duration("timeout", 3*time.Second, "timeout to wait for STUN server's response")
	loglevel := set.String("loglevel", "info", "logging level")
	_ = set.Parse(args[1:])

	log := logging.NewDefaultLeveledLoggerForScope("", parseLogLevel(*loglevel), os.Stdout)
	cmd := &natCmd{
		log:            log,
		timeout:        *timeout,
		stunServerAddr: util.AppendPort(*stunServer, gowormhole.DefaultStunPort),
	}

	if err := cmd.mappingTests(); err != nil {
		log.Warn("NAT mapping behavior: inconclusive")
	}
	if err := cmd.filteringTests(); err != nil {
		log.Warn("NAT filtering behavior: inconclusive")
	}
}

func parseLogLevel(level string) logging.LogLevel {
	switch strings.ToLower(level) {
	case "warn":
		return logging.LogLevelWarn
	case "info":
		return logging.LogLevelInfo
	case "debug":
		return logging.LogLevelDebug
	case "trace":
		return logging.LogLevelTrace
	default:
		return logging.LogLevelInfo
	}
}

type natCmd struct {
	log            *logging.DefaultLeveledLogger
	stunServerAddr string
	timeout        time.Duration
}

type stunServerConn struct {
	conn        net.PacketConn
	LocalAddr   net.Addr
	RemoteAddr  *net.UDPAddr
	OtherAddr   *net.UDPAddr
	messageChan chan *stun.Message

	log     *logging.DefaultLeveledLogger
	timeout time.Duration
}

func (c *stunServerConn) Close() error {
	return c.conn.Close()
}

const (
	messageHeaderSize = 20
)

var (
	errResponseMessage = errors.New("error reading from response message channel")
	errTimedOut        = errors.New("timed out waiting for response")
	errNoOtherAddress  = errors.New("no OTHER-ADDRESS in message")
)

// RFC5780: 4.3.  Determining NAT Mapping Behavior
func (n *natCmd) mappingTests() error {
	mapTestConn, err := n.connect(n.stunServerAddr)
	if err != nil {
		n.log.Warnf("Error creating STUN connection: %s", err.Error())
		return err
	}

	// Test I: Regular binding request
	n.log.Info("Mapping Test I: Regular binding request")
	request := stun.MustBuild(stun.TransactionID, stun.BindingRequest)

	resp, err := mapTestConn.roundTrip(request, mapTestConn.RemoteAddr)
	if err != nil {
		return err
	}

	// Parse response message for XOR-MAPPED-ADDRESS and make sure OTHER-ADDRESS valid
	resps1 := n.parse(resp)
	if resps1.xorAddr == nil || resps1.otherAddr == nil {
		n.log.Info("Error: NAT discovery feature not supported by this server")
		return errNoOtherAddress
	}
	addr, err := net.ResolveUDPAddr("udp4", resps1.otherAddr.String())
	if err != nil {
		n.log.Infof("Failed resolving OTHER-ADDRESS: %v", resps1.otherAddr)
		return err
	}
	mapTestConn.OtherAddr = addr
	n.log.Infof("Received XOR-MAPPED-ADDRESS: %v", resps1.xorAddr)

	// Assert mapping behavior
	if resps1.xorAddr.String() == mapTestConn.LocalAddr.String() {
		n.log.Warn("=> NAT mapping behavior: endpoint independent (no NAT)")
		return nil
	}

	// Test II: Send binding request to the other address but primary port
	n.log.Info("Mapping Test II: Send binding request to the other address but primary port")
	otherAddr := *mapTestConn.OtherAddr
	otherAddr.Port = mapTestConn.RemoteAddr.Port
	resp, err = mapTestConn.roundTrip(request, &otherAddr)
	if err != nil {
		return err
	}

	// Assert mapping behavior
	resps2 := n.parse(resp)
	n.log.Infof("Received XOR-MAPPED-ADDRESS: %v", resps2.xorAddr)
	if resps2.xorAddr.String() == resps1.xorAddr.String() {
		n.log.Warn("=> NAT mapping behavior: endpoint independent")
		return nil
	}

	// Test III: Send binding request to the other address and port
	n.log.Info("Mapping Test III: Send binding request to the other address and port")
	resp, err = mapTestConn.roundTrip(request, mapTestConn.OtherAddr)
	if err != nil {
		return err
	}

	// Assert mapping behavior
	resps3 := n.parse(resp)
	n.log.Infof("Received XOR-MAPPED-ADDRESS: %v", resps3.xorAddr)
	if resps3.xorAddr.String() == resps2.xorAddr.String() {
		n.log.Warn("=> NAT mapping behavior: address dependent")
	} else {
		n.log.Warn("=> NAT mapping behavior: address and port dependent")
	}

	return mapTestConn.Close()
}

// RFC5780: 4.4.  Determining NAT Filtering Behavior
func (n *natCmd) filteringTests() error {
	mapTestConn, err := n.connect(n.stunServerAddr)
	if err != nil {
		n.log.Warnf("Error creating STUN connection: %s", err.Error())
		return err
	}

	// Test I: Regular binding request
	n.log.Info("Filtering Test I: Regular binding request")
	request := stun.MustBuild(stun.TransactionID, stun.BindingRequest)

	resp, err := mapTestConn.roundTrip(request, mapTestConn.RemoteAddr)
	if err != nil || errors.Is(err, errTimedOut) {
		return err
	}
	resps := n.parse(resp)
	if resps.xorAddr == nil || resps.otherAddr == nil {
		n.log.Warn("Error: NAT discovery feature not supported by this server")
		return errNoOtherAddress
	}
	addr, err := net.ResolveUDPAddr("udp4", resps.otherAddr.String())
	if err != nil {
		n.log.Infof("Failed resolving OTHER-ADDRESS: %v", resps.otherAddr)
		return err
	}
	mapTestConn.OtherAddr = addr

	// Test II: Request to change both IP and port
	n.log.Info("Filtering Test II: Request to change both IP and port")
	request = stun.MustBuild(stun.TransactionID, stun.BindingRequest)
	request.Add(stun.AttrChangeRequest, []byte{0x00, 0x00, 0x00, 0x06})

	resp, err = mapTestConn.roundTrip(request, mapTestConn.RemoteAddr)
	if err == nil {
		n.parse(resp) // just to print out the resp
		n.log.Warn("=> NAT filtering behavior: endpoint independent")
		return nil
	} else if !errors.Is(err, errTimedOut) {
		return err // something else went wrong
	}

	// Test III: Request to change port only
	n.log.Info("Filtering Test III: Request to change port only")
	request = stun.MustBuild(stun.TransactionID, stun.BindingRequest)
	request.Add(stun.AttrChangeRequest, []byte{0x00, 0x00, 0x00, 0x02})

	resp, err = mapTestConn.roundTrip(request, mapTestConn.RemoteAddr)
	if err == nil {
		n.parse(resp) // just to print out the resp
		n.log.Warn("=> NAT filtering behavior: address dependent")
	} else if errors.Is(err, errTimedOut) {
		n.log.Warn("=> NAT filtering behavior: address and port dependent")
	}

	return mapTestConn.Close()
}

// Parse a STUN message
func (n *natCmd) parse(msg *stun.Message) (ret struct {
	xorAddr    *stun.XORMappedAddress
	otherAddr  *stun.OtherAddress
	respOrigin *stun.ResponseOrigin
	mappedAddr *stun.MappedAddress
	software   *stun.Software
},
) {
	ret.mappedAddr = &stun.MappedAddress{}
	ret.xorAddr = &stun.XORMappedAddress{}
	ret.respOrigin = &stun.ResponseOrigin{}
	ret.otherAddr = &stun.OtherAddress{}
	ret.software = &stun.Software{}
	if ret.xorAddr.GetFrom(msg) != nil {
		ret.xorAddr = nil
	}
	if ret.otherAddr.GetFrom(msg) != nil {
		ret.otherAddr = nil
	}
	if ret.respOrigin.GetFrom(msg) != nil {
		ret.respOrigin = nil
	}
	if ret.mappedAddr.GetFrom(msg) != nil {
		ret.mappedAddr = nil
	}
	if ret.software.GetFrom(msg) != nil {
		ret.software = nil
	}
	n.log.Debugf("%v", msg)
	n.log.Debugf("\tMAPPED-ADDRESS:     %v", ret.mappedAddr)
	n.log.Debugf("\tXOR-MAPPED-ADDRESS: %v", ret.xorAddr)
	n.log.Debugf("\tRESPONSE-ORIGIN:    %v", ret.respOrigin)
	n.log.Debugf("\tOTHER-ADDRESS:      %v", ret.otherAddr)
	n.log.Debugf("\tSOFTWARE: %v", ret.software)
	for _, attr := range msg.Attributes {
		switch attr.Type {
		case
			stun.AttrXORMappedAddress,
			stun.AttrOtherAddress,
			stun.AttrResponseOrigin,
			stun.AttrMappedAddress,
			stun.AttrSoftware:
			break
		default:
			n.log.Debugf("\t%v (l=%v)", attr, attr.Length)
		}
	}
	return ret
}

// Given an address string, returns a StunServerConn
func (n *natCmd) connect(addrStr string) (*stunServerConn, error) {
	n.log.Infof("connecting to STUN server: %s", addrStr)
	addr, err := net.ResolveUDPAddr("udp4", addrStr)
	if err != nil {
		n.log.Warnf("Error resolving address: %s", err.Error())
		return nil, err
	}

	c, err := net.ListenUDP("udp4", nil)
	if err != nil {
		return nil, err
	}
	n.log.Infof("Local address: %s", c.LocalAddr())
	n.log.Infof("Remote address: %s", addr.String())

	mChan := n.listen(c)

	return &stunServerConn{
		log:         n.log,
		timeout:     n.timeout,
		conn:        c,
		LocalAddr:   c.LocalAddr(),
		RemoteAddr:  addr,
		messageChan: mChan,
	}, nil
}

// Send request and wait for response or timeout
func (c *stunServerConn) roundTrip(msg *stun.Message, addr net.Addr) (*stun.Message, error) {
	_ = msg.NewTransactionID()
	c.log.Infof("Sending to %v: (%v bytes)", addr, msg.Length+messageHeaderSize)
	c.log.Debugf("%v", msg)
	for _, attr := range msg.Attributes {
		c.log.Debugf("\t%v (l=%v)", attr, attr.Length)
	}
	_, err := c.conn.WriteTo(msg.Raw, addr)
	if err != nil {
		c.log.Warnf("Error sending request to %v", addr)
		return nil, err
	}

	// Wait for response or timeout
	select {
	case m, ok := <-c.messageChan:
		if !ok {
			return nil, errResponseMessage
		}
		return m, nil
	case <-time.After(c.timeout):
		c.log.Infof("Timed out waiting for response from server %v", addr)
		return nil, errTimedOut
	}
}

// taken from https://github.com/pion/stun/blob/master/cmd/stun-traversal/main.go
func (n *natCmd) listen(conn *net.UDPConn) (messages chan *stun.Message) {
	messages = make(chan *stun.Message)
	log := n.log
	go func() {
		for {
			buf := make([]byte, 1024)

			n, addr, err := conn.ReadFromUDP(buf)
			if err != nil {
				close(messages)
				return
			}
			log.Infof("Response from %v: (%v bytes)", addr, n)
			buf = buf[:n]

			m := new(stun.Message)
			m.Raw = buf
			err = m.Decode()
			if err != nil {
				log.Infof("Error decoding message: %v", err)
				close(messages)
				return
			}

			messages <- m
		}
	}()
	return
}
