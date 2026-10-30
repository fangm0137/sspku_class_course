#!/usr/bin/env bash
# 个人项目验收的端到端取证脚本。
#
# 前置：容器已按 deploy/README.md 串行初始化（migrate → seed → seed-materials → seed-skills → seed-apps），
#      api / nginx / mcp 已启动，.env 里配了 MCP_AUTH_TOKEN 与三个 SEED_*_PASSWORD。
#
# 产物：$OUT 目录下的原始响应与一份 summary.txt，用于报告里的"运行结果"取证。
set -u

API=${API:-http://127.0.0.1:8080}
OUT=${OUT:-./deploy/evidence}
TEACHER=${TEACHER:-teacher_a}
STUDENT=${STUDENT:-student_a1}
MCP_NAME=${MCP_NAME:-campus-tools}

mkdir -p "$OUT"
log() { printf '\n=== %s ===\n' "$*" | tee -a "$OUT/summary.txt"; }
say() { printf '%s\n' "$*" | tee -a "$OUT/summary.txt"; }

# jget <json> <python 表达式里的 d>：只取一个字段，避免为脚本引入 jq。
jget() { python -c 'import json,sys;d=json.loads(sys.argv[1]);print(eval(sys.argv[2]))' "$1" "$2"; }

login() {
  curl -s -X POST "$API/api/login" -H 'Content-Type: application/json' \
    -d "{\"username\":\"$1\",\"password\":\"$2\"}"
}

: > "$OUT/summary.txt"
log "0. 登录取令牌"
if [ -z "${SEED_TEACHER_A_PASSWORD:-}" ] || [ -z "${SEED_STUDENT_A1_PASSWORD:-}" ]; then
  say "缺少 SEED_TEACHER_A_PASSWORD / SEED_STUDENT_A1_PASSWORD，先 set -a; . ./.env; set +a"
  exit 2
fi
TEACHER_LOGIN=$(login "$TEACHER" "$SEED_TEACHER_A_PASSWORD")
STUDENT_LOGIN=$(login "$STUDENT" "$SEED_STUDENT_A1_PASSWORD")
TEACHER_TOKEN=$(jget "$TEACHER_LOGIN" "d['token']")
STUDENT_TOKEN=$(jget "$STUDENT_LOGIN" "d['token']")
if [ -z "$TEACHER_TOKEN" ] || [ -z "$STUDENT_TOKEN" ]; then
  say "登录失败：检查账号与密码"
  exit 3
fi
say "教师令牌长度 ${#TEACHER_TOKEN}，学生令牌长度 ${#STUDENT_TOKEN}（不打印令牌本身）"

T="Authorization: Bearer $TEACHER_TOKEN"
S="Authorization: Bearer $STUDENT_TOKEN"

log "1. Q1/Q2 准备：新建提示词模板"
TPL_JSON=$(curl -s -X POST "$API/api/prompt-templates" -H "$T" -H 'Content-Type: application/json' -d '{
  "name":"验收模板-数学",
  "body":"你是{{app_name}}，只依据本班材料作答。今天是{{today}}。\n学生的问题：{{question}}\n回答不超过三句话，并在结论后标出处。",
  "description":"验收脚本创建","enabled":true}')
echo "$TPL_JSON" > "$OUT/01-template.json"
TPL=$(jget "$TPL_JSON" "d['id']")
say "模板编号 $TPL"

log "2. 新建应用并引用模板"
APP_JSON=$(curl -s -X POST "$API/api/agents" -H "$T" -H 'Content-Type: application/json' -d "{
  \"name\":\"验收应用-数学\",\"prompt\":\"\",\"prompt_template_id\":$TPL,\"enabled\":true}")
echo "$APP_JSON" > "$OUT/02-app.json"
APP=$(jget "$APP_JSON" "d['id']")
say "应用编号 $APP"

log "3. 登记并连接 MCP 服务器"
SRV_JSON=$(curl -s -X POST "$API/api/mcp/servers" -H "$T" -H 'Content-Type: application/json' -d "{
  \"name\":\"$MCP_NAME\",\"base_url\":\"http://mcp:8090\",\"token\":\"$MCP_AUTH_TOKEN\"}")
echo "$SRV_JSON" > "$OUT/03-server.json"
SRV=$(jget "$SRV_JSON" "d['id']")
CONNECT_JSON=$(curl -s -X POST "$API/api/mcp/servers/$SRV/connect" -H "$T")
echo "$CONNECT_JSON" > "$OUT/04-tools.json"
say "服务器编号 $SRV，发现工具：$(jget "$CONNECT_JSON" "','.join(t['name'] for t in d['tools'])")"
say "令牌回显检查（应为空）：$(curl -s "$API/api/mcp/servers" -H "$T" | grep -c "$MCP_AUTH_TOKEN" || true)"

log "4. 新建技能：声明内置检索 + 一个外部工具"
SKILL_TEXT='名称：验收解题引导
适用条件：换算,公里,米,方程
资料：数学
工具：knowledge_search、unit_convert@'"$MCP_NAME"'
启用：是

学生正在自己做题。先看他写到哪一步，只指出下一步该检查什么。需要换算时调用 unit_convert，再据此提示。'
SKILL_JSON=$(curl -s -X POST "$API/api/skills" -H "$T" -H 'Content-Type: application/json' \
  --data-binary "$(python -c "import json,sys;print(json.dumps({'source_text':sys.argv[1]}))" "$SKILL_TEXT")")
echo "$SKILL_JSON" > "$OUT/05-skill.json"
SKILL=$(jget "$SKILL_JSON" "d['id']")
say "技能编号 $SKILL"

log "5. 绑定技能与材料到应用"
curl -s -X PUT "$API/api/agents/$APP/skills" -H "$T" -H 'Content-Type: application/json' -d "{\"skill_ids\":[$SKILL]}" > /dev/null
MATERIALS=$(curl -s "$API/api/materials" -H "$T")
MATH_ID=$(jget "$MATERIALS" "next((m['id'] for m in d['items'] if '数学' in m['title']), 0)")
if [ "$MATH_ID" = "0" ]; then
  say "本班没有标题含「数学」的材料：应用将不绑定材料，检索为空（不影响工具循环取证）"
  BIND="[]"
else
  BIND="[$MATH_ID]"
  say "绑定材料 #$MATH_ID"
fi
curl -s -X PUT "$API/api/agents/$APP/materials" -H "$T" -H 'Content-Type: application/json' -d "{\"material_ids\":$BIND}" > /dev/null
COMP_JSON=$(curl -s "$API/api/agents/$APP/composition" -H "$T")
echo "$COMP_JSON" > "$OUT/06-composition.json"
say "组成视图：模板=$(jget "$COMP_JSON" "d['template_name']") 技能=$(jget "$COMP_JSON" "','.join(s['name'] for s in d['skills'])") 工具=$(jget "$COMP_JSON" "','.join(d['tool_refs'])")"

log "6. 学生提问（触发工具循环）"
CONV_JSON=$(curl -s -X POST "$API/api/conversations" -H "$S" -H 'Content-Type: application/json' -d "{\"agent_id\":$APP}")
CONV=$(jget "$CONV_JSON" "d['id']")
say "会话编号 $CONV"
curl -s -N -X POST "$API/api/conversations/$CONV/messages" -H "$S" -H 'Content-Type: application/json' \
  -d '{"question":"3.2 公里等于多少米？顺便说下一步该检查什么。"}' > "$OUT/07-stream.txt"
say "SSE 事件序列：$(grep -o '^event: .*' "$OUT/07-stream.txt" | tr '\n' ' ')"
say "meta 里的工具调用：$(grep '^data: ' "$OUT/07-stream.txt" | head -1 | python -c "import json,sys;line=sys.stdin.read().strip()[6:];d=json.loads(line);print(d.get('tool_calls','（无该字段）'))")"

log "7. 审计：每轮一行"
CALLS=$(curl -s "$API/api/llm-calls?limit=10" -H "$T")
echo "$CALLS" > "$OUT/08-calls.json"
python - "$CALLS" <<'PY' | tee -a "$OUT/summary.txt"
import json,sys
d=json.loads(sys.argv[1])
for row in reversed(d['items']):
    print(f"#{row['id']} 轮次={row['round_no']} 消息={row['message_count']} 注入字符={row['context_chars']} "
          f"技能={row['skill_name'] or '-'} 工具声明={row['tool_def_count']} 工具调用={row['tool_call_count']} "
          f"状态={row['status']} finish={row['finish_reason']}")
PY
LAST=$(jget "$CALLS" "d['items'][0]['id']")
FIRST=$(jget "$CALLS" "d['items'][-1]['id']")

log "8. Q1/Q5：第一轮的请求体（含 tools？）"
DETAIL1=$(curl -s "$API/api/llm-calls/$FIRST" -H "$T")
echo "$DETAIL1" > "$OUT/09-call-first.json"
python - "$DETAIL1" <<'PY' | tee -a "$OUT/summary.txt"
import json,sys
d=json.loads(sys.argv[1])
req=json.loads(d['request_json'])
print("请求体顶层字段：", sorted(req.keys()))
print("messages 条数：", len(req['messages']))
print("tools 条数：", len(req.get('tools',[])))
if req.get('tools'):
    fn=req['tools'][0]['function']
    print("tools[0].function.name =", fn['name'])
    print("tools[0].function.parameters =", json.dumps(fn['parameters'],ensure_ascii=False))
print("system 首段：", d['system_prompt'].split('\n')[0])
print("请求体里是否有 template_id：", 'template_id' in d['request_json'])
PY

log "9. Q5：第二轮的请求体（含 role:tool 消息？）"
DETAIL2=$(curl -s "$API/api/llm-calls/$LAST" -H "$T")
echo "$DETAIL2" > "$OUT/10-call-last.json"
python - "$DETAIL2" <<'PY' | tee -a "$OUT/summary.txt"
import json,sys
d=json.loads(sys.argv[1])
req=json.loads(d['request_json'])
roles=[m['role'] for m in req['messages']]
print("messages 角色序列：", roles)
print("是否有 role=tool：", 'tool' in roles)
print("tools 是否仍在：", 'tools' in req)
for m in req['messages']:
    if m['role']=='tool':
        print("tool_call_id =", m.get('tool_call_id'), "内容前 60 字 =", m['content'][:60])
print("工具调用审计：", [(t['round_no'],t['server_name'],t['tool_name'],t['status'],t['latency_ms']) for t in d['tool_calls']])
PY

log "10. Q2：材料正文字数与注入字数"
SIZE=$(curl -s "$API/api/llm-calls/$LAST/context-size" -H "$T")
echo "$SIZE" > "$OUT/11-context-size.json"
say "$(python -c "import json,sys;d=json.loads(sys.argv[1]);print(f\"材料 {d['material_count']} 份，正文合计 {d['source_chars']} 字，本次注入 {d['context_chars']} 字\")" "$SIZE")"

log "11. 越权检查"
say "学生读本班审计：$(curl -s -o /dev/null -w '%{http_code}' "$API/api/llm-calls" -H "$S")（应 200，只看自己）"
say "学生写模板：$(curl -s -o /dev/null -w '%{http_code}' -X POST "$API/api/prompt-templates" -H "$S" -H 'Content-Type: application/json' -d '{"name":"x","body":"y"}')（应 403）"
say "无令牌读审计：$(curl -s -o /dev/null -w '%{http_code}' "$API/api/llm-calls")（应 401）"

log "完成"
say "原始响应在 $OUT/，汇总在本文件。"
