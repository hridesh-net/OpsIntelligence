package tuibridge

import "encoding/json"

const JSONRPCVersion = "2.0"

type Message struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *uint64          `json:"id,omitempty"`
	Method  string           `json:"method,omitempty"`
	Params  json.RawMessage  `json:"params,omitempty"`
	Result  json.RawMessage  `json:"result,omitempty"`
	Error   *ErrorObject     `json:"error,omitempty"`
}

type ErrorObject struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func NewNotification(method string, params any) (Message, error) {
	raw, err := marshalParams(params)
	if err != nil {
		return Message{}, err
	}
	return Message{JSONRPC: JSONRPCVersion, Method: method, Params: raw}, nil
}

func NewRequest(id uint64, method string, params any) (Message, error) {
	raw, err := marshalParams(params)
	if err != nil {
		return Message{}, err
	}
	return Message{JSONRPC: JSONRPCVersion, ID: &id, Method: method, Params: raw}, nil
}

func marshalParams(v any) (json.RawMessage, error) {
	if v == nil {
		return nil, nil
	}
	if raw, ok := v.(json.RawMessage); ok {
		return raw, nil
	}
	return json.Marshal(v)
}

// IsNotification reports whether the message is a JSON-RPC notification (no id).
func (m Message) IsNotification() bool { return m.ID == nil && m.Method != "" }

// IsRequest reports whether the message is a JSON-RPC request (id + method).
func (m Message) IsRequest() bool { return m.ID != nil && m.Method != "" }

// IsResponse reports whether the message is a JSON-RPC response (id, no method).
func (m Message) IsResponse() bool { return m.ID != nil && m.Method == "" }
