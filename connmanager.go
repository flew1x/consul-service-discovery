// Package consulservicediscovery implements gRPC connection management backed by
// HashiCorp Consul service discovery.
//
// # Overview
//
//   - Watches the health catalogue for a set of services.
//   - Establishes/updates *grpc.ClientConn for each healthy service instance.
//   - Exposes GetConn for fast, read-only access, safe for concurrent use.
//   - Uses functional-options for configuration and structured Zap logging.
//
// Example:
//
//	client, _ := api.NewClient(api.DefaultConfig())
//	mgr, _ := consulservicediscovery.New(
//		client,
//		[]string{"users", "billing"},
//		consulservicediscovery.WithLogger(zap.L()),
//	)
//
//	ctx, cancel := context.WithCancel(context.Background())
//	defer cancel()
//
//	go mgr.Start(ctx)
//	conn, _ := mgr.GetConn("users")
//	defer mgr.Stop()

package consul_service_discovery

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"sync"
	"time"

	"github.com/hashicorp/consul/api"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	addrTemplate = "%s:%d" // target address format
)

// ErrConnNotFound is returned when no connection exists for a requested service
var ErrConnNotFound = errors.New("grpc_connection_not_found")

// Option configures a ConnManager.
type Option func(*ConnManager) error

// WithLogger injects a structured zap.Logger. Defaults to a no-op logger.
func WithLogger(l *zap.Logger) Option {
	return func(cm *ConnManager) error {
		if l == nil {
			return errors.New("nil logger")
		}

		cm.logger = l

		return nil
	}
}

// WithRefreshInterval sets the maximum period between Consul blocking queries
// (lower values == faster reaction, higher == less load). Default: 30 s
func WithRefreshInterval(d time.Duration) Option {
	return func(cm *ConnManager) error {
		if d <= 0 {
			return errors.New("interval_must_be_positive")
		}

		cm.refreshInterval = d

		return nil
	}
}

// WithDialOptions appends extra grpc.DialOptions
func WithDialOptions(opts ...grpc.DialOption) Option {
	return func(cm *ConnManager) error {
		cm.dialOpts = append(cm.dialOpts, opts...)

		return nil
	}
}

// ConnManager maintains gRPC client connections discovered via Consul
type ConnManager struct {
	client    *api.Client
	watchList []string

	// conns
	mu    sync.RWMutex
	conns map[string]*managedConn

	logger          *zap.Logger
	dialOpts        []grpc.DialOption
	refreshInterval time.Duration
}

// managedConn couples a connection with its target address for quick comparison
type managedConn struct {
	target string
	conn   *grpc.ClientConn
}

// New creates a ConnManager watching the given services. It never mutates the
// supplied Consul client; call Start to begin discovery
func New(client *api.Client, services []string, opts ...Option) (*ConnManager, error) {
	if client == nil {
		return nil, errors.New("nil_consul_client")
	}

	if len(services) == 0 {
		return nil, errors.New("empty_service_list")
	}

	cm := &ConnManager{
		client:          client,
		watchList:       append([]string(nil), services...),
		conns:           make(map[string]*managedConn),
		logger:          zap.NewNop(),
		refreshInterval: 30 * time.Second,
		dialOpts:        []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())},
	}

	for _, opt := range opts {
		if err := opt(cm); err != nil {
			return nil, err
		}
	}

	return cm, nil
}

// Start launches background discovery until ctx is canceled
//
// The implementation issues long-poll (blocking) queries to Consul's /health
// endpoint. Each response includes X-Consul-Index; we pass that index back as
// WaitIndex to achieve efficient, server-side blocking queries
func (cm *ConnManager) Start(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)

	// Ensure that connections close when ctx is done.
	go func() {
		<-ctx.Done()
		cm.CloseAll()
		cancel()
	}()

	for _, svc := range cm.watchList {
		go cm.watchService(ctx, svc)
	}
}

// Stop cancels discovery and closes all active gRPC connections
func (cm *ConnManager) Stop() { cm.CloseAll() }

// CloseAll is idempotent and threadsafe
func (cm *ConnManager) CloseAll() {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	for name, mc := range cm.conns {
		if err := mc.conn.Close(); err != nil {
			cm.logger.Warn("close conn", zap.String("service", name), zap.Error(err))
		}
	}

	cm.conns = make(map[string]*managedConn)
}

// GetConn returns a live *grpc.ClientConn for the requested service
// Callers should not Close the returned connection
func (cm *ConnManager) GetConn(service string) (*grpc.ClientConn, error) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	mc, ok := cm.conns[service]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrConnNotFound, service)
	}

	return mc.conn, nil
}

// watchService performs a Consul blocking query loop for a single service
func (cm *ConnManager) watchService(ctx context.Context, service string) {
	var waitIdx uint64

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		q := &api.QueryOptions{
			WaitTime:   cm.refreshInterval,
			WaitIndex:  waitIdx,
			AllowStale: false,
		}

		entries, meta, err := cm.client.Health().Service(service, "", true, q)
		if err != nil {
			cm.logger.Warn("consul query error", zap.String("service", service), zap.Error(err))
			time.Sleep(backoff(cm.refreshInterval))

			continue
		}

		// meta.LastIndex updates only when the result set changes
		waitIdx = meta.LastIndex
		if len(entries) == 0 {
			cm.logger.Warn("no healthy instances", zap.String("service", service))
			cm.replaceConn(service, nil, "")

			continue
		}

		selected := entries[rand.Intn(len(entries))]
		addr := selected.Service.Address

		if addr == "" {
			addr = selected.Node.Address
		}

		if _, err := net.LookupHost(addr); err != nil {
			cm.logger.Warn("unresolvable host", zap.String("service", service), zap.String("addr", addr), zap.Error(err))

			continue
		}

		target := fmt.Sprintf(addrTemplate, addr, selected.Service.Port)

		conn, err := grpc.NewClient(target, cm.dialOpts...)
		if err != nil {
			cm.logger.Warn("dial failed", zap.String("service", service), zap.String("target", target), zap.Error(err))

			continue
		}

		cm.replaceConn(service, conn, target)
	}
}

// replaceConn swaps an existing connection atomically
func (cm *ConnManager) replaceConn(service string, conn *grpc.ClientConn, target string) {
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

// backoff returns jittered sleep duration on failures
func backoff(base time.Duration) time.Duration {
	delta := base / 2

	return base + time.Duration(rand.Int63n(int64(delta)))
}
