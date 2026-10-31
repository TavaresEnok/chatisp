package httpapi

import "github.com/google/uuid"

type assignmentInput struct {
	ConversationInput
	RawBody []byte
}
type assignmentBody struct {
	ExpectedVersion int64     `json:"expected_version"`
	ClientRequestID string    `json:"client_request_id"`
	TargetID        uuid.UUID `json:"target_id,omitempty"`
}
type candidatesOutput struct {
	Body struct {
		Candidates any `json:"candidates"`
	}
}
type capacityInput struct {
	Credentials
	MembershipID uuid.UUID `path:"membership_id"`
	RawBody      []byte
}
type capacityBody struct {
	Capacity int `json:"capacity"`
}
type capacityOutput struct {
	Body struct {
		Capacity int `json:"capacity"`
	}
}
