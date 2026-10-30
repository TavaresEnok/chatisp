package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"chatisp/internal/platform/testdb"
)

func TestDevPrepare(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	env, err := testdb.Start(ctx)
	if err != nil {
		t.Fatalf("testdb start: %v", err)
	}
	t.Cleanup(env.Close)

	var stdout, stderr bytes.Buffer
	err = run(ctx, []string{"dev-prepare", "-url", env.AdminURL}, nil, &stdout, &stderr)
	if err != nil {
		t.Fatalf("dev-prepare failed: %v, stderr: %s", err, stderr.String())
	}

	out := stdout.String()
	requiredVars := []string{
		"CHATISP_APP_DATABASE_URL=",
		"CHATISP_PLATFORM_DATABASE_URL=",
		"CHATISP_INGRESS_DATABASE_URL=",
		"CHATISP_JOBS_DATABASE_URL=",
		"CHATISP_DATA_KEYS=",
		"CHATISP_DATA_KEY_VERSION=",
		"CHATISP_DEV_EMAIL=",
	}
	if strings.Contains(out, "CHATISP_DATABASE_URL=") {
		t.Fatal("dev-prepare expôs credencial owner ao runtime")
	}
	for _, v := range requiredVars {
		if !strings.Contains(out, v) {
			t.Fatalf("dev-prepare output missing %s; got:\n%s", v, out)
		}
	}
}
