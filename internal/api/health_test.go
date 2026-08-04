package api

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"net/http/httptest"
	"sync"
	"testing"
)

var registerHealthDrivers sync.Once

type healthDriver struct{}
type healthConn struct{}
type unhealthyDriver struct{}
type unhealthyConn struct{ healthConn }

func (healthDriver) Open(string) (driver.Conn, error)    { return healthConn{}, nil }
func (healthConn) Prepare(string) (driver.Stmt, error)   { return nil, errors.New("not supported") }
func (healthConn) Close() error                          { return nil }
func (healthConn) Begin() (driver.Tx, error)             { return nil, errors.New("not supported") }
func (healthConn) Ping(context.Context) error            { return nil }
func (unhealthyDriver) Open(string) (driver.Conn, error) { return unhealthyConn{}, nil }
func (unhealthyConn) Ping(context.Context) error         { return errors.New("database unavailable") }

func TestHealthzReturnsReady(t *testing.T) {
	registerHealthDrivers.Do(func() { sql.Register("health-ok", healthDriver{}) })
	database, err := sql.Open("health-ok", "")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "/healthz", nil)
	(&Server{db: database}).handleHealthz(recorder, request)

	if recorder.Code != 200 {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestHealthzReturnsUnavailableWhenDatabasePingFails(t *testing.T) {
	sql.Register("health-fail", unhealthyDriver{})
	database, err := sql.Open("health-fail", "")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "/healthz", nil)
	(&Server{db: database}).handleHealthz(recorder, request)

	if recorder.Code != 503 {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}
