package main

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestJSONUnmarshal(t *testing.T) {
	j := `{"timeouts": {"disconnectedTimeout": "5s", "failedTimeout": "10s", "keepAliveInterval": "2s"}}`
	var arg sendFileArg
	err := json.Unmarshal([]byte(j), &arg)
	assert.Nil(t, err)

	jj, err := json.Marshal(arg)
	assert.Nil(t, err)
	t.Log(string(jj))
}
