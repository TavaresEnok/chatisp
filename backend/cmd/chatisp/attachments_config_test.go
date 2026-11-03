package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"chatisp/internal/attachments"
)

func TestAttachmentStorageFailsClosed(t *testing.T) {
	storage, err := attachmentStorage(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := storage.Create(context.Background(), "key", strings.NewReader(""), 0); !errors.Is(err, attachments.ErrStorage) {
		t.Fatalf("storage vazio: %v", err)
	}
	_, err = attachmentStorage([]string{"CHATISP_ATTACHMENTS_S3_BUCKET=secret-bucket-value"})
	if err == nil || strings.Contains(err.Error(), "secret-bucket-value") || !strings.Contains(err.Error(), "CHATISP_ATTACHMENTS_S3_ENDPOINT") {
		t.Fatalf("configuração parcial: %v", err)
	}
	_, err = attachmentStorage([]string{
		"CHATISP_ATTACHMENTS_S3_ENDPOINT=http://insecure.example",
		"CHATISP_ATTACHMENTS_S3_REGION=us-east-1",
		"CHATISP_ATTACHMENTS_S3_BUCKET=private",
		"CHATISP_ATTACHMENTS_S3_ACCESS_KEY=key",
		"CHATISP_ATTACHMENTS_S3_SECRET_KEY=secret-value",
	})
	if err == nil || strings.Contains(err.Error(), "secret-value") {
		t.Fatalf("configuração rejeitada vazou segredo: %v", err)
	}
}
