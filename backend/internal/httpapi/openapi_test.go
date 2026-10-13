package httpapi

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strings"
	"testing"
)

var (
	documentedPathPattern   = regexp.MustCompile(`^  (/[^\s:]+):\s*$`)
	documentedMethodPattern = regexp.MustCompile(`^    (get|post|put|delete|patch|head|options):\s*$`)
)

// documentedRoutes 从内嵌的 OpenAPI 文档里抽出 (方法, 路径) 集合。
// 只要文档保持"路径键缩进两格、方法键缩进四格"的写法，这个解析就够用，
// 而且不需要为了测试引入 YAML 依赖。
func documentedRoutes(t *testing.T) map[string]string {
	t.Helper()
	routes := map[string]string{}
	current := ""
	for _, line := range strings.Split(string(openAPIDocument), "\n") {
		if match := documentedPathPattern.FindStringSubmatch(line); match != nil {
			current = match[1]
			continue
		}
		if match := documentedMethodPattern.FindStringSubmatch(line); match != nil && current != "" {
			routes[strings.ToUpper(match[1])+" "+current] = current
		}
	}
	if len(routes) == 0 {
		t.Fatal("no routes parsed out of the OpenAPI document; the layout probably changed")
	}
	return routes
}

// routeTable 是路由的唯一来源，因此"文档与代码是否一致"只需要和它比一次。
func TestOpenAPIDocumentsEveryRoute(t *testing.T) {
	table := map[string]bool{}
	for _, item := range routeTable(routeHandlers{security: &Security{}}) {
		table[item.Method+" "+item.Path] = true
	}
	documented := documentedRoutes(t)
	for key := range table {
		if _, ok := documented[key]; !ok {
			t.Errorf("route %s is registered but missing from openapi.yaml", key)
		}
	}
	for key := range documented {
		if !table[key] {
			t.Errorf("openapi.yaml documents %s but no handler is registered for it", key)
		}
	}
}

// 每条路由都必须真的注册在 mux 上：未注册的路径会落到兜底处理器返回 404，
// 而受保护的路由在缺令牌时返回 401，两者可以直接区分。
func TestEveryRouteIsRegisteredOnTheMux(t *testing.T) {
	handler := newHandler(nil, "test-only-secret", newUploadStore(nil, ""))
	for _, item := range routeTable(routeHandlers{security: &Security{}}) {
		path := strings.ReplaceAll(item.Path, "{id}", "1")
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(item.Method, path, nil))
		if recorder.Code == http.StatusNotFound {
			t.Errorf("%s %s fell through to the 404 handler: it is not registered", item.Method, item.Path)
		}
		if !item.Public && recorder.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without a token = %d, want 401", item.Method, item.Path, recorder.Code)
		}
	}
}

// 受保护路由必须都带上 bearer 校验，公开路由则必须不带。
func TestRouteTableMarksOnlyLoginHealthAndDocumentAsPublic(t *testing.T) {
	public := make([]string, 0, 4)
	for _, item := range routeTable(routeHandlers{security: &Security{}}) {
		if item.Public {
			if item.Teacher {
				t.Errorf("%s %s is marked public and teacher-only at once", item.Method, item.Path)
			}
			public = append(public, item.Method+" "+item.Path)
		}
	}
	sort.Strings(public)
	want := []string{"GET /health", "GET /openapi.yaml", "POST /api/login"}
	if strings.Join(public, ",") != strings.Join(want, ",") {
		t.Fatalf("public routes = %v, want %v", public, want)
	}
}

// 未知路径与已移除的 /api/csrf 都必须是 404，而不是任何形式的"仍可用"。
func TestUnknownAndRemovedRoutesAnswer404(t *testing.T) {
	handler := newHandler(nil, "test-only-secret", newUploadStore(nil, ""))
	for _, target := range []string{"/api/csrf", "/api/nope", "/api/agents/1/bogus"} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, target, nil))
		if recorder.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", target, recorder.Code)
		}
		if !strings.Contains(recorder.Body.String(), "not_found") {
			t.Errorf("GET %s body = %s", target, recorder.Body.String())
		}
	}
}

// 接口文档是不需要令牌就能读的：Swagger 页面在登录前就要能加载它。
func TestOpenAPIDocumentIsReadableWithoutAToken(t *testing.T) {
	handler := newHandler(nil, "test-only-secret", newUploadStore(nil, ""))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, openAPIPath, nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200", openAPIPath, recorder.Code)
	}
	if contentType := recorder.Header().Get("Content-Type"); !strings.Contains(contentType, "yaml") {
		t.Fatalf("content type = %q", contentType)
	}
	if !strings.Contains(recorder.Body.String(), "openapi: 3.0.3") {
		t.Fatal("document body does not look like an OpenAPI 3 document")
	}

	// ETag 命中时以 304 收尾，避免 Swagger 页面每次刷新都重传整份文档。
	etag := recorder.Header().Get("ETag")
	if etag == "" {
		t.Fatal("document must carry an ETag")
	}
	conditional := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, openAPIPath, nil)
	request.Header.Set("If-None-Match", etag)
	handler.ServeHTTP(conditional, request)
	if conditional.Code != http.StatusNotModified {
		t.Fatalf("conditional request = %d, want 304", conditional.Code)
	}
}

// 文档里声明的安全方案要与实现一致：只有 bearerAuth，没有 Cookie 或 CSRF。
func TestOpenAPIDeclaresBearerOnly(t *testing.T) {
	document := string(openAPIDocument)
	for _, forbidden := range []string{"cookieAuth", "X-CSRF-Token", "apiKey:"} {
		if strings.Contains(document, forbidden) {
			t.Errorf("openapi.yaml still mentions %q", forbidden)
		}
	}
	if !strings.Contains(document, "bearerFormat: JWT") {
		t.Error("openapi.yaml must declare the bearer JWT scheme")
	}
}
