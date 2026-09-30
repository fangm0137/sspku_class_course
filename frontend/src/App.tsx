import { useEffect, useRef, useState, type FormEvent } from 'react';
import { ApiError, ask, download, forgetToken, read, signIn, write, type AskCitation, type Detail, type Material, type User } from './api';

function Mark() { return <span className="brand-mark" aria-hidden="true"><svg viewBox="0 0 32 32"><path d="M6 8h8l2 3 2-3h8v17h-8l-2 2-2-2H6zM16 11v16" /></svg></span>; }
function FileIcon() { return <span className="file-icon" aria-hidden="true"><svg viewBox="0 0 24 28"><path d="M4 2h10l6 6v18H4zM14 2v7h6M8 14h8M8 19h6" /></svg></span>; }
function dateLabel(value: string) { return new Intl.DateTimeFormat('zh-CN', { year: 'numeric', month: 'short', day: 'numeric' }).format(new Date(value)); }

// 一轮问答：问题 + 依据回答 + 引用。引用字段直接来自 /api/ask，n 就是回答里的 [n]。
type Turn = { question: string; answer: string; hits: AskCitation[] };

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
  const [view, setView] = useState<'materials' | 'qa'>('materials');
  const [question, setQuestion] = useState('');
  const [turns, setTurns] = useState<Turn[]>([]);
  const inputRef = useRef<HTMLInputElement>(null);
  const running = useRef(false);

  function signedOut() {
    setUser(null); setKnown(true); setItems([]); setListed(false); setDetail(null); setFile(null);
    setTurns([]); setQuestion(''); setView('materials'); forgetToken();
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

  const navButton = (target: 'materials' | 'qa', label: string, glyph: string) =>
    <button type="button" className={`nav-item ${view === target ? 'active' : ''}`} aria-current={view === target ? 'page' : undefined} onClick={() => setView(target)} disabled={busy}><span aria-hidden="true">{glyph}</span> {label}</button>;

  const materialsView = <>
    <div className="page-heading"><div><span className="eyebrow">CLASS COLLECTION</span><h1>本班教学材料<span className="dot">.</span></h1><p className="muted">课堂的重点，值得再次翻阅。</p></div><button disabled={busy} onClick={() => void act(restore)} className="secondary">{busy ? '处理中…' : '刷新列表'}</button></div>
    {user.role === 'teacher' && <section className="upload-card" aria-labelledby="upload-title"><div><span className="small-icon" aria-hidden="true">↑</span><h2 id="upload-title">分享一份教学材料</h2><p className="muted">上传后，本班学生即可查看与提问。支持 TXT、MD，最大 5 MiB。</p></div><form onSubmit={upload}><label className="file-picker" htmlFor="material-file">选择文件<input ref={inputRef} id="material-file" type="file" accept=".txt,.md" disabled={busy} onChange={e => setFile(e.target.files?.[0] || null)} /></label><span className="file-selection">{file ? file.name : '尚未选择文件'}</span><button className="primary" disabled={busy || !file}>上传材料</button></form></section>}
    <div className="material-layout"><section className="material-list" aria-labelledby="list-title"><div className="section-heading"><h2 id="list-title">全部材料</h2><span>{listed ? `${items.length} 份材料` : '等待加载'}</span></div>
      {!listed ? <div className="empty"><FileIcon /><h3>材料尚未加载</h3><p>请刷新列表重试。</p></div> : items.length === 0 ? <div className="empty"><FileIcon /><h3>还没有教学材料</h3><p>{user.role === 'teacher' ? '上传第一份材料，与班级开始分享。' : '教师上传后，材料会出现在这里。'}</p></div> : <ul>{items.map(item => <li key={item.id}><button disabled={busy} className={`material-row ${detail?.id === item.id ? 'selected' : ''}`} onClick={() => void act(async () => {setDetail(await read<Detail>(`/api/materials/${item.id}`));})}><FileIcon /><span className="material-info"><strong>{item.title}</strong><small>{item.title.toLowerCase().endsWith('.md') ? 'Markdown' : '文本材料'}<span>·</span>{dateLabel(item.created_at)}</small></span><span className="row-arrow" aria-hidden="true">↗</span></button></li>)}</ul>}
    </section><section className="detail-panel" aria-labelledby="detail-title">{detail ? <><div className="detail-heading"><span className="eyebrow">材料预览</span><button className="text-button" disabled={busy} onClick={() => setDetail(null)}>关闭预览</button></div><h2 id="detail-title">{detail.title}</h2><p className="muted">{dateLabel(detail.created_at)} · 原文内容</p><button className="secondary" disabled={busy} onClick={() => void act(async () => download(detail))}>下载原文件 ↓</button><pre data-testid="material-text">{detail.text}</pre></> : <div className="preview-empty"><span aria-hidden="true">⌑</span><h2 id="detail-title">留一点时间，读一份材料</h2><p>选择左侧材料，即可在这里阅读正文<br />或下载原文件。</p></div>}</section></div>
  </>;

  const qaView = <>
    <div className="page-heading"><div><span className="eyebrow">KNOWLEDGE QA</span><h1>知识问答<span className="dot">.</span></h1><p className="muted">每个问题先在本班做检索，取前 4 条切片再生成简短回答，并标出 [1]、[2]。没有命中时不调用模型，直接说明资料里没有。</p></div><button disabled={busy || turns.length === 0} onClick={() => setTurns([])} className="secondary">清空对话</button></div>
    <section className="ask-panel" aria-labelledby="ask-title">
      <h2 id="ask-title" className="ask-title">依据回答</h2>
      {turns.length === 0 ? <div className="preview-empty"><span aria-hidden="true">⌑</span><h2>问一个本班材料里的问题</h2><p>回答只依据检索到的切片，并逐条标出出处。<br />没有依据时如实说明，不编造。</p></div> : <div className="ask-thread" data-testid="ask-thread">{turns.map((turn, index) => <div className="ask-turn" key={index}>
        <div className="ask-bubble ask-user"><span className="ask-role">你</span><p>{turn.question}</p></div>
        <div className="ask-bubble ask-answer"><span className="ask-role">依据回答</span><p className="ask-text" data-testid="ask-answer">{turn.answer}</p>
          {turn.hits.length > 0 && <ol className="ask-cites">{turn.hits.map(hit => <li key={hit.citation} value={hit.citation}>
            <span className="ask-cite-head">[{hit.citation}] {hit.title} · 切片 {hit.chunk_index} · 偏移 {hit.start_offset}–{hit.end_offset}</span>
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

  return <div className="workspace">
    <aside className="sidebar"><div className="brand"><Mark /><span>CampusClaw</span></div><p className="sidebar-caption">我的学习空间</p><nav className="nav-list" aria-label="功能导航">{navButton('materials', '班级资料', '▤')}{navButton('qa', '知识问答', '✦')}</nav><div className="sidebar-bottom"><span className="status-dot" /> 仅班级成员可见<p>每一次分享，<br />都是下一次学习的起点。</p></div></aside>
    <div className="main-shell"><header className="topbar"><span>班级资料室 <span className="crumb">/ {view === 'qa' ? '知识问答' : '教学材料'}</span></span><div className="identity"><span className="avatar" aria-hidden="true">{user.role === 'teacher' ? '教' : '学'}</span><span data-testid="identity">{user.username}<small>{user.role === 'teacher' ? '教师' : '学生'} · 班级 #{user.class_id}</small></span><button className="text-button" onClick={logout} disabled={busy}>退出登录</button></div></header>
      <main className="content">{feedback}
        {view === 'qa' ? qaView : materialsView}
        <footer className="content-footer">CampusClaw <span>让知识在班级里流动</span></footer>
      </main>
    </div>
  </div>;
}
