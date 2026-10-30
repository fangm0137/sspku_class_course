package httpapi

import (
	"database/sql"
	"io"
	"log"
	"net/http"
	"os"

	"campusclaw/backend/internal/chat"
	"campusclaw/backend/internal/mcp"
	"campusclaw/backend/internal/rag"
)

// route 是路由表的一行。它同时是两件事的唯一来源：
//   - 注册到 ServeMux 的全部业务路由；
//   - GET /openapi.yaml 的漂移测试比对依据（见 openapi_test.go）。
//
// 这样"文档写了但代码没有""代码有但文档没写"都会在测试里直接失败，
// 不需要靠人工同步两份清单。
type route struct {
	Method  string
	Path    string
	Teacher bool
	Public  bool
	Handler http.HandlerFunc
}

// Health does not touch the account database, even when it is unavailable.
func NewHandler(db *sql.DB, secret string, uploadDirectories ...string) http.Handler {
	directory := ""
	if len(uploadDirectories) > 0 {
		directory = uploadDirectories[0]
	}
	return newHandler(db, secret, newUploadStore(db, directory), rag.New(db, rag.FromEnv(os.Getenv)))
}

// newHandler wires every route behind bearer token authentication. Only login,
// the liveness probe and the API description stay anonymous; GET /api/csrf is
// intentionally gone, so unknown API paths fall through to a plain 404.
// ragService 允许为空，此时检索与问答按依赖不可用处理，其余接口不受影响。
func newHandler(db *sql.DB, secret string, store *uploadStore, ragServices ...*rag.Service) http.Handler {
	ragService := (*rag.Service)(nil)
	if len(ragServices) > 0 {
		ragService = ragServices[0]
	}
	mux := http.NewServeMux()
	security := NewSecurity(db, secret)
	materials := materialReader{db: db, directory: store.directory}
	ask := askHandler{service: ragService}
	search := searchHandler{service: ragService}
	reindex := reindexHandler{service: ragService}
	// 第 5 课：智能体、技能、会话与流式回答。db 为空（单元测试）时这些接口按依赖不可用处理。
	var (
		agents        agentsHandler
		skillsH       skillsHandler
		conversations conversationsHandler
		answer        answerHandler
		templates     templatesHandler
		servers       mcpHandler
		llmCalls      llmCallsHandler
	)
	if db != nil {
		// MCP 配置全部可选：缺失或非法时只是关掉外部工具，不阻断第 3–5 课的路径。
		settings, err := mcp.FromEnv(os.Getenv, secret)
		if err != nil {
			log.Printf("mcp configuration ignored: %v", err)
			settings = mcp.Settings{
				CallTimeout: mcp.DefaultCallTimeout, MaxRounds: mcp.DefaultMaxRounds, MaxCalls: mcp.DefaultMaxCalls,
			}
		}
		chatService := chat.New(db, ragService, chat.Options{Settings: settings})
		agents.store = chatService.Agents()
		skillsH.store = chatService.Skills()
		skillsH.servers = chatService.Servers()
		conversations.chats = chatService.Conversations()
		conversations.agents = chatService.Agents()
		answer.service = chatService
		templates.store = chatService.Templates()
		servers.store = chatService.Servers()
		llmCalls.store = chatService.Audit()
	}
	for _, item := range routeTable(routeHandlers{
		upload: store.upload, materials: materials, ask: ask, search: search, reindex: reindex,
		agents: agents, skills: skillsH, conversations: conversations, answer: answer,
		templates: templates, servers: servers, llmCalls: llmCalls,
		security: security,
	}) {
		if item.Public {
			mux.HandleFunc(item.Method+" "+item.Path, item.Handler)
			continue
		}
		mux.Handle(item.Method+" "+item.Path, security.RequireIdentity(item.Handler, item.Teacher))
	}
	// 兜底：未匹配的路径（含被移除的 /api/csrf）一律返回 404，与"资源不存在"同形。
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { materialNotFound(w) })
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// /health 与接口文档不参与业务就绪门禁：前者只代表进程活着，
		// 后者是让使用者能看到有哪些接口，都不该在恢复期间一起 503。
		if r.URL.Path != "/health" && r.URL.Path != openAPIPath && !store.ready.Load() {
			securityError(w, 503)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// routeHandlers 把各处理器打包传入，避免 routeTable 的签名过长。
type routeHandlers struct {
	upload        http.HandlerFunc
	materials     materialReader
	ask           askHandler
	search        searchHandler
	reindex       reindexHandler
	agents        agentsHandler
	skills        skillsHandler
	conversations conversationsHandler
	answer        answerHandler
	templates     templatesHandler
	servers       mcpHandler
	llmCalls      llmCallsHandler
	security      *Security
}

// routeTable 声明全部路由。顺序即 /openapi.yaml 的书写顺序，便于对照。
func routeTable(h routeHandlers) []route {
	authenticated := func(method, path string, handler http.HandlerFunc) route {
		return route{Method: method, Path: path, Handler: handler}
	}
	teacherOnly := func(method, path string, handler http.HandlerFunc) route {
		return route{Method: method, Path: path, Teacher: true, Handler: handler}
	}
	anonymous := func(method, path string, handler http.HandlerFunc) route {
		return route{Method: method, Path: path, Public: true, Handler: handler}
	}
	return []route{
		anonymous("POST", "/api/login", h.security.login),
		authenticated("GET", "/api/me", h.security.me),
		authenticated("POST", "/api/logout", h.security.logout),

		teacherOnly("POST", "/api/materials", h.upload),
		authenticated("GET", "/api/materials", h.materials.list),
		authenticated("GET", "/api/materials/{id}", h.materials.detail),
		authenticated("GET", "/api/materials/{id}/file", h.materials.file),
		teacherOnly("POST", "/api/materials/{id}/reindex", h.reindex.reindex),

		authenticated("GET", "/api/search", h.search.search),
		authenticated("POST", "/api/ask", h.ask.ask),

		// 学生可读本班已启用的模板（理解应用会怎样被引导），但不可写。
		authenticated("GET", "/api/prompt-templates", h.templates.list),
		teacherOnly("POST", "/api/prompt-templates", h.templates.create),
		authenticated("GET", "/api/prompt-templates/{id}", h.templates.get),
		teacherOnly("PUT", "/api/prompt-templates/{id}", h.templates.update),
		teacherOnly("DELETE", "/api/prompt-templates/{id}", h.templates.remove),

		teacherOnly("POST", "/api/agents", h.agents.create),
		authenticated("GET", "/api/agents", h.agents.list),
		authenticated("GET", "/api/agents/{id}", h.agents.get),
		authenticated("GET", "/api/agents/{id}/composition", h.agents.composition),
		teacherOnly("PUT", "/api/agents/{id}", h.agents.update),
		teacherOnly("DELETE", "/api/agents/{id}", h.agents.remove),
		teacherOnly("PUT", "/api/agents/{id}/skills", h.agents.replaceSkills),
		teacherOnly("PUT", "/api/agents/{id}/materials", h.agents.replaceMaterials),

		teacherOnly("POST", "/api/skills", h.skills.create),
		teacherOnly("POST", "/api/skills/upload", h.skills.upload),
		authenticated("GET", "/api/skills", h.skills.list),
		authenticated("GET", "/api/skills/{id}", h.skills.get),
		teacherOnly("PUT", "/api/skills/{id}", h.skills.update),
		teacherOnly("DELETE", "/api/skills/{id}", h.skills.remove),

		teacherOnly("POST", "/api/mcp/servers", h.servers.create),
		authenticated("GET", "/api/mcp/servers", h.servers.list),
		teacherOnly("PUT", "/api/mcp/servers/{id}", h.servers.update),
		teacherOnly("DELETE", "/api/mcp/servers/{id}", h.servers.remove),
		teacherOnly("POST", "/api/mcp/servers/{id}/connect", h.servers.connect),
		teacherOnly("POST", "/api/mcp/servers/{id}/disconnect", h.servers.disconnect),
		authenticated("GET", "/api/mcp/servers/{id}/tools", h.servers.tools),

		authenticated("GET", "/api/llm-calls", h.llmCalls.list),
		authenticated("GET", "/api/llm-calls/{id}", h.llmCalls.get),
		authenticated("GET", "/api/llm-calls/{id}/context-size", h.llmCalls.contextSize),

		authenticated("GET", "/api/conversations", h.conversations.list),
		authenticated("POST", "/api/conversations", h.conversations.create),
		authenticated("GET", "/api/conversations/{id}", h.conversations.get),
		authenticated("POST", "/api/conversations/{id}/messages", h.answer.stream),

		anonymous("GET", openAPIPath, serveOpenAPI),
		anonymous("GET", "/health", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"status":"ok"}`)
		}),
	}
}
