package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"harbor/internal/docs"
)

func TestRoutesMatchContract(t *testing.T) {
	s := New(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	var spec struct {
		Paths map[string]map[string]struct {
			Roles     []string                   `json:"x-roles"`
			Responses map[string]json.RawMessage `json:"responses"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(docs.OpenAPI, &spec); err != nil {
		t.Fatal(err)
	}
	for _, route := range s.Routes {
		op, ok := spec.Paths[route.Path][strings.ToLower(route.Method)]
		if !ok {
			t.Fatalf("undocumented route %s %s", route.Method, route.Path)
		}
		if strings.Join(op.Roles, ",") != strings.Join(route.Roles, ",") {
			t.Fatalf("roles differ: %s %s", route.Method, route.Path)
		}
		if len(op.Responses) == 0 {
			t.Fatal("missing responses")
		}
	}
	for path, ops := range spec.Paths {
		if strings.HasPrefix(path, "/docs") {
			continue
		}
		for method := range ops {
			found := false
			for _, r := range s.Routes {
				if r.Path == path && strings.EqualFold(r.Method, method) {
					found = true
				}
			}
			if !found {
				t.Fatalf("documented route missing implementation: %s %s", method, path)
			}
		}
	}
}
func TestPublicDocsAndErrors(t *testing.T) {
	s := New(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	for _, path := range []string{"/docs", "/docs/", "/docs/openapi.yaml", "/docs/ui/swagger-ui-bundle.js", "/docs/ui/swagger-ui.css"} {
		r := httptest.NewRecorder()
		s.ServeHTTP(r, httptest.NewRequest("GET", path, nil))
		if r.Code != 200 || r.Body.Len() == 0 {
			t.Fatalf("docs failed: %s %d", path, r.Code)
		}
	}
	for _, tc := range []struct {
		method, path, body, content string
		want                        int
	}{{"GET", "/api/v1/products", "", "", 401}, {"GET", "/unknown", "", "", 404}, {"POST", "/api/v1/auth/login", "{", "application/json", 400}, {"POST", "/api/v1/auth/login", "{} {}", "application/json", 400}, {"POST", "/api/v1/auth/login", "{}", "text/plain", 415}, {"POST", "/api/v1/auth/login", `{"unexpected":true}`, "application/json", 400}} {
		r := httptest.NewRecorder()
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		req.Header.Set("Content-Type", tc.content)
		s.ServeHTTP(r, req)
		var envelope struct {
			Error struct{ Code, Message string }
		}
		if err := json.Unmarshal(r.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		if r.Code != tc.want || envelope.Error.Code == "" || envelope.Error.Message == "" {
			t.Fatal(r.Code, r.Body.String())
		}
	}
}
