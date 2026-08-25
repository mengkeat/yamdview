// Package mcp implements the Model Context Protocol (MCP) stdio server for
// yamdview (`yamdview mcp`). It exposes three tools over newline-delimited
// JSON-RPC 2.0 on stdin/stdout: present_markdown, await_feedback, and
// request_review, all backed by an in-process agentapi session manager.
//
// # Recorded decision: hand-rolled JSON-RPC subset
//
// The required MCP surface over stdio (initialize, notifications/initialized,
// ping, tools/list, tools/call) is implemented as a minimal hand-rolled
// JSON-RPC 2.0 subset on the standard library rather than by vendoring the
// official modelcontextprotocol/go-sdk: the surface is small and the
// project's dependency policy favors stdlib-only implementations
// (PLAN.md §3.3, §22.7).
//
// The server owns a full agentapi HTTP server bound to loopback
// (127.0.0.1:0 by default): it hosts the per-session viewer pages and doubles
// as an optional HTTP escape hatch for agents. Its bearer token stays a
// process secret: stdout is protocol-only, so the token is exposed via
// Server.Token for stderr logging, never written to stdout.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"

	"github.com/mengkeat/yamdview/internal/agentapi"
	"github.com/mengkeat/yamdview/internal/server"
)

// ProtocolVersion is the MCP protocol version this server speaks, reported
// in the initialize result regardless of the client's requested version.
const ProtocolVersion = "2024-11-05"

// ServerVersion is the yamdview version reported in initialize serverInfo.
// There is no release stamping yet, so development builds report "dev".
const ServerVersion = "dev"

// Server is the MCP stdio server: newline-delimited JSON-RPC 2.0 on the
// injected reader/writer pair, backed by an in-process agentapi server that
// hosts the review sessions.
type Server struct {
	api     *agentapi.Server
	manager *agentapi.Manager
	in      io.Reader
	out     *bufio.Writer
	enc     *json.Encoder
}

// New builds an MCP server that reads protocol messages from in and writes
// responses to out. The underlying agentapi server listens on addr (empty
// means 127.0.0.1:0). Loopback enforcement is the CLI layer's job, mirroring
// the serve mode rules.
func New(assets server.Assets, addr string, in io.Reader, out io.Writer) (*Server, error) {
	if in == nil {
		in = os.Stdin
	}
	if out == nil {
		out = os.Stdout
	}
	api, err := agentapi.New(assets, addr)
	if err != nil {
		return nil, fmt.Errorf("create session api: %w", err)
	}
	writer := bufio.NewWriter(out)
	enc := json.NewEncoder(writer)
	enc.SetEscapeHTML(false)
	return &Server{
		api:     api,
		manager: api.Manager(),
		in:      in,
		out:     writer,
		enc:     enc,
	}, nil
}

// Token returns the underlying agent API bearer token. It is a process
// secret: log it to stderr only, never to stdout.
func (s *Server) Token() string { return s.api.Token() }

// URL returns the http:// base URL of the underlying agent API listener.
func (s *Server) URL() string { return s.api.URL() }

// Manager returns the shared session manager backing the tools.
func (s *Server) Manager() *agentapi.Manager { return s.manager }

// Serve starts the underlying API server and processes protocol lines until
// stdin reaches EOF, ctx is cancelled, or a response write fails. EOF and
// cancellation are clean shutdowns (nil error). The API server is closed on
// return, which also cancels any still-open sessions.
func (s *Server) Serve(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	s.api.Start()
	defer s.api.Close()

	// readResult carries one input line or the reader's terminal error; a
	// single channel keeps ordering deterministic. The buffer is large
	// enough for the reader goroutine to deposit its final results and exit
	// even after Serve has already returned via ctx cancellation.
	type readResult struct {
		line []byte
		err  error
	}
	reads := make(chan readResult, 2)
	go func() {
		reader := bufio.NewReader(s.in)
		for {
			line, err := reader.ReadBytes('\n')
			if len(line) > 0 {
				reads <- readResult{line: line}
			}
			if err != nil {
				reads <- readResult{err: err}
				return
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return nil
		case res := <-reads:
			if len(res.line) > 0 {
				if err := s.handleLine(ctx, res.line); err != nil {
					return err
				}
				continue
			}
			if errors.Is(res.err, io.EOF) {
				return nil
			}
			return fmt.Errorf("read stdin: %w", res.err)
		}
	}
}

// logf logs to the standard logger (stderr); stdout stays protocol-only.
func (s *Server) logf(format string, args ...any) {
	log.Printf("mcp: "+format, args...)
}
