package web_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk/web"
)

func TestSSEHeartbeat(t *testing.T) {
	ch := make(chan web.SSEEvent)
	srv := httptest.NewServer(web.NewSSEStream(ch, web.WithHeartbeat(50*time.Millisecond)))
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	buf := make([]byte, 256)
	deadline := time.Now().Add(2 * time.Second)
	var got strings.Builder
	for time.Now().Before(deadline) {
		n, err := resp.Body.Read(buf)
		got.Write(buf[:n])
		if strings.Contains(got.String(), ": ping") {
			t.Log("ping received")
			return
		}
		if err == io.EOF {
			break
		}
	}
	t.Fatalf("no ping within deadline; got %q", got.String())
}
