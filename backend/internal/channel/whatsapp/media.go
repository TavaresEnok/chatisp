package whatsapp

import (
	"context"
	"errors"
)

// ErrMediaDisabled means this process has no fetcher for inbound media.
// It is not a network failure and it does not call a provider.
var ErrMediaDisabled = errors.New("download de mídia indisponível")

// MediaRef identifies one remote media object. The token is not a log field.
type MediaRef struct {
	MediaID       string
	TenantID      string
	ConnectorID   string
	AccessToken   string
	PhoneNumberID string
}

// MediaBlob is the provider bytes and the MIME the provider declared.
type MediaBlob struct {
	MIME string
	Body []byte
}

// MediaFetcher downloads one media object. Implementations used in tests must
// not open an external network connection.
type MediaFetcher interface {
	Fetch(ctx context.Context, ref MediaRef) (MediaBlob, error)
}

// DisabledFetcher fails closed without dialing.
type DisabledFetcher struct{}

// Fetch returns ErrMediaDisabled.
func (DisabledFetcher) Fetch(context.Context, MediaRef) (MediaBlob, error) {
	return MediaBlob{}, ErrMediaDisabled
}

// FakeFetcher serves bytes registered by media id. A missing id is an error.
type FakeFetcher struct {
	Objects map[string]MediaBlob
	Errors  map[string]error
	Calls   int
}

// Fetch returns the stand-in object or the registered error.
func (f *FakeFetcher) Fetch(_ context.Context, ref MediaRef) (MediaBlob, error) {
	if f == nil {
		return MediaBlob{}, ErrMediaDisabled
	}
	f.Calls++
	if err := f.Errors[ref.MediaID]; err != nil {
		return MediaBlob{}, err
	}
	blob, ok := f.Objects[ref.MediaID]
	if !ok {
		return MediaBlob{}, ErrMediaDisabled
	}
	out := MediaBlob{MIME: blob.MIME, Body: append([]byte(nil), blob.Body...)}
	return out, nil
}
