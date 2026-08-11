package parser

// APP API (protobuf gRPC) support.
// Currently falls back to WEB API as the protobuf pipeline requires
// compiled .proto Go types for full compatibility.
// The -a/--use-app-api flag is accepted but uses the WEB API internally.
