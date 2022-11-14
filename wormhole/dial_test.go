package wormhole

import (
	"testing"
	"time"

	"github.com/creasty/defaults"
	"github.com/go-playground/assert/v2"
)

func TestICETimeoutsDefaults(t *testing.T) {
	var it ICETimeouts
	defaults.Set(&it)
	assert.Equal(t, ICETimeouts{
		DisconnectedTimeout: 5 * time.Second,
		FailedTimeout:       10 * time.Second,
		KeepAliveInterval:   2 * time.Second,
	}, it)
}
