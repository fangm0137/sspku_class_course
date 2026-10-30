import { useEffect, useRef, useState, type FormEvent } from 'react';
import {
  ApiError, connectMcpServer, createMcpServer, createTemplate, deleteMcpServer, deleteTemplate,
  disconnectMcpServer, getComposition, getContextSize, getLlmCall, listAgents, listLlmCalls,
  listMcpServers, listMcpTools, listSkills, listTemplates, read, updateAgent, updateTemplate,
  type Agent, type Composition, type ContextSize, type LlmCall, type LlmCallDetail, type Material,
  type McpServer, type McpTool, type PromptTemplate, type Skill,
} from './api';

// 四个视图各自管理自己的状态：它们的数据互不相干，放在 App 里只会让那一个组件继续膨胀。
function useRunner() {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const running = useRef(false);
  async function run(action: () => Promise<void>) {
    if (running.current) return;
    running.current = true; setBusy(true); setError(''); setNotice('');
    try { await action(); }
    catch (cause) {
      if (cause instanceof ApiError && cause.status === 401) setError('登录已失效，请重新登录。');
      else setError(cause instanceof ApiError ? cause.message : '网络或服务暂不可用，请稍后重试。');
    } finally { running.current = false; setBusy(false); }
  }
  const feedback = <>
    {error && <div role="alert" className="message error">{error}</div>}
    {notice && <div role="status" className="message success">{notice}</div>}
  </>;
  return { busy, run, setNotice, feedback };
}

const badge = (text: string, tone: 'ok' | 'warn' | 'muted' = 'muted') =>
  <span className={`pill ${tone}`}>{text}</span>;

/* ─────────────── AI 应用与组成视图 ─────────────── */

export function AppsView({ role }: { role: 'teacher' | 'student' }) {
  const { busy, run, setNotice, feedback } = useRunner();
  const [agents, setAgents] = useState<Agent[]>([]);
  const [templates, setTemplates] = useState<PromptTemplate[]>([]);
  const [skills, setSkills] = useState<Skill[]>([]);
  const [materials, setMaterials] = useState<Material[]>([]);
  const [composition, setComposition] = useState<Composition | null>(null);
  const [editing, setEditing] = useState<Agent | null>(null);
  const [draft, setDraft] = useState({ name: '', prompt: '', enabled: true, template: 0 });

  async function load() {
    const [agentList, templateList, skillList, materialList] = await Promise.all([
      listAgents(true), listTemplates(true), listSkills(), read<{ items: Material[] }>('/api/materials'),
    ]);
    setAgents(agentList); setTemplates(templateList); setSkills(skillList); setMaterials(materialList.items);
  }
  useEffect(() => { void run(async () => { await load(); }); }, []);

  function inspect(agent: Agent) {
    void run(async () => { setComposition(await getComposition(agent.id)); });
  }
  function edit(agent: Agent) {
    setEditing(agent);
    setDraft({ name: agent.name, prompt: agent.prompt, enabled: agent.enabled, template: agent.prompt_template_id ?? 0 });
  }
  function save(event: FormEvent) {
    event.preventDefault();
    if (!editing) return;
    void run(async () => {
      await updateAgent(editing.id, {
        name: draft.name, prompt: draft.prompt, enabled: draft.enabled,
        prompt_template_id: draft.template > 0 ? draft.template : null,
      });
      setNotice('应用已保存，下一次提问即按新的模板与绑定生效。');
      await load();
      setComposition(await getComposition(editing.id));
    });
  }

  return <>
    <div className="page-heading"><div><span className="eyebrow">AI APPLICATIONS</span><h1>AI 应用<span className="dot">.</span></h1><p className="muted">一个应用由模板（或自由文本）、候选技能、材料范围与可调用工具四部分组成，四者都能在不改代码的情况下调整。</p></div><button className="secondary" disabled={busy} onClick={() => void run(load)}>{busy ? '处理中…' : '刷新'}</button></div>
    {feedback}
    <div className="admin-layout">
      <section className="admin-list">
        <div className="section-heading"><h2>本班应用</h2><span>{agents.length} 个</span></div>
        <ul>{agents.map(agent => <li key={agent.id} className={`admin-row ${composition?.agent_id === agent.id ? 'selected' : ''}`}>
          <div className="admin-row-head"><strong>{agent.name}</strong><span className="row-badges">
            {agent.builtin && badge('内置', 'ok')}
            {badge(agent.enabled ? '已启用' : '已停用', agent.enabled ? 'ok' : 'muted')}
          </span></div>
          <p className="muted">{agent.prompt_template_id ? `模板 #${agent.prompt_template_id}` : (agent.prompt ? '自由文本提示词' : '默认角色设定')} · 技能 {agent.skill_ids.length} 个 · 材料 {agent.material_ids.length} 份</p>
          <div className="admin-actions">
            <button type="button" className="text-button" disabled={busy} onClick={() => inspect(agent)}>组成视图</button>
            {role === 'teacher' && <button type="button" className="text-button" disabled={busy} onClick={() => edit(agent)}>编辑</button>}
          </div>
        </li>)}</ul>
      </section>
      <section className="admin-form">
        {role !== 'teacher'
          ? <div className="preview-empty"><span aria-hidden="true">✎</span><h2>学生只能查看</h2><p>模板的创建与修改由教师完成；<br />你可以在这里看到应用被怎样引导。</p></div>
          : editing
          ? <>
            <div className="section-heading"><h2>编辑「{editing.name}」</h2><button type="button" className="text-button" onClick={() => setEditing(null)}>关闭</button></div>
            <form onSubmit={save}>
              <label htmlFor="app-name">名称</label>
              <input id="app-name" value={draft.name} maxLength={64} disabled={busy} onChange={e => setDraft({ ...draft, name: e.target.value })} />
              <label htmlFor="app-template">系统提示词来源</label>
              <select id="app-template" value={draft.template} disabled={busy} onChange={e => setDraft({ ...draft, template: Number(e.target.value) })}>
                <option value={0}>不引用模板（用下方自由文本）</option>
                {templates.map(item => <option key={item.id} value={item.id}>{item.name}{item.enabled ? '' : '（已停用）'}</option>)}
              </select>
              <label htmlFor="app-prompt">自由文本提示词（引用模板时被忽略）</label>
              <textarea id="app-prompt" rows={5} value={draft.prompt} maxLength={2000} disabled={busy} onChange={e => setDraft({ ...draft, prompt: e.target.value })} />
              <label className="checkbox-row"><input type="checkbox" checked={draft.enabled} disabled={busy} onChange={e => setDraft({ ...draft, enabled: e.target.checked })} /> 启用（内置应用可停用，但不可删除）</label>
              <button className="primary" disabled={busy || !draft.name.trim()}>保存</button>
              <p className="muted">技能与材料的勾选在「智能体」页完成；这里只决定提示词来源。</p>
            </form>
          </>
          : <div className="preview-empty"><span aria-hidden="true">◈</span><h2>选一个应用看它由什么组成</h2><p>组成视图会列出模板名、候选技能、材料标题与工具引用，<br />与提问时实际生效的内容一致。</p></div>}
        {composition && <div className="composition">
          <div className="section-heading"><h2>组成视图 · {composition.name}</h2>{composition.builtin && badge('内置', 'ok')}</div>
          <dl>
            <dt>模板</dt><dd>{composition.template_name || '（未引用模板）'}{composition.template_id ? ` · #${composition.template_id}` : ''}</dd>
            <dt>技能</dt><dd>{composition.skills.length === 0 ? '（无）' : composition.skills.map(item => item.name).join('、')}</dd>
            <dt>材料</dt><dd>{composition.materials.length === 0 ? '（无）' : composition.materials.map(item => item.name).join('、')}</dd>
            <dt>工具引用</dt><dd>{composition.tool_refs.length === 0 ? '（无外部工具）' : composition.tool_refs.join('、')}</dd>
          </dl>
          <p className="muted">本班技能共 {skills.length} 个、材料共 {materials.length} 份；只有上面列出的那些会参与。</p>
        </div>}
      </section>
    </div>
  </>;
}

/* ─────────────── 提示词模板 ─────────────── */

const emptyTemplate = { name: '', body: '', description: '', enabled: true };

export function TemplatesView({ role }: { role: 'teacher' | 'student' }) {
  const { busy, run, setNotice, feedback } = useRunner();
  const [items, setItems] = useState<PromptTemplate[]>([]);
  const [draft, setDraft] = useState({ ...emptyTemplate });
  const [editingID, setEditingID] = useState(0);

  async function load() { setItems(await listTemplates(true)); }
  useEffect(() => { void run(async () => { await load(); }); }, []);

  function save(event: FormEvent) {
    event.preventDefault();
    void run(async () => {
      if (editingID) { await updateTemplate(editingID, draft); setNotice('模板已保存，被引用的应用下次提问即用新内容。'); }
      else { const id = await createTemplate(draft); setEditingID(id); setNotice('模板已创建，可以在「AI 应用」里引用它。'); }
      await load();
    });
  }
  function remove(id: number) {
    void run(async () => {
      await deleteTemplate(id);
      setNotice('模板已删除。');
      if (editingID === id) { setEditingID(0); setDraft({ ...emptyTemplate }); }
      await load();
    });
  }

  return <>
    <div className="page-heading"><div><span className="eyebrow">PROMPT TEMPLATES</span><h1>提示词模板<span className="dot">.</span></h1><p className="muted">模板体支持三个占位符：{'{{app_name}}'} 换成应用名、{'{{today}}'} 换成服务端当天日期、{'{{question}}'} 换成本轮提问。替换只做一次，未识别的占位符原样保留。</p></div><button className="secondary" disabled={busy} onClick={() => void run(load)}>{busy ? '处理中…' : '刷新'}</button></div>
    {feedback}
    <div className="admin-layout">
      <section className="admin-form">
        {role !== 'teacher'
          ? <div className="preview-empty"><span aria-hidden="true">✎</span><h2>学生只能查看</h2><p>模板体在服务端渲染后随请求发出；<br />这里可以看到本班启用的模板。</p></div>
          : <>
        <div className="section-heading"><h2>{editingID ? `编辑模板 #${editingID}` : '新建模板'}</h2>{editingID > 0 && <button type="button" className="text-button" onClick={() => { setEditingID(0); setDraft({ ...emptyTemplate }); }}>改为新建</button>}</div>
        <form onSubmit={save}>
          <label htmlFor="tpl-name">名称（班内唯一，不超过 64 字）</label>
          <input id="tpl-name" value={draft.name} maxLength={64} disabled={busy} onChange={e => setDraft({ ...draft, name: e.target.value })} placeholder="例如：数学助教模板" />
          <label htmlFor="tpl-body">模板体（1–4000 字）</label>
          <textarea id="tpl-body" rows={8} value={draft.body} maxLength={4000} disabled={busy} onChange={e => setDraft({ ...draft, body: e.target.value })} placeholder={'你是{{app_name}}，负责本班数学答疑。今天是{{today}}。\n学生的问题：{{question}}'} />
          <label htmlFor="tpl-desc">说明（可选）</label>
          <input id="tpl-desc" value={draft.description} maxLength={255} disabled={busy} onChange={e => setDraft({ ...draft, description: e.target.value })} />
          <label className="checkbox-row"><input type="checkbox" checked={draft.enabled} disabled={busy} onChange={e => setDraft({ ...draft, enabled: e.target.checked })} /> 启用（停用只影响新建引用，已引用的应用仍按它渲染）</label>
          <button className="primary" disabled={busy || !draft.name.trim() || !draft.body.trim()}>{editingID ? '保存修改' : '创建模板'}</button>
        </form>
          </>}
      </section>
      <section className="admin-list">
        <div className="section-heading"><h2>本班模板</h2><span>{items.length} 个</span></div>
        <ul>{items.map(item => <li key={item.id} className={`admin-row ${editingID === item.id ? 'selected' : ''}`}>
          <div className="admin-row-head"><strong>{item.name}</strong>{badge(item.enabled ? '已启用' : '已停用', item.enabled ? 'ok' : 'muted')}</div>
          <pre className="tpl-body">{item.body}</pre>
          <div className="admin-actions">
            <button type="button" className="text-button" disabled={busy} onClick={() => { setEditingID(item.id); setDraft({ name: item.name, body: item.body, description: item.description, enabled: item.enabled }); }}>编辑</button>
            <button type="button" className="text-button" disabled={busy} onClick={() => remove(item.id)}>删除</button>
          </div>
        </li>)}</ul>
        <p className="muted">仍被应用引用的模板不能删除：接口返回 409，不会把应用悄悄改成默认角色。</p>
      </section>
    </div>
  </>;
}

/* ─────────────── MCP 工具 ─────────────── */

export function McpView({ role }: { role: 'teacher' | 'student' }) {
  const { busy, run, setNotice, feedback } = useRunner();
  const [servers, setServers] = useState<McpServer[]>([]);
  const [tools, setTools] = useState<Record<number, McpTool[]>>({});
  const [draft, setDraft] = useState({ name: '', base_url: '', token: '' });
  const [recent, setRecent] = useState<LlmCall[]>([]);
  const [detail, setDetail] = useState<LlmCallDetail | null>(null);

  async function load() {
    const [list, calls] = await Promise.all([listMcpServers(), listLlmCalls(20)]);
    setServers(list); setRecent(calls.filter(item => item.tool_call_count > 0));
  }
  useEffect(() => { void run(async () => { await load(); }); }, []);

  function register(event: FormEvent) {
    event.preventDefault();
    void run(async () => {
      await createMcpServer(draft);
      setDraft({ name: '', base_url: '', token: '' });
      setNotice('服务器已登记，令牌以密文保存，接口与页面都只显示"是否已配置"。');
      await load();
    });
  }
  function connect(id: number) {
    void run(async () => {
      const found = await connectMcpServer(id);
      setTools(current => ({ ...current, [id]: found }));
      setNotice(`连接成功，本次发现 ${found.length} 个工具（清单整体替换）。`);
      await load();
    });
  }
  function disconnect(id: number) {
    void run(async () => {
      await disconnectMcpServer(id);
      setTools(current => ({ ...current, [id]: [] }));
      setNotice('已断开，工具缓存已清空；引用其工具的技能会在调用时被拒绝。');
      await load();
    });
  }
  function remove(id: number) {
    void run(async () => { await deleteMcpServer(id); setNotice('服务器已删除。'); await load(); });
  }
  function inspect(id: number) {
    void run(async () => { setTools(current => ({ ...current, [id]: current[id] ?? [] })); const found = await listMcpTools(id); setTools(current => ({ ...current, [id]: found })); });
  }
  function openCall(id: number) {
    void run(async () => { setDetail(await getLlmCall(id)); });
  }

  return <>
    <div className="page-heading"><div><span className="eyebrow">MCP TOOLS</span><h1>MCP 工具<span className="dot">.</span></h1><p className="muted">教师登记本班的外部工具服务器，连接时用 <code>tools/list</code> 发现工具并整体替换缓存。工具声明与参数校验都用这份发现结果，不另行手写。</p></div><button className="secondary" disabled={busy} onClick={() => void run(load)}>{busy ? '处理中…' : '刷新'}</button></div>
    {feedback}
    <div className="admin-layout">
      <section className="admin-form">
        <div className="section-heading"><h2>登记服务器</h2></div>
        <form onSubmit={register}>
          <label htmlFor="mcp-name">名称（班内唯一）</label>
          <input id="mcp-name" value={draft.name} maxLength={64} disabled={busy} onChange={e => setDraft({ ...draft, name: e.target.value })} placeholder="campus-tools" />
          <label htmlFor="mcp-url">Base URL（http/https，不含账号密码）</label>
          <input id="mcp-url" value={draft.base_url} maxLength={255} disabled={busy} onChange={e => setDraft({ ...draft, base_url: e.target.value })} placeholder="http://mcp:8090" />
          <label htmlFor="mcp-token">令牌（只写入，永不回显）</label>
          <input id="mcp-token" type="password" value={draft.token} disabled={busy} onChange={e => setDraft({ ...draft, token: e.target.value })} placeholder="MCP_AUTH_TOKEN 的值" />
          <button className="primary" disabled={busy || !draft.name.trim() || !draft.base_url.trim() || !draft.token.trim()}>登记</button>
        </form>
        {detail && <div className="composition">
          <div className="section-heading"><h2>调用 #{detail.id} 的工具记录</h2>{badge(detail.status, detail.status === 'ok' ? 'ok' : 'warn')}</div>
          <dl>
            <dt>轮次</dt><dd>{detail.round_no} · 工具声明 {detail.tool_def_count} 条 · 调用 {detail.tool_call_count} 次</dd>
            <dt>技能</dt><dd>{detail.skill_name || '（未命中技能）'}</dd>
            <dt>工具</dt><dd>{detail.tool_calls.length === 0 ? '（本轮没有工具调用）' : detail.tool_calls.map(item => `${item.server_name}.${item.tool_name} → ${item.status}（${item.latency_ms}ms）`).join('；')}</dd>
          </dl>
        </div>}
        <div className="composition">
          <div className="section-heading"><h2>最近带工具的调用</h2><span>{recent.length} 条</span></div>
          {recent.length === 0 ? <p className="muted">还没有发生工具调用。</p> : <ul className="plain-list">{recent.map(item => <li key={item.id}>
            <button type="button" className="text-button" disabled={busy} onClick={() => openCall(item.id)}>#{item.id} · 第 {item.round_no} 轮 · 调用 {item.tool_call_count} 次 · {item.status}</button>
          </li>)}</ul>}
        </div>
      </section>
      <section className="admin-list">
        <div className="section-heading"><h2>本班服务器</h2><span>{servers.length} 台</span></div>
        <ul>{servers.map(server => <li key={server.id} className="admin-row">
          <div className="admin-row-head"><strong>{server.name}</strong><span className="row-badges">
            {badge(server.status === 'connected' ? '已连接' : server.status === 'error' ? '连接失败' : '未连接', server.status === 'connected' ? 'ok' : server.status === 'error' ? 'warn' : 'muted')}
            {server.has_token ? badge('令牌已配置') : badge('令牌未配置', 'warn')}
          </span></div>
          <p className="muted">{server.base_url} · 工具 {server.tool_count} 个{server.last_error ? ` · ${server.last_error}` : ''}</p>
          <div className="admin-actions">
            <button type="button" className="text-button" disabled={busy} onClick={() => connect(server.id)}>连接</button>
            <button type="button" className="text-button" disabled={busy} onClick={() => disconnect(server.id)}>断开</button>
            <button type="button" className="text-button" disabled={busy} onClick={() => inspect(server.id)}>查看工具</button>
            <button type="button" className="text-button" disabled={busy} onClick={() => remove(server.id)}>删除</button>
          </div>
          {tools[server.id] && <div className="tool-list">
            {tools[server.id].length === 0
              ? <p className="muted">清单为空：未连接或断开后缓存已被清空。</p>
              : tools[server.id].map(tool => <div className="tool-card" key={tool.name}>
                <strong>{tool.name}</strong>
                <p>{tool.description}</p>
                <pre>{JSON.stringify(tool.input_schema, null, 2)}</pre>
              </div>)}
          </div>}
        </li>)}</ul>
        <p className="muted">界面上不会出现令牌原文：登记后只显示"令牌已配置"。</p>
      </section>
    </div>
  </>;
}

/* ─────────────── 调用审计 ─────────────── */

export function AuditView() {
  const { busy, run, feedback } = useRunner();
  const [items, setItems] = useState<LlmCall[]>([]);
  const [detail, setDetail] = useState<LlmCallDetail | null>(null);
  const [size, setSize] = useState<ContextSize | null>(null);

  async function load() { setItems(await listLlmCalls(50)); }
  useEffect(() => { void run(async () => { await load(); }); }, []);

  function open(id: number) {
    void run(async () => {
      setDetail(await getLlmCall(id));
      setSize(await getContextSize(id));
    });
  }

  return <>
    <div className="page-heading"><div><span className="eyebrow">LLM CALL AUDIT</span><h1>调用审计<span className="dot">.</span></h1><p className="muted">每次模型调用一行，保存真正发出的请求体原文。工具循环会在一次提问里产生多轮，<code>round_no</code> 区分它们。教师看本班，学生只看自己。</p></div><button className="secondary" disabled={busy} onClick={() => void run(load)}>{busy ? '处理中…' : '刷新'}</button></div>
    {feedback}
    <section className="audit-panel">
      <table className="audit-table">
        <thead><tr><th>#</th><th>时间</th><th>轮次</th><th>模型</th><th>技能</th><th>消息</th><th>注入字符</th><th>工具声明/调用</th><th>状态</th><th /></tr></thead>
        <tbody>{items.map(item => <tr key={item.id} className={detail?.id === item.id ? 'selected' : ''}>
          <td>{item.id}</td>
          <td>{new Date(item.created_at).toLocaleString('zh-CN')}</td>
          <td>{item.round_no}</td>
          <td>{item.model}</td>
          <td>{item.skill_name || '—'}</td>
          <td>{item.message_count}</td>
          <td>{item.context_chars}</td>
          <td>{item.tool_def_count} / {item.tool_call_count}</td>
          <td>{item.status === 'ok' ? badge('成功', 'ok') : badge('失败', 'warn')}</td>
          <td><button type="button" className="text-button" disabled={busy} onClick={() => open(item.id)}>查看</button></td>
        </tr>)}</tbody>
      </table>
      {items.length === 0 && <p className="muted">还没有调用记录：先到「智能体对话」提一个问题。</p>}
    </section>
    {detail && <section className="audit-detail">
      <div className="section-heading"><h2>调用 #{detail.id} · 第 {detail.round_no} 轮</h2><span className="row-badges">{detail.finish_reason && badge(detail.finish_reason)}{badge(`HTTP ${detail.http_status}`)}{badge(`${detail.latency_ms}ms`)}</span></div>
      <dl>
        <dt>消息条数</dt><dd>{detail.message_count}</dd>
        <dt>注入字符</dt><dd>{detail.context_chars}{size && ` · 材料正文共 ${size.source_chars} 字（${size.material_count} 份）`}</dd>
        <dt>Token</dt><dd>prompt {detail.prompt_tokens} · completion {detail.completion_tokens}</dd>
      </dl>
      <h3>系统消息（system_prompt 派生列）</h3>
      <pre className="audit-pre">{detail.system_prompt}</pre>
      <h3>用户消息（user_prompt 派生列）</h3>
      <pre className="audit-pre">{detail.user_prompt}</pre>
      <h3>请求体原文（request_json）</h3>
      <pre className="audit-pre">{pretty(detail.request_json)}</pre>
      <h3>响应</h3>
      <pre className="audit-pre">{pretty(detail.response_json) || detail.response_text}</pre>
      {detail.tool_calls.length > 0 && <>
        <h3>工具调用</h3>
        <table className="audit-table"><thead><tr><th>轮次</th><th>服务器</th><th>工具</th><th>状态</th><th>耗时</th><th>结果摘要</th></tr></thead>
          <tbody>{detail.tool_calls.map(item => <tr key={item.id}>
            <td>{item.round_no}</td><td>{item.server_name}</td><td>{item.tool_name}</td>
            <td>{item.status}</td><td>{item.latency_ms}ms</td><td>{item.result_excerpt || item.error_text || '—'}</td>
          </tr>)}</tbody></table>
      </>}
    </section>}
  </>;
}

function pretty(value: string): string {
  try { return JSON.stringify(JSON.parse(value), null, 2); }
  catch { return value; }
}
