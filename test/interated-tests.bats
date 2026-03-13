#!/usr/bin/bats

setup_file() {
  cd ${BATS_TEST_DIRNAME}
  docker build -t systemd-mcp-leap16 -f leap16.docker .
}

@test "Run systemd-mcp --noauth without parameter fails" {
  run docker run systemd-mcp-leap16 --noauth
  [ "$status" -ne 0 ]
}

