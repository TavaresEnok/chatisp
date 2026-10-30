package httpapi

import (
	"slices"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
)

func TestOpenAPIRequestSchemas(t *testing.T) {
	api := humachi.New(chi.NewRouter(), huma.DefaultConfig("t", "1"))
	Register(api, Dependencies{})
	oa := api.OpenAPI()
	schema := func(p string, post bool) *huma.Schema {
		item := oa.Paths[p]
		op := item.Put
		if post {
			op = item.Post
		}
		s := op.RequestBody.Content["application/json"].Schema
		return oa.Components.Schemas.SchemaFromRef(s.Ref)
	}
	c := schema("/api/panel/contacts", true)
	if !slices.Contains(c.Required, "phone") || slices.Contains(c.Required, "email") || slices.Contains(c.Required, "notes") {
		t.Fatalf("create required=%v", c.Required)
	}
	u := schema("/api/panel/contacts/{contact_id}", false)
	if !slices.Contains(u.Required, "name") || slices.Contains(u.Required, "phone") || slices.Contains(u.Required, "email") || slices.Contains(u.Required, "notes") {
		t.Fatalf("update required=%v", u.Required)
	}
	b := schema("/api/panel/contacts/{contact_id}/block", false)
	if !slices.Contains(b.Required, "blocked") || !slices.Contains(b.Required, "reason") || b.Properties["blocked"].Nullable || b.Properties["blocked"].Type != "boolean" {
		t.Fatalf("block schema=%v %v", b.Required, b.Properties["blocked"])
	}
}
