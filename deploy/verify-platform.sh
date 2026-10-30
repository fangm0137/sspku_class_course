#!/usr/bin/env bash
# 个人项目验收的端到端取证脚本。
#
# 前置：容器已按 deploy/README.md 串行初始化（migrate → seed → seed-materials → seed-skills → seed-apps），
#      api / nginx / mcp 已启动，.env 里配了 MCP_AUTH_TOKEN 与三个 SEED_*_PASSWORD。
#      运行时先 `set -a; . ./.env; set +a`。
#
# 两个设计约束：
#   1) 可重复执行：模板、应用、技能、服务器都先按名称查找，存在就复用。
#   2) 所有响应先落文件再解析：Windows 对命令行长度有上限，把整段 JSON 当参数传会被截断。
set -u

API=${API:-http://127.0.0.1:8080}
OUT=${OUT:-./deploy/evidence}
TEACHER=${TEACHER:-teacher_a}
STUDENT=${STUDENT:-student_a1}
MCP_NAME=${MCP_NAME:-campus-tools}
TPL_NAME=${TPL_NAME:-验收模板-数学}
APP_NAME=${APP_NAME:-验收应用-数学}
SKILL_NAME=${SKILL_NAME:-验收解题引导}
# 提问一：材料能回答，用来证明"注入的是切片而不是全文"与"没有工具时 meta 不含 tool_calls"。
QUESTION_DOC=${QUESTION_DOC:-单位换算时先做什么、要检查哪两件事？}
# 提问二：材料回答不了，必须换算，用来证明工具循环。
QUESTION_TOOL=${QUESTION_TOOL:-帮我把 12.5 毫米换算成米，再说下一步该检查什么。}
# 提问三：不命中任何技能，用来证明"没有候选工具时请求体不带 tools 字段"。
QUESTION_PLAIN=${QUESTION_PLAIN:-请用一句话说明你今天能帮我做什么。}

mkdir -p "$OUT" "$OUT/tmp"
log() { printf '\n=== %s ===\n' "$*" | tee -a "$OUT/summary.txt"; }
say() { printf '%s\n' "$*" | tee -a "$OUT/summary.txt"; }

# jgetf <文件> <对 d 的 python 表达式>：从落盘的 JSON 里取一个值。
jgetf() { python -c 'import json,sys;d=json.load(open(sys.argv[1],encoding="utf-8"));print(eval(sys.argv[2]))' "$1" "$2"; }

: > "$OUT/summary.txt"

log "0. 登录取令牌"
curl -s -X POST "$API/api/login" -H 'Content-Type: application/json' \
  -d "{\"username\":\"$TEACHER\",\"password\":\"$SEED_TEACHER_A_PASSWORD\"}" > "$OUT/tmp/login-teacher.json"
curl -s -X POST "$API/api/login" -H 'Content-Type: application/json' \
  -d "{\"username\":\"$STUDENT\",\"password\":\"$SEED_STUDENT_A1_PASSWORD\"}" > "$OUT/tmp/login-student.json"
TEACHER_TOKEN=$(jgetf "$OUT/tmp/login-teacher.json" "d['token']")
STUDENT_TOKEN=$(jgetf "$OUT/tmp/login-student.json" "d['token']")
T="Authorization: Bearer $TEACHER_TOKEN"
S="Authorization: Bearer $STUDENT_TOKEN"
say "教师/学生令牌长度 ${#TEACHER_TOKEN} / ${#STUDENT_TOKEN}（不打印令牌本身）"

log "1. 提示词模板（按名称复用）"
curl -s "$API/api/prompt-templates?all=1" -H "$T" > "$OUT/01-templates.json"
TPL=$(jgetf "$OUT/01-templates.json" "next((t['id'] for t in d['items'] if t['name']=='''$TPL_NAME'''),0)")
if [ "$TPL" = "0" ]; then
  curl -s -X POST "$API/api/prompt-templates" -H "$T" -H 'Content-Type: application/json' -d "{
    \"name\":\"$TPL_NAME\",
    \"body\":\"你是{{app_name}}，只依据本班材料作答。今天是{{today}}。\\n学生的问题：{{question}}\\n回答不超过三句话，并在结论后标出处。\",
    \"description\":\"验收脚本创建\",\"enabled\":true}" > "$OUT/tmp/template-create.json"
  TPL=$(jgetf "$OUT/tmp/template-create.json" "d['id']")
  say "新建模板 #$TPL"
else
  say "复用模板 #$TPL"
fi

log "2. 应用（按名称复用，指向该模板）"
curl -s "$API/api/agents?all=1" -H "$T" > "$OUT/02-agents.json"
APP=$(jgetf "$OUT/02-agents.json" "next((a['id'] for a in d['items'] if a['name']=='''$APP_NAME'''),0)")
if [ "$APP" = "0" ]; then
  curl -s -X POST "$API/api/agents" -H "$T" -H 'Content-Type: application/json' -d "{
    \"name\":\"$APP_NAME\",\"prompt\":\"\",\"prompt_template_id\":$TPL,\"enabled\":true}" > "$OUT/tmp/app-create.json"
  APP=$(jgetf "$OUT/tmp/app-create.json" "d['id']")
  say "新建应用 #$APP"
else
  curl -s -X PUT "$API/api/agents/$APP" -H "$T" -H 'Content-Type: application/json' -d "{
    \"name\":\"$APP_NAME\",\"prompt\":\"\",\"prompt_template_id\":$TPL,\"enabled\":true}" > /dev/null
  say "复用应用 #$APP（已重新指向模板）"
fi

log "3. 数学材料（标题含「数学」的都绑定；缺"附录"那份就补一份大材料）"
curl -s "$API/api/materials" -H "$T" > "$OUT/03-materials.json"
APPENDIX_ID=$(jgetf "$OUT/03-materials.json" "next((m['id'] for m in d['items'] if '附录' in m['title']),0)")
if [ "$APPENDIX_ID" = "0" ]; then
  # 生成一份明显大于"4 条切片 × 800 字"的材料：只有这样第 3 问才有对照意义。
  python - "$OUT/tmp/数学自学笔记-附录.md" <<'PY'
import sys
base = """# 数学自学笔记（含错题附录）

## 一、解一元二次方程的四步

先把方程整理成 ax²+bx+c=0 的标准形，再算判别式 Δ=b²-4ac：
Δ>0 有两个不相等实根，Δ=0 有两个相等实根，Δ<0 没有实根。
判别式为负时不要再套求根公式，那一步会得到不存在的数。

## 二、单位换算的检查顺序

长度单位的进率是十进制的：1 千米 = 1000 米，1 米 = 10 分米 = 100 厘米 = 1000 毫米。
换算时先统一单位再计算，不要在同一个式子里混用公里与米；
先写出换算关系，再代入数值，最后检查结果的数量级是否合理。
"""
items = [
    "去括号时漏乘括号里的第二项",
    "分母不同却直接相加，没有先通分",
    "把判别式算成了 b²-4c，漏掉 a 的系数",
    "单位没有统一就代入数值",
    "应用题设了未知数却没有写清它的含义",
    "检查时只看计算，不看条件是否都用上了",
    "把小数的位数当成有效数字",
    "结果的单位与题目要求的单位不一致",
]
lines = [base]
for round_no in range(1, 9):
    lines.append(f"## 附录 {round_no}：第 {round_no} 组易错点与对应检查动作\n")
    for index, item in enumerate(items, 1):
        lines.append(
            f"{index}. 常见错误是{item}。出现这类错误时，先回到题目把已知量逐个写出，"
            f"再确认这一步用的是哪条关系式；确认之后才继续往下算。第 {round_no} 组的同学"
            f"在订正时要把这一步写在草稿的最上面，避免下一次重复同样的错误。\n"
        )
    lines.append("\n")
open(sys.argv[1], "w", encoding="utf-8").write("".join(lines))
PY
  curl -s -X POST "$API/api/materials" -H "$T" -F "file=@$OUT/tmp/数学自学笔记-附录.md" > "$OUT/tmp/material-create.json"
  APPENDIX_ID=$(jgetf "$OUT/tmp/material-create.json" "d['material_id']")
  say "已上传大材料 #$APPENDIX_ID（约 $(python -c "import sys;print(len(open(sys.argv[1],encoding='utf-8').read()))" "$OUT/tmp/数学自学笔记-附录.md") 字）"
  for _ in $(seq 1 40); do
    curl -s --get "$API/api/search" --data-urlencode "q=易错点" --data-urlencode "mode=keyword" -H "$T" > "$OUT/tmp/search.json"
    HITS=$(jgetf "$OUT/tmp/search.json" "len(d['hits'])")
    [ "$HITS" != "0" ] && break
    sleep 1
  done
  say "关键字检索命中切片数：$HITS"
else
  say "复用大材料 #$APPENDIX_ID"
fi
curl -s "$API/api/materials" -H "$T" > "$OUT/03-materials.json"
MATH_IDS=$(jgetf "$OUT/03-materials.json" "','.join(str(m['id']) for m in d['items'] if '数学' in m['title'])")
say "将绑定的数学材料编号：$MATH_IDS"

log "4. MCP 服务器：登记（按名称复用）与连接"
curl -s "$API/api/mcp/servers" -H "$T" > "$OUT/04-servers.json"
SRV=$(jgetf "$OUT/04-servers.json" "next((s['id'] for s in d['items'] if s['name']=='''$MCP_NAME'''),0)")
if [ "$SRV" = "0" ]; then
  curl -s -X POST "$API/api/mcp/servers" -H "$T" -H 'Content-Type: application/json' -d "{
    \"name\":\"$MCP_NAME\",\"base_url\":\"http://mcp:8090\",\"token\":\"$MCP_AUTH_TOKEN\"}" > "$OUT/tmp/server-create.json"
  SRV=$(jgetf "$OUT/tmp/server-create.json" "d['id']")
  say "登记服务器 #$SRV"
else
  say "复用服务器 #$SRV"
fi
curl -s -X POST "$API/api/mcp/servers/$SRV/connect" -H "$T" > "$OUT/04-tools.json"
say "本次发现工具：$(jgetf "$OUT/04-tools.json" "','.join(t['name'] for t in d['tools'])")"
curl -s "$API/api/mcp/servers" -H "$T" > "$OUT/04-servers-after.json"
say "服务器列表里出现令牌原文的次数（应为 0）：$(grep -c "$MCP_AUTH_TOKEN" "$OUT/04-servers-after.json" || true)"
say "令牌字段呈现：$(jgetf "$OUT/04-servers-after.json" "','.join(f\"{s['name']}:has_token={s['has_token']}\" for s in d['items'])")"

log "5. 技能（按名称复用）：声明内置检索 + 一个外部工具"
curl -s "$API/api/skills" -H "$T" > "$OUT/05-skills.json"
SKILL=$(jgetf "$OUT/05-skills.json" "next((s['id'] for s in d['items'] if s['name']=='''$SKILL_NAME'''),0)")
if [ "$SKILL" = "0" ]; then
  SKILL_TEXT='名称：验收解题引导
适用条件：换算,公里,米,毫米,方程,判别式
资料：数学
工具：knowledge_search、unit_convert@'"$MCP_NAME"'
启用：是

学生正在自己做题。先看他写到哪一步，只指出下一步该检查什么。涉及不同单位的数量时调用 unit_convert 换算，再据此提示，不要直接给出最终答案。'
  python - "$SKILL_TEXT" "$OUT/tmp/skill.json" <<'PY'
import json,sys
json.dump({"source_text":sys.argv[1]}, open(sys.argv[2],"w",encoding="utf-8"), ensure_ascii=False)
PY
  curl -s -X POST "$API/api/skills" -H "$T" -H 'Content-Type: application/json' --data-binary "@$OUT/tmp/skill.json" > "$OUT/05-skill-create.json"
  SKILL=$(jgetf "$OUT/05-skill-create.json" "d['id']")
  say "新建技能 #$SKILL"
else
  say "复用技能 #$SKILL"
fi

log "6. 绑定技能与材料，查看组成视图"
curl -s -X PUT "$API/api/agents/$APP/skills" -H "$T" -H 'Content-Type: application/json' -d "{\"skill_ids\":[$SKILL]}" > /dev/null
curl -s -X PUT "$API/api/agents/$APP/materials" -H "$T" -H 'Content-Type: application/json' -d "{\"material_ids\":[$MATH_IDS]}" > /dev/null
curl -s "$API/api/agents/$APP/composition" -H "$T" > "$OUT/06-composition.json"
say "模板=$(jgetf "$OUT/06-composition.json" "d['template_name']")"
say "技能=$(jgetf "$OUT/06-composition.json" "','.join(s['name'] for s in d['skills'])")"
say "材料=$(jgetf "$OUT/06-composition.json" "','.join(m['name'] for m in d['materials'])")"
say "工具引用=$(jgetf "$OUT/06-composition.json" "','.join(d['tool_refs'])")"

log "7. 提问一：材料能回答的问题（不应触发工具调用）"
curl -s -X POST "$API/api/conversations" -H "$S" -H 'Content-Type: application/json' -d "{\"agent_id\":$APP}" > "$OUT/tmp/conv.json"
CONV=$(jgetf "$OUT/tmp/conv.json" "d['id']")
say "会话 #$CONV"
python - "$QUESTION_DOC" "$OUT/tmp/q.json" <<'PY'
import json,sys
json.dump({"question":sys.argv[1]}, open(sys.argv[2],"w",encoding="utf-8"), ensure_ascii=False)
PY
curl -s -N -X POST "$API/api/conversations/$CONV/messages" -H "$S" -H 'Content-Type: application/json' \
  --data-binary "@$OUT/tmp/q.json" > "$OUT/07-stream-doc.txt"
say "事件序列：$(grep -o '^event: .*' "$OUT/07-stream-doc.txt" | sed 's/event: //' | tr '\n' ' ')"
say "正文：$(grep -A1 '^event: delta' "$OUT/07-stream-doc.txt" | grep '^data:' | sed 's/^data: //' | python -c 'import json,sys;print("".join(json.loads(l)["text"] for l in sys.stdin if l.strip()))')"
say "meta 是否有 tool_calls：$(grep -A1 '^event: meta' "$OUT/07-stream-doc.txt" | grep '^data:' | sed 's/^data: //' | python -c 'import json,sys
text=sys.stdin.read().strip()
print("有" if text and "tool_calls" in json.loads(text) else "没有（字段整体省略）")')"
say "引用条数：$(grep -A1 '^event: meta' "$OUT/07-stream-doc.txt" | grep '^data:' | sed 's/^data: //' | python -c 'import json,sys
text=sys.stdin.read().strip()
print(len(json.loads(text)["citations"]) if text else 0)')"

log "8. 提问二：材料回答不了的换算（应触发工具调用）"
python - "$QUESTION_TOOL" "$OUT/tmp/q2.json" <<'PY'
import json,sys
json.dump({"question":sys.argv[1]}, open(sys.argv[2],"w",encoding="utf-8"), ensure_ascii=False)
PY
say "提问：$QUESTION_TOOL"
for attempt in 1 2 3; do
  curl -s -N -X POST "$API/api/conversations/$CONV/messages" -H "$S" -H 'Content-Type: application/json' \
    --data-binary "@$OUT/tmp/q2.json" > "$OUT/07-stream-tool.txt"
  grep -q '^event: delta' "$OUT/07-stream-tool.txt" && break
  say "第 $attempt 次尝试没有正文（外部模型网关偶发失败），重试"
  sleep 2
done
say "事件序列：$(grep -o '^event: .*' "$OUT/07-stream-tool.txt" | sed 's/event: //' | tr '\n' ' ')"
say "正文：$(grep -A1 '^event: delta' "$OUT/07-stream-tool.txt" | grep '^data:' | sed 's/^data: //' | python -c 'import json,sys;print("".join(json.loads(l)["text"] for l in sys.stdin if l.strip()))')"
say "meta 的 tool_calls：$(grep -A1 '^event: meta' "$OUT/07-stream-tool.txt" | grep '^data:' | sed 's/^data: //' | python -c 'import json,sys
text=sys.stdin.read().strip()
print(json.dumps(json.loads(text)["tool_calls"], ensure_ascii=False) if text and "tool_calls" in json.loads(text) else "（无该字段）")')"

log "8b. 提问三：不命中任何技能（请求体应当不带 tools 字段）"
python - "$QUESTION_PLAIN" "$OUT/tmp/q3.json" <<'PY'
import json,sys
json.dump({"question":sys.argv[1]}, open(sys.argv[2],"w",encoding="utf-8"), ensure_ascii=False)
PY
say "提问：$QUESTION_PLAIN"
for attempt in 1 2 3; do
  curl -s -N -X POST "$API/api/conversations/$CONV/messages" -H "$S" -H 'Content-Type: application/json' \
    --data-binary "@$OUT/tmp/q3.json" > "$OUT/07-stream-plain.txt"
  grep -q '^event: delta' "$OUT/07-stream-plain.txt" && break
  say "第 $attempt 次尝试没有正文，重试"
  sleep 2
done
say "事件序列：$(grep -o '^event: .*' "$OUT/07-stream-plain.txt" | sed 's/event: //' | tr '\n' ' ')"
say "meta 是否有 tool_calls：$(grep -A1 '^event: meta' "$OUT/07-stream-plain.txt" | grep '^data:' | sed 's/^data: //' | python -c 'import json,sys
text=sys.stdin.read().strip()
print("有" if text and "tool_calls" in json.loads(text) else "没有（字段整体省略）")')"

log "9. 审计：本次会话的每一轮"
curl -s "$API/api/llm-calls?limit=20" -H "$T" > "$OUT/08-calls.json"
python - "$OUT/08-calls.json" "$CONV" <<'PY' | tee -a "$OUT/summary.txt"
import json,sys
d=json.load(open(sys.argv[1],encoding="utf-8")); conv=int(sys.argv[2])
rows=[r for r in d['items'] if r['conversation_id']==conv]
for row in reversed(rows):
    print(f"#{row['id']} 轮次={row['round_no']} 消息={row['message_count']} 注入={row['context_chars']}字 "
          f"技能={row['skill_name'] or '-'} 声明={row['tool_def_count']} 调用={row['tool_call_count']} "
          f"状态={row['status']} finish={row['finish_reason']} {row['latency_ms']}ms")
print(f"本次会话共 {len(rows)} 条调用记录")
PY
DOC_ID=$(jgetf "$OUT/08-calls.json" "min(r['id'] for r in d['items'] if r['conversation_id']==$CONV)")
TOOL_FIRST=$(jgetf "$OUT/08-calls.json" "min(r['id'] for r in d['items'] if r['conversation_id']==$CONV and r['tool_call_count']>0)")
TOOL_LAST=$(jgetf "$OUT/08-calls.json" "max(r['id'] for r in d['items'] if r['conversation_id']==$CONV and r['round_no']>1)")
PLAIN_ID=$(jgetf "$OUT/08-calls.json" "max(r['id'] for r in d['items'] if r['conversation_id']==$CONV)")

log "10. Q1/Q3：没有工具的那一轮（模板渲染 + 技能注入）"
curl -s "$API/api/llm-calls/$DOC_ID" -H "$T" > "$OUT/09-call-doc.json"
python - "$OUT/09-call-doc.json" <<'PY' | tee -a "$OUT/summary.txt"
import json,sys
d=json.load(open(sys.argv[1],encoding="utf-8"))
req=json.loads(d['request_json'])
print("请求体顶层字段：", sorted(req.keys()))
print("messages 条数：", len(req['messages']))
print("system 首段：", d['system_prompt'].split('\n')[0])
print("system 是否含「当前技能：」：", "当前技能：" in d['system_prompt'])
print("请求体含 template_id：", 'template_id' in d['request_json'])
print("系统消息字符数：", len(d['system_prompt']))
PY

log "11. Q5：带工具的那一轮之后（工具结果回到消息序列）"
curl -s "$API/api/llm-calls/$TOOL_FIRST" -H "$T" > "$OUT/10-call-tool-first.json"
curl -s "$API/api/llm-calls/$TOOL_LAST" -H "$T" > "$OUT/10-call-tool-last.json"
python - "$OUT/10-call-tool-first.json" "$OUT/10-call-tool-last.json" <<'PY' | tee -a "$OUT/summary.txt"
import json,sys
first=json.load(open(sys.argv[1],encoding="utf-8"))
last=json.load(open(sys.argv[2],encoding="utf-8"))
req1=json.loads(first['request_json']); req2=json.loads(last['request_json'])
print("第一轮：tools 条数 =", len(req1.get('tools',[])), " finish =", first['finish_reason'])
for t in req1.get('tools',[]):
    fn=t['function']
    print(f"  {fn['name']} · required={fn['parameters'].get('required')} · 顶层键={sorted(fn['parameters'].keys())}")
print("工具调用审计：")
for t in first['tool_calls']:
    print(f"  轮次{t['round_no']} {t['server_name']}.{t['tool_name']} 状态={t['status']} {t['latency_ms']}ms 结果={t['result_excerpt']}")
print("第二轮：round_no =", last['round_no'], " messages 角色 =", [m['role'] for m in req2['messages']])
for m in req2['messages']:
    if m['role']=='tool':
        print("  role=tool tool_call_id =", m.get('tool_call_id'), " content =", m['content'][:60])
print("第二轮是否仍带 tools：", 'tools' in req2)
PY

log "11b. 不命中技能那一轮：请求体里没有 tools"
curl -s "$API/api/llm-calls/$PLAIN_ID" -H "$T" > "$OUT/10-call-plain.json"
python - "$OUT/10-call-plain.json" <<'PY' | tee -a "$OUT/summary.txt"
import json,sys
d=json.load(open(sys.argv[1],encoding="utf-8"))
req=json.loads(d['request_json'])
print("请求体顶层字段：", sorted(req.keys()))
print("是否含 tools / tool_choice：", 'tools' in req, "/", 'tool_choice' in req)
print("请求体里 tools 字样出现次数：", d['request_json'].count('"tools"'))
print("技能名：", d['skill_name'] or "（未命中）")
print("系统消息含「没有匹配到已启用的学科技能」：", "没有匹配到已启用的学科技能" in d['system_prompt'])
print("系统消息字符数：", len(d['system_prompt']))
PY

log "12. Q2：材料正文字数 vs 本次注入字数"
curl -s "$API/api/llm-calls/$DOC_ID/context-size" -H "$T" > "$OUT/11-context-size.json"
say "无工具那轮：$(jgetf "$OUT/11-context-size.json" "f\"材料 {d['material_count']} 份，正文合计 {d['source_chars']} 字，本次注入 {d['context_chars']} 字\"")"
curl -s "$API/api/llm-calls/$TOOL_LAST/context-size" -H "$T" > "$OUT/11-context-size-tool.json"
say "有工具那轮：$(jgetf "$OUT/11-context-size-tool.json" "f\"材料正文 {d['source_chars']} 字，本次注入 {d['context_chars']} 字（含工具结果）\"")"

log "13. 越权与鉴权检查"
say "学生读审计：$(curl -s -o /dev/null -w '%{http_code}' "$API/api/llm-calls" -H "$S")（200，只看自己）"
say "学生写模板：$(curl -s -o /dev/null -w '%{http_code}' -X POST "$API/api/prompt-templates" -H "$S" -H 'Content-Type: application/json' -d '{"name":"x","body":"y"}')（403）"
say "学生登记服务器：$(curl -s -o /dev/null -w '%{http_code}' -X POST "$API/api/mcp/servers" -H "$S" -H 'Content-Type: application/json' -d '{"name":"x","base_url":"http://x","token":"t"}')（403）"
say "无令牌读审计：$(curl -s -o /dev/null -w '%{http_code}' "$API/api/llm-calls")（401）"
curl -s "$API/api/agents?all=1" -H "$T" > "$OUT/tmp/agents2.json"
curl -s "$API/api/skills" -H "$T" > "$OUT/tmp/skills2.json"
BUILTIN_APP=$(jgetf "$OUT/tmp/agents2.json" "next((a['id'] for a in d['items'] if a['builtin']),0)")
BUILTIN_SKILL=$(jgetf "$OUT/tmp/skills2.json" "next((s['id'] for s in d['items'] if s['builtin']),0)")
say "内置应用 #$BUILTIN_APP 删除：$(curl -s -o /dev/null -w '%{http_code}' -X DELETE "$API/api/agents/$BUILTIN_APP" -H "$T")（409）"
say "内置技能 #$BUILTIN_SKILL 删除：$(curl -s -o /dev/null -w '%{http_code}' -X DELETE "$API/api/skills/$BUILTIN_SKILL" -H "$T")（409）"

log "完成"
say "原始响应在 $OUT/，汇总在本文件。"
