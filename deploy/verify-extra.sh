#!/usr/bin/env bash
# 补充验收：数据库约束、MCP 服务端自身、内置应用幂等冲突、轮次上限降级。
# 前置：容器已初始化；.env 已 source；MCP_AUTH_TOKEN 已配置。
set -u
API=${API:-http://127.0.0.1:8080}
OUT=${OUT:-./deploy/evidence}
TEACHER=${TEACHER:-teacher_a}
STUDENT=${STUDENT:-student_a1}
PASS=0; FAIL=0
ok() { PASS=$((PASS+1)); printf 'PASS  %s\n' "$*"; }
no() { FAIL=$((FAIL+1)); printf 'FAIL  %s\n' "$*"; }
check() { if [ "$2" = "$3" ]; then ok "$1 → $3"; else no "$1：期望 $2，实际 $3"; fi; }
jgetf() { python -c 'import json,sys;d=json.load(open(sys.argv[1],encoding="utf-8"));print(eval(sys.argv[2]))' "$1" "$2"; }
sql() { docker compose exec -T mysql sh -c "MYSQL_PWD=\"\$MYSQL_PASSWORD\" mysql -u\"\$MYSQL_USER\" \"\$MYSQL_DATABASE\" --batch --skip-column-names -e \"$1\"" 2>&1; }
token() { curl -s -X POST "$API/api/login" -H 'Content-Type: application/json' -d "{\"username\":\"$1\",\"password\":\"$2\"}" | python -c 'import json,sys;print(json.load(sys.stdin)["token"])'; }
T="Authorization: Bearer $(token "$TEACHER" "$SEED_TEACHER_A_PASSWORD")"
S="Authorization: Bearer $(token "$STUDENT" "$SEED_STUDENT_A1_PASSWORD")"
mkdir -p "$OUT" "$OUT/tmp"

echo "== 1.1 数据库约束 =="
check "模板体 CHECK（超 4000 字被拒）" "Check constraint 'ck_templates_body' is violated." "$(sql "INSERT INTO prompt_templates (class_id,name,body) VALUES (1,'x-超长',REPEAT('a',4001));" | sed "s/^ERROR 3819 (HY000) at line 1: //")"
check "班内模板重名被拒（唯一键）" "Duplicate entry" "$(sql "INSERT INTO prompt_templates (class_id,name,body) VALUES (1,'验收模板-数学','dup')" | grep -o 'Duplicate entry' | head -1)"
echo "  外键与唯一键（来自 SHOW CREATE TABLE）："
sql "SHOW CREATE TABLE prompt_templates" | grep -o 'CONSTRAINT `[a-z_]*`\( CHECK\| FOREIGN KEY\)' | sed 's/^/    /'
sql "SHOW CREATE TABLE tutor_agents" | grep -o 'UNIQUE KEY `uq_agents_builtin`\|CONSTRAINT `fk_agents_template` FOREIGN KEY' | sed 's/^/    /'
sql "SHOW CREATE TABLE mcp_tools" | grep -o 'CONSTRAINT `fk_mcp_tool_server` FOREIGN KEY ([^)]*)' | sed 's/^/    /'

echo "== 5. MCP 服务端自身 =="
check "不带令牌 /mcp 返回 401" "yes" "$(docker compose exec -T api sh -c 'wget -S -qO- --post-data="{}" http://mcp:8090/mcp 2>&1 | grep -q "401" && echo yes || echo no' | tr -d '\r')"
check "健康检查不需要令牌" "{\"status\":\"ok\"}" "$(docker compose exec -T api wget -qO- http://mcp:8090/health | tr -d '\r\n ')"
docker compose exec -T api wget -qO- --header="Authorization: Bearer $MCP_AUTH_TOKEN" --header='Content-Type: application/json' \
  --post-data='{"jsonrpc":"2.0","id":1,"method":"tools/list"}' http://mcp:8090/mcp > "$OUT/tmp/mcp-tools.json" 2>/dev/null
check "带令牌 tools/list 返回两个工具" 2 "$(jgetf "$OUT/tmp/mcp-tools.json" "len(d['result']['tools'])")"
docker compose exec -T api wget -qO- --header="Content-Type: application/json" \
  --post-data='{"jsonrpc":"2.0","id":1,"method":"tools/list"}' http://mcp:8090/mcp > "$OUT/tmp/mcp-401.json" 2>/dev/null
check "错误令牌不泄露期望值" "0" "$(grep -c "$MCP_AUTH_TOKEN" "$OUT/tmp/mcp-401.json" || true)"
docker compose exec -T api wget -qO- --header="Authorization: Bearer $MCP_AUTH_TOKEN" --header='Content-Type: application/json' \
  --post-data='{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"unit_convert","arguments":{"value":1,"from":"公里","to":"米"}}}' http://mcp:8090/mcp > "$OUT/tmp/mcp-call.json" 2>/dev/null
check "tools/call 返回 content 且 isError 假" "True False" "$(jgetf "$OUT/tmp/mcp-call.json" "f\"{len(d['result']['content'])>0} {d['result']['isError']}\"")"
docker compose exec -T api wget -qO- --header="Authorization: Bearer $MCP_AUTH_TOKEN" --header='Content-Type: application/json' \
  --post-data='{"jsonrpc":"2.0","id":3,"method":"nope"}' http://mcp:8090/mcp > "$OUT/tmp/mcp-nope.json" 2>/dev/null
check "未知方法 -32601" "-32601" "$(jgetf "$OUT/tmp/mcp-nope.json" "d['error']['code']")"
docker compose exec -T api wget -qO- --header="Authorization: Bearer $MCP_AUTH_TOKEN" --header='Content-Type: application/json' \
  --post-data='{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"unit_convert","arguments":{}}}' http://mcp:8090/mcp > "$OUT/tmp/mcp-params.json" 2>/dev/null
check "缺必填参数 -32602" "-32602" "$(jgetf "$OUT/tmp/mcp-params.json" "d['error']['code']")"

echo "== 3.1 内置应用种子：改过之后再跑报冲突 =="
curl -s "$API/api/agents?all=1" -H "$T" > "$OUT/tmp/agents.json"
BUILTIN_APP=$(jgetf "$OUT/tmp/agents.json" "next((a['id'] for a in d['items'] if a['builtin']),0)")
BUILTIN_NAME=$(jgetf "$OUT/tmp/agents.json" "next((a['name'] for a in d['items'] if a['id']==$BUILTIN_APP),'')")
curl -s -X PUT "$API/api/agents/$BUILTIN_APP" -H "$T" -H 'Content-Type: application/json' \
  -d "{\"name\":\"$BUILTIN_NAME\",\"prompt\":\"临时改过的提示词\",\"prompt_template_id\":null,\"enabled\":true}" > /dev/null
MSYS_NO_PATHCONV=1 docker compose run --rm --no-deps --entrypoint /app/seed-apps api > "$OUT/tmp/seed.out" 2>&1
check "教师改过后重跑报冲突" "conflict" "$(grep -q 'conflicts with expected fixture' "$OUT/tmp/seed.out" && echo conflict || echo none)"
curl -s -X GET "$API/api/agents/$BUILTIN_APP" -H "$T" > "$OUT/tmp/agent-back.json"
check "改动被保留（未被种子覆盖）" "临时改过的提示词" "$(jgetf "$OUT/tmp/agent-back.json" "d['prompt']")"
# 改回去，保持内置应用与样板一致。
TPL=$(jgetf "$(curl -s "$API/api/prompt-templates?all=1" -H "$T" > "$OUT/tmp/tpls.json"; echo "$OUT/tmp/tpls.json")" "next((t['id'] for t in d['items'] if t['name']=='数学助教模板'),0)")
curl -s -X PUT "$API/api/agents/$BUILTIN_APP" -H "$T" -H 'Content-Type: application/json' \
  -d "{\"name\":\"$BUILTIN_NAME\",\"prompt\":\"\",\"prompt_template_id\":$TPL,\"enabled\":true}" > /dev/null

echo "== 3.2 停用内置技能后，路由不再命中 =="
curl -s "$API/api/skills" -H "$T" > "$OUT/tmp/skills.json"
REAL_SKILL=$(jgetf "$OUT/tmp/skills.json" "next((s['id'] for s in d['items'] if s['name']=='数学解题引导'),0)")
SKILL_SRC=$(curl -s "$API/api/skills/$REAL_SKILL" -H "$T" > "$OUT/tmp/skill.json"; jgetf "$OUT/tmp/skill.json" "d['source_text'].replace('启用：是','启用：否')")
python - "$SKILL_SRC" "$OUT/tmp/skill-off.json" <<'PY'
import json,sys
json.dump({"source_text":sys.argv[1]}, open(sys.argv[2],"w",encoding="utf-8"), ensure_ascii=False)
PY
curl -s -X PUT "$API/api/skills/$REAL_SKILL" -H "$T" -H 'Content-Type: application/json' --data-binary "@$OUT/tmp/skill-off.json" > /dev/null
check "停用后技能 enabled=false" "0" "$(sql "SELECT enabled FROM skills WHERE id=$REAL_SKILL")"
python - '{"question":"一元二次方程的判别式该怎么用？"}' "$OUT/tmp/q-off.json" <<'PY'
import json,sys
open(sys.argv[2],"w",encoding="utf-8").write(sys.argv[1])
PY
curl -s -X POST "$API/api/conversations" -H "$S" -H 'Content-Type: application/json' -d "{\"agent_id\":$BUILTIN_APP}" > "$OUT/tmp/conv-off.json"
CONV_OFF=$(jgetf "$OUT/tmp/conv-off.json" "d['id']")
curl -s -N -X POST "$API/api/conversations/$CONV_OFF/messages" -H "$S" -H 'Content-Type: application/json' \
  --data-binary "@$OUT/tmp/q-off.json" > "$OUT/tmp/stream-off.txt"
CALL_OFF=$(sql "SELECT id FROM llm_calls WHERE conversation_id=$CONV_OFF ORDER BY id DESC LIMIT 1")
curl -s "$API/api/llm-calls/$CALL_OFF" -H "$T" > "$OUT/tmp/call-off.json"
check "停用后系统消息不含该技能的引导规程" "0" "$(grep -c '当前技能：数学解题引导' "$OUT/tmp/call-off.json" || true)"
# 恢复启用。
python - "$(jgetf "$OUT/tmp/skill.json" "d['source_text']")" "$OUT/tmp/skill-on.json" <<'PY'
import json,sys
json.dump({"source_text":sys.argv[1]}, open(sys.argv[2],"w",encoding="utf-8"), ensure_ascii=False)
PY
curl -s -X PUT "$API/api/skills/$REAL_SKILL" -H "$T" -H 'Content-Type: application/json' --data-binary "@$OUT/tmp/skill-on.json" > /dev/null

echo
echo "通过 $PASS 项，失败 $FAIL 项"
[ "$FAIL" = "0" ]
