# Minimal Go Agent

A complete, ~200-line agent you can run and extend.

```go
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Message represents a conversation turn.
type Message struct {
	Role    string `json:"role"`    // user, assistant, tool
	Content string `json:"content"`
}

// Tool is something the model can invoke.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Schema      json.RawMessage `json:"parameters"`
	Run         func(args map[string]any) (string, error)
}

// Agent runs the observe-plan-act-reflect loop.
type Agent struct {
	Tools     map[string]Tool
	Messages  []Message
	MaxIter   int
	ToolCall  func(name string, args map[string]any) (string, error) // mockable
}

func NewAgent() *Agent {
	return &Agent{
		Tools:    make(map[string]Tool),
		Messages: make([]Message, 0),
		MaxIter:  10,
	}
}

func (a *Agent) Register(t Tool) {
	a.Tools[t.Name] = t
}

func (a *Agent) Run(ctx context.Context, prompt string) (string, error) {
	a.Messages = append(a.Messages, Message{Role: "user", Content: prompt})

	for i := 0; i < a.MaxIter; i++ {
		// In real code: call LLM API with a.Messages and tool schemas
		// Here we simulate the model deciding to call a tool or answer.
		reply := a.fakeLLM()

		if reply.Role == "assistant" && !strings.Contains(reply.Content, "TOOL_CALL:") {
			return reply.Content, nil // final answer
		}

		// Parse tool call: "TOOL_CALL: calculator{\"a\":1,\"b\":2}"
		name, args, err := parseToolCall(reply.Content)
		if err != nil {
			return "", err
		}

		tool, ok := a.Tools[name]
		if !ok {
			return "", fmt.Errorf("unknown tool: %s", name)
		}

		result, err := tool.Run(args)
		if err != nil {
			result = fmt.Sprintf("error: %v", err)
		}

		a.Messages = append(a.Messages,
			Message{Role: "assistant", Content: reply.Content},
			Message{Role: "tool", Content: result},
		)
	}

	return "", fmt.Errorf("max iterations reached")
}

func (a *Agent) fakeLLM() Message {
	// Simulation: if last user message asks for math, call calculator.
	last := a.Messages[len(a.Messages)-1]
	if strings.Contains(last.Content, "1 + 2") {
		return Message{Role: "assistant", Content: `TOOL_CALL: calculator{"a":1,"b":2}`}
	}
	return Message{Role: "assistant", Content: "Done."}
}

func parseToolCall(s string) (string, map[string]any, error) {
	// Very naive parser for demo purposes.
	prefix := "TOOL_CALL: "
	if !strings.HasPrefix(s, prefix) {
		return "", nil, fmt.Errorf("not a tool call")
	}
	rest := strings.TrimPrefix(s, prefix)
	// Find JSON braces
	idx := strings.Index(rest, "{")
	if idx == -1 {
		return rest, nil, nil // no args
	}
	name := rest[:idx]
	var args map[string]any
	if err := json.Unmarshal([]byte(rest[idx:]), &args); err != nil {
		return "", nil, err
	}
	return name, args, nil
}

func main() {
	agent := NewAgent()

	agent.Register(Tool{
		Name:        "calculator",
		Description: "Add two numbers",
		Run: func(args map[string]any) (string, error) {
			a, _ := args["a"].(float64)
			b, _ := args["b"].(float64)
			return fmt.Sprintf("%.0f", a+b), nil
		},
	})

	reader := bufio.NewReader(os.Stdin)
	fmt.Print("Ask me anything: ")
	prompt, _ := reader.ReadString('\n')
	prompt = strings.TrimSpace(prompt)

	result, err := agent.Run(context.Background(), prompt)
	if err != nil {
		fmt.Println("Error:", err)
		return
	}
	fmt.Println("Result:", result)
}
```

## What to extend

1. **Replace `fakeLLM()`** with a real HTTP call to OpenAI, Anthropic, or Ollama.
2. **Add tool schemas** — serialize `Tool.Schema` into the system prompt or API request.
3. **Add memory** — persist `Messages` to SQLite between sessions.
4. **Add planning** — before the loop, ask the model to generate a numbered plan.
5. **Add reflection** — after each tool call, ask "Did this help? Should I revise the plan?"
