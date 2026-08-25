package app

import (
	"context"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mengkeat/yamdview/web"
)

// waitServeAPILine polls the captured log until the parseable startup line
// appears and returns the API base URL and bearer token from it.
func waitServeAPILine(t *testing.T, logs *syncBuffer) (baseURL, token string) {
	t.Helper()
	re := regexp.MustCompile(`api: listening on (http://\S+) bearer ([0-9a-f]+)`)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if m := re.FindStringSubmatch(logs.String()); m != nil {
			return m[1], m[2]
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("startup line with URL and bearer token not logged: %q", logs.String())
	return "", ""
}

func TestRunServeAPI(t *testing.T) {
	assets, err := web.LoadAssets()
	if err != nil {
		t.Fatal(err)
	}

	logs := &syncBuffer{}
	log.SetOutput(logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	ctx, cancel := context.WithCancel(context.Background())
	application := New(Config{
		Mode:    ModeServe,
		Addr:    "127.0.0.1:0",
		API:     true,
		Context: ctx,
	}, assets)

	type outcome struct{ err error }
	done := make(chan outcome, 1)
	go func() {
		done <- outcome{err: application.Run()}
	}()

	baseURL, token := waitServeAPILine(t, logs)
	if !strings.HasPrefix(baseURL, "http://127.0.0.1:") {
		t.Fatalf("startup URL is not loopback: %q", baseURL)
	}

	// The logged token must authenticate a real session create on the
	// listener the startup line advertised.
	req, err := http.NewRequest(http.MethodPost, baseURL+"/api/v1/sessions", strings.NewReader(`{"markdown":"# Plan\n\nHello."}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("authenticated create failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("authenticated create status = %d, want 201", resp.StatusCode)
	}

	cancel()
	select {
	case o := <-done:
		if o.err != nil {
			t.Fatalf("RunServeAPI returned error after context cancellation: %v", o.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RunServeAPI did not return after context cancellation")
	}
}
