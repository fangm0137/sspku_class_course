package mcp

import (
	"encoding/json"
	"fmt"
	"strings"
)

// schemaNode 是 JSON Schema 里本系统用到的子集。
// 只校验"能拦住无效调用"所必需的部分：类型、必填、枚举、数组元素。
type schemaNode struct {
	Type       string                `json:"type"`
	Properties map[string]schemaNode `json:"properties"`
	Required   []string              `json:"required"`
	Enum       []any                 `json:"enum"`
	Items      *schemaNode           `json:"items"`
}

// ValidateArguments 用发现结果里的 inputSchema 校验模型给出的参数。
// 校验依据与发给模型的声明是同一份字节，因此"模型看到的结构"与"执行前检查的结构"不会漂移。
// 失败返回面向模型的说明，该说明会被当作工具结果回传，让模型有机会修正。
func ValidateArguments(schema json.RawMessage, arguments json.RawMessage) error {
	if len(schema) == 0 {
		return nil
	}
	var node schemaNode
	if err := json.Unmarshal(schema, &node); err != nil {
		return fmt.Errorf("工具的输入结构无法解析")
	}
	if len(arguments) == 0 {
		arguments = json.RawMessage(`{}`)
	}
	var value any
	decoder := json.NewDecoder(strings.NewReader(string(arguments)))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return fmt.Errorf("参数不是合法的 JSON 对象")
	}
	if _, ok := value.(map[string]any); !ok {
		return fmt.Errorf("参数应为 JSON 对象")
	}
	return validateNode("参数", node, value)
}

func validateNode(label string, node schemaNode, value any) error {
	if node.Type != "" && !matchesType(node.Type, value) {
		return fmt.Errorf("%s 应为 %s，实际是 %s", label, node.Type, actualType(value))
	}
	if len(node.Enum) > 0 {
		if !inEnum(node.Enum, value) {
			return fmt.Errorf("%s 的取值不在允许范围内", label)
		}
	}
	switch node.Type {
	case "object":
		object, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("%s 应为对象", label)
		}
		for _, key := range node.Required {
			item, present := object[key]
			if !present || item == nil {
				return fmt.Errorf("%s 缺少必填字段 %s", label, key)
			}
		}
		for key, child := range node.Properties {
			item, present := object[key]
			if !present || item == nil {
				continue
			}
			if err := validateNode(fieldLabel(label, key), child, item); err != nil {
				return err
			}
		}
	case "array":
		items, ok := value.([]any)
		if !ok {
			return fmt.Errorf("%s 应为数组", label)
		}
		if node.Items != nil {
			for index, item := range items {
				if err := validateNode(fmt.Sprintf("%s[%d]", label, index), *node.Items, item); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func fieldLabel(parent, key string) string {
	if parent == "参数" {
		return key
	}
	return parent + "." + key
}

func matchesType(kind string, value any) bool {
	switch kind {
	case "string":
		_, ok := value.(string)
		return ok
	case "number":
		_, ok := value.(json.Number)
		return ok
	case "integer":
		number, ok := value.(json.Number)
		if !ok {
			return false
		}
		_, err := number.Int64()
		return err == nil
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	case "null":
		return value == nil
	}
	// 未声明的类型不做判断，宁可放过也不误拦。
	return true
}

func actualType(value any) string {
	switch value.(type) {
	case string:
		return "string"
	case json.Number:
		return "number"
	case bool:
		return "boolean"
	case map[string]any:
		return "object"
	case []any:
		return "array"
	case nil:
		return "null"
	}
	return "unknown"
}

func inEnum(enum []any, value any) bool {
	encoded, err := json.Marshal(value)
	if err != nil {
		return false
	}
	for _, candidate := range enum {
		other, err := json.Marshal(candidate)
		if err != nil {
			continue
		}
		if string(other) == string(encoded) {
			return true
		}
	}
	return false
}
