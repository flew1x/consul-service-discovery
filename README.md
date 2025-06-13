# consul-service-discovery

Connection manager for services using Consul and gRPC.

## Description

This package provides a wrapper for managing gRPC connections to services registered with Consul. The manager allows you to create, replace, and delete connections, ensuring that old connections are closed correctly when they are replaced. Thread safety is supported.

## Features
- Managing a pool of gRPC connections by service name
- Automatic connection replacement when the target service address is changed
- Correct closing of old connections
- Thread safety
- Flexible configuration through functional options
- Integration with structured logging (Zap)
- Monitoring the status of services through the Consul Health Catalog

## Basic structures
- `ConnManager' — the main number of connections
- `managedConn` — structures for storing connections and target address information

## Quick start

### Installation

```sh
go get github.com/your-org/consul-service-discovery
```

### Usage example

```go
package main

import (
    "context"
    "log"

    "github.com/hashicorp/consul/api"
    "go.uber.org/zap"
    "github.com/your-org/consul-service-discovery"
)

func main() {
// Creating a Consul client
    client, err := api.NewClient(api.DefaultConfig())
    if err != nil {
        log.Fatalf("failed to create consul client: %v", err)
    }

    // Creating a connection manager for the "users" and "billing" services
    mgr, err := consulservicediscovery.New(
        client,
        []string{"users", "billing"},
        consulservicediscovery.WithLogger(zap.L()),
    )
    if err != nil {
        log.Fatalf("failed to create conn manager: %v", err)
    }

    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()

    // Getting a gRPC connection for the "users" service
    conn := mgr.GetConn("users")
    if conn == nil {
        log.Println("no connection available for users service")
        return
    }

    // Use conn to create gRPC clients...
}
```

## Testing

To run the unit tests, run:

```sh
go test
```

## License

MIT License

---
