package utils

import (
	"fmt"
	"net"
	"os"

	"charm.land/log/v2"
)

// CheckLocalPortAvailable verifies that nothing is listening on 127.0.0.1:port for TCP.
// It binds briefly and closes; if bind fails, the port is assumed in use or unavailable.
func CheckLocalPortAvailable(port int) error {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("local port %d is already in use", port)
	}
	return ln.Close()
}

func RequiredFileExists(file string) {
	if _, err := os.Stat(file); os.IsNotExist(err) {
		log.Fatal("A required file for this command does not exist.", "file", file)
	}
}
