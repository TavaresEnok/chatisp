package whatsapp

import (
	"context"
	"errors"
	"testing"
)

func TestFakeFetcherDoesNotRequireNetwork(t *testing.T) {
	fake := &FakeFetcher{
		Objects: map[string]MediaBlob{"100": {MIME: "image/jpeg", Body: []byte{0xff, 0xd8, 0xff}}},
		Errors:  map[string]error{"fail": errors.New("provedor")},
	}
	got, err := fake.Fetch(context.Background(), MediaRef{MediaID: "100"})
	if err != nil || string(got.Body) == "" || got.MIME != "image/jpeg" || fake.Calls != 1 {
		t.Fatalf("fake: %#v %v", got, err)
	}
	if _, err := fake.Fetch(context.Background(), MediaRef{MediaID: "fail"}); err == nil {
		t.Fatal("erro do fake não retornou")
	}
	if _, err := (DisabledFetcher{}).Fetch(context.Background(), MediaRef{MediaID: "100"}); !errors.Is(err, ErrMediaDisabled) {
		t.Fatal(err)
	}
}
