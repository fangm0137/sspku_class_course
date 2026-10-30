package chat

import "strings"

// screenedNote 是命中指令式句式后的固定替代文本。
// 为什么不只删掉可疑的那一句：被改写过的段落容易自相矛盾，
// 整条丢弃并留下说明，模型与验收者都能看到"这里有一条被拦下了"。
const screenedNote = "（该工具返回的内容含有指令式语句，已按不可信文本整条丢弃。）"

// injectionMarkers 是工具输出里"试图改变行为"的句式特征。
// 工具返回与上传材料同等对待：都是数据，不是指令。
var injectionMarkers = []string{
	"忽略以上", "忽略上述", "忽略前面", "忽略之前", "无视以上", "无视上述",
	"忘掉以上", "覆盖以上指示", "以下是系统指令", "新的系统提示",
	"ignore previous", "ignore all previous", "ignore the above", "disregard previous",
	"disregard all previous", "forget previous instructions", "new system prompt",
}

// screenToolOutput 扫一遍工具输出，命中即整条替换。
func screenToolOutput(text string) string {
	lowered := strings.ToLower(text)
	for _, marker := range injectionMarkers {
		if strings.Contains(lowered, strings.ToLower(marker)) {
			return screenedNote
		}
	}
	return text
}

// ScreenToolOutput 暴露给测试与排障：用同一套规则判断一段工具输出是否会被丢弃。
func ScreenToolOutput(text string) string { return screenToolOutput(text) }
