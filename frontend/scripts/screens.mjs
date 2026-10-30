// 用已安装的 Edge（channel: msedge）给五个新视图截图，作为报告的界面证据。
// 不下载浏览器：Playwright 直接驱动本机 Edge。
import { chromium } from 'playwright';

const BASE = process.env.API || 'http://127.0.0.1:8080';
const OUT = process.env.OUT || './deploy/evidence/screens';
const TEACHER = process.env.TEACHER || 'teacher_a';
const PASSWORD = process.env.SEED_TEACHER_A_PASSWORD;
const STUDENT = process.env.STUDENT || 'student_a1';
const STUDENT_PASSWORD = process.env.SEED_STUDENT_A1_PASSWORD;

if (!PASSWORD || !STUDENT_PASSWORD) {
  console.error('缺少 SEED_TEACHER_A_PASSWORD / SEED_STUDENT_A1_PASSWORD');
  process.exit(2);
}

const browser = await chromium.launch({ channel: 'msedge' });
const page = await browser.newPage({ viewport: { width: 1500, height: 980 }, deviceScaleFactor: 1 });

async function login(username, password) {
  await page.goto(BASE, { waitUntil: 'domcontentloaded' });
  await page.getByLabel('账号').fill(username);
  await page.getByLabel('密码').fill(password);
  await page.getByRole('button', { name: /登录资料室/ }).click();
  await page.getByTestId('identity').waitFor({ timeout: 20000 });
}

async function shot(name) {
  await page.waitForTimeout(600);
  await page.screenshot({ path: `${OUT}/${name}.png`, fullPage: true });
  console.log('saved', name);
}

await login(TEACHER, PASSWORD);

// AI 应用：先点「组成视图」，让模板/技能/材料/工具四部分同时可见。
await page.getByRole('button', { name: 'AI 应用' }).click();
await page.getByRole('button', { name: '组成视图' }).first().click();
await page.getByText('组成视图 ·').waitFor({ timeout: 15000 });
await shot('01-apps');

// 提示词模板
await page.getByRole('button', { name: '提示词模板' }).click();
await page.getByText('本班模板').waitFor({ timeout: 15000 });
await shot('02-templates');

// MCP 工具：连接后展开工具清单
await page.getByRole('button', { name: 'MCP 工具' }).click();
await page.getByText('本班服务器').waitFor({ timeout: 15000 });
await page.getByRole('button', { name: '查看工具' }).first().click();
await page.getByText('inputSchema').first().waitFor({ timeout: 15000 }).catch(() => {});
await page.waitForTimeout(800);
await shot('03-mcp');

// 调用审计：点开一条带工具调用的记录
await page.getByRole('button', { name: '调用审计' }).click();
await page.getByText('调用审计').first().waitFor({ timeout: 15000 });
const viewButtons = page.getByRole('button', { name: '查看' });
const total = await viewButtons.count();
for (let index = 0; index < total; index += 1) {
  await viewButtons.nth(index).click();
  await page.waitForTimeout(700);
  const text = await page.locator('.audit-detail').innerText().catch(() => '');
  if (text.includes('工具调用')) break;
}
// 审计页很长：整页截图缩到 A4 会看不清，改为"列表视口 + 详情区"两张。
await page.screenshot({ path: `${OUT}/04-audit-list.png` });
await page.locator('.audit-detail').screenshot({ path: `${OUT}/04-audit-detail.png` });
console.log('saved 04-audit-list / 04-audit-detail');

// 智能体对话：以学生身份提一个会触发工具的问题，截到工具栏与正文。
await page.getByRole('button', { name: '退出登录' }).click();
await page.getByRole('button', { name: /登录资料室/ }).waitFor({ timeout: 15000 });
await login(STUDENT, STUDENT_PASSWORD);
await page.getByRole('button', { name: '智能体对话' }).click();
await page.getByText('我的会话').waitFor({ timeout: 15000 });
const rows = page.locator('.conversation-row');
if (await rows.count() > 0) {
  await rows.first().click();
  await page.waitForTimeout(800);
  await page.getByLabel('提问').fill('帮我把 7.5 毫米换算成米，再说下一步该检查什么。');
  await page.getByRole('button', { name: '发送' }).click();
  await page.getByTestId('tool-bar').first().waitFor({ timeout: 90000 }).catch(() => {});
  await page.getByTestId('stream-text').waitFor({ timeout: 90000 }).catch(() => {});
  await page.waitForTimeout(1500);
}
await shot('05-chat-toolbar');

await browser.close();
console.log('done');
