# consul-service-discovery

Connection manager for services using Consul and gRPC.

## Description

This package provides a wrapper for managing gRPC connections to services registered in Consul. The manager allows you to create, replace, and remove connections, ensuring proper closure of old connections when replaced.

## Features
- Manage a pool of gRPC connections by service name
- Automatic connection replacement when the target address changes
- Proper closure of old connections
- Thread safety

## Structures
- `ConnManager` — main connection manager
- `managedConn` — structure for storing connection and target information

## Usage Example

```go
cm := &ConnManager{conns: make(map[string]*managedConn)}
conn, err := grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
if err != nil {
    // handle error
}
cm.replaceConn("service-name", conn, target)
```

## Testing

To run unit tests:

```
go test
```

## License

MIT License
