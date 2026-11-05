// Package attachments owns durable, conversation-scoped, private media.
package attachments

import (
	"context"
	"errors"
	"io"
	"time"
)

var (
	ErrInvalid   = errors.New("invalid attachment")
	ErrForbidden = errors.New("attachment access denied")
	ErrConflict  = errors.New("attachment state conflict")
	ErrLimit     = errors.New("attachment storage limit reached")
	// ErrPlanUnset means the provider's plan has no storage limit configured.
	// Attachments fail closed: there is no commercial default.
	ErrPlanUnset = errors.New("limite de armazenamento do plano não configurado")
	ErrStorage   = errors.New("private attachment storage unavailable")
)

// UnavailableStorage keeps attachment routes mounted when object storage is
// not configured. Every operation fails with ErrStorage and writes nothing.
func UnavailableStorage() Storage { return unavailableStorage{} }

type unavailableStorage struct{}

func (unavailableStorage) Available() bool { return false }
func (unavailableStorage) Create(context.Context, string, io.ReadSeeker, int64) (string, error) {
	return "", ErrStorage
}
func (unavailableStorage) Read(context.Context, string, string) (io.ReadCloser, error) {
	return nil, ErrStorage
}
func (unavailableStorage) Sign(context.Context, string, string, string, time.Duration) (string, error) {
	return "", ErrStorage
}
func (unavailableStorage) Delete(context.Context, string, string) error { return ErrStorage }

// Storage is private and versioned. Create MUST be create-only, never overwrite.
// Read/Sign/Delete MUST address the exact version; Delete with empty version
// removes every version of a never-published key (crash/orphan recovery).
// No implementation may return a public permanent URL or log signed URLs.
type Storage interface {
	Create(context.Context, string, io.ReadSeeker, int64) (string, error)
	Read(context.Context, string, string) (io.ReadCloser, error)
	Sign(context.Context, string, string, string, time.Duration) (string, error)
	Delete(context.Context, string, string) error
}

// ConversionRequest is a contract for the later sandboxed worker. This module
// does not execute ffmpeg or certify duration/codecs from signature inspection.
// InputPath must be a local file. Worker must disable networking/protocols and
// enforce wall time, duration, memory, CPU and output byte limits independently.
type ConversionRequest struct {
	InputPath            string
	SHA256               string
	OutputMIME           string
	MaxDuration, Timeout time.Duration
	MaxOutputBytes       int64
}
