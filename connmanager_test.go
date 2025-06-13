package consul_service_discovery_test

import (
	"sync"
	"testing"

	"google.golang.org/grpc"
)

type mockConn struct {
	grpc.ClientConn
	closed bool
}

func (m *mockConn) Close() error {
	m.closed = true
	return nil
}

type managedConn struct {
	target string
	conn   *mockConn
}

type ConnManager struct {
	mu    sync.Mutex
	conns map[string]*managedConn
}

func (cm *ConnManager) replaceConn(service string, conn *mockConn, target string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if existing, ok := cm.conns[service]; ok && existing.target == target {
		if conn != nil {
			_ = conn.Close()
		}

		return
	}

	if old, ok := cm.conns[service]; ok {
		_ = old.conn.Close()
	}

	if conn != nil {
		cm.conns[service] = &managedConn{target: target, conn: conn}
	} else {
		delete(cm.conns, service)
	}
}

func TestReplaceConn_NewConn(t *testing.T) {
	cm := &ConnManager{conns: make(map[string]*managedConn)}
	conn := &mockConn{}
	cm.replaceConn("svc", conn, "target1")

	if cm.conns["svc"].conn != conn {
		t.Error("connection was not set")
	}
}

func TestReplaceConn_SameTarget(t *testing.T) {
	cm := &ConnManager{conns: make(map[string]*managedConn)}
	oldConn := &mockConn{}
	cm.conns["svc"] = &managedConn{target: "target1", conn: oldConn}
	newConn := &mockConn{}

	cm.replaceConn("svc", newConn, "target1")

	if !newConn.closed {
		t.Error("new connection should be closed if target is the same")
	}

	if cm.conns["svc"].conn != oldConn {
		t.Error("old connection should remain if target is the same")
	}
}

func TestReplaceConn_ReplaceTarget(t *testing.T) {
	cm := &ConnManager{conns: make(map[string]*managedConn)}
	oldConn := &mockConn{}
	cm.conns["svc"] = &managedConn{target: "target1", conn: oldConn}
	newConn := &mockConn{}

	cm.replaceConn("svc", newConn, "target2")

	if !oldConn.closed {
		t.Error("old connection should be closed when replaced")
	}

	if cm.conns["svc"].conn != newConn {
		t.Error("new connection should be set")
	}
}

func TestReplaceConn_DeleteConn(t *testing.T) {
	cm := &ConnManager{conns: make(map[string]*managedConn)}
	oldConn := &mockConn{}

	cm.conns["svc"] = &managedConn{target: "target1", conn: oldConn}
	cm.replaceConn("svc", nil, "target2")

	if _, ok := cm.conns["svc"]; ok {
		t.Error("connection should be deleted if new conn is nil")
	}

	if !oldConn.closed {
		t.Error("old connection should be closed when deleted")
	}
}
