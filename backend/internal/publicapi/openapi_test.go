package publicapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestOpenAPIMatchesCommittedDocument(t *testing.T) {
	got := OpenAPIJSON()
	path := filepath.Join("..", "..", "api", "public-openapi.json")
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("public-openapi.json divergiu (%d bytes gerados, %d no arquivo)", len(got), len(want))
	}
}

func TestOpenAPIMatchesRegisteredRoutes(t *testing.T) {
	router, ok := NewHandler(nil).(chi.Routes)
	if !ok {
		t.Fatal("o handler público não expõe as rotas")
	}
	var seen []string
	err := chi.Walk(router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if method != "" {
			seen = append(seen, method+" "+route)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := make([]string, 0, len(publicRoutes()))
	for _, route := range publicRoutes() {
		want = append(want, route.Method+" "+route.Path)
	}
	sort.Strings(seen)
	sort.Strings(want)
	if len(seen) != len(want) {
		t.Fatalf("rotas %v\ndocumento %v", seen, want)
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Fatalf("rota %s, tabela %s\n%v", seen[i], want[i], seen)
		}
	}
	if !bytes.Contains(OpenAPIJSON(), []byte(`"/v1/conversations/{ref}/template"`)) {
		t.Fatal("OpenAPI sem envio de template")
	}
}

func TestOpenAPIDocumentsPublicWrites(t *testing.T) {
	var doc struct {
		Paths map[string]map[string]struct {
			Description string `json:"description"`
			Scope       string `json:"x-required-scope"`
			RequestBody *struct {
				Content map[string]struct {
					Schema map[string]any `json:"schema"`
				} `json:"content"`
			} `json:"requestBody"`
			Responses map[string]struct {
				Content map[string]struct {
					Schema map[string]any `json:"schema"`
				} `json:"content"`
			} `json:"responses"`
		} `json:"paths"`
		Components struct {
			Schemas map[string]struct {
				Required   []string                  `json:"required"`
				Properties map[string]map[string]any `json:"properties"`
			} `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(OpenAPIJSON(), &doc); err != nil {
		t.Fatal(err)
	}
	contact := doc.Components.Schemas["ContactCreate"]
	if !containsAll(contact.Required, "name") || contact.Properties["external_ref"] == nil || contact.Properties["phone"] == nil {
		t.Fatalf("contato %+v", contact)
	}
	template := doc.Components.Schemas["TemplateSend"]
	for _, field := range []string{"template_id", "language", "client_request_id", "expected_version"} {
		if !containsAll(template.Required, field) || template.Properties[field] == nil {
			t.Fatalf("template sem %s em %+v", field, template)
		}
	}
	conversation := doc.Components.Schemas["Conversation"]
	if !containsAll(conversation.Required, "version") || conversation.Properties["version"] == nil {
		t.Fatalf("conversa %+v", conversation)
	}
	post := doc.Paths["/v1/conversations/{ref}/template"]["post"]
	if post.Scope != "templates.send" || !bytes.Contains([]byte(post.Description), []byte("templates.send")) {
		t.Fatalf("escopo do template %+v", post.Scope)
	}
	for _, code := range []string{"200", "201", "400", "401", "403", "404", "409", "422", "429", "503"} {
		if _, ok := post.Responses[code]; !ok {
			t.Fatalf("template sem resposta %s", code)
		}
	}
	if post.RequestBody == nil || post.Responses["201"].Content["application/json"].Schema["$ref"] == nil {
		t.Fatal("template sem schema de entrada ou de 201")
	}
	created := doc.Paths["/v1/contacts"]["post"]
	if _, ok := created.Responses["201"]; !ok || created.RequestBody == nil {
		t.Fatal("contato sem 201 ou corpo")
	}
	if created.RequestBody.Content["application/json"].Schema["$ref"] != "#/components/schemas/ContactCreate" {
		t.Fatalf("corpo do contato %+v", created.RequestBody.Content["application/json"].Schema)
	}
}

func containsAll(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
