package triage_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"chatisp/internal/sectors"
	"chatisp/internal/triage"
	"github.com/google/uuid"
)

func TestMenuLimitsAndEmptyDecision(t *testing.T) {
	sector := uuid.New()
	human := triage.Option{Key: "0", Label: "Atendente", Destination: triage.DestHuman}
	support := triage.Option{Key: "1", Label: "Suporte", Destination: triage.DestSector, SectorID: &sector}
	valid := []triage.Option{support, human}
	if err := triage.Validate(2, valid); err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("á", 81)
	many := make([]triage.Option, 0, 13)
	for i := 0; i < 12; i++ {
		many = append(many, triage.Option{Key: string(rune('a' + i)), Label: "Opção " + string(rune('A'+i)), Destination: triage.DestHuman})
	}
	cases := [][]triage.Option{
		nil,
		{support},
		{human, {Key: "1", Label: "Suporte", Destination: triage.DestSector}},
		{human, {Key: "1", Label: "Suporte", Destination: triage.DestSector, SectorID: &sector, Reason: "financeiro"}, {Key: "1", Label: "Outro", Destination: triage.DestHuman}},
		{support, {Key: "0", Label: "Atendente", Destination: triage.DestHuman, Reason: "nao"}},
		{support, {Key: "0", Label: "Atendente", Destination: triage.DestHuman, SectorID: &sector}},
		{support, {Key: "00 1", Label: "Ruim", Destination: triage.DestHuman}},
		{support, {Key: "2", Label: " " + "Ruim", Destination: triage.DestHuman}},
		{support, {Key: "2", Label: long, Destination: triage.DestHuman}},
		{support, {Key: "2", Label: "Nulo\x00", Destination: triage.DestHuman}},
		append(many, triage.Option{Key: "z", Label: "Mais", Destination: triage.DestHuman}),
		{{Key: "1", Label: "0", Destination: triage.DestSector, SectorID: &sector}, human},
		{{Key: "1", Label: "humano", Destination: triage.DestSector, SectorID: &sector}, human},
		{{Key: "1", Label: "Falar com atendente", Destination: triage.DestSector, SectorID: &sector}, human},
		{{Key: "1", Label: "Suporte", Destination: triage.DestSector, SectorID: &sector}, {Key: "0", Label: "1", Destination: triage.DestHuman}},
		{support, {Key: "A", Label: "Letras", Destination: triage.DestHuman}},
		{support, {Key: "B", Label: "Outra", Destination: triage.DestHuman}},
		{{Key: "A", Label: "Suporte", Destination: triage.DestSector, SectorID: &sector}, {Key: "B", Label: "Atendente", Destination: triage.DestHuman}},
		{support, {Key: "0", Label: "1 - Suporte", Destination: triage.DestHuman}},
		{support, {Key: "0", Label: "1-Suporte", Destination: triage.DestHuman}},
	}
	for i, options := range cases {
		if err := triage.Validate(2, options); !errors.Is(err, triage.ErrInvalid) {
			t.Fatalf("case %d: %v", i, err)
		}
	}
	if err := triage.Validate(0, valid); !errors.Is(err, triage.ErrInvalid) {
		t.Fatal(err)
	}
	if err := triage.Validate(6, valid); !errors.Is(err, triage.ErrInvalid) {
		t.Fatal(err)
	}
	if err := triage.Validate(2, []triage.Option{support, {Key: "0", Label: "humano", Destination: triage.DestHuman}}); err != nil {
		t.Fatal(err)
	}
	_, err := triage.Service{}.Put(context.Background(), triage.Principal{Role: "admin"}, uuid.New(), triage.Command{MaxAttempts: 9})
	if !errors.Is(err, triage.ErrInvalid) {
		t.Fatal(err)
	}
	_, err = triage.Service{}.Put(context.Background(), triage.Principal{Role: "agent"}, uuid.New(), triage.Command{RequestID: uuid.New(), MaxAttempts: 2, Options: valid})
	if !errors.Is(err, sectors.ErrForbidden) {
		t.Fatal(err)
	}
	_, err = triage.Decide(context.Background(), nil, triage.Input{})
	if !errors.Is(err, triage.ErrInvalid) {
		t.Fatal(err)
	}
}
