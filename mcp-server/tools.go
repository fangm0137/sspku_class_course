package main

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// toolCatalog 是 tools/list 的返回内容：每项都必须含 name / description / inputSchema。
// 工具名小写字母开头、不含连字符——宿主侧会把它当作 function name 发给模型。
func toolCatalog() []map[string]any {
	return []map[string]any{
		{
			"name":        "formula_hint",
			"description": "给出解题思路提示（只指方向与下一步该检查什么，绝不给出最终答案、选项或得数）。",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"topic": map[string]any{
						"type":        "string",
						"description": "题目涉及的知识点，例如「一元二次方程」「速度」「面积」",
					},
				},
				"required": []string{"topic"},
			},
		},
		{
			"name":        "unit_convert",
			"description": "在长度单位之间换算（毫米/厘米/分米/米/千米），返回换算结果文本。",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"value": map[string]any{"type": "number", "description": "待换算的数值"},
					"from":  map[string]any{"type": "string", "description": "原单位，如「公里」或「km」"},
					"to":    map[string]any{"type": "string", "description": "目标单位，如「米」或「m」"},
				},
				"required": []string{"value", "from", "to"},
			},
		},
	}
}

// callTool 执行一次工具调用。参数缺失一律 -32602，不以空结果冒充成功。
func callTool(raw json.RawMessage) (any, *rpcError) {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if len(raw) == 0 {
		return nil, &rpcError{Code: codeInvalidParams, Message: "缺少 params"}
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: "params 不是合法对象"}
	}
	if strings.TrimSpace(params.Name) == "" {
		return nil, &rpcError{Code: codeInvalidParams, Message: "缺少工具名 name"}
	}
	switch params.Name {
	case "formula_hint":
		var args struct {
			Topic string `json:"topic"`
		}
		if err := decodeArguments(params.Arguments, &args); err != nil {
			return nil, err
		}
		if strings.TrimSpace(args.Topic) == "" {
			return nil, &rpcError{Code: codeInvalidParams, Message: "缺少必填参数 topic"}
		}
		return textResult(hintFor(args.Topic), false), nil
	case "unit_convert":
		var args struct {
			Value *float64 `json:"value"`
			From  string   `json:"from"`
			To    string   `json:"to"`
		}
		if err := decodeArguments(params.Arguments, &args); err != nil {
			return nil, err
		}
		if args.Value == nil {
			return nil, &rpcError{Code: codeInvalidParams, Message: "缺少必填参数 value"}
		}
		if strings.TrimSpace(args.From) == "" || strings.TrimSpace(args.To) == "" {
			return nil, &rpcError{Code: codeInvalidParams, Message: "缺少必填参数 from 或 to"}
		}
		text, ok := convert(*args.Value, args.From, args.To)
		return textResult(text, !ok), nil
	}
	return nil, &rpcError{Code: codeInvalidParams, Message: "未知工具：" + params.Name}
}

func decodeArguments(raw json.RawMessage, out any) *rpcError {
	if len(raw) == 0 || string(raw) == "null" {
		return &rpcError{Code: codeInvalidParams, Message: "缺少 arguments"}
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return &rpcError{Code: codeInvalidParams, Message: "arguments 与工具的输入结构不符"}
	}
	return nil
}

func textResult(text string, isError bool) map[string]any {
	return map[string]any{
		"content": []map[string]string{{"type": "text", "text": text}},
		"isError": isError,
	}
}

// hints 是按知识点给的思路提示。它们只指方向，不含任何最终答案。
var hints = []struct {
	keywords []string
	text     string
}{
	{[]string{"二次", "方程", "求根"}, "先把方程整理成 ax²+bx+c=0 的标准形，再算判别式 Δ=b²-4ac 判断根的情况；接下来该做的是核对 a、b、c 有没有抄错。"},
	{[]string{"速度", "路程", "时间"}, "先确认三个量的单位是否统一，再想「路程=速度×时间」里你已知哪两个；下一步该检查的是单位换算。"},
	{[]string{"面积", "三角形", "矩形"}, "先判断图形属于哪一类，再找对应的面积关系；下一步该检查的是底与高是否垂直对应。"},
	{[]string{"分数", "通分", "约分"}, "先看分母是否相同，不同就先通分；下一步该检查的是约分后是否还能再约。"},
}

func hintFor(topic string) string {
	lowered := strings.ToLower(topic)
	for _, hint := range hints {
		for _, keyword := range hint.keywords {
			if strings.Contains(lowered, keyword) {
				return hint.text
			}
		}
	}
	return "先把题目里已知条件与要求的量分列出来，再想它们之间可能的关系；下一步该检查的是条件有没有漏读。"
}

// lengthFactors 把各单位折算到米。别名覆盖中英文常见写法。
var lengthFactors = map[string]float64{
	"毫米": 0.001, "mm": 0.001,
	"厘米": 0.01, "cm": 0.01,
	"分米": 0.1, "dm": 0.1,
	"米": 1, "m": 1,
	"千米": 1000, "公里": 1000, "km": 1000,
}

// convert 返回换算文本与是否成功。失败时文本本身也是给人看的说明——
// 工具错误也要能让模型读懂，而不是只给一个 500。
func convert(value float64, from, to string) (string, bool) {
	fromFactor, okFrom := lengthFactors[strings.ToLower(strings.TrimSpace(from))]
	toFactor, okTo := lengthFactors[strings.ToLower(strings.TrimSpace(to))]
	if !okFrom || !okTo {
		units := "毫米、厘米、分米、米、千米"
		return fmt.Sprintf("不支持的单位换算：%s → %s。当前只支持长度单位（%s）。", from, to, units), false
	}
	result := value * fromFactor / toFactor
	return fmt.Sprintf("%s %s = %s %s", trim(value), from, trim(result), to), true
}

// trim 去掉浮点误差造成的长尾，同时保留有效数字。
func trim(value float64) string {
	rounded := math.Round(value*1e6) / 1e6
	return strconv.FormatFloat(rounded, 'f', -1, 64)
}
