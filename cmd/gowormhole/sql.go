package main

import (
	"context"
	"time"

	"github.com/bingoohuang/gowormhole/internal/util"
)

func newSaveN(ctx context.Context, hash string, pos uint64, bar util.ProgressBar) *SaveN {
	return &SaveN{
		ctx:       ctx,
		hash:      hash,
		Pos:       pos,
		Bar:       bar,
		StartTime: time.Now(),
	}
}

type SaveN struct {
	Pos uint64
	N   uint64
	Bar util.ProgressBar

	ctx       context.Context
	hash      string
	StartTime time.Time
}

func (s *SaveN) Start(filename string, n uint64) {
	s.Bar.Start(filename, n)
}

func (s *SaveN) Add(n uint64) {
	if n > 0 {
		s.Bar.Add(n)
		s.N += n
	}
}

func (s *SaveN) Finish() {
	s.Bar.Finish()
}
