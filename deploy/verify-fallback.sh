#!/usr/bin/env bash
# 补充验收（二）：提示词来源的三种回落、上传技能立即可绑定并命中。
set -u
API=${API:-http://127.0.0.1:8080}
OUT=${OUT:-./deploy/evidence}
PASS=0; FAIL=0
ok() { PASS=$((PASS+1)); printf 'PASS  %s\n' "$*"; }
no() { FAIL=$((FAIL+1)); printf 'FAIL  %s\n' "$*"; }
check() { if [ "$2" = "$3" ]; then ok "$1 → $3"; else no "$1：期望 $2，实际 $3"; fi; }
jgetf() { python -c 'import json,sys;d=json.load(open(sys.argv[1],encoding="utf-8"));print(eval(sys.argv[2]))' "$1" "$2"; }
token() { curl -s -X POST "$API/api/login" -H 'Content-Type: application/json' -d "{\"username\":\"$1\",\"password\":\"$2\"}" | python -c 'import json,sys;print(json.load(sys.stdin)["token"])'; }
T="Authorization: Bearer $(token "${TEACHER:-teacher_a}" "$SEED_TEACHER_A_PASSWORD")"
S="Authorization: Bearer $(token "${STUDENT:-student_a1}" "$SEED_STUDENT_A1_PASSWORD")"
mkdir -p "$OUT" "$OUT/tmp"
SUFFIX=$(date +%H%M%S)

# ask <应用名> <提示词> <模板编号或空> <问题> → 打印该轮的 system 首段
make_app() { # name prompt template
  curl -s -X POST "$API/api/agents" -H "$T" -H 'Content-Type: application/json' \
    -d "{\"name\":\"$1\",\"prompt\":\"$2\",\"prompt_template_id\":${3:-null},\"enabled\":true}" > "$OUT/tmp/app.json"
  jgetf "$OUT/tmp/app.json" "d.get('id',0)"
}
ask_app() { # appid question tag
  python - "$2" "$OUT/tmp/q-$3.json" <<'PY'
import json,sys
json.dump({"question":sys.argv[1]}, open(sys.argv[2],"w",encoding="utf-8"), ensure_ascii=False)
PY
  local conv
  conv=$(curl -s -X POST "$API/api/conversations" -H "$S" -H 'Content-Type: application/json' -d "{\"agent_id\":$1}" | python -c 'import json,sys;print(json.load(sys.stdin)["id"])')
  curl -s -N -X POST "$API/api/conversations/$conv/messages" -H "$S" -H 'Content-Type: application/json' \
    --data-binary "@$OUT/tmp/q-$3.json" > "$OUT/tmp/stream-$3.txt"
  echo "$conv"
}

echo "== 2.3 提示词来源的三种情形 =="
TPL=$(curl -s "$API/api/prompt-templates?all=1" -H "$T" > "$OUT/tmp/tpls.json"; jgetf "$OUT/tmp/tpls.json" "next((t['id'] for t in d['items'] if t['name']=='验收模板-数学'),0)")
A=$(make_app "回落-仅模板-$SUFFIX" "" "$TPL")
B=$(make_app "回落-仅自由文本-$SUFFIX" "你是自由文本助教，今天是{{today}}。" "")
C=$(make_app "回落-两者皆空-$SUFFIX" "" "")

CONV_A=$(ask_app "$A" "单位换算要先做什么？" "a")
CONV_B=$(ask_app "$B" "你好" "b")
CONV_C=$(ask_app "$C" "你好" "c")
for pair in "$CONV_A:模板" "$CONV_B:自由文本" "$CONV_C:默认角色"; do
  conv=${pair%%:*}; label=${pair##*:}
  curl -s "$API/api/llm-calls?limit=20" -H "$T" > "$OUT/tmp/calls.json"
  call=$(jgetf "$OUT/tmp/calls.json" "min(r['id'] for r in d['items'] if r['conversation_id']==$conv)")
  curl -s "$API/api/llm-calls/$call" -H "$T" > "$OUT/tmp/call-$conv.json"
  first=$(jgetf "$OUT/tmp/call-$conv.json" "d['system_prompt'].split(chr(10))[0]")
  echo "  应用（$label）系统消息首段：$first"
done
check "仅模板：首段来自模板渲染" "True" "$(jgetf "$OUT/tmp/call-$CONV_A.json" "'今天是' in d['system_prompt'] and '验收模板' not in d['system_prompt']")"
check "仅自由文本：首段含自由文本" "True" "$(jgetf "$OUT/tmp/call-$CONV_B.json" "'自由文本助教' in d['system_prompt']")"
check "两者皆空：回落到默认角色" "True" "$(jgetf "$OUT/tmp/call-$CONV_C.json" "'班级资料室的助教' in d['system_prompt']")"

echo "== 4.2 上传的技能立即可绑定并在下一次提问命中 =="
curl -s "$API/api/skills" -H "$T" > "$OUT/tmp/skills.json"
UPLOADED=$(jgetf "$OUT/tmp/skills.json" "next((s['id'] for s in d['items'] if s['name'].startswith('验收-上传技能')),0)")
if [ "$UPLOADED" = "0" ]; then
  no "找不到上传的技能（先跑 verify-acceptance.sh）"
else
  D=$(make_app "上传技能应用-$SUFFIX" "" "")
  curl -s -X PUT "$API/api/agents/$D/skills" -H "$T" -H 'Content-Type: application/json' -d "{\"skill_ids\":[$UPLOADED]}" > /dev/null
  CONV_D=$(ask_app "$D" "这是一次上传测试" "d")
  curl -s "$API/api/llm-calls?limit=20" -H "$T" > "$OUT/tmp/calls.json"
  callD=$(jgetf "$OUT/tmp/calls.json" "min(r['id'] for r in d['items'] if r['conversation_id']==$CONV_D)")
  curl -s "$API/api/llm-calls/$callD" -H "$T" > "$OUT/tmp/call-d.json"
  UPNAME=$(jgetf "$OUT/tmp/skills.json" "next((s['name'] for s in d['items'] if s['id']==$UPLOADED),'')")
  check "上传的技能被路由命中（skill_name）" "$UPNAME" "$(jgetf "$OUT/tmp/call-d.json" "d['skill_name']")"
fi

echo
echo "通过 $PASS 项，失败 $FAIL 项"
[ "$FAIL" = "0" ]
