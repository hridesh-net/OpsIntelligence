// Package tuibridge spawns the Rust ratatui frontend and exchanges
// newline-delimited JSON-RPC messages with it over stdio.
//
// The Rust binary is embedded at compile time via go:embed (see embed.go)
// and extracted to a per-hash directory under the user cache on first use.
// A development override path may be supplied via SetDevBinary or the
// OPSINTEL_TUI_DEV_BINARY environment variable.
package tuibridge

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"
)

var (
	devBinaryMu sync.RWMutex
	devBinary   string
)

// SetDevBinary overrides the embedded Rust binary with the given filesystem
// path for the remainder of the process lifetime. Pass "" to clear.
func SetDevBinary(path string) {
	devBinaryMu.Lock()
	devBinary = path
	devBinaryMu.Unlock()
}

func currentDevBinary() string {
	devBinaryMu.RLock()
	defer devBinaryMu.RUnlock()
	if devBinary != "" {
		return devBinary
	}
	return os.Getenv("OPSINTEL_TUI_DEV_BINARY")
}

// Handler is invoked for every inbound message from the Rust TUI. It receives
// the raw Message so it can dispatch on method/id as needed. The handler may
// be called concurrently with calls to Send/Request and runs on the bridge's
// internal reader goroutine — keep it fast or hand work off to a channel.
type Handler func(Message)

// Bridge is a running connection to the Rust TUI subprocess.
type Bridge struct {
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	stdout     *bufio.Reader
	writeMu    sync.Mutex
	nextID     atomic.Uint64
	pending    sync.Map // map[uint64]chan Message
	handler    atomic.Pointer[Handler]
	doneCh     chan struct{}
	doneErr    error
	doneOnce   sync.Once
	extraArgs  []string
}

// Options configures a Bridge launch.
type Options struct {
	// Args are extra command-line arguments to pass to the Rust binary.
	Args []string
	// Env overrides specific environment variables (in KEY=VALUE form).
	// They are appended to os.Environ.
	Env []string
	// Handler receives every inbound message. Required.
	Handler Handler
	// LogDir, if non-empty, is exposed to the Rust child as
	// OPSINTEL_TUI_LOG_DIR for in-child logging without polluting stdout.
	LogDir string
}

// Spawn launches the embedded Rust TUI and returns a Bridge ready for use.
// The caller is responsible for invoking Close (or Wait) to clean up.
func Spawn(ctx context.Context, opts Options) (*Bridge, error) {
	if opts.Handler == nil {
		return nil, errors.New("tuibridge: Handler is required")
	}
	binPath, err := resolveBinary(currentDevBinary())
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, binPath, opts.Args...)
	cmd.Env = append(os.Environ(), opts.Env...)
	if opts.LogDir != "" {
		cmd.Env = append(cmd.Env, "OPSINTEL_TUI_LOG_DIR="+opts.LogDir)
	}
	// Inherit the controlling terminal: Rust draws to stderr, reads keys
	// from /dev/tty directly via crossterm. stdin/stdout are reserved for
	// JSON-RPC.
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start opsintel-tui (%s): %w", binPath, err)
	}

	b := &Bridge{
		cmd:    cmd,
		stdin:  stdin,
		stdout: bufio.NewReaderSize(stdout, 64*1024),
		doneCh: make(chan struct{}),
	}
	h := opts.Handler
	b.handler.Store(&h)
	go b.readLoop()
	return b, nil
}

// SetHandler swaps the inbound message handler at runtime.
func (b *Bridge) SetHandler(h Handler) {
	if h == nil {
		return
	}
	b.handler.Store(&h)
}

// Send dispatches a JSON-RPC notification (no response expected).
func (b *Bridge) Send(method string, params any) error {
	msg, err := NewNotification(method, params)
	if err != nil {
		return err
	}
	return b.writeMessage(msg)
}

// Request dispatches a JSON-RPC request and waits for the matching response,
// or until ctx is cancelled.
func (b *Bridge) Request(ctx context.Context, method string, params any) (Message, error) {
	id := b.nextID.Add(1)
	msg, err := NewRequest(id, method, params)
	if err != nil {
		return Message{}, err
	}
	ch := make(chan Message, 1)
	b.pending.Store(id, ch)
	defer b.pending.Delete(id)

	if err := b.writeMessage(msg); err != nil {
		return Message{}, err
	}
	select {
	case resp := <-ch:
		if resp.Error != nil {
			return resp, fmt.Errorf("rpc error %d: %s", resp.Error.Code, resp.Error.Message)
		}
		return resp, nil
	case <-ctx.Done():
		return Message{}, ctx.Err()
	case <-b.doneCh:
		return Message{}, fmt.Errorf("bridge closed before response: %w", b.doneErr)
	}
}

func (b *Bridge) writeMessage(msg Message) error {
	if msg.JSONRPC == "" {
		msg.JSONRPC = JSONRPCVersion
	}
	raw, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	b.writeMu.Lock()
	defer b.writeMu.Unlock()
	if _, err := b.stdin.Write(append(raw, '\n')); err != nil {
		return err
	}
	return nil
}

func (b *Bridge) readLoop() {
	defer b.markDone(nil)
	for {
		line, err := b.stdout.ReadBytes('\n')
		if len(line) > 0 {
			var msg Message
			if jerr := json.Unmarshal(line, &msg); jerr == nil {
				b.dispatch(msg)
			}
		}
		if err != nil {
			b.doneErr = err
			return
		}
	}
}

func (b *Bridge) dispatch(msg Message) {
	if msg.IsResponse() {
		if ch, ok := b.pending.Load(*msg.ID); ok {
			select {
			case ch.(chan Message) <- msg:
			default:
			}
			return
		}
	}
	if h := b.handler.Load(); h != nil {
		(*h)(msg)
	}
}

func (b *Bridge) markDone(err error) {
	b.doneOnce.Do(func() {
		if err != nil && b.doneErr == nil {
			b.doneErr = err
		}
		close(b.doneCh)
	})
}

// Wait blocks until the Rust subprocess exits.
func (b *Bridge) Wait() error {
	<-b.doneCh
	if err := b.cmd.Wait(); err != nil {
		return err
	}
	return nil
}

// Close requests a graceful shutdown of the Rust TUI. If it does not exit
// within the timeout, the process is killed.
func (b *Bridge) Close(timeout time.Duration) error {
	_ = b.Send("exit", nil)
	_ = b.stdin.Close()

	exited := make(chan error, 1)
	go func() { exited <- b.cmd.Wait() }()
	select {
	case err := <-exited:
		b.markDone(err)
		return err
	case <-time.After(timeout):
		_ = b.cmd.Process.Kill()
		err := <-exited
		b.markDone(err)
		return fmt.Errorf("opsintel-tui did not exit within %s: %w", timeout, err)
	}
}

// Done returns a channel closed when the bridge's read loop has finished.
func (b *Bridge) Done() <-chan struct{} { return b.doneCh }

// Err returns any error encountered by the read loop (after Done is closed).
func (b *Bridge) Err() error { return b.doneErr }
