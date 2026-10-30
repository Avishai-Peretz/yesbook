package main

import (
	"context"
	"strings"
	"testing"
)

func TestRunRejectsUsage(t *testing.T) {
	err := run(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "usage:") {
		t.Fatal(err)
	}
}
