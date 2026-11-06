package catalog

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers/legacy"
)

// TestOpenAPIContract fails when real responses drift from ../../openapi.yaml.
func TestOpenAPIContract(t *testing.T) {
	spec, err := openapi3.NewLoader().LoadFromFile("../../openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err := spec.Validate(context.Background()); err != nil {
		t.Fatal(err)
	}
	router, err := legacy.NewRouter(spec)
	if err != nil {
		t.Fatal(err)
	}
	second := strings.Replace(card, `"example"`, `"second"`, 1)
	third := strings.Replace(strings.Replace(card, `"example"`, `"third"`, 1), `Sony - PlayStation`, `Sega - Saturn`, 1)
	catalog, packages := fixture(t, "["+card+","+second+","+third+"]", map[string][]zipEntry{
		"example": {{name: "game.txt", data: "a"}}, "second": {{name: "game.txt", data: "b"}}, "third": {{name: "game.txt", data: "c"}},
	})
	h, err := New(catalog, packages)
	if err != nil {
		t.Fatal(err)
	}
	ids := func(body []byte) string {
		var cards []struct{ ID string }
		if err := json.Unmarshal(body, &cards); err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, c := range cards {
			out = append(out, c.ID)
		}
		return strings.Join(out, ",")
	}
	for _, tc := range []struct {
		target string
		status int
		ids    string // checked for /api/games 200 only
	}{
		{"/api/games", 200, "example,second,third"},
		{"/api/games?platform=Sega+-+Saturn", 200, "third"},
		{"/api/games?platform=nope", 200, ""},
		{"/api/games?skip=1", 200, "second,third"},
		{"/api/games?skip=1&count=1", 200, "second"},
		{"/api/games?count=0", 200, ""},
		{"/api/games?platform=Sony+-+PlayStation&skip=1&count=5", 200, "second"},
		{"/api/games?skip=-1", 400, ""},
		{"/api/games?count=x", 400, ""},
		{"/api/packages/example.zip", 200, ""},
		{"/assets/cores/.index-dirs", 200, ""},
		{"/assets/cores/Sega%20-%20Saturn/.index", 200, ""},
	} {
		t.Run(tc.target, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, spec.Servers[0].URL+tc.target, nil)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != tc.status {
				t.Fatalf("status %d, body %s", w.Code, w.Body.String())
			}
			if strings.HasPrefix(tc.target, "/api/games") && tc.status == 200 {
				if got := ids(w.Body.Bytes()); got != tc.ids {
					t.Fatalf("ids %q, want %q", got, tc.ids)
				}
			}
			route, params, err := router.FindRoute(req)
			if err != nil {
				t.Fatal(err)
			}
			err = openapi3filter.ValidateResponse(context.Background(), &openapi3filter.ResponseValidationInput{
				RequestValidationInput: &openapi3filter.RequestValidationInput{
					Request: req, PathParams: params, Route: route,
					Options: &openapi3filter.Options{AuthenticationFunc: openapi3filter.NoopAuthenticationFunc},
				},
				Status: w.Code, Header: w.Header(), Body: io.NopCloser(bytes.NewReader(w.Body.Bytes())),
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
