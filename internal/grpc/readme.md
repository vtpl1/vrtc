```bash
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
cd internal/grpc
mkdir service
protoc interfaces/*.proto \
    --go_out=service \
    --go_opt=paths=source_relative \
    --go-grpc_out=service \
    --go-grpc_opt=paths=source_relative \
    --proto_path=interfaces
```