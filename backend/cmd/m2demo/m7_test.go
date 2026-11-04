package main

import (
	"strings"
	"testing"
)

func TestM7UsageRejectsArguments(t *testing.T) {
	err := run([]string{"m7", "extra"})
	if err == nil || !strings.Contains(err.Error(), "m2demo m7") {
		t.Fatalf("uso: %v", err)
	}
}
