export type User = { id: number; username: string; role: 'teacher' | 'student'; class_id: number };
export type Material = { id: number; title: string; class_id: number; created_at: string };
export type Detail = Material & { text: string };

const messages: Record<string, string> = {
  invalid_credentials: '账号或密码错误，请重新输入。',
  unauthenticated: '登录已失效，请重新登录。',
  csrf_invalid: '请求验证已失效，请重新操作。',
  forbidden: '当前账号没有执行此操作的权限。',
  invalid_request: '请求无效，请检查输入或文件格式。',
  payload_too_large: '文件超过大小限制，请选择不超过 5 MiB 的文件。',
  not_found: '这份材料不存在或不属于你的班级。',
  internal_error: '操作未完成，请稍后重试。',
};

export class ApiError extends Error {
  constructor(public status: number, code = '') {
    super(status === 503 ? '服务暂不可用，请稍后重试。' : messages[code] || '网络或服务暂不可用，请稍后重试。');
  }
}

async function response(path: string, init?: RequestInit): Promise<Response> {
  let res: Response;
  try { res = await fetch(path, { ...init, credentials: 'same-origin', cache: 'no-store' }); }
  catch { throw new ApiError(0); }
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new ApiError(res.status, typeof body.error === 'string' ? body.error : '');
  }
  return res;
}

export async function read<T>(path: string): Promise<T> {
  try { return await (await response(path)).json(); }
  catch (error) { if (error instanceof ApiError) throw error; throw new ApiError(0); }
}
let csrfToken = '';
export function forgetToken() { csrfToken = ''; }
export async function renewToken() { csrfToken = (await read<{csrf_token: string}>('/api/csrf')).csrf_token; }
export async function write<T>(path: string, body?: FormData | { username: string; password: string }): Promise<T> {
  await renewToken();
  const form = body instanceof FormData;
  const headers: Record<string, string> = { 'X-CSRF-Token': csrfToken };
  if (body && !form) headers['Content-Type'] = 'application/json';
  const res = await response(path, { method: 'POST', headers, body: form ? body : body ? JSON.stringify(body) : undefined });
  try { return await res.json(); } catch { throw new ApiError(0); }
}
export async function download(item: Material) {
  const res = await response(`/api/materials/${item.id}/file`);
  const url = URL.createObjectURL(await res.blob());
  const anchor = document.createElement('a');
  anchor.href = url; anchor.download = item.title; document.body.append(anchor); anchor.click(); anchor.remove();
  window.setTimeout(() => URL.revokeObjectURL(url), 1000);
}
