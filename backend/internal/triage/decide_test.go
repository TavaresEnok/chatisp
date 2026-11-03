package triage

import (
	"context"
	"errors"
	"testing"

	"chatisp/internal/businesshours"
	"github.com/google/uuid"
)

func TestUnambiguousNumberedChoice(t *testing.T) {
	sector := uuid.New()
	options := []Option{
		{Key: "1", Label: "Suporte", Destination: DestSector, SectorID: &sector},
		{Key: "0", Label: "Atendente", Destination: DestHuman},
	}
	if err := Validate(2, options); err != nil {
		t.Fatal(err)
	}
	letters := []Option{
		{Key: "A", Label: "Suporte", Destination: DestSector, SectorID: &sector},
		{Key: "B", Label: "Atendente", Destination: DestHuman},
	}
	if err := Validate(2, letters); !errors.Is(err, ErrInvalid) {
		t.Fatalf("alfabético %v", err)
	}
	copied := []Option{options[0], {Key: "0", Label: "1 - Suporte", Destination: DestHuman}}
	if err := Validate(2, copied); !errors.Is(err, ErrInvalid) {
		t.Fatalf("linha copiada %v", err)
	}

	fallback := numbered(options)
	if fallback != "1 - Suporte\n0 - Atendente" {
		t.Fatalf("numeração %q", fallback)
	}
	action := menuAction(options)
	if action.Kind != KindSendMenu || action.Text != "Responda com o número da opção." || action.Fallback != fallback {
		t.Fatalf("menu %+v", action)
	}

	for _, text := range []string{"1", "1 - Suporte", "1 - suporte", "Suporte", "1-Suporte", "1.Suporte", "1)Suporte", "1:Suporte", "1 Suporte"} {
		opt, ok := matchOption(text, options)
		if !ok || opt.Key != "1" || opt.Destination != DestSector {
			t.Fatalf("setor %q %+v %v", text, opt, ok)
		}
		if requestsHuman(text, options) {
			t.Fatalf("humano indevido %q", text)
		}
	}
	for _, text := range []string{"0", "0 - Atendente", "Atendente", "falar com atendente"} {
		if !requestsHuman(text, options) {
			t.Fatalf("humano %q", text)
		}
	}
	for _, text := range []string{"1 não sei", "0 não quero atendente", "10", "1 suporte extra", "A"} {
		if opt, ok := matchOption(text, options); ok {
			t.Fatalf("casou %q %+v", text, opt)
		}
		if requestsHuman(text, options) {
			t.Fatalf("humano %q", text)
		}
	}

	def := uuid.New()
	hours := businesshours.Decision{Open: true, DefaultSectorID: &def}
	menu := Menu{Version: 1, Configured: true, MaxAttempts: 2, Options: options}
	shown := stateRow{Phase: PhaseMenu}
	out, next, err := branch(context.Background(), nil, Input{Text: "1 não sei"}, hours, menu, shown, "P-1")
	if err != nil || next.Attempts != 1 || out.Phase != PhaseMenu || hasKind(out, KindRouteSector) || hasKind(out, KindForwardHuman) {
		t.Fatalf("imprevisto %+v %v %+v", out, err, next)
	}
	out, next, err = branch(context.Background(), nil, Input{Text: "0 não quero atendente"}, hours, menu, shown, "P-1")
	if err != nil || next.Attempts != 1 || out.Phase != PhaseMenu || hasKind(out, KindForwardHuman) || hasKind(out, KindRouteSector) {
		t.Fatalf("não humano %+v %v %+v", out, err, next)
	}
	limited := menu
	limited.MaxAttempts = 1
	out, next, err = branch(context.Background(), nil, Input{Text: "1 não sei"}, hours, limited, shown, "P-1")
	if err != nil || next.Attempts != 1 || out.Phase != PhaseExhausted || !hasKind(out, KindForwardHuman) || !hasKind(out, KindRouteSector) {
		t.Fatalf("limite %+v %v %+v", out, err, next)
	}
	for _, action := range out.Actions {
		if action.Kind == KindRouteSector && (action.SectorID == nil || *action.SectorID != def) {
			t.Fatalf("destino %+v", action)
		}
	}

	ten := uuid.New()
	wide := []Option{
		{Key: "1", Label: "Um", Destination: DestSector, SectorID: &sector},
		{Key: "10", Label: "Dez", Destination: DestSector, SectorID: &ten},
		{Key: "0", Label: "Atendente", Destination: DestHuman},
	}
	if err := Validate(2, wide); err != nil {
		t.Fatal(err)
	}
	if numbered(wide) != "1 - Um\n10 - Dez\n0 - Atendente" {
		t.Fatalf("numeração %q", numbered(wide))
	}
	opt, ok := matchOption("10", wide)
	if !ok || opt.Key != "10" {
		t.Fatalf("10 %+v %v", opt, ok)
	}
	opt, ok = matchOption("1", wide)
	if !ok || opt.Key != "1" {
		t.Fatalf("1 %+v %v", opt, ok)
	}

	added := ensureHuman([]Option{{Key: "0", Label: "Zero", Destination: DestSector, SectorID: &sector}})
	if len(added) != 2 || added[1].Destination != DestHuman || added[1].Key != "1" || numbered(added) != "0 - Zero\n1 - Falar com atendente" {
		t.Fatalf("saída humana %+v", added)
	}
}

func hasKind(out Outcome, kind string) bool {
	for _, action := range out.Actions {
		if action.Kind == kind {
			return true
		}
	}
	return false
}
