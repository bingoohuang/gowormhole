package wormhole

import (
	"testing"
	"time"

	"github.com/creasty/defaults"
	"github.com/go-playground/assert/v2"
)

func TestICETimeoutsDefaults(t *testing.T) {
	var it Timeouts
	defaults.Set(&it)
	assert.Equal(t, Timeouts{
		DisconnectedTimeout: 5 * time.Second,
		FailedTimeout:       10 * time.Second,
		KeepAliveInterval:   2 * time.Second,
		CloseTimeout:        10 * time.Second,
		RwTimeout:           10 * time.Second,
	}, it)

	var wrap Wrap

	defaults.Set(&wrap)
	assert.Equal(t, it, wrap.Timeouts)
}

type Wrap struct {
	Timeouts Timeouts
}
