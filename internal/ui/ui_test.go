package ui

import (
	"context"
	"testing"
)

func TestServeIsDeclared(t *testing.T) {
	if err := Serve(context.Background()); err == nil {
		t.Fatal("Serve should say it is not ready yet")
	}
}
