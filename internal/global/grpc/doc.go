// Package hubgrpc implements the gRPC service definitions and transport layer
// for hub-spoke communication in OptiPilot's multi-cluster orchestrator.
//
// Instead of requiring a protoc toolchain, the message types are defined as
// plain Go structs with JSON tags (usable with grpc-gateway or direct encoding).
// The service contracts are expressed as Go interfaces that are implemented by
// the hub server and consumed by the spoke client.
package hubgrpc
