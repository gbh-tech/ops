package utils_test

import (
	"net"
	"testing"

	"ops/pkg/utils"
)

func TestCheckLocalPortAvailable(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port

	if err := utils.CheckLocalPortAvailable(port); err == nil {
		t.Fatal("expected error while port is bound")
	}

	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}

	if err := utils.CheckLocalPortAvailable(port); err != nil {
		t.Fatalf("expected nil after listener closed, got %v", err)
	}
}
