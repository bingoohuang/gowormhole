package main

import (
	"fmt"
	"strconv"
	"sync"

	"github.com/bingoohuang/gowormhole/internal/util"
	"github.com/bingoohuang/gowormhole/wormhole"
	"nhooyr.io/websocket"
)

type slotError struct {
	SlotKey     string
	CloseCode   websocket.StatusCode
	CloseReason string
	Err         error
}

func NewSlotError(slotKey string, closeCode websocket.StatusCode, closeReason string, err error) *slotError {
	return &slotError{
		SlotKey:     slotKey,
		CloseCode:   closeCode,
		CloseReason: closeReason,
		Err:         err,
	}
}

func (s slotError) Error() string {
	return fmt.Sprintf("SlotKey: %s, CloseCode: %d, closeReason: %s, error: %v", s.SlotKey, s.CloseCode, s.CloseReason, s.Err)
}

var _ error = (*slotError)(nil)

type SlotItem struct {
	SlotKey string
	C       chan *websocket.Conn
	Mode    wormhole.SlotItemMode
}

type Slots struct {
	m    map[string]*SlotItem
	lock sync.RWMutex
}

func (r *Slots) Delete(slotKey string) {
	r.lock.Lock()
	defer r.lock.Unlock()

	delete(r.m, slotKey)
	slotsGuage.Set(float64(len(r.m)))
}

// slots is a map of allocated slot numbers.
var slots = Slots{m: make(map[string]*SlotItem)}

func (r *Slots) Reserve() (*SlotItem, error) {
	slotKey, _ := r.free()
	if slotKey == "" {
		rendezvousCounter.WithLabelValues("nomoreslots").Inc()
		return nil, fmt.Errorf("no more slots available")
	}

	item := &SlotItem{
		SlotKey: slotKey,
		C:       make(chan *websocket.Conn),
		Mode:    wormhole.ModeNone,
	}

	r.m[slotKey] = item
	return item, nil
}

func (r *Slots) Setup(slotKey string) (*SlotItem, error) {
	r.lock.Lock()
	defer r.lock.Unlock()

	item, exists := r.m[slotKey]
	if !exists {
		if slotKey == "" {
			if slotKey, _ = r.free(); slotKey == "" {
				rendezvousCounter.WithLabelValues("nomoreslots").Inc()
				return nil, fmt.Errorf("no more slots available")
			}
		}

		item = &SlotItem{
			SlotKey: slotKey,
			C:       make(chan *websocket.Conn),
			Mode:    wormhole.ModePeer1,
		}

		r.m[slotKey] = item
		slotsGuage.Set(float64(len(r.m)))
		rendezvousCounter.WithLabelValues("nosuchslot").Inc()
		return item, nil
	}

	if item.Mode == wormhole.ModeNone {
		item.Mode = wormhole.ModePeer1
		return item, nil
	} else if item.Mode == wormhole.ModePeer1 {
		item.Mode = wormhole.ModePeer2
		delete(r.m, slotKey)
		slotsGuage.Set(float64(len(r.m)))
	}

	return item, nil
}

// free tries to find an available numeric slot, favouring smaller numbers.
// This assumes slots is locked.
func (r *Slots) free() (slot string, ok bool) {
	// Assuming varint encoding, we first t for one byte. That's 7 bits in varint.
	tries := []struct {
		tryTimes int
		bits     int
	}{
		{64, 7},    // try for one byte. That's 7 bits in varint.
		{1024, 11}, // try for two bytes. 11 bits.
		{2048, 16}, // try for three bytes. 16 bits.
		{2048, 21}, // try for four bytes. 21 bits.
	}
	for _, t := range tries {
		for i := 0; i < t.tryTimes; i++ {
			s := strconv.Itoa(util.RandIntn(1 << t.bits))
			if _, ok := r.m[s]; !ok {
				return s, true
			}
		}
	}

	// Give up.
	return "", false
}
