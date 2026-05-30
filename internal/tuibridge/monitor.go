package tuibridge

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// MonitorOptions configures RunMonitor.
type MonitorOptions struct {
	Status  DashboardStatus
	LogPath string
	LogDir  string
}

// RunMonitor launches the live monitor view. It polls the supplied log file
// for new newline-delimited JSON events and pushes them along with a status
// snapshot every second.
func RunMonitor(ctx context.Context, opts MonitorOptions) error {
	quit := make(chan struct{})
	handler := func(msg Message) {
		if msg.Method == "view.exit" {
			select {
			case <-quit:
			default:
				close(quit)
			}
		}
	}

	b, err := Spawn(ctx, Options{Handler: handler, LogDir: opts.LogDir})
	if err != nil {
		return err
	}
	defer func() { _ = b.Close(2 * time.Second) }()

	if err := b.Send("view.push", map[string]any{
		"view": "monitor",
		"monitor": map[string]any{
			"version":  opts.Status.Version,
			"log_path": opts.LogPath,
		},
	}); err != nil {
		return err
	}

	st := &monitorState{
		opts:   opts,
		events: make([]monitorEventJSON, 0, 32),
	}
	_ = b.Send("monitor.snapshot", st.snapshot())

	tk := time.NewTicker(time.Second)
	defer tk.Stop()
	for {
		select {
		case <-quit:
			return nil
		case <-b.Done():
			return b.CloseErr()
		case <-ctx.Done():
			return ctx.Err()
		case <-tk.C:
			st.refresh()
			_ = b.Send("monitor.snapshot", st.snapshot())
		}
	}
}

type monitorState struct {
	opts       MonitorOptions
	events     []monitorEventJSON
	lastOffset int64
}

type monitorEventJSON struct {
	Time      string `json:"time"`
	Iteration uint32 `json:"iteration"`
	Message   string `json:"message"`
	Tool      string `json:"tool"`
}

type logEventWire struct {
	Level     string `json:"level"`
	Timestamp string `json:"ts"`
	Message   string `json:"msg"`
	Iteration int    `json:"iteration,omitempty"`
	Tool      string `json:"tool,omitempty"`
	Chain     string `json:"chain,omitempty"`
}

func (s *monitorState) refresh() {
	if s.opts.LogPath == "" {
		return
	}
	f, err := os.Open(s.opts.LogPath)
	if err != nil {
		return
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		return
	}
	if s.lastOffset == 0 {
		// First-run: tail the last 4 KiB so the view isn't empty for static logs.
		s.lastOffset = stat.Size() - 4096
		if s.lastOffset < 0 {
			s.lastOffset = 0
		}
	}
	if stat.Size() <= s.lastOffset {
		return
	}
	if _, err := f.Seek(s.lastOffset, 0); err != nil {
		return
	}
	data := make([]byte, stat.Size()-s.lastOffset)
	n, _ := f.Read(data)
	s.lastOffset = stat.Size()
	for _, line := range strings.Split(string(data[:n]), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var ev logEventWire
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue
		}
		ts := ev.Timestamp
		if len(ts) >= 19 {
			ts = ts[11:19]
		}
		tool := ev.Tool
		if tool == "" {
			tool = ev.Chain
		}
		s.events = append(s.events, monitorEventJSON{
			Time:      ts,
			Iteration: uint32(ev.Iteration),
			Message:   ev.Message,
			Tool:      tool,
		})
	}
	if len(s.events) > 20 {
		s.events = append([]monitorEventJSON(nil), s.events[len(s.events)-20:]...)
	}
}

func (s *monitorState) snapshot() map[string]any {
	ps := fetchPS(s.opts.Status.PID)
	cpu := 0.0
	fmt.Sscanf(ps.cpu, "%f", &cpu)
	return map[string]any{
		"status": map[string]any{
			"alive":         ps.alive,
			"pid":           s.opts.Status.PID,
			"etime":         ps.etime,
			"cpu_percent":   cpu,
			"rss_mb":        float64(ps.rssKB) / 1024.0,
			"version":       s.opts.Status.Version,
			"gateway_base":  s.opts.Status.GatewayBase,
			"gateway_bind":  s.opts.Status.GatewayBind,
			"run_trace_file": s.opts.Status.RunTraceFile,
		},
		"events": s.events,
	}
}
