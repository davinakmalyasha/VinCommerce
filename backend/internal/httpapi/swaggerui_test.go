package httpapi

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// The docs were moved from /docs to /api-docs (the SPA owns /docs) and the move was
// only half done: the handler still special-cased /docs, it had no StripPrefix so
// every asset 404'd, and the HTML pointed at /docs. The API documentation was
// unreachable in every deployment, and only an e2e spec noticed.
//
// These are behavioural tests over the real handler, so they fail for any of the
// three ways that break the page again.
func TestSwaggerIndexServesAtItsMountPath(t *testing.T) {
	h := SwaggerUIHandler()

	for _, path := range []string{swaggerMountPath, swaggerMountPath + "/"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))

		if rec.Code != 200 {
			t.Fatalf("GET %s = %d, want 200: the index must be served at the mount "+
				"path, not at /docs", path, rec.Code)
		}
		body := rec.Body.String()
		if !strings.Contains(body, "swagger-ui") {
			t.Fatalf("GET %s does not look like the Swagger index", path)
		}

		// Every reference must sit under the mount path. A page that loads and then
		// renders nothing is exactly the state this shipped in.
		for _, ref := range []string{"swagger-ui.css", "swagger-ui-bundle.js", "openapi.json"} {
			want := swaggerMountPath + "/" + ref
			if !strings.Contains(body, want) {
				t.Fatalf("GET %s: index does not reference %q; assets and spec must "+
					"resolve at the same prefix the handler is mounted at", path, want)
			}
		}
	}
}

func TestSwaggerAssetsResolveUnderTheMountPath(t *testing.T) {
	h := SwaggerUIHandler()

	for _, asset := range []string{"swagger-ui.css", "swagger-ui-bundle.js"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", swaggerMountPath+"/"+asset, nil))

		if rec.Code != 200 {
			t.Fatalf("GET %s/%s = %d, want 200: FileServer resolves against the "+
				"embedded FS root, so the mount prefix must be stripped",
				swaggerMountPath, asset, rec.Code)
		}
	}
}
