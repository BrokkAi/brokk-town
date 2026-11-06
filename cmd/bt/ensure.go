package main

import (
	"context"
	"errors"
	"time"

	"github.com/BrokkAi/brokk-town/internal/osrun"
)

// defaultListen is the service address when nothing else was ever chosen.
const defaultListen = "127.0.0.1:8099"

// serviceAlive reports a service that is running and answering.
func serviceAlive(ctx context.Context, dir string) (connection, bool) {
	conn, err := readConnection(dir)
	if err != nil || !osrun.Alive(conn.PID) {
		return conn, false
	}
	probe, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var state struct{}
	return conn, request(probe, conn, "GET", "/api/state", nil, &state) == nil
}

// requireService finds the running service; clients never start one.
func requireService(ctx context.Context, dir string) (connection, error) {
	conn, alive := serviceAlive(ctx, dir)
	if !alive {
		return conn, errors.New("Brokk Town is not running")
	}
	return conn, nil
}
