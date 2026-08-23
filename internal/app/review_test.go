package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mengkeat/yamdview/internal/feedback"
	"github.com/mengkeat/yamdview/web"
)

func waitReviewURL(t *testing.T, application *App) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if url := application.ReviewURL(); url != "" {
			return url
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("review server did not start")
	return ""
}

func TestReviewStdinSubmitEmitsFrozenPayload(t *testing.T) {
	assets, err := web.LoadAssets()
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	application := New(Config{
		Mode:         ModeReview,
		MarkdownPath: "-",
		Addr:         "127.0.0.1:0",
		NoOpen:       true,
		Input:        strings.NewReader("# Frozen\n\nOriginal text.\n"),
		Output:       &output,
		Review: ReviewConfig{
			Title: "Plan", Prompt: "Review this", Choices: []string{"approve"}, Format: feedback.FormatJSON,
		},
	}, assets)

	result := make(chan struct {
		status ReviewExitStatus
		err    error
	}, 1)
	go func() {
		status, err := application.RunReview()
		result <- struct {
			status ReviewExitStatus
			err    error
		}{status, err}
	}()

	url := waitReviewURL(t, application)
	review := application.ReviewSession()
	if review == nil || string(review.Source) != "# Frozen\n\nOriginal text.\n" {
		t.Fatalf("review did not retain stdin snapshot: %#v", review)
	}
	page, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	pageBody, _ := io.ReadAll(page.Body)
	page.Body.Close()
	if !strings.Contains(string(pageBody), "Review session") || !strings.Contains(string(pageBody), "Frozen") {
		t.Fatalf("review banner or document missing: %s", pageBody)
	}

	req, err := http.NewRequest(http.MethodPost, url+"api/session/submit", strings.NewReader(`{"verdict":"approve","summary":"Looks good"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Yamdview-Token", review.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("submit status = %d", resp.StatusCode)
	}

	outcome := <-result
	if outcome.err != nil || outcome.status != ReviewSubmitted {
		t.Fatalf("review outcome = %d, %v", outcome.status, outcome.err)
	}
	var payload feedback.Payload
	if err := json.Unmarshal(output.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.SessionID == "" || payload.Verdict != "approve" || payload.Summary != "Looks good" {
		t.Fatalf("unexpected payload: %+v", payload)
	}
}

func TestReviewTimeoutEmitsPayloadToOutputPath(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "doc.md")
	outputPath := filepath.Join(dir, "feedback.json")
	if err := os.WriteFile(source, []byte("# Timeout\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	application := New(Config{
		Mode: ModeReview, MarkdownPath: source, Addr: "127.0.0.1:0", NoOpen: true,
		Review: ReviewConfig{Timeout: 10 * time.Millisecond, Output: outputPath, Format: feedback.FormatJSON},
	}, testAssets)
	status, err := application.RunReview()
	if err != nil || status != ReviewTimeout {
		t.Fatalf("review outcome = %d, %v", status, err)
	}
	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := feedback.DecodeJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	if payload.Verdict != "timeout" {
		t.Fatalf("timeout payload verdict = %q, want timeout", payload.Verdict)
	}
}

func TestReviewCancellationAndStdinNeverWatch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var output bytes.Buffer
	application := New(Config{
		Mode: ModeReview, MarkdownPath: "-", Addr: "127.0.0.1:0", NoOpen: true,
		Input: strings.NewReader("# stdin\n"), Output: &output, Context: ctx,
		Review: ReviewConfig{Watch: true, Format: feedback.FormatMarkdown},
	}, testAssets)
	result := make(chan ReviewExitStatus, 1)
	go func() {
		status, err := application.RunReview()
		if err != nil {
			t.Errorf("RunReview: %v", err)
		}
		result <- status
	}()
	waitReviewURL(t, application)
	cancel()
	if status := <-result; status != ReviewCancelled {
		t.Fatalf("status = %d, want cancelled", status)
	}
	if !strings.Contains(output.String(), "Review feedback") {
		t.Fatalf("missing markdown feedback: %s", output.String())
	}
}
