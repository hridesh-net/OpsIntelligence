package gateway

import (
	"testing"
)

func TestHub_MaxWebSocketClients_RejectsAtCap(t *testing.T) {
	t.Parallel()
	h := NewHub(1)
	go h.Run()

	c1 := &Client{ID: "a", Hub: h, Send: make(chan []byte, 4)}
	ok1 := make(chan bool, 1)
	h.register <- registerOp{client: c1, ok: ok1}
	if !<-ok1 {
		t.Fatal("first client should register")
	}

	c2 := &Client{ID: "b", Hub: h, Send: make(chan []byte, 4)}
	ok2 := make(chan bool, 1)
	h.register <- registerOp{client: c2, ok: ok2}
	if <-ok2 {
		t.Fatal("second client should be rejected at cap 1")
	}

	h.unregister <- c1
	c3 := &Client{ID: "c", Hub: h, Send: make(chan []byte, 4)}
	ok3 := make(chan bool, 1)
	h.register <- registerOp{client: c3, ok: ok3}
	if !<-ok3 {
		t.Fatal("after unregister, next client should register")
	}
}

func TestHub_MaxWebSocketClientsZero_Unlimited(t *testing.T) {
	t.Parallel()
	h := NewHub(0)
	go h.Run()

	for i := 0; i < 5; i++ {
		c := &Client{ID: string(rune('A' + i)), Hub: h, Send: make(chan []byte, 4)}
		ok := make(chan bool, 1)
		h.register <- registerOp{client: c, ok: ok}
		if !<-ok {
			t.Fatalf("client %d should register when cap is 0", i)
		}
	}
}
