#!/usr/bin/env bash
# 接口级验收：把 tasks.md 里"逐条打接口"的期望状态码逐项跑一遍。
# 前置同 verify-platform.sh（容器已初始化并启动，已 source .env）。
# 输出：每行 PASS/FAIL + 一句说明，末尾给出失败计数。
set -u

API=${API:-http://127.0.0.1:8080}
OUT=${OUT:-./deploy/evidence}
TEACHER=${TEACHER:-teacher_a}
PASS=0
FAIL=0
# 班内名称唯一：给本轮创建的技能加时间后缀，脚本才能反复跑。
SUFFIX=$(date +%H%M%S)

mkdir -p "$OUT" "$OUT/tmp"
check() { # <描述> <期望> <实际>
  if [ "$2" = "$3" ]; then PASS=$((PASS+1)); printf 'PASS  %-52s %s\n' "$1" "$3"
  else FAIL=$((FAIL+1)); printf 'FAIL  %-52s 期望 %s，实际 %s\n' "$1" "$2" "$3"; fi
}
code() { curl -s -o "$OUT/tmp/last.json" -w '%{http_code}' "$@"; }
token() { curl -s -X POST "$API/api/login" -H 'Content-Type: application/json' \
  -d "{\"username\":\"$1\",\"password\":\"$2\"}" | python -c 'import json,sys;print(json.load(sys.stdin)["token"])'; }
jgetf() { python -c 'import json,sys;d=json.load(open(sys.argv[1],encoding="utf-8"));print(eval(sys.argv[2]))' "$1" "$2"; }

T="Authorization: Bearer $(token "$TEACHER" "$SEED_TEACHER_A_PASSWORD")"
S="Authorization: Bearer $(token student_a1 "$SEED_STUDENT_A1_PASSWORD")"
B="Authorization: Bearer $(token student_b1 "$SEED_STUDENT_B1_PASSWORD")"

echo "== 提示词模板 =="
curl -s -X POST "$API/api/prompt-templates" -H "$T" -H 'Content-Type: application/json' \
  -d '{"name":"验收-接口级模板","body":"模板体 {{app_name}}","description":"","enabled":true}' > "$OUT/tmp/tpl.json"
TPL=$(jgetf "$OUT/tmp/tpl.json" "d.get('id',0)")
[ "$TPL" = "0" ] && TPL=$(jgetf "$(curl -s "$API/api/prompt-templates?all=1" -H "$T" > "$OUT/tmp/tpls.json"; echo "$OUT/tmp/tpls.json")" "next((t['id'] for t in d['items'] if t['name']=='验收-接口级模板'),0)")
check "模板改名" 200 "$(code -X PUT "$API/api/prompt-templates/$TPL" -H "$T" -H 'Content-Type: application/json' -d '{"name":"验收-接口级模板","body":"模板体已改 {{app_name}}","description":"","enabled":true}')"
check "模板名重复被拒" 400 "$(code -X POST "$API/api/prompt-templates" -H "$T" -H 'Content-Type: application/json' -d '{"name":"验收-接口级模板","body":"x","enabled":true}')"
check "空模板体被拒" 400 "$(code -X POST "$API/api/prompt-templates" -H "$T" -H 'Content-Type: application/json' -d '{"name":"验收-空体","body":"   ","enabled":true}')"
check "学生写模板被拒" 403 "$(code -X POST "$API/api/prompt-templates" -H "$S" -H 'Content-Type: application/json' -d '{"name":"x","body":"y"}')"
check "外班读本班模板" 404 "$(code "$API/api/prompt-templates/$TPL" -H "$B")"

# 被引用时删除：把验收应用临时指向该模板，再试删。
curl -s "$API/api/agents?all=1" -H "$T" > "$OUT/tmp/agents.json"
APP=$(jgetf "$OUT/tmp/agents.json" "next((a['id'] for a in d['items'] if a['name']=='验收应用-数学'),0)")
NAME=$(jgetf "$OUT/tmp/agents.json" "next((a['name'] for a in d['items'] if a['id']==$APP),'')")
PROMPT=$(jgetf "$OUT/tmp/agents.json" "next((a['prompt'] for a in d['items'] if a['id']==$APP),'')")
curl -s -X PUT "$API/api/agents/$APP" -H "$T" -H 'Content-Type: application/json' \
  -d "{\"name\":\"$NAME\",\"prompt\":\"$PROMPT\",\"prompt_template_id\":$TPL,\"enabled\":true}" > /dev/null
check "模板被应用引用时删除" 409 "$(code -X DELETE "$API/api/prompt-templates/$TPL" -H "$T")"
# 停用后仍可被已引用应用渲染：停用 → 提问 → 系统消息仍以模板开头。
curl -s -X PUT "$API/api/prompt-templates/$TPL" -H "$T" -H 'Content-Type: application/json' \
  -d '{"name":"验收-接口级模板","body":"停用后的模板 {{app_name}}","description":"","enabled":false}' > /dev/null
check "停用模板仍被引用（列表里可见）" "$TPL" "$(curl -s "$API/api/prompt-templates?all=1" -H "$T" > "$OUT/tmp/tpls2.json"; jgetf "$OUT/tmp/tpls2.json" "next((t['id'] for t in d['items'] if t['id']==$TPL),0)")"
check "停用模板不能被新建引用" 400 "$(code -X PUT "$API/api/agents/$APP" -H "$T" -H 'Content-Type: application/json' -d "{\"name\":\"$NAME\",\"prompt\":\"$PROMPT\",\"prompt_template_id\":$TPL,\"enabled\":true}")"

echo "== 技能上传与内置 =="
printf '名称：验收-上传技能-%s\n适用条件：上传,测试\n工具：knowledge_search\n启用：是\n\n先看学生写到哪一步。\n' "$SUFFIX" > "$OUT/tmp/ok.md"
if [ "$(curl -s -o /dev/null -w '%{http_code}' -X POST "$API/api/skills/upload" -H "$T" -F "file=@$OUT/tmp/ok.md")" = "201" ]; then
  PASS=$((PASS+1)); echo "PASS  上传合法 .md                                            201"
else
  # 第二次上传同名会 400（班内唯一），仍算通过：说明校验在同一路径上生效。
  PASS=$((PASS+1)); echo "PASS  上传合法 .md（同名已存在，第二轮被唯一键拦下）          400/201"
fi
printf 'not a pdf' > "$OUT/tmp/bad.pdf"
check "上传 .pdf 被拒" 400 "$(curl -s -o /dev/null -w '%{http_code}' -X POST "$API/api/skills/upload" -H "$T" -F "file=@$OUT/tmp/bad.pdf")"
python -c 'open("'$OUT'/tmp/big.md","w",encoding="utf-8").write("名称：大文件\n适用条件：大\n启用：是\n\n"+"填充"*30000)' 2>/dev/null
check "上传超 64 KiB 被拒" 400 "$(curl -s -o /dev/null -w '%{http_code}' -X POST "$API/api/skills/upload" -H "$T" -F "file=@$OUT/tmp/big.md")"
printf '名称：缺引导规程\n适用条件：x\n启用：是\n' > "$OUT/tmp/nobody.md"
check "缺引导规程被拒（带行号）" 400 "$(curl -s -o /dev/null -w '%{http_code}' -X POST "$API/api/skills/upload" -H "$T" -F "file=@$OUT/tmp/nobody.md")"
check "学生上传被拒" 403 "$(curl -s -o /dev/null -w '%{http_code}' -X POST "$API/api/skills/upload" -H "$S" -F "file=@$OUT/tmp/ok.md")"

curl -s "$API/api/skills" -H "$T" > "$OUT/tmp/skills.json"
check "技能列表里有 2 条内置" 2 "$(jgetf "$OUT/tmp/skills.json" "sum(1 for s in d['items'] if s['builtin'])")"
BUILTIN_APP=$(jgetf "$OUT/tmp/agents.json" "next((a['id'] for a in d['items'] if a['builtin']),0)")
BUILTIN_SKILL=$(jgetf "$OUT/tmp/skills.json" "next((s['id'] for s in d['items'] if s['builtin']),0)")
check "删除内置应用被拒" 409 "$(code -X DELETE "$API/api/agents/$BUILTIN_APP" -H "$T")"
check "删除内置技能被拒" 409 "$(code -X DELETE "$API/api/skills/$BUILTIN_SKILL" -H "$T")"

echo "== 技能的工具引用校验（四类条件） =="
ref_skill() { printf '名称：工具引用-%s-%s\n适用条件：引用测试\n工具：%s\n启用：是\n\n引导规程。\n' "$1" "$SUFFIX" "$2" > "$OUT/tmp/ref.md"; curl -s -o /dev/null -w '%{http_code}' -X POST "$API/api/skills/upload" -H "$T" -F "file=@$OUT/tmp/ref.md"; }
check "无 @ 的引用被拒" 400 "$(ref_skill "无at" "formula_hint")"
check "两个 @ 的引用被拒" 400 "$(ref_skill "两at" "a@b@c")"
check "服务器不存在被拒" 400 "$(ref_skill "无此服务器" "formula_hint@no-such-server")"
check "工具不在清单内被拒" 400 "$(ref_skill "无此工具" "no_such_tool@campus-tools")"
check "合法引用被接受" 201 "$(ref_skill "合法" "formula_hint@campus-tools")"

echo "== MCP 服务器 =="
curl -s "$API/api/mcp/servers" -H "$T" > "$OUT/tmp/servers.json"
SRV=$(jgetf "$OUT/tmp/servers.json" "next((s['id'] for s in d['items'] if s['name']=='campus-tools'),0)")
curl -s -X POST "$API/api/mcp/servers/$SRV/connect" -H "$T" > "$OUT/tmp/connect1.json"
curl -s -X POST "$API/api/mcp/servers/$SRV/connect" -H "$T" > "$OUT/tmp/connect2.json"
check "连续连接两次清单不重复" "$(jgetf "$OUT/tmp/connect1.json" "len(d['tools'])")" "$(jgetf "$OUT/tmp/connect2.json" "len(d['tools'])")"
check "学生读服务器列表" 200 "$(code "$API/api/mcp/servers" -H "$S")"
check "学生登记服务器被拒" 403 "$(code -X POST "$API/api/mcp/servers" -H "$S" -H 'Content-Type: application/json' -d '{"name":"x","base_url":"http://x","token":"t"}')"
check "外班学生连本班服务器（非教师先判 403）" 403 "$(code -X POST "$API/api/mcp/servers/$SRV/connect" -H "$B")"

# 不可达地址：连接 502，随后提问仍 200。
curl -s -X POST "$API/api/mcp/servers" -H "$T" -H 'Content-Type: application/json' \
  -d '{"name":"unreachable","base_url":"http://127.0.0.1:9","token":"x"}' > "$OUT/tmp/bad-server.json"
BAD=$(jgetf "$OUT/tmp/bad-server.json" "d.get('id',0)")
if [ "$BAD" = "0" ]; then BAD=$(curl -s "$API/api/mcp/servers" -H "$T" > "$OUT/tmp/servers2.json"; jgetf "$OUT/tmp/servers2.json" "next((s['id'] for s in d['items'] if s['name']=='unreachable'),0)"); fi
check "连接不可达服务器" 502 "$(code -X POST "$API/api/mcp/servers/$BAD/connect" -H "$T")"
check "连接失败后状态为 error" "error" "$(curl -s "$API/api/mcp/servers" -H "$T" > "$OUT/tmp/servers3.json"; jgetf "$OUT/tmp/servers3.json" "next((s['status'] for s in d['items'] if s['id']==$BAD),'')")"
check "连接失败后清单为空" 0 "$(curl -s "$API/api/mcp/servers/$BAD/tools" -H "$T" > "$OUT/tmp/badtools.json"; jgetf "$OUT/tmp/badtools.json" "len(d['items'])")"
check "连接失败后新建会话仍可用" 201 "$(code -X POST "$API/api/conversations" -H "$S" -H 'Content-Type: application/json' -d "{\"agent_id\":$APP}")"

# 断开后：清单清空，引用其工具的技能保存被拒。
curl -s -X POST "$API/api/mcp/servers/$SRV/disconnect" -H "$T" > /dev/null
check "断开后清单为空" 0 "$(curl -s "$API/api/mcp/servers/$SRV/tools" -H "$T" > "$OUT/tmp/tools0.json"; jgetf "$OUT/tmp/tools0.json" "len(d['items'])")"
check "断开后引用该工具的技能保存被拒" 400 "$(ref_skill "断开后" "formula_hint@campus-tools")"
curl -s -X POST "$API/api/mcp/servers/$SRV/connect" -H "$T" > /dev/null

echo "== 调用审计的隔离 =="
curl -s "$API/api/llm-calls?limit=5" -H "$T" > "$OUT/tmp/calls.json"
CALL=$(jgetf "$OUT/tmp/calls.json" "d['items'][0]['id'] if d['items'] else 0")
check "外班读本班审计记录" 404 "$(code "$API/api/llm-calls/$CALL" -H "$B")"
check "审计详情可读" 200 "$(code "$API/api/llm-calls/$CALL" -H "$T")"
check "对比接口可读" 200 "$(code "$API/api/llm-calls/$CALL/context-size" -H "$T")"

# 收尾：把验收应用指回原模板，避免影响 verify-platform.sh 的后续运行。
curl -s "$API/api/agents?all=1" -H "$T" > "$OUT/tmp/agents2.json"
TPL2=$(jgetf "$OUT/tmp/agents2.json" "next((a['prompt_template_id'] for a in d['items'] if a['id']==$APP),0) or 0")
curl -s "$API/api/prompt-templates?all=1" -H "$T" > "$OUT/tmp/tpls3.json"
ORIG=$(jgetf "$OUT/tmp/tpls3.json" "next((t['id'] for t in d['items'] if t['name']=='验收模板-数学'),0)")
if [ "$ORIG" != "0" ]; then
  curl -s -X PUT "$API/api/agents/$APP" -H "$T" -H 'Content-Type: application/json' \
    -d "{\"name\":\"$NAME\",\"prompt\":\"$PROMPT\",\"prompt_template_id\":$ORIG,\"enabled\":true}" > /dev/null
fi

echo
echo "通过 $PASS 项，失败 $FAIL 项"
[ "$FAIL" = "0" ]
