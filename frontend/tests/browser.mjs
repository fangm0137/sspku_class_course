import { chromium, request, expect as baseExpect } from '@playwright/test';
import { createServer } from 'vite';
import { mkdir, readFile } from 'node:fs/promises';
import { existsSync } from 'node:fs';
import path from 'node:path';

// One-time credentials are supplied by the isolated Go harness, never stored.
const password = process.env.CAMPUSCLAW_TEST_PASSWORD;
if (!password || !process.env.CAMPUSCLAW_TEST_TLS || !process.env.CAMPUSCLAW_API_TARGET) {
  throw new Error('Use backend/scripts/verify-schema.ps1 -Suite frontend');
}
const expect = baseExpect.configure({ timeout: 10000 });
let stage = 'start', vite, browser, tls;
const errors = [];
function check(value, message) { if (!value) throw new Error(message); }
function passed(name) { console.log(`PASS ${name}`); }
const isToken = value => typeof value === 'string' && /^ey[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+$/.test(value);
async function storedToken(page) { return page.evaluate(() => localStorage.getItem('campusclaw_token')); }
try {
  vite = await createServer({ server: { port: 0, strictPort: false }, logLevel: 'silent' });
  await vite.listen();
  const address = vite.httpServer.address();
  const base = `http://127.0.0.1:${address.port}`;
  const edge = process.env.CAMPUSCLAW_BROWSER || 'C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe';
  browser = await chromium.launch({ headless: true, ...(existsSync(edge) ? { executablePath: edge } : {}) });
  console.log(`Browser ${browser.version()}`);
  await mkdir('test-results', { recursive: true });
  const clients = {};
  async function login(name) {
    const context = await browser.newContext({ viewport: { width: 1440, height: 960 }, acceptDownloads: true });
    const page = await context.newPage(); page.on('pageerror', () => errors.push(true));
    await page.goto(base); await expect(page.getByRole('heading', { name: '登录班级资料室' })).toBeVisible();
    if (name === 'teacher_a') await page.screenshot({ path: 'test-results/login.png', fullPage: true });
    // Every protected read must carry a bearer token; nothing may ride in a cookie.
    const authorized = [];
    const listener = plain => {
      if (new URL(plain.url()).pathname.startsWith('/api/materials')) authorized.push(plain.headers()['authorization'] || '');
    };
    page.on('request', listener);
    await page.getByLabel('账号', { exact: true }).fill(name);
    await page.getByLabel('密码', { exact: true }).fill(password);
    await page.getByRole('button', { name: '登录资料室' }).click();
    await expect(page.getByTestId('identity')).toContainText(name);
    await expect(page.getByRole('button', { name: '刷新列表' })).toBeEnabled();
    await expect.poll(() => authorized.length).toBeGreaterThan(0); page.off('request', listener);
    check(authorized.every(header => header.startsWith('Bearer ') && isToken(header.slice(7))), 'protected request missing a bearer token');
    check(isToken(await storedToken(page)), 'token was not stored for session restoration');
    check(await page.evaluate(() => document.cookie === '' && sessionStorage.length === 0), 'credential leaked into a cookie or session storage');
    clients[name] = { context, page }; return clients[name];
  }
  stage = 'token login and session restoration';
  const teacher = await login('teacher_a');
  await teacher.page.reload(); await expect(teacher.page.getByTestId('identity')).toContainText('teacher_a');
  await expect(teacher.page.getByRole('heading', { name: '还没有教学材料' })).toBeVisible();
  passed('bearer token login, Authorization header on reads, refresh restores from localStorage');

  stage = 'upload and class views';
  const student = await login('student_a1'); const other = await login('student_b1');
  await expect(student.page.getByLabel('选择文件')).toHaveCount(0); await expect(other.page.getByRole('button', { name: '上传材料' })).toHaveCount(0);
  const ids = [];
  for (const name of ['class-a-intro.txt', 'class-a-notes.md']) {
    await teacher.page.getByLabel('选择文件').setInputFiles(path.resolve('../backend/fixtures', name));
    const response = teacher.page.waitForResponse(res => new URL(res.url()).pathname === '/api/materials' && res.request().method() === 'POST');
    await teacher.page.getByRole('button', { name: '上传材料' }).click();
    const res = await response; check(res.status() === 201, 'upload status'); ids.push((await res.json()).material_id);
    const created = await (await teacher.context.request.get(`${base}/api/materials/${ids.at(-1)}`, { headers: { Authorization: `Bearer ${await storedToken(teacher.page)}` } })).json();
    check(Math.abs(Date.now() - Date.parse(created.created_at)) < 60000, 'upload timestamp has timezone drift');
    await expect(teacher.page.getByRole('heading', { name, exact: true })).toBeVisible();
    await expect(teacher.page.getByRole('button', { name: '刷新列表' })).toBeEnabled();
    await student.page.getByRole('button', { name: '刷新列表' }).click();
    await expect(student.page.getByRole('button', { name: new RegExp(name.replace('.', '\\.')) })).toBeVisible();
    await other.page.getByRole('button', { name: '刷新列表' }).click();
    await expect(other.page.getByRole('button', { name: '刷新列表' })).toBeEnabled();
    await expect(other.page.getByRole('button', { name: new RegExp(name.replace('.', '\\.')) })).toHaveCount(0);
  }
  await expect(teacher.page.getByTestId('material-text')).toContainText('# A notes');
  await teacher.page.screenshot({ path: 'test-results/teacher.png', fullPage: true });
  await student.page.getByRole('button', { name: /class-a-notes.md/ }).click();
  await expect(student.page.getByTestId('material-text')).toContainText('CLASS_A_MD_ONLY');
  const downloaded = student.page.waitForEvent('download');
  await student.page.getByRole('button', { name: '下载原文件' }).click();
  const download = await downloaded; const stream = await download.createReadStream(); const chunks = [];
  for await (const chunk of stream) chunks.push(chunk);
  check(Buffer.concat(chunks).equals(await readFile('../backend/fixtures/class-a-notes.md')), 'download bytes');
  check(download.suggestedFilename() === 'class-a-notes.md', 'download name');
  const otherToken = await storedToken(other.page);
  const teacherToken = await storedToken(teacher.page);
  for (const id of ids) {
    for (const suffix of ['', '/file']) {
      check((await other.context.request.get(`${base}/api/materials/${id}${suffix}`, { headers: { Authorization: `Bearer ${otherToken}` } })).status() === 404, 'cross-class access');
      check((await other.context.request.get(`${base}/api/materials/${id}${suffix}`)).status() === 401, 'anonymous access');
    }
  }
  const bItems = await (await other.context.request.get(`${base}/api/materials`, { headers: { Authorization: `Bearer ${otherToken}` } })).json();
  check(bItems.items.length === 1, 'B list changed');
  for (const token of [teacherToken, await storedToken(student.page)]) check((await other.context.request.get(`${base}/api/materials/${bItems.items[0].id}`, { headers: { Authorization: `Bearer ${token}` } })).status() === 404, 'A read B');
  const forged = await other.context.request.post(`${base}/api/materials`, { headers: { Authorization: 'Bearer eyJhbGciOiJub25lIn0.e30.' }, multipart: { file: { name: 'forged.txt', mimeType: 'text/plain', buffer: Buffer.from('forged') } } });
  check(forged.status() === 401, 'forged token accepted');
  const forbidden = await other.context.request.post(`${base}/api/materials?role=teacher&class_id=999`, { headers: { Authorization: `Bearer ${otherToken}` }, multipart: { file: { name: 'forged.txt', mimeType: 'text/plain', buffer: Buffer.from('forged') } } });
  check(forbidden.status() === 403, 'student API upload accepted');
  passed('teacher TXT/MD upload, same-class refresh/read/download, both-way isolation, forged-token 401 and student 403');

  stage = 'untrusted text and mobile layout';
  const scriptText = '<script>window.campusXss=1</script>\n<img src=x onerror="window.campusXss=1">';
  await teacher.page.getByLabel('选择文件').setInputFiles({ name: 'safe-text.md', mimeType: 'text/plain', buffer: Buffer.from(scriptText) });
  await teacher.page.getByRole('button', { name: '上传材料' }).click();
  await expect(teacher.page.getByTestId('material-text')).toHaveText(scriptText);
  check(await teacher.page.evaluate(() => window.campusXss === undefined && document.querySelector('[data-testid="material-text"] script') === null && document.querySelector('[data-testid="material-text"] img') === null), 'text executed');
  await student.page.setViewportSize({ width: 390, height: 844 });
  check(await student.page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), 'mobile horizontal overflow');
  await student.page.screenshot({ path: 'test-results/student-mobile.png', fullPage: true });
  passed('escaped script text, attachment bytes and mobile layout');

  stage = '503 preserves identity and retry';
  const unavailable = route => route.fulfill({ status: 503, contentType: 'application/json', body: JSON.stringify({ error: 'service_unavailable', message: '服务暂不可用' }) });
  await teacher.page.route('**/api/me', unavailable);
  await teacher.page.getByRole('button', { name: '刷新列表' }).click();
  await expect(teacher.page.getByRole('alert')).toContainText('服务暂不可用');
  await expect(teacher.page.getByTestId('identity')).toContainText('teacher_a');
  await expect(teacher.page.getByRole('heading', { name: '登录班级资料室' })).toHaveCount(0);
  await teacher.page.unroute('**/api/me', unavailable);
  await teacher.page.getByRole('button', { name: '刷新列表' }).click(); await expect(teacher.page.getByRole('alert')).toHaveCount(0);
  await expect(teacher.page.getByRole('button', { name: '刷新列表' })).toBeEnabled();
  await teacher.page.route('**/api/logout', unavailable);
  await teacher.page.getByRole('button', { name: '退出登录' }).click();
  await expect(teacher.page.getByRole('alert')).toContainText('服务暂不可用'); await expect(teacher.page.getByTestId('identity')).toContainText('teacher_a');
  await teacher.page.unroute('**/api/logout', unavailable);
  const cold = await browser.newContext(); const coldPage = await cold.newPage(); await coldPage.route('**/api/me', unavailable); await coldPage.goto(base);
  await expect(coldPage.getByText('暂时无法确认登录状态')).toBeVisible(); await expect(coldPage.getByRole('heading', { name: '登录班级资料室' })).toHaveCount(0);
  await coldPage.unroute('**/api/me', unavailable); await coldPage.getByRole('button', { name: '重新连接' }).click(); await expect(coldPage.getByRole('heading', { name: '登录班级资料室' })).toBeVisible(); await cold.close();
  passed('injected 503 keeps identity, failed logout stays signed in, initial unknown state and recovery');

  stage = 'logout clears the stored token';
  await teacher.page.getByRole('button', { name: '退出登录' }).click(); await expect(teacher.page.getByRole('heading', { name: '登录班级资料室' })).toBeVisible();
  check(await storedToken(teacher.page) === null, 'logout kept the stored token');
  check(await teacher.page.evaluate(() => document.cookie === ''), 'logout left an identity cookie behind');
  const studentLogout = await student.context.request.post(`${base}/api/logout`, { headers: { Authorization: `Bearer ${await storedToken(student.page)}` } });
  check(studentLogout.status() === 200, 'external bearer logout failed');
  check((await student.context.request.post(`${base}/api/logout`)).status() === 401, 'anonymous logout accepted');
  passed('logout clears the stored token, bearer logout returns 200 and anonymous logout is 401');

  stage = 'HTTPS bearer token';
  tls = await request.newContext({ baseURL: process.env.CAMPUSCLAW_TEST_TLS, ignoreHTTPSErrors: true });
  const tlsLogin = await tls.post('/api/login', { data: { username: 'teacher_a', password } });
  check(tlsLogin.status() === 200, 'TLS login failed');
  const tlsToken = (await tlsLogin.json()).token;
  check(isToken(tlsToken), 'TLS login returned no usable token');
  check(!tlsLogin.headers()['set-cookie'], 'login must not set a session cookie');
  check((await tls.get('/api/me', { headers: { Authorization: `Bearer ${tlsToken}` } })).status() === 200, 'TLS token rejected');
  check((await tls.get('/api/me')).status() === 401, 'anonymous TLS identity accepted');
  check(errors.length === 0, 'browser runtime errors');
  passed('direct HTTPS bearer token, no cookie, and no browser runtime errors');
  passed('all frontend browser checks');
} catch {
  // Do not print assertion operands, traces, request headers, credentials or snapshots.
  console.error(`FAIL browser verification at: ${stage}`); process.exitCode = 1;
} finally {
  await tls?.dispose(); await browser?.close(); await vite?.close();
}
