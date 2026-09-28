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
    const tokens = [];
    const listener = async response => {
      if (response.url().endsWith('/api/csrf') && response.status() === 200) tokens.push((await response.json()).csrf_token);
    };
    page.on('response', listener);
    await page.getByLabel('账号', { exact: true }).fill(name);
    await page.getByLabel('密码', { exact: true }).fill(password);
    await page.getByRole('button', { name: '登录资料室' }).click();
    await expect(page.getByTestId('identity')).toContainText(name);
    await expect(page.getByRole('button', { name: '刷新列表' })).toBeEnabled();
    await expect.poll(() => tokens.length).toBe(2); page.off('response', listener);
    check(tokens[0] !== tokens[1], 'token did not rotate after login');
    const oldStatus = await page.evaluate(async token => (await fetch('/api/logout', { method: 'POST', headers: { 'X-CSRF-Token': token } })).status, tokens[0]);
    check(oldStatus === 403, 'old token accepted');
    const cookie = (await context.cookies()).find(cookie => cookie.name === 'campusclaw_session');
    check(cookie && cookie.httpOnly && cookie.sameSite === 'Lax' && cookie.path === '/' && !cookie.secure && cookie.value.length === 43, 'HTTP cookie attributes');
    check(await page.evaluate(() => localStorage.length === 0 && sessionStorage.length === 0 && !document.cookie.includes('campusclaw_session')), 'credential persisted in JS storage');
    clients[name] = { context, page }; return clients[name];
  }
  stage = 'login and session restoration';
  const teacher = await login('teacher_a');
  await teacher.page.reload(); await expect(teacher.page.getByTestId('identity')).toContainText('teacher_a');
  await expect(teacher.page.getByRole('heading', { name: '还没有教学材料' })).toBeVisible();
  passed('T6.1 login, token rotation, old-token 403, HTTP Cookie, refresh and no Web Storage');

  stage = 'upload and class views';
  const student = await login('student_a1'); const other = await login('student_b1');
  await expect(student.page.getByLabel('选择文件')).toHaveCount(0); await expect(other.page.getByRole('button', { name: '上传材料' })).toHaveCount(0);
  const ids = [];
  for (const name of ['class-a-intro.txt', 'class-a-notes.md']) {
    await teacher.page.getByLabel('选择文件').setInputFiles(path.resolve('../backend/fixtures', name));
    const response = teacher.page.waitForResponse(res => new URL(res.url()).pathname === '/api/materials' && res.request().method() === 'POST');
    await teacher.page.getByRole('button', { name: '上传材料' }).click();
    const res = await response; check(res.status() === 201, 'upload status'); ids.push((await res.json()).material_id);
    const created = await (await teacher.context.request.get(`${base}/api/materials/${ids.at(-1)}`)).json();
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
  for (const id of ids) {
    for (const suffix of ['', '/file']) {
      const res = await other.context.request.get(`${base}/api/materials/${id}${suffix}`); check(res.status() === 404, 'cross-class access');
    }
  }
  const bItems = await (await other.context.request.get(`${base}/api/materials`)).json();
  check(bItems.items.length === 1, 'B list changed');
  for (const client of [teacher, student]) check((await client.context.request.get(`${base}/api/materials/${bItems.items[0].id}`)).status() === 404, 'A read B');
  const forgedToken = (await (await other.context.request.get(`${base}/api/csrf`)).json()).csrf_token;
  const forbidden = await other.context.request.post(`${base}/api/materials?role=teacher&class_id=999`, { headers: { Origin: base, 'X-CSRF-Token': forgedToken }, multipart: { file: { name: 'forged.txt', mimeType: 'text/plain', buffer: Buffer.from('forged') } } });
  check(forbidden.status() === 403, 'student API upload accepted');
  passed('T6.2 teacher TXT/MD upload, same-class refresh/read/download, both-way isolation and student controls');

  stage = 'untrusted text and mobile layout';
  const scriptText = '<script>window.campusXss=1</script>\n<img src=x onerror="window.campusXss=1">';
  await teacher.page.getByLabel('选择文件').setInputFiles({ name: 'safe-text.md', mimeType: 'text/plain', buffer: Buffer.from(scriptText) });
  await teacher.page.getByRole('button', { name: '上传材料' }).click();
  await expect(teacher.page.getByTestId('material-text')).toHaveText(scriptText);
  check(await teacher.page.evaluate(() => window.campusXss === undefined && document.querySelector('[data-testid="material-text"] script') === null && document.querySelector('[data-testid="material-text"] img') === null), 'text executed');
  await student.page.setViewportSize({ width: 390, height: 844 });
  check(await student.page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), 'mobile horizontal overflow');
  await student.page.screenshot({ path: 'test-results/student-mobile.png', fullPage: true });
  passed('T6.2 escaped script text, attachment bytes and mobile layout');

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
  passed('T6.1 injected 503 keeps identity, failed logout stays signed in, initial unknown state and recovery');

  stage = 'logout and expired UI session';
  const oldCookies = await teacher.context.cookies();
  await teacher.page.getByRole('button', { name: '退出登录' }).click(); await expect(teacher.page.getByRole('heading', { name: '登录班级资料室' })).toBeVisible();
  const replay = await browser.newContext(); await replay.addCookies(oldCookies); check((await replay.request.get(`${base}/api/me`)).status() === 401, 'logout replay accepted'); await replay.close();
  check(!(await teacher.context.cookies()).some(cookie => cookie.name === 'campusclaw_session'), 'logout cookie not cleared');
  const studentToken = (await (await student.context.request.get(`${base}/api/csrf`)).json()).csrf_token;
  check((await student.context.request.post(`${base}/api/logout`, { headers: {Origin: base, 'X-CSRF-Token': studentToken} })).status() === 200, 'external logout failed');
  await student.page.getByRole('button', { name: '刷新列表' }).click(); await expect(student.page.getByRole('heading', { name: '登录班级资料室' })).toBeVisible();
  passed('T6.1 logout clears Cookie, old Cookie 401, UI clears materials on confirmed session loss');

  stage = 'HTTPS Cookie';
  tls = await request.newContext({ baseURL: process.env.CAMPUSCLAW_TEST_TLS, ignoreHTTPSErrors: true });
  const token = (await (await tls.get('/api/csrf')).json()).csrf_token;
  const tlsLogin = await tls.post('/api/login', { headers: { Origin: process.env.CAMPUSCLAW_TEST_TLS, 'X-CSRF-Token': token }, data: { username: 'teacher_a', password } });
  check(tlsLogin.status() === 200, 'TLS login failed'); const cookieHeader = tlsLogin.headers()['set-cookie'];
  check(cookieHeader.includes('Secure') && cookieHeader.includes('HttpOnly') && cookieHeader.includes('SameSite=Lax') && cookieHeader.includes('Path=/') && cookieHeader.includes('Max-Age=28800'), 'TLS cookie attributes');
  check(errors.length === 0, 'browser runtime errors');
  passed('T6.1 direct HTTPS Secure Cookie and no browser runtime errors');
  passed('all frontend browser checks');
} catch {
  // Do not print assertion operands, traces, request headers, credentials or snapshots.
  console.error(`FAIL browser verification at: ${stage}`); process.exitCode = 1;
} finally {
  await tls?.dispose(); await browser?.close(); await vite?.close();
}
