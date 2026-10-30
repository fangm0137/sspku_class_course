export type User = { id: number; username: string; role: 'teacher' | 'student'; class_id: number };
export type Material = { id: number; title: string; class_id: number; created_at: string };
export type Detail = Material & { text: string };
// 引用字段与后端 /api/ask 的响应一一对应：citation 就是回答里的 [n] 序号，
// 字符区间与 /api/search 用同一套字段名。
export type AskCitation = {
  material_id: number; title: string; chunk_id: number; chunk_index: number;
  char_start: number; char_end: number; excerpt: string; citation: number;
};
export type AskReply = { answer: string; hits: AskCitation[] };

// 检索：模式与班级都由服务端决定，查询串里不带任何班级参数。
export type SearchMode = 'keyword' | 'vector' | 'hybrid';
// 命中字段与后端 /api/search 一一对应；score 只是本次模式的排序分。
export type SearchHit = {
  material_id: number; title: string; chunk_id: number; chunk_index: number;
  char_start: number; char_end: number; excerpt: string; score: number;
};
export type SearchReply = { mode: SearchMode; hits: SearchHit[] };
export type ReindexReply = { material_id: number; strategy: string; chunks: number };
// 重建索引的参数：strategy 决定切分方式，custom 才需要 size/overlap。
export type ReindexOptions = {
  strategy: 'auto' | 'custom' | 'hierarchy'; size?: number; overlap?: number;
  remove_urls?: boolean; remove_emails?: boolean; collapse_whitespace?: boolean;
};

const messages: Record<string, string> = {
  invalid_credentials: '账号或密码错误，请重新输入。',
  unauthenticated: '登录已失效，请重新登录。',
  forbidden: '当前账号没有执行此操作的权限。',
  invalid_request: '请求无效，请检查输入或文件格式。',
  payload_too_large: '文件超过大小限制，请选择不超过 5 MiB 的文件。',
  not_found: '这份材料不存在或不属于你的班级。',
  conflict: '该资源仍被引用，先解除引用再删除。',
  upstream_error: '外部工具连接失败，请检查地址与令牌。',
  internal_error: '操作未完成，请稍后重试。',
};
export class ApiError extends Error {
  constructor(public status: number, code = '') {
    super(status === 503 ? '服务暂不可用，请稍后重试。' : messages[code] || '网络或服务暂不可用，请稍后重试。');
  }
}

// The bearer token is the only credential the browser keeps. It is stored in
// localStorage so a page refresh can restore the session, and it never travels
// in a cookie, which is why no CSRF token is needed.
const tokenKey = 'campusclaw_token';
let token = '';

function currentToken(): string {
  if (token) return token;
  try { token = localStorage.getItem(tokenKey) || ''; } catch { token = ''; }
  return token;
}

export function storeToken(value: string) {
  token = value;
  try { localStorage.setItem(tokenKey, value); } catch { /* storage unavailable */ }
}

export function forgetToken() {
  token = '';
  try { localStorage.removeItem(tokenKey); } catch { /* storage unavailable */ }
}

function authHeaders(init?: HeadersInit): Headers {
  const headers = new Headers(init);
  const value = currentToken();
  if (value) headers.set('Authorization', `Bearer ${value}`);
  return headers;
}

// 把失败响应统一翻译成 ApiError。流式接口也复用这里：HTTP 状态在流开始前
// 就可能失败，必须用与普通请求完全相同的口径提示。
async function failure(res: Response): Promise<ApiError> {
  const body = await res.json().catch(() => ({}));
  const code = typeof body.error === 'string' ? body.error : '';
  // Only a genuinely rejected credential clears the stored token; a failed
  // sign-in attempt must not sign the current user out.
  if (res.status === 401 && code === 'unauthenticated') forgetToken();
  return new ApiError(res.status, code);
}

async function response(path: string, init?: RequestInit): Promise<Response> {
  let res: Response;
  try {
    res = await fetch(path, { ...init, headers: authHeaders(init?.headers), credentials: 'omit', cache: 'no-store' });
  } catch { throw new ApiError(0); }
  if (!res.ok) throw await failure(res);
  return res;
}

export async function read<T>(path: string): Promise<T> {
  try { return await (await response(path)).json(); }
  catch (error) { if (error instanceof ApiError) throw error; throw new ApiError(0); }
}

export async function write<T>(path: string, body?: FormData | Record<string, unknown>): Promise<T> {
  const form = body instanceof FormData;
  const headers: Record<string, string> = {};
  if (body && !form) headers['Content-Type'] = 'application/json';
  const res = await response(path, { method: 'POST', headers, body: form ? body : body ? JSON.stringify(body) : undefined });
  try { return await res.json(); } catch { throw new ApiError(0); }
}

// 提问：检索与生成都在服务端完成，浏览器只拿到回答与可回溯的引用。
export async function ask(question: string): Promise<AskReply> {
  return write<AskReply>('/api/ask', { question });
}

// 检索：三种模式都由服务端执行，空查询与非法模式会被后端拒绝为 400。
export async function search(question: string, mode: SearchMode): Promise<SearchReply> {
  return read<SearchReply>(`/api/search?q=${encodeURIComponent(question)}&mode=${mode}`);
}

// 重建索引：只有教师可用，按本次策略重新切片并重新嵌入。
export async function reindex(materialID: number, options: ReindexOptions): Promise<ReindexReply> {
  return write<ReindexReply>(`/api/materials/${materialID}/reindex`, options);
}

// Sign in returns a freshly signed token, which becomes the stored credential.
export async function signIn(username: string, password: string): Promise<User> {
  const reply = await write<{ user: User; token: string; expires_at: string }>('/api/login', { username, password });
  storeToken(reply.token);
  return reply.user;
}

export async function download(item: Material) {
  const res = await response(`/api/materials/${item.id}/file`);
  const url = URL.createObjectURL(await res.blob());
  const anchor = document.createElement('a');
  anchor.href = url; anchor.download = item.title; document.body.append(anchor); anchor.click(); anchor.remove();
  window.setTimeout(() => URL.revokeObjectURL(url), 1000);
}

/* ─────────────── 第 5 课：智能体、技能、会话与流式回答 ─────────────── */

// 智能体：提示词留空时由服务端回退到默认角色设定，前端不做任何补齐。
export type Agent = {
  id: number; name: string; prompt: string; enabled: boolean;
  prompt_template_id: number | null; builtin: boolean;
  skill_ids: number[]; material_ids: number[];
};
export type AgentInput = { name: string; prompt: string; enabled: boolean; prompt_template_id?: number | null };

// 技能：记录的每一列都由服务端从文件原文解析得到，前端只提交原文。
export type Skill = {
  id: number; name: string; trigger_text: string; description: string;
  subject_text: string; tools: string[]; enabled: boolean; builtin: boolean;
};
export type SkillDetail = { skill: Skill; source_text: string };

export type Conversation = { id: number; title: string; agent_id: number; updated_at: string };
// 引用字段与流式回答里 meta 事件的 citations 一一对应，n 就是正文里的 [n]。
export type Citation = {
  material_id: number; title: string; chunk_id: number;
  chunk_index: number; char_start: number; char_end: number;
};
export type ChatMessage = {
  id: number; role: 'user' | 'assistant'; content: string;
  citations?: Citation[]; skill_name?: string; created_at: string;
};
export type ConversationDetail = Conversation & { messages: ChatMessage[] };

// 服务端的四个事件名与后端实现保持一致；meta 里的 tool_calls 只在确有调用时出现。
export type ToolCallRecord = { ref: string; status: 'ok' | 'denied' | 'error'; latency_ms: number; text: string };
export type StreamEvent =
  | { type: 'meta'; status: string; conversation_id: number; user_message_id: number; skill: { name: string } | null; citations: Citation[]; tool_calls?: ToolCallRecord[] }
  | { type: 'delta'; text: string }
  | { type: 'done'; assistant_message_id: number }
  | { type: 'error'; error: string; message: string };

export async function listAgents(includeDisabled: boolean): Promise<Agent[]> {
  return (await read<{ items: Agent[] }>(`/api/agents${includeDisabled ? '?all=1' : ''}`)).items;
}
export async function createAgent(input: AgentInput): Promise<number> {
  return (await write<{ id: number }>('/api/agents', input)).id;
}
export async function updateAgent(id: number, input: AgentInput): Promise<void> {
  await put(`/api/agents/${id}`, input);
}
export async function deleteAgent(id: number): Promise<void> {
  await remove(`/api/agents/${id}`);
}
export async function setAgentSkills(id: number, skillIds: number[]): Promise<void> {
  await put(`/api/agents/${id}/skills`, { skill_ids: skillIds });
}
export async function setAgentMaterials(id: number, materialIds: number[]): Promise<void> {
  await put(`/api/agents/${id}/materials`, { material_ids: materialIds });
}

export async function listSkills(): Promise<Skill[]> {
  return (await read<{ items: Skill[] }>('/api/skills')).items;
}
export async function getSkill(id: number): Promise<SkillDetail> {
  return read<SkillDetail>(`/api/skills/${id}`);
}
export async function createSkill(sourceText: string): Promise<number> {
  return (await write<{ id: number }>('/api/skills', { source_text: sourceText })).id;
}
export async function updateSkill(id: number, sourceText: string): Promise<void> {
  await put(`/api/skills/${id}`, { source_text: sourceText });
}
export async function deleteSkill(id: number): Promise<void> {
  await remove(`/api/skills/${id}`);
}

export async function listConversations(): Promise<Conversation[]> {
  return (await read<{ items: Conversation[] }>('/api/conversations')).items;
}
export async function createConversation(agentId: number): Promise<{ id: number; title: string }> {
  return write<{ id: number; title: string }>('/api/conversations', { agent_id: agentId });
}
export async function getConversation(id: number): Promise<ConversationDetail> {
  return read<ConversationDetail>(`/api/conversations/${id}`);
}

// PUT / DELETE 在前几课里没有用到，这里补上同一套头与错误翻译。
async function put(path: string, body: unknown): Promise<void> {
  await response(path, { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });
}
async function remove(path: string): Promise<void> {
  await response(path, { method: 'DELETE' });
}

// parseFrame 解析一帧 SSE：返回 null 表示这一帧不是我们认识的事件（跳过即可）。
export function parseFrame(frame: string): StreamEvent | null {
  let name = '';
  const data: string[] = [];
  for (const raw of frame.split('\n')) {
    const line = raw.replace(/\r$/, '');
    if (line.startsWith('event:')) name = line.slice(6).trim();
    else if (line.startsWith('data:')) data.push(line.slice(5).trim());
  }
  if (!name || data.length === 0) return null;
  try { return { type: name, ...JSON.parse(data.join('\n')) } as StreamEvent; }
  catch { return null; }
}

// sendMessage 提问并逐帧回调。服务端保证事件顺序为 meta → delta×n → done，
// 建立流之前的失败仍以 HTTP 状态返回，因此这里先判状态再读流。
export async function sendMessage(
  conversationId: number,
  question: string,
  onEvent: (event: StreamEvent) => void,
): Promise<void> {
  let res: Response;
  try {
    res = await fetch(`/api/conversations/${conversationId}/messages`, {
      method: 'POST',
      headers: authHeaders({ 'Content-Type': 'application/json', Accept: 'text/event-stream' }),
      body: JSON.stringify({ question }),
      credentials: 'omit',
      cache: 'no-store',
    });
  } catch { throw new ApiError(0); }
  if (!res.ok) throw await failure(res);
  if (!res.body) throw new ApiError(0);

  const reader = res.body.getReader();
  const decoder = new TextDecoder();
  let buffer = '';
  for (;;) {
    const { value, done } = await reader.read();
    if (done) break;
    buffer += decoder.decode(value, { stream: true });
    let boundary = buffer.indexOf('\n\n');
    while (boundary >= 0) {
      const event = parseFrame(buffer.slice(0, boundary));
      buffer = buffer.slice(boundary + 2);
      if (event) onEvent(event);
      boundary = buffer.indexOf('\n\n');
    }
  }
  if (buffer.trim()) {
    const event = parseFrame(buffer);
    if (event) onEvent(event);
  }
}

/* ─────────────── 第 6 课：模板、组成视图、MCP 与调用审计 ─────────────── */

// 模板：模板体只做三个占位符的一次替换，渲染在服务端完成。
export type PromptTemplate = { id: number; name: string; body: string; description: string; enabled: boolean };
export type PromptTemplateInput = { name: string; body: string; description: string; enabled: boolean };

// 组成视图：这个应用当前实际生效的东西。
export type NamedItem = { id: number; name: string };
export type Composition = {
  agent_id: number; name: string; builtin: boolean; prompt: string;
  template_id: number | null; template_name: string;
  skills: NamedItem[]; materials: NamedItem[]; tool_refs: string[];
};

// MCP 服务器：令牌只进不出，响应里只有 has_token。
export type McpServer = {
  id: number; name: string; base_url: string; has_token: boolean;
  status: 'disconnected' | 'connected' | 'error'; last_error: string;
  connected_at?: string; tool_count: number;
};
export type McpTool = { name: string; description: string; input_schema: unknown };

// 调用审计：llm_calls 一行即一轮模型调用；工具调用单独一张表。
export type LlmCall = {
  id: number; conversation_id: number; user_id: number; agent_id: number; round_no: number; model: string;
  system_prompt: string; user_prompt: string; message_count: number; context_chars: number; skill_name: string;
  tool_def_count: number; tool_call_count: number; finish_reason: string; status: string; http_status: number;
  prompt_tokens: number; completion_tokens: number; latency_ms: number; error_text: string; created_at: string;
};
export type ToolCallDetail = {
  id: number; round_no: number; server_name: string; tool_name: string; arguments_json: string;
  status: string; result_excerpt: string; error_text: string; latency_ms: number; created_at: string;
};
export type LlmCallDetail = LlmCall & {
  request_json: string; response_text: string; response_json: string; tool_calls: ToolCallDetail[];
};
export type ContextSize = { material_count: number; source_chars: number; context_chars: number; material_titles: string[] };

export async function listTemplates(includeDisabled: boolean): Promise<PromptTemplate[]> {
  return (await read<{ items: PromptTemplate[] }>(`/api/prompt-templates${includeDisabled ? '?all=1' : ''}`)).items;
}
export async function createTemplate(input: PromptTemplateInput): Promise<number> {
  return (await write<{ id: number }>('/api/prompt-templates', input)).id;
}
export async function updateTemplate(id: number, input: PromptTemplateInput): Promise<void> {
  await put(`/api/prompt-templates/${id}`, input);
}
export async function deleteTemplate(id: number): Promise<void> {
  await remove(`/api/prompt-templates/${id}`);
}
export async function getComposition(id: number): Promise<Composition> {
  return read<Composition>(`/api/agents/${id}/composition`);
}

export async function listMcpServers(): Promise<McpServer[]> {
  return (await read<{ items: McpServer[] }>('/api/mcp/servers')).items;
}
export async function createMcpServer(input: { name: string; base_url: string; token: string }): Promise<number> {
  return (await write<{ id: number }>('/api/mcp/servers', input)).id;
}
export async function connectMcpServer(id: number): Promise<McpTool[]> {
  return (await write<{ tools: McpTool[] }>(`/api/mcp/servers/${id}/connect`)).tools;
}
export async function disconnectMcpServer(id: number): Promise<void> {
  await write<{ ok: boolean }>(`/api/mcp/servers/${id}/disconnect`);
}
export async function deleteMcpServer(id: number): Promise<void> {
  await remove(`/api/mcp/servers/${id}`);
}
export async function listMcpTools(id: number): Promise<McpTool[]> {
  return (await read<{ items: McpTool[] }>(`/api/mcp/servers/${id}/tools`)).items;
}

export async function listLlmCalls(limit = 50): Promise<LlmCall[]> {
  return (await read<{ items: LlmCall[] }>(`/api/llm-calls?limit=${limit}`)).items;
}
export async function getLlmCall(id: number): Promise<LlmCallDetail> {
  return read<LlmCallDetail>(`/api/llm-calls/${id}`);
}
export async function getContextSize(id: number): Promise<ContextSize> {
  return read<ContextSize>(`/api/llm-calls/${id}/context-size`);
}

// 技能上传：与 JSON 创建同一条落库路径，只是原文来自文件。
export async function uploadSkill(file: File): Promise<number> {
  const data = new FormData();
  data.append('file', file);
  return (await write<{ id: number }>('/api/skills/upload', data)).id;
}
