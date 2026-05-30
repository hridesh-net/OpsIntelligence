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

	// The Rust subprocess needs a real TTY for keyboard input + alt-screen
	// drawing. Inherit stdin/stdout/stderr from our process (so crossterm
	// sees the controlling terminal) and use two extra pipes for JSON-RPC:
	//
	//   fd 3 (subprocess) ← write end of "go → rust" pipe (Go writes requests here)
	//   fd 4 (subprocess) → read end  of "rust → go" pipe (Go reads responses here)
	//
	// ExtraFiles[0] becomes fd 3 in the child, ExtraFiles[1] becomes fd 4.
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	goToRustR, goToRustW, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("go→rust pipe: %w", err)
	}
	rustToGoR, rustToGoW, err := os.Pipe()
	if err != nil {
		goToRustR.Close()
		goToRustW.Close()
		return nil, fmt.Errorf("rust→go pipe: %w", err)
	}
	cmd.ExtraFiles = []*os.File{goToRustR, rustToGoW}
	// Tell the Rust binary to use the protocol fds. Crossterm will own
	// stdin/stdout once it switches to raw alt-screen mode.
	cmd.Env = append(cmd.Env, "OPSINTEL_TUI_PROTO_IN=3", "OPSINTEL_TUI_PROTO_OUT=4")

	if err := cmd.Start(); err != nil {
		goToRustR.Close()
		goToRustW.Close()
		rustToGoR.Close()
		rustToGoW.Close()
		// Print a recognizable breadcrumb to stderr so silent spawn failures
		// (e.g. cached binary missing, exec format mismatch, permission denied)
		// can be diagnosed without rerunning the command under strace.
		fmt.Fprintf(os.Stderr, "tuibridge: spawn %s failed: %v\n", binPath, err)
		return nil, fmt.Errorf("start opsintel-tui (%s): %w", binPath, err)
	}
	// The child now owns the read-end of go→rust and the write-end of rust→go.
	goToRustR.Close()
	rustToGoW.Close()

	b := &Bridge{
		cmd:    cmd,
		stdin:  goToRustW,
		stdout: bufio.NewReaderSize(rustToGoR, 64*1024),
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
			} else {
				// Surface protocol corruption instead of silently dropping —
				// matches the new Rust-side behaviour for the same error.
				fmt.Fprintf(os.Stderr, "tuibridge: rust→go parse error: %v (line: %.200s)\n", jerr, string(line))
			}
		}
		if err != nil {
			b.doneErr = err
			// If the subprocess died with a non-zero exit, the user almost
			// never sees the alt-screen panic message because crossterm tore
			// it down. Print an exit-code breadcrumb to stderr so the failure
			// is at least attributable.
			if b.cmd != nil && b.cmd.ProcessState != nil {
				if ec := b.cmd.ProcessState.ExitCode(); ec != 0 {
					fmt.Fprintf(os.Stderr, "tuibridge: opsintel-tui exited with code %d (read err: %v)\n", ec, err)
				}
			}
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

// CloseErr returns the read-loop error, except that a clean Rust EOF is
// reported as nil. Use this in view-runner loops so the CLI doesn't print
// "Error: EOF" when the user simply pressed `q` to close a TUI.
func (b *Bridge) CloseErr() error {
	err := b.doneErr
	if err == nil {
		return nil
	}
	if errors.Is(err, io.EOF) {
		return nil
	}
	return err
}
