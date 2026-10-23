package httpapi

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed swaggerui/*
var swaggerUIFS embed.FS

// swaggerMountPath is where router.go registers the docs. It is NOT /docs: the SPA
// owns that route (web/src/router.tsx -> DocsPage), and proxying /docs here made the
// SPA pages unreachable behind the edge. Keep this in step with the r.Handle calls.
const swaggerMountPath = "/api-docs"

// SwaggerUIHandler serves the self-contained Swagger UI.
func SwaggerUIHandler() http.Handler {
	sub, err := fs.Sub(swaggerUIFS, "swaggerui")
	if err != nil {
		panic(err)
	}
	// StripPrefix is REQUIRED. FileServer resolves against the embedded FS root,
	// where the files sit without the mount path, so a request for
	// /api-docs/swagger-ui.css was looked up as "api-docs/swagger-ui.css" and
	// 404'd. The page itself would have loaded and then rendered nothing.
	fileServer := http.StripPrefix(swaggerMountPath, http.FileServer(http.FS(sub)))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == swaggerMountPath || r.URL.Path == swaggerMountPath+"/" {
			serveSwaggerIndex(w, r)
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}

func serveSwaggerIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <title>VinCommerce API — Documentation</title>
  <link rel="stylesheet" href="/api-docs/swagger-ui.css">
  <link rel="icon" href="/api-docs/favicon-32x32.png">
  <style>html { box-sizing: border-box; overflow-y: scroll; } body { margin: 0; }</style>
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="/api-docs/swagger-ui-bundle.js"></script>
  <script src="/api-docs/swagger-ui-standalone-preset.js"></script>
  <script>
    window.onload = function () {
      window.ui = SwaggerUIBundle({
        url: '/api-docs/openapi.json',
        dom_id: '#swagger-ui',
        deepLinking: true,
        presets: [SwaggerUIBundle.presets.apis, SwaggerUIStandalonePreset],
        layout: 'StandaloneLayout',
        persistAuthorization: true,
      })
    }
  </script>
</body>
</html>`))
}
