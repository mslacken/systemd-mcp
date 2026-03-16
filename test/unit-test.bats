#!/usr/bin/bats
setup_file() {
  cd ${BATS_TEST_DIRNAME}/../
  go mod tidy
  go mod vendor
}

@test "Build binary" {
  cd ${BATS_TEST_DIRNAME}/../
  go build systemd-mcp.go
}

@test "Build test client" {
  cd ${BATS_TEST_DIRNAME}/../
  go build -o test-client ./testClient
}

@test "Run go unit tests" {
  cd ${BATS_TEST_DIRNAME}/../
  go test ./...
}
