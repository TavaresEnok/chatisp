package metacloud

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Shape copied from the public reference of phone_number_quality_update
// (read 05/11/2026). It has the WABA in entry.id and no phone_number_id.
const limitEnvelope = `{"object":"whatsapp_business_account","entry":[{"id":"102290129340398","time":1752000000,"changes":[{"field":"phone_number_quality_update","value":{"display_phone_number":"15550783881","event":"THROUGHPUT_UPGRADE","current_limit":"TIER_10K","old_limit":"TIER_2K","max_daily_conversations_per_business":10000}}]}]}`

func TestParseNumberLimitKeepsOfficialFieldsWithoutPhoneID(t *testing.T) {
	ingested := time.Unix(1_800_000_000, 0).UTC()
	items, err := Parse("app-trusted", []byte(limitEnvelope), ingested)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("itens: %#v", items)
	}
	it := items[0]
	if it.Kind != ItemNumberLimit || it.AppID != "app-trusted" || it.WABAID != "102290129340398" || it.DisplayPhone != "15550783881" {
		t.Fatalf("item: %#v", it)
	}
	if it.PhoneNumberID != "" {
		t.Fatalf("phone_number_id inventado: %q", it.PhoneNumberID)
	}
	if it.Limit.Event != "THROUGHPUT_UPGRADE" || it.Limit.CurrentLimit != "TIER_10K" || it.Limit.OldLimit != "TIER_2K" || it.Limit.MaxDaily == nil || *it.Limit.MaxDaily != 10000 {
		t.Fatalf("limite: %#v", it.Limit)
	}
	if !strings.HasPrefix(it.DedupKey, "number_limit:") || it.IngestedAt != ingested {
		t.Fatalf("chave/relógio: %#v", it)
	}
	again, _ := Parse("app-trusted", []byte(limitEnvelope), ingested.Add(time.Hour))
	if again[0].DedupKey != it.DedupKey {
		t.Fatalf("repetição mudou a chave: %s %s", again[0].DedupKey, it.DedupKey)
	}
}

func TestParseNumberLimitQuarantinesUntrustedShapes(t *testing.T) {
	cases := map[string]string{
		"sem waba":        strings.Replace(limitEnvelope, `"id":"102290129340398"`, `"id":""`, 1),
		"sem número":      strings.Replace(limitEnvelope, `"display_phone_number":"15550783881"`, `"display_phone_number":""`, 1),
		"número letras":   strings.Replace(limitEnvelope, `"15550783881"`, `"1555abc3881"`, 1),
		"evento vazio":    strings.Replace(limitEnvelope, `"event":"THROUGHPUT_UPGRADE"`, `"event":""`, 1),
		"tier minúsculo":  strings.Replace(limitEnvelope, `"TIER_10K"`, `"tier_10k"`, 1),
		"diário negativo": strings.Replace(limitEnvelope, `10000}`, `-1}`, 1),
		"diário fração":   strings.Replace(limitEnvelope, `10000}`, `10.5}`, 1),
		"sem limite":      `{"object":"whatsapp_business_account","entry":[{"id":"1","changes":[{"field":"phone_number_quality_update","value":{"display_phone_number":"15550783881","event":"ONBOARDING"}}]}]}`,
	}
	for name, raw := range cases {
		items, err := Parse("app-trusted", []byte(raw), time.Unix(1, 0).UTC())
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(items) != 1 || items[0].Kind != ItemQuarantine || items[0].Reason != ReasonInvalidLimit || items[0].PhoneNumberID != "" {
			t.Fatalf("%s: %#v", name, items)
		}
	}
}

func TestDisplayDigits(t *testing.T) {
	for in, want := range map[string]string{
		"+55 (11) 99999-0001": "5511999990001",
		"15550783881":         "15550783881",
		"1234567":             "",
		"55119999900011234":   "",
		"55 11 9999 x":        "",
	} {
		if got := DisplayDigits(in); got != want {
			t.Fatalf("%q: %q, esperado %q", in, got, want)
		}
	}
}

func TestQualityRatingReadsStandInWithoutSending(t *testing.T) {
	var gotPath, gotQuery, gotMethod, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery, gotMethod, gotAuth = r.URL.Path, r.URL.RawQuery, r.Method, r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		switch r.Header.Get("X-Case") {
		default:
			_, _ = w.Write([]byte(`{"quality_rating":"GREEN","id":"111"}`))
		}
	}))
	defer srv.Close()
	d, err := Activate(Config{Mode: ModeSimulation, GraphVersion: "v23.0", BaseURL: srv.URL, PhoneNumberID: "111", AccessToken: "tok-sim"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := d.QualityRating(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Rating != "GREEN" || !got.Simulation {
		t.Fatalf("leitura: %#v", got)
	}
	if gotMethod != http.MethodGet || gotPath != "/v23.0/111" || gotQuery != "fields=quality_rating" || gotAuth != "Bearer tok-sim" {
		t.Fatalf("requisição: %s %s?%s %q", gotMethod, gotPath, gotQuery, gotAuth)
	}
}

func TestQualityRatingClassifiesGraphFailures(t *testing.T) {
	cases := []struct {
		status int
		body   string
		want   error
	}{
		{401, `{"error":{"code":190}}`, ErrQualityTokenInvalid},
		{400, `{"error":{"code":100}}`, ErrQualityRejected},
		{500, `{"error":{"code":2,"is_transient":true}}`, ErrQualityUnavailable},
		{503, `indisponível`, ErrQualityUnavailable},
		{200, `{"id":"111"}`, ErrQualityMalformed},
		{200, `{"quality_rating":"verde<script>"}`, ErrQualityMalformed},
	}
	for _, c := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(c.status)
			_, _ = w.Write([]byte(c.body))
		}))
		d, err := Activate(Config{Mode: ModeSimulation, GraphVersion: "v23.0", BaseURL: srv.URL, PhoneNumberID: "111", AccessToken: "tok-sim"})
		if err != nil {
			t.Fatal(err)
		}
		_, err = d.QualityRating(context.Background())
		srv.Close()
		if !errors.Is(err, c.want) {
			t.Fatalf("%d %s: %v, esperado %v", c.status, c.body, err, c.want)
		}
		if err != nil && strings.Contains(err.Error(), "tok-sim") {
			t.Fatalf("token vazou: %v", err)
		}
	}
}

func TestQualityRatingOfficialRequiresExplicitVersion(t *testing.T) {
	if _, err := Activate(Config{Mode: ModeOfficial, PhoneNumberID: "111", AccessToken: "tok", PrimaryDocURL: "https://developers.facebook.com/docs/whatsapp/cloud-api/reference/phone-numbers"}); err == nil {
		t.Fatal("modo oficial ativou sem versão")
	}
}
