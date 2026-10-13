import { useEffect, useRef, useState, type FormEvent } from 'react';
import {
  ApiError, ask, createAgent, createConversation, createSkill, deleteAgent, deleteSkill, download,
  forgetToken, getConversation, getSkill, listAgents, listConversations, listSkills, read,
  reindex, search, sendMessage, setAgentMaterials, setAgentSkills, signIn, updateAgent, updateSkill,
  write,
  type Agent, type AgentInput, type AskCitation, type Citation, type Conversation, type ConversationDetail,
  type Detail, type Material, type ReindexOptions, type SearchHit, type SearchMode, type Skill, type User,
} from './api';

function Mark() { return <span className="brand-mark" aria-hidden="true"><svg viewBox="0 0 32 32"><path d="M6 8h8l2 3 2-3h8v17h-8l-2 2-2-2H6zM16 11v16" /></svg></span>; }
function FileIcon() { return <span className="file-icon" aria-hidden="true"><svg viewBox="0 0 24 28"><path d="M4 2h10l6 6v18H4zM14 2v7h6M8 14h8M8 19h6" /></svg></span>; }
function dateLabel(value: string) { return new Intl.DateTimeFormat('zh-CN', { year: 'numeric', month: 'short', day: 'numeric' }).format(new Date(value)); }

// 一轮问答：问题 + 依据回答 + 引用。引用字段直接来自 /api/ask，n 就是回答里的 [n]。
type Turn = { question: string; answer: string; hits: AskCitation[] };

type View = 'materials' | 'search' | 'qa' | 'chat' | 'agents' | 'skills';

const emptyAgentDraft: AgentInput = { name: '', prompt: '', enabled: true };

// 在数组里增删一个编号，用于绑定技能与材料时的多选。
function toggled(list: number[], id: number): number[] {
  return list.includes(id) ? list.filter(item => item !== id) : [...list, id];
}

export function App() {
  const [user, setUser] = useState<User | null>(null);
  const [known, setKnown] = useState(false);
  const [items, setItems] = useState<Material[]>([]);
  const [listed, setListed] = useState(false);
  const [detail, setDetail] = useState<Detail | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [file, setFile] = useState<File | null>(null);
  const [view, setView] = useState<View>('materials');
  const [question, setQuestion] = useState('');
  const [turns, setTurns] = useState<Turn[]>([]);
  const [query, setQuery] = useState('');
  const [mode, setMode] = useState<SearchMode>('hybrid');
  const [results, setResults] = useState<SearchHit[] | null>(null);
  const [resultMode, setResultMode] = useState<SearchMode>('hybrid');
  const [strategy, setStrategy] = useState<ReindexOptions['strategy']>('auto');

  // 第 5 课：智能体对话。streaming 是正在到达的增量文本，落库后会被服务端返回的消息替换。
  const [chatAgents, setChatAgents] = useState<Agent[]>([]);
  const [conversations, setConversations] = useState<Conversation[]>([]);
  const [conversation, setConversation] = useState<ConversationDetail | null>(null);
  const [selectedAgent, setSelectedAgent] = useState(0);
  const [chatQuestion, setChatQuestion] = useState('');
  const [streaming, setStreaming] = useState('');
  const [streamSkill, setStreamSkill] = useState('');
  const [streamCitations, setStreamCitations] = useState<Citation[]>([]);
  const [streamError, setStreamError] = useState('');

  // 第 5 课：教师侧管理智能体与技能。
  const [agents, setAgents] = useState<Agent[]>([]);
  const [agentDraft, setAgentDraft] = useState<AgentInput>(emptyAgentDraft);
  const [editingAgent, setEditingAgent] = useState(0);
  const [skills, setSkills] = useState<Skill[]>([]);
  const [skillSource, setSkillSource] = useState('');
  const [editingSkill, setEditingSkill] = useState(0);

  const inputRef = useRef<HTMLInputElement>(null);
  const running = useRef(false);

  function signedOut() {
    setUser(null); setKnown(true); setItems([]); setListed(false); setDetail(null); setFile(null);
    setTurns([]); setQuestion(''); setView('materials'); forgetToken();
    setQuery(''); setResults(null); setMode('hybrid'); setStrategy('auto');
    setChatAgents([]); setConversations([]); setConversation(null); setSelectedAgent(0);
    setChatQuestion(''); setStreaming(''); setStreamSkill(''); setStreamCitations([]); setStreamError('');
    setAgents([]); setAgentDraft(emptyAgentDraft); setEditingAgent(0);
    setSkills([]); setSkillSource(''); setEditingSkill(0);
  }
  function report(cause: unknown) {
    if (cause instanceof ApiError && cause.status === 401) signedOut();
    setError(cause instanceof ApiError ? cause.message : '网络或服务暂不可用，请稍后重试。');
  }
  async function act(action: () => Promise<void>) {
    if (running.current) return;
    running.current = true; setBusy(true); setError(''); setNotice('');
    try { await action(); } catch (cause) { report(cause); }
    finally { running.current = false; setBusy(false); }
  }
  async function loadList() {
    const result = await read<{items: Material[]}>('/api/materials');
    setItems(result.items); setListed(true);
  }
  async function restore() {
    let current: User;
    try { current = await read<User>('/api/me'); }
    catch (cause) {
      if (cause instanceof ApiError && cause.status === 401) { signedOut(); return; }
      throw cause;
    }
    setUser(current); setKnown(true);
    await loadList();
  }
  useEffect(() => { void act(restore); }, []);
  // 切到某个视图时按需拉取它需要的数据，避免一进资料室就把所有接口都打一遍。
  useEffect(() => {
    if (!user) return;
    if (view === 'chat') openChat();
    else if (view === 'agents') openAgents();
    else if (view === 'skills') openSkills();
  }, [view, user]);

  function login(event: FormEvent) {
    event.preventDefault();
    void act(async () => {
      try {
        // signIn stores the returned bearer token; every later request reuses it.
        const current = await signIn(username, password);
        setUser(current); setKnown(true); setDetail(null); setItems([]); setListed(false); setTurns([]);
        await loadList();
      } finally { setPassword(''); }
    });
  }
  function logout() {
    void act(async () => { await write<{ok: boolean}>('/api/logout'); signedOut(); setNotice('已安全退出登录。'); });
  }
  function upload(event: FormEvent) {
    event.preventDefault();
    if (!file) return;
    void act(async () => {
      if (file.size > 5 * 1024 * 1024) throw new ApiError(413, 'payload_too_large');
      const data = new FormData(); data.append('file', file);
      let result: {material_id: number};
      try { result = await write<{material_id: number}>('/api/materials', data); }
      catch (cause) {
        if (cause instanceof ApiError && (cause.status === 0 || cause.status === 503)) setNotice('若文件已提交，请先刷新列表确认结果；再次上传会生成一份新材料。');
        throw cause;
      }
      setFile(null); if (inputRef.current) inputRef.current.value = '';
      setNotice('上传成功，本班成员现在可以查看并提问。');
      await loadList();
      setDetail(await read<Detail>(`/api/materials/${result.material_id}`));
    });
  }
  function sendQuestion(event: FormEvent) {
    event.preventDefault();
    const text = question.trim();
    if (!text) return;
    void act(async () => {
      const reply = await ask(text);
      setTurns(previous => [...previous, { question: text, answer: reply.answer, hits: reply.hits }]);
      setQuestion('');
    });
  }
  // 检索只把词交给服务端：模式与班级都由服务端判定，浏览器不参与过滤。
  function runSearch(event: FormEvent) {
    event.preventDefault();
    const text = query.trim();
    if (!text) return;
    void act(async () => {
      const reply = await search(text, mode);
      setResults(reply.hits); setResultMode(reply.mode);
    });
  }
  function openMaterial(id: number) {
    void act(async () => { setDetail(await read<Detail>(`/api/materials/${id}`)); setView('materials'); });
  }
  // 教师按所选策略重建本材料的切片与向量；旧切片与旧向量会被替换。
  function runReindex() {
    if (!detail) return;
    void act(async () => {
      const options: ReindexOptions = strategy === 'custom'
        ? { strategy: 'custom', size: 600, overlap: 60, collapse_whitespace: true }
        : { strategy };
      const reply = await reindex(detail.id, options);
      setDetail(await read<Detail>(`/api/materials/${reply.material_id}`));
      setResults(null);
      setNotice(`已按 ${reply.strategy} 重建索引：${reply.chunks} 个切片。`);
    });
  }

  /* ─────────── 智能体对话 ─────────── */

  async function refreshConversations() {
    const [available, list] = await Promise.all([listAgents(false), listConversations()]);
    setChatAgents(available); setConversations(list);
    setSelectedAgent(current => (available.some(agent => agent.id === current) ? current : available[0]?.id ?? 0));
  }
  function openChat() {
    void act(async () => {
      await refreshConversations();
      setStreaming(''); setStreamError(''); setStreamSkill(''); setStreamCitations([]);
    });
  }
  function startConversation() {
    if (!selectedAgent) return;
    void act(async () => {
      const created = await createConversation(selectedAgent);
      setConversation(await getConversation(created.id));
      await refreshConversations();
      setNotice('会话已建立，所选智能体在本会话内固定；要换智能体请新建会话。');
    });
  }
  function openConversation(id: number) {
    void act(async () => {
      setConversation(await getConversation(id));
      setStreaming(''); setStreamError(''); setStreamSkill(''); setStreamCitations([]);
    });
  }
  // 提问：先清空上一轮的增量缓冲，逐帧渲染，结束后以服务端重新读取的结果为准。
  function askAgent(event: FormEvent) {
    event.preventDefault();
    const text = chatQuestion.trim();
    const current = conversation;
    if (!current || !text) return;
    void act(async () => {
      setChatQuestion('');
      setStreaming(''); setStreamSkill(''); setStreamCitations([]); setStreamError('');
      let accumulated = '';
      await sendMessage(current.id, text, incoming => {
        switch (incoming.type) {
          case 'meta':
            setStreamCitations(incoming.citations ?? []);
            setStreamSkill(incoming.skill?.name ?? '');
            break;
          case 'delta':
            accumulated += incoming.text;
            setStreaming(accumulated);
            break;
          case 'error':
            setStreamError(incoming.message);
            break;
          default:
            break;
        }
      });
      // 以落库结果覆盖临时缓冲：界面显示的就是服务端真正保存的内容。
      setConversation(await getConversation(current.id));
      setStreaming('');
      await refreshConversations();
    });
  }

  /* ─────────── 教师：智能体与技能 ─────────── */

  // loadAgentData 只取数据，不动表单状态；勾选绑定与保存后都走它，
  // 否则每点一次勾选都会把正在编辑的智能体重置掉。
  async function loadAgentData() {
    const [available, allSkills, materials] = await Promise.all([listAgents(true), listSkills(), read<{items: Material[]}>('/api/materials')]);
    setAgents(available); setSkills(allSkills);
    setItems(materials.items); setListed(true);
  }
  function openAgents() {
    void act(async () => {
      await loadAgentData();
      setAgentDraft(emptyAgentDraft); setEditingAgent(0);
    });
  }
  function editAgent(agent: Agent) {
    setEditingAgent(agent.id);
    setAgentDraft({ name: agent.name, prompt: agent.prompt, enabled: agent.enabled });
  }
  function saveAgent(event: FormEvent) {
    event.preventDefault();
    void act(async () => {
      let target = editingAgent;
      if (target) {
        await updateAgent(target, agentDraft);
        setNotice('智能体已保存。');
      } else {
        target = await createAgent(agentDraft);
        setNotice('智能体已创建，可以在下面勾选它绑定的技能与材料。');
      }
      await loadAgentData();
      setEditingAgent(target);
      const saved = agents.find(item => item.id === target);
      if (saved) setAgentDraft({ name: saved.name, prompt: saved.prompt, enabled: saved.enabled });
    });
  }
  function removeAgent(id: number) {
    void act(async () => {
      await deleteAgent(id);
      setNotice('智能体已删除，它名下的会话与消息一并清理。');
      await loadAgentData();
      if (editingAgent === id) { setEditingAgent(0); setAgentDraft(emptyAgentDraft); }
    });
  }
  function bindAgentSkills(id: number, ids: number[]) {
    void act(async () => { await setAgentSkills(id, ids); await loadAgentData(); });
  }
  function bindAgentMaterials(id: number, ids: number[]) {
    void act(async () => { await setAgentMaterials(id, ids); await loadAgentData(); });
  }

  function openSkills() {
    void act(async () => { setSkills(await listSkills()); setSkillSource(''); setEditingSkill(0); });
  }
  function editSkill(id: number) {
    void act(async () => {
      const loaded = await getSkill(id);
      setEditingSkill(id); setSkillSource(loaded.source_text);
    });
  }
  function saveSkill(event: FormEvent) {
    event.preventDefault();
    const source = skillSource;
    if (!source.trim()) return;
    void act(async () => {
      if (editingSkill) { await updateSkill(editingSkill, source); setNotice('技能已更新，下次提问即按新规程作答。'); }
      else { const id = await createSkill(source); setEditingSkill(id); setNotice('技能已创建。'); }
      setSkills(await listSkills());
    });
  }
  function removeSkill(id: number) {
    void act(async () => {
      await deleteSkill(id);
      setNotice('技能已删除。');
      if (editingSkill === id) { setEditingSkill(0); setSkillSource(''); }
      setSkills(await listSkills());
    });
  }

  const feedback = <>{error && <div role="alert" className="message error">{error}</div>}{notice && <div role="status" className="message success">{notice}</div>}</>;
  if (!known) return <main className="waiting"><Mark /><h1>CampusClaw</h1><p>{busy ? '正在恢复登录状态…' : '暂时无法确认登录状态'}</p>{feedback}<button disabled={busy} onClick={() => void act(restore)}>重新连接</button></main>;
  if (!user) return <main className="login-page">
    <section className="welcome"><div className="brand"><Mark /><span>CampusClaw</span></div><div className="welcome-copy"><span className="eyebrow">一起学习 · 有序分享</span><h1>让每一份资料，<br />回到学习的现场。</h1><p>专属于班级的教学资料空间。<br />连接课堂与课后，让知识随时可读。</p><div className="book-art" aria-hidden="true"><span>课堂笔记</span><span>阅读材料</span><span>CAMPUS<br />COLLECTION</span></div></div><footer>一个班级，一间共享的资料室。</footer></section>
    <section className="login-panel"><div className="login-card"><span className="eyebrow">欢迎回来</span><h2>登录班级资料室</h2><p className="muted">使用已分配的教师或学生账号登录。</p>{feedback}<form onSubmit={login}>
      <label htmlFor="username">账号</label><input id="username" autoComplete="username" required value={username} onChange={e => setUsername(e.target.value)} placeholder="请输入账号" disabled={busy} />
      <label htmlFor="password">密码</label><input id="password" type="password" autoComplete="current-password" required value={password} onChange={e => setPassword(e.target.value)} placeholder="请输入密码" disabled={busy} />
      <button className="primary wide" disabled={busy}>{busy ? '正在登录…' : '登录资料室'}<span aria-hidden="true"> →</span></button>
    </form><p className="login-note">登录后仅能访问所属班级的教学材料。</p></div></section>
  </main>;

  const navButton = (target: View, label: string, glyph: string) =>
    <button type="button" className={`nav-item ${view === target ? 'active' : ''}`} aria-current={view === target ? 'page' : undefined} onClick={() => setView(target)} disabled={busy}><span aria-hidden="true">{glyph}</span> {label}</button>;

  const materialsView = <>
    <div className="page-heading"><div><span className="eyebrow">CLASS COLLECTION</span><h1>本班教学材料<span className="dot">.</span></h1><p className="muted">课堂的重点，值得再次翻阅。</p></div><button disabled={busy} onClick={() => void act(restore)} className="secondary">{busy ? '处理中…' : '刷新列表'}</button></div>
    {user.role === 'teacher' && <section className="upload-card" aria-labelledby="upload-title"><div><span className="small-icon" aria-hidden="true">↑</span><h2 id="upload-title">分享一份教学材料</h2><p className="muted">上传后，本班学生即可查看与提问。支持 TXT、MD，最大 5 MiB。</p></div><form onSubmit={upload}><label className="file-picker" htmlFor="material-file">选择文件<input ref={inputRef} id="material-file" type="file" accept=".txt,.md" disabled={busy} onChange={e => setFile(e.target.files?.[0] || null)} /></label><span className="file-selection">{file ? file.name : '尚未选择文件'}</span><button className="primary" disabled={busy || !file}>上传材料</button></form></section>}
    <div className="material-layout"><section className="material-list" aria-labelledby="list-title"><div className="section-heading"><h2 id="list-title">全部材料</h2><span>{listed ? `${items.length} 份材料` : '等待加载'}</span></div>
      {!listed ? <div className="empty"><FileIcon /><h3>材料尚未加载</h3><p>请刷新列表重试。</p></div> : items.length === 0 ? <div className="empty"><FileIcon /><h3>还没有教学材料</h3><p>{user.role === 'teacher' ? '上传第一份材料，与班级开始分享。' : '教师上传后，材料会出现在这里。'}</p></div> : <ul>{items.map(item => <li key={item.id}><button disabled={busy} className={`material-row ${detail?.id === item.id ? 'selected' : ''}`} onClick={() => void act(async () => {setDetail(await read<Detail>(`/api/materials/${item.id}`));})}><FileIcon /><span className="material-info"><strong>{item.title}</strong><small>{item.title.toLowerCase().endsWith('.md') ? 'Markdown' : '文本材料'}<span>·</span>{dateLabel(item.created_at)}</small></span><span className="row-arrow" aria-hidden="true">↗</span></button></li>)}</ul>}
    </section><section className="detail-panel" aria-labelledby="detail-title">{detail ? <><div className="detail-heading"><span className="eyebrow">材料预览</span><button className="text-button" disabled={busy} onClick={() => setDetail(null)}>关闭预览</button></div><h2 id="detail-title">{detail.title}</h2><p className="muted">{dateLabel(detail.created_at)} · 原文内容</p><button className="secondary" disabled={busy} onClick={() => void act(async () => download(detail))}>下载原文件 ↓</button>{user.role === 'teacher' && <div className="reindex-row"><label htmlFor="reindex-strategy">重建检索索引</label><select id="reindex-strategy" value={strategy} disabled={busy} onChange={e => setStrategy(e.target.value as ReindexOptions['strategy'])}><option value="auto">auto · 800 / 重叠 80</option><option value="hierarchy">hierarchy · 按标题分章</option><option value="custom">custom · 600 / 重叠 60</option></select><button className="secondary" disabled={busy} onClick={runReindex}>{busy ? '处理中…' : '按该策略重建'}</button><span className="muted">改变策略必须显式重建，旧切片与旧向量会被替换。</span></div>}<pre data-testid="material-text">{detail.text}</pre></> : <div className="preview-empty"><span aria-hidden="true">⌑</span><h2 id="detail-title">留一点时间，读一份材料</h2><p>选择左侧材料，即可在这里阅读正文<br />或下载原文件。</p></div>}</section></div>
  </>;

  const searchView = <>
    <div className="page-heading"><div><span className="eyebrow">KNOWLEDGE SEARCH</span><h1>知识检索<span className="dot">.</span></h1><p className="muted">在本班材料里找依据。关键字只看字面命中；向量看语义相近；混合模式同时跑两条路径并按名次融合，是默认模式。</p></div><button disabled={busy || results === null} onClick={() => setResults(null)} className="secondary">清空结果</button></div>
    <section className="search-panel" aria-labelledby="hit-title">
      <form className="search-form" onSubmit={runSearch}>
        <label className="ask-label" htmlFor="search-query">词或一句话问题</label>
        <div className="search-input-row">
          <input id="search-query" value={query} onChange={e => setQuery(e.target.value)} placeholder="例如：磁铁能吸木块吗？" disabled={busy} autoComplete="off" />
          <button className="primary" disabled={busy || !query.trim()}>{busy ? '检索中…' : '检索'}</button>
        </div>
        <fieldset className="mode-group" disabled={busy}>
          <legend>检索模式</legend>
          {(['keyword', 'vector', 'hybrid'] as SearchMode[]).map(item => <label key={item} className={`mode-chip ${mode === item ? 'active' : ''}`}>
            <input type="radio" name="search-mode" value={item} checked={mode === item} onChange={() => setMode(item)} />
            {item === 'keyword' ? '关键字' : item === 'vector' ? '向量' : '混合（默认）'}
          </label>)}
        </fieldset>
      </form>
      <h2 id="hit-title" className="ask-title">命中切片</h2>
      {results === null
        ? <div className="preview-empty"><span aria-hidden="true">⌕</span><h2>还没有检索结果</h2><p>命中会列出材料标题、切片序号、字符区间与摘录，<br />摘录一律取自关系库里的切片正文。</p></div>
        : results.length === 0
          ? <div className="preview-empty"><span aria-hidden="true">⌕</span><h2>没有命中</h2><p>本班材料里没有匹配的切片。<br />其他班级的资料不会出现在结果里。</p></div>
          : <ol className="hit-list" data-testid="hit-list">{results.map(hit => <li key={hit.chunk_id}>
            <div className="hit-head"><strong>{hit.title}</strong><span className="hit-meta">切片 {hit.chunk_index} · 字符 {hit.char_start}–{hit.char_end} · 排序分 {hit.score}</span></div>
            <pre className="hit-text">{hit.excerpt}</pre>
            <button type="button" className="text-button" disabled={busy} onClick={() => openMaterial(hit.material_id)}>打开对应材料 ↗</button>
          </li>)}</ol>}
      {(busy || results !== null) && <p className="ask-pending" role="status" data-testid="search-status">{busy ? '正在检索本班材料…' : `本次使用「${resultMode}」模式，命中 ${results?.length ?? 0} 条。`}</p>}
    </section>
  </>;

  const qaView = <>
    <div className="page-heading"><div><span className="eyebrow">KNOWLEDGE QA</span><h1>知识问答<span className="dot">.</span></h1><p className="muted">每个问题先在本班做检索，取前 4 条切片再生成简短回答，并标出 [1]、[2]。没有命中时不调用模型，直接说明资料里没有。</p></div><button disabled={busy || turns.length === 0} onClick={() => setTurns([])} className="secondary">清空对话</button></div>
    <section className="ask-panel" aria-labelledby="ask-title">
      <h2 id="ask-title" className="ask-title">依据回答</h2>
      {turns.length === 0 ? <div className="preview-empty"><span aria-hidden="true">⌑</span><h2>问一个本班材料里的问题</h2><p>回答只依据检索到的切片，并逐条标出出处。<br />没有依据时如实说明，不编造。</p></div> : <div className="ask-thread" data-testid="ask-thread">{turns.map((turn, index) => <div className="ask-turn" key={index}>
        <div className="ask-bubble ask-user"><span className="ask-role">你</span><p>{turn.question}</p></div>
        <div className="ask-bubble ask-answer"><span className="ask-role">依据回答</span><p className="ask-text" data-testid="ask-answer">{turn.answer}</p>
          {turn.hits.length > 0 && <ol className="ask-cites">{turn.hits.map(hit => <li key={hit.citation} value={hit.citation}>
            <span className="ask-cite-head">[{hit.citation}] {hit.title} · 切片 {hit.chunk_index} · 字符 {hit.char_start}–{hit.char_end}</span>
            <pre className="ask-cite-text">{hit.excerpt}</pre>
          </li>)}</ol>}
        </div>
      </div>)}</div>}
      {busy && <p className="ask-pending" role="status">正在检索本班材料并生成回答…</p>}
      <form className="ask-form" onSubmit={sendQuestion}>
        <label className="ask-label" htmlFor="ask-question">问题</label>
        <div className="ask-input-row">
          <input id="ask-question" value={question} onChange={e => setQuestion(e.target.value)} placeholder="例如：磁铁能吸木块吗？" disabled={busy} autoComplete="off" />
          <button className="primary" disabled={busy || !question.trim()}>{busy ? '处理中…' : '提问'}</button>
        </div>
      </form>
    </section>
  </>;

  // 智能体对话：会话锁定智能体，回答以 SSE 逐段到达，引用列表先于正文。
  const citationList = (list: Citation[]) => list.length === 0 ? null
    : <ol className="ask-cites">{list.map((item, index) => <li key={item.chunk_id} value={index + 1}>
        <span className="ask-cite-head">[{index + 1}] {item.title} · 切片 {item.chunk_index} · 字符 {item.char_start}–{item.char_end}</span>
      </li>)}</ol>;

  const chatView = <>
    <div className="page-heading"><div><span className="eyebrow">TUTOR CHAT</span><h1>智能体对话<span className="dot">.</span></h1><p className="muted">每个智能体有自己的角色提示词，只在自己绑定的技能与材料范围内作答。没有检索到资料时仍会按角色回答，此时正文不带编号。</p></div><button disabled={busy} onClick={openChat} className="secondary">{busy ? '处理中…' : '刷新会话'}</button></div>
    <section className="chat-panel">
      <aside className="chat-side">
        <div className="section-heading"><h2>我的会话</h2><span>{conversations.length} 个</span></div>
        <div className="new-conversation">
          <label htmlFor="chat-agent">新建会话时选择智能体</label>
          <select id="chat-agent" value={selectedAgent} disabled={busy || chatAgents.length === 0} onChange={e => setSelectedAgent(Number(e.target.value))}>
            {chatAgents.length === 0 && <option value={0}>本班暂无已启用的智能体</option>}
            {chatAgents.map(agent => <option key={agent.id} value={agent.id}>{agent.name}</option>)}
          </select>
          <button className="primary" disabled={busy || !selectedAgent} onClick={startConversation}>新建会话</button>
          <p className="muted">会话建立后智能体即固定，要换请新建会话。</p>
        </div>
        <ul className="conversation-list">{(conversations.length === 0 ? [] : conversations).map(item => <li key={item.id}>
          <button type="button" disabled={busy} className={`conversation-row ${conversation?.id === item.id ? 'selected' : ''}`} onClick={() => openConversation(item.id)}>
            <strong>{item.title}</strong><small>智能体 #{item.agent_id} · {dateLabel(item.updated_at)}</small>
          </button>
        </li>)}</ul>
      </aside>
      <div className="chat-main">
        {!conversation
          ? <div className="preview-empty"><span aria-hidden="true">✦</span><h2>先建一个会话</h2><p>选定智能体后开始提问，回答会逐段到达，<br />并附上它依据的材料位置。</p></div>
          : <>
            <div className="chat-heading"><span className="eyebrow">当前会话</span><h2>{conversation.title}</h2><p className="muted">智能体 #{conversation.agent_id} · {conversation.messages.length} 条消息</p></div>
            <div className="chat-thread" data-testid="chat-thread">
              {conversation.messages.map(message => <div className="ask-turn" key={message.id}>
                <div className={`ask-bubble ${message.role === 'user' ? 'ask-user' : 'ask-answer'}`}>
                  <span className="ask-role">{message.role === 'user' ? '你' : '助教'}</span>
                  <p className="ask-text">{message.content}</p>
                  {message.skill_name && <span className="ask-cite-head">本次技能：{message.skill_name}</span>}
                  {message.citations && message.citations.length > 0 && citationList(message.citations)}
                </div>
              </div>)}
              {streaming && <div className="ask-turn"><div className="ask-bubble ask-answer">
                <span className="ask-role">助教{streamSkill ? ` · ${streamSkill}` : ''}</span>
                <p className="ask-text" data-testid="stream-text">{streaming}<span className="caret" aria-hidden="true">▍</span></p>
                {!streamSkill && streamCitations.length > 0 && citationList(streamCitations)}
              </div></div>}
              {!streaming && streamCitations.length > 0 && <div className="ask-turn"><div className="ask-bubble ask-answer">
                <span className="ask-role">本次依据</span>{citationList(streamCitations)}
              </div></div>}
              {streamError && <div role="alert" className="message error">{streamError}</div>}
              {busy && !streaming && <p className="ask-pending" role="status">正在检索并生成回答…</p>}
            </div>
            <form className="ask-form" onSubmit={askAgent}>
              <label className="ask-label" htmlFor="chat-question">提问</label>
              <div className="ask-input-row">
                <input id="chat-question" value={chatQuestion} onChange={e => setChatQuestion(e.target.value)} placeholder="例如：这道方程我下一步该检查什么？" disabled={busy} autoComplete="off" />
                <button className="primary" disabled={busy || !chatQuestion.trim()}>{busy ? '生成中…' : '发送'}</button>
              </div>
            </form>
          </>}
      </div>
    </section>
  </>;

  // 教师：智能体管理（含技能与材料绑定）。
  const boundAgent = agents.find(agent => agent.id === editingAgent) || null;
  const agentsView = <>
    <div className="page-heading"><div><span className="eyebrow">TUTOR AGENTS</span><h1>智能体<span className="dot">.</span></h1><p className="muted">每个智能体由一段提示词、若干技能与若干材料组成。技能与材料都是整体替换：勾选后立即生效。</p></div><button disabled={busy} onClick={openAgents} className="secondary">{busy ? '处理中…' : '刷新'}</button></div>
    <div className="admin-layout">
      <section className="admin-form">
        <div className="section-heading"><h2>{editingAgent ? `编辑智能体 #${editingAgent}` : '新建智能体'}</h2>{editingAgent > 0 && <button type="button" className="text-button" onClick={() => { setEditingAgent(0); setAgentDraft(emptyAgentDraft); }}>改为新建</button>}</div>
        <form onSubmit={saveAgent}>
          <label htmlFor="agent-name">名称（不超过 64 字，班内唯一）</label>
          <input id="agent-name" value={agentDraft.name} maxLength={64} disabled={busy} onChange={e => setAgentDraft({ ...agentDraft, name: e.target.value })} placeholder="例如：数学助教" />
          <label htmlFor="agent-prompt">提示词（不超过 2000 字，留空则用默认角色设定）</label>
          <textarea id="agent-prompt" rows={6} value={agentDraft.prompt} maxLength={2000} disabled={busy} onChange={e => setAgentDraft({ ...agentDraft, prompt: e.target.value })} placeholder="你是班级资料室的数学助教……" />
          <label className="checkbox-row"><input type="checkbox" checked={agentDraft.enabled} disabled={busy} onChange={e => setAgentDraft({ ...agentDraft, enabled: e.target.checked })} /> 启用（停用后学生不能新建使用它的会话）</label>
          <button className="primary" disabled={busy || !agentDraft.name.trim()}>{editingAgent ? '保存修改' : '创建智能体'}</button>
        </form>
      </section>
      <section className="admin-list">
        <div className="section-heading"><h2>本班智能体</h2><span>{agents.length} 个</span></div>
        <ul>{agents.map(agent => <li key={agent.id} className={`admin-row ${editingAgent === agent.id ? 'selected' : ''}`}>
          <div className="admin-row-head"><strong>{agent.name}</strong><span className="hit-meta">{agent.enabled ? '已启用' : '已停用'}</span></div>
          <p className="muted">{agent.prompt ? agent.prompt.slice(0, 60) : '（使用默认角色设定）'}</p>
          <div className="admin-actions">
            <button type="button" className="text-button" disabled={busy} onClick={() => editAgent(agent)}>编辑</button>
            <button type="button" className="text-button" disabled={busy} onClick={() => removeAgent(agent.id)}>删除</button>
          </div>
          {boundAgent && boundAgent.id === agent.id && <>
            <fieldset className="bind-group" disabled={busy}>
              <legend>绑定的技能（只在这些技能里路由）</legend>
              {skills.length === 0 ? <p className="muted">本班还没有技能。</p> : skills.map(item => <label key={item.id} className="checkbox-row">
                <input type="checkbox" checked={agent.skill_ids.includes(item.id)} onChange={() => bindAgentSkills(agent.id, toggled(agent.skill_ids, item.id))} /> {item.name}{item.enabled ? '' : '（已停用）'}
              </label>)}
            </fieldset>
            <fieldset className="bind-group" disabled={busy}>
              <legend>绑定的材料（只在这些材料里检索）</legend>
              {items.length === 0 ? <p className="muted">本班还没有材料。</p> : items.map(item => <label key={item.id} className="checkbox-row">
                <input type="checkbox" checked={agent.material_ids.includes(item.id)} onChange={() => bindAgentMaterials(agent.id, toggled(agent.material_ids, item.id))} /> {item.title}
              </label>)}
            </fieldset>
          </>}
        </li>)}</ul>
      </section>
    </div>
  </>;

  // 教师：技能管理。原文是唯一输入，所有列都由服务端解析。
  const skillsView = <>
    <div className="page-heading"><div><span className="eyebrow">AGENT SKILLS</span><h1>技能<span className="dot">.</span></h1><p className="muted">一份技能就是一个文本文件：空行以上是键行（名称、适用条件、资料、工具、启用），空行以下是引导规程。解析失败会带回行号。</p></div><button disabled={busy} onClick={openSkills} className="secondary">{busy ? '处理中…' : '刷新'}</button></div>
    <div className="admin-layout">
      <section className="admin-form">
        <div className="section-heading"><h2>{editingSkill ? `编辑技能 #${editingSkill}` : '新建技能'}</h2>{editingSkill > 0 && <button type="button" className="text-button" onClick={() => { setEditingSkill(0); setSkillSource(''); }}>改为新建</button>}</div>
        <form onSubmit={saveSkill}>
          <label htmlFor="skill-source">技能文件原文</label>
          <textarea id="skill-source" rows={12} value={skillSource} disabled={busy} onChange={e => setSkillSource(e.target.value)} placeholder={'名称：数学解题引导\n适用条件：方程,去括号,分数\n资料：数学\n工具：knowledge_search\n启用：是\n\n学生正在自己做数学作业……'} />
          <button className="primary" disabled={busy || !skillSource.trim()}>{editingSkill ? '保存修改' : '创建技能'}</button>
        </form>
      </section>
      <section className="admin-list">
        <div className="section-heading"><h2>本班技能</h2><span>{skills.length} 个</span></div>
        <ul>{skills.map(item => <li key={item.id} className={`admin-row ${editingSkill === item.id ? 'selected' : ''}`}>
          <div className="admin-row-head"><strong>{item.name}</strong><span className="hit-meta">{item.enabled ? '已启用' : '已停用'}</span></div>
          <p className="muted">适用条件：{item.trigger_text}{item.subject_text && ` · 资料范围：${item.subject_text}`}</p>
          <div className="admin-actions">
            <button type="button" className="text-button" disabled={busy} onClick={() => editSkill(item.id)}>编辑</button>
            <button type="button" className="text-button" disabled={busy} onClick={() => removeSkill(item.id)}>删除</button>
          </div>
        </li>)}</ul>
      </section>
    </div>
  </>;

  const crumb = view === 'qa' ? '知识问答' : view === 'search' ? '知识检索' : view === 'chat' ? '智能体对话' : view === 'agents' ? '智能体' : view === 'skills' ? '技能' : '教学材料';

  return <div className="workspace">
    <aside className="sidebar"><div className="brand"><Mark /><span>CampusClaw</span></div><p className="sidebar-caption">我的学习空间</p><nav className="nav-list" aria-label="功能导航">{navButton('materials', '班级资料', '▤')}{navButton('search', '知识检索', '⌕')}{navButton('qa', '知识问答', '✦')}{navButton('chat', '智能体对话', '✎')}{user.role === 'teacher' && navButton('agents', '智能体', '◈')}{user.role === 'teacher' && navButton('skills', '技能', '⚙')}</nav><div className="sidebar-bottom"><span className="status-dot" /> 仅班级成员可见<p>每一次分享，<br />都是下一次学习的起点。</p></div></aside>
    <div className="main-shell"><header className="topbar"><span>班级资料室 <span className="crumb">/ {crumb}</span></span><div className="identity"><span className="avatar" aria-hidden="true">{user.role === 'teacher' ? '教' : '学'}</span><span data-testid="identity">{user.username}<small>{user.role === 'teacher' ? '教师' : '学生'} · 班级 #{user.class_id}</small></span><button className="text-button" onClick={logout} disabled={busy}>退出登录</button></div></header>
      <main className="content">{feedback}
        {view === 'qa' ? qaView
          : view === 'search' ? searchView
            : view === 'chat' ? chatView
              : view === 'agents' ? agentsView
                : view === 'skills' ? skillsView
                  : materialsView}
        <footer className="content-footer">CampusClaw <span>让知识在班级里流动</span></footer>
      </main>
    </div>
  </div>;
}
