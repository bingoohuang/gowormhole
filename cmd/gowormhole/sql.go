package main

import (
	"context"
	"database/sql"
	"log"
	"time"

	"github.com/bingoohuang/gowormhole/internal/util"
	_ "modernc.org/sqlite"
)

const (
	createTableRecvSQL = `
	create table if not exists gowormhole_recv(
		hash text not null, 
		size integer not null, 
		pos integer not null, 
		expired datetime, 
		updated datetime, 
		name text not null, 
		full text not null, 
		hostname text, 
		ips text, 
		whoami text, 
		cost text,
		primary key(hash)
    )`
	insertTableRecvSQL = `insert into gowormhole_recv(hash,  size, pos, expired, updated, name, full, hostname, ips, whoami) values (?, ?, ?,  ?, ?, ?, ?, ?, ?, ?)`
	updateTableRecvSQL = `update  gowormhole_recv set pos = ?, updated = ?, cost = ? where hash = ?`
	hashQuerySQL       = `select hash, size , pos, expired, updated, name, full, hostname, ips, whoami, cost from gowormhole_recv where hash = ?`
)

func newSaveN(ctx context.Context, db *sql.DB, hash string, pos uint64, bar util.ProgressBar) *SaveN {
	return &SaveN{
		ctx:       ctx,
		db:        db,
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
	db        *sql.DB
	hash      string
	StartTime time.Time
}

func (s *SaveN) Start(filename string, n uint64) {
	s.Bar.Start(filename, n)
}

func (s *SaveN) Add(n uint64) {
	if n <= 0 {
		return
	}

	s.Bar.Add(n)

	s.N += n
	if s.N > 10240 {
		s.savePosToDB()
	}
}

func (s *SaveN) Finish() {
	s.Bar.Finish()
	if s.N > 0 {
		s.savePosToDB()
	}
}

func (s *SaveN) savePosToDB() {
	s.Pos += s.N
	s.N = 0

	if err := updateRecvTable(s.ctx, s.db, s.hash, s.Pos, time.Since(s.StartTime).String()); err != nil {
		log.Printf("update recv pos failed: %v", err)
	}
}
