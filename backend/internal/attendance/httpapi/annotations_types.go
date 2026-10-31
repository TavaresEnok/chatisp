package httpapi

import (
	"chatisp/internal/attendance/store"
	"github.com/google/uuid"
)

type annotationInput struct {
	ConversationInput
	RawBody []byte
}
type annotationCatalogInput struct {
	Credentials
	RawBody []byte
}
type annotationTagInput struct {
	Credentials
	TagID   uuid.UUID `path:"tag_id"`
	RawBody []byte
}
type annotationTagBody struct {
	Name string `json:"name"`
}
type annotationChangeBody struct {
	Kind      string    `json:"kind"`
	SubjectID uuid.UUID `json:"subject_id"`
	Value     string    `json:"value"`
	RequestID string    `json:"request_id"`
}
type annotationReadBody struct {
	Watermark   int64  `json:"watermark"`
	Unread      bool   `json:"unread"`
	ReadVersion *int64 `json:"read_version"`
}
type annotationStateOutput struct{ Body store.AnnotationState }
type annotationTagsOutput struct{ Body []store.AnnotationTag }
type annotationTagOutput struct{ Body store.AnnotationTag }
type annotationVersionOutput struct {
	Body struct {
		Version int64 `json:"version"`
	}
}
type annotationOKOutput struct {
	Body struct {
		OK bool `json:"ok"`
	}
}
