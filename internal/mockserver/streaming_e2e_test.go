package mockserver_test

import (
	"bufio"
	"crypto/tls"
	"net"
	nethttp "net/http"
	"testing"
	"time"

	"golang.org/x/net/http2"

	"livecharge/loadtest/internal/config"
	"livecharge/loadtest/internal/mockserver"
)

func TestMock_StreamingResponse_ChunksArriveOverTime(t *testing.T) {
	cfg := config.MockConfig{
		Transport: config.TransportConfig{
			Type:  "http",
			URL:   "127.0.0.1:0",
			HTTP2: &config.HTTP2Config{},
		},
		Endpoints: []config.MockEndpointConfig{{
			Path: "/", Method: "GET", OkResponse: "AAABBBCCC",
			Stream: &config.StreamConfig{Chunks: 3, DelayMs: 30},
		}},
	}
	srv, err := mockserver.NewMockServer(cfg)
	if err != nil {
		t.Fatalf("NewMockServer: %v", err)
	}
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	client := &nethttp.Client{Transport: &http2.Transport{
		AllowHTTP: true,
		DialTLS: func(network, addr string, _ *tls.Config) (net.Conn, error) {
			return net.Dial(network, addr)
		},
	}, Timeout: 2 * time.Second}

	start := time.Now()
	resp, err := client.Get("http://" + srv.Addr() + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	reader := bufio.NewReader(resp.Body)
	var arrivals []time.Time
	buf := make([]byte, 1)
	for {
		_, err := reader.Read(buf)
		if err != nil {
			break
		}
		arrivals = append(arrivals, time.Now())
	}

	if len(arrivals) < 9 {
		t.Fatalf("got %d bytes, want 9", len(arrivals))
	}
	elapsed := time.Since(start)
	if elapsed < 50*time.Millisecond {
		t.Errorf("transfer too fast (%v) — chunks may not be honoured", elapsed)
	}
}
