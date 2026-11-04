package pipeline

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestPublishWebhookSourceRejectsEmpty(t *testing.T) {
	if _, err := PublishWebhookSource(context.Background(), nil, nil, uuid.Nil, "", uuid.Nil); err == nil {
		t.Fatal("origem vazia deveria ser recusada")
	}
}
