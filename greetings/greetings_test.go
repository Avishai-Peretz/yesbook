package greetings

import "testing"

func TestHello(t *testing.T) {
	got, err := Hello("Avi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "Hi, Avi. Welcome!"
	if got != want {
		t.Errorf("Hello(\"Avi\") = %q, want %q", got, want)
	}
}

func TestHelloEmpty(t *testing.T) {
	if _, err := Hello(""); err == nil {
		t.Error("expected an error for empty name")
	}
}
