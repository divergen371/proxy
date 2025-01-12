package main

import (
	"bufio"
	"net"
	"net/http"
	"testing"
)

// TestHandleConnection tests the HandleConnection function by simulating various
// CONNECT requests and verifying the expected behavior.

func TestHandleConnection(t *testing.T) {
	testCases := []struct {
		name    string
		request string
		wantErr bool
	}{
		{
			"Valid HTTPS",
			"CONNECT example.com:443 HTTP/1.1\r\nHost: example.com:443\r\n\r\n",
			false,
		},
		{
			"Invalid Port",
			"CONNECT example.com:123456 HTTP/1.1\r\nHost: example.com:123456\r\n\r\n",
			true,
		},
		{
			"Invalid Host",
			"CONNECT invalid-host HTTP/1.1\r\nHost: invalid-host\r\n\r\n", true,
		},
		{"Missing Host", "CONNECT HTTP/1.1\r\n\r\n", true},
		{
			"Invalid Protocol",
			"GET example.com:443 HTTP/1.1\r\nHost: example.com:443\r\n\r\n",
			true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			clientConn, serverConn := net.Pipe()
			t.Cleanup(func() {
				clientConn.Close()
				serverConn.Close()
			})

			go HandleConnection(clientConn)

			_, err := serverConn.Write([]byte(tc.request))
			if err != nil {
				t.Fatalf("Failed to write request: %v", err)
			}

			reader := bufio.NewReader(serverConn)
			resp, err := http.ReadResponse(reader, nil)

			if tc.wantErr {
				if err == nil {
					t.Errorf("Expected an error for %s, but got nil", tc.name)
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error for %s: %v", tc.name, err)
				}
				if resp.StatusCode != http.StatusOK {
					t.Errorf("Expected status code %d for %s, but got %d",
						http.StatusOK, tc.name, resp.StatusCode)
				}
			}
		})
	}
}
