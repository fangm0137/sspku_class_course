package skill

import "strings"

// Row 是一条技能记录（不含 source_text，路由不需要它）。
type Row struct {
	ID          uint64
	ClassID     uint64
	Name        string
	TriggerText string
	Description string
	SubjectText string
	Enabled     bool
}

// Score 计算一条技能对某个提问的命中词数：适用条件按分隔符切词，
// 命中一个词得一分；两侧都先转小写，因此大小写不敏感但完全按字面判断。
func Score(question, triggerText string) int {
	lowered := strings.ToLower(question)
	score := 0
	for _, trigger := range SplitList(triggerText) {
		if trigger == "" {
			continue
		}
		if strings.Contains(lowered, strings.ToLower(trigger)) {
			score++
		}
	}
	return score
}

// Route 在候选集内选出至多一个技能。候选集由调用方负责（只含该智能体绑定且已启用的技能，
// 且按编号升序）；本函数按得分取最高，同分保留更靠前的一条，因此同分实际取编号较小者。
// 全部为零分时不选技能。
func Route(question string, candidates []Row) (Row, bool) {
	best := Row{}
	bestScore := 0
	for _, candidate := range candidates {
		score := Score(question, candidate.TriggerText)
		if score > bestScore {
			best, bestScore = candidate, score
		}
	}
	if bestScore == 0 {
		return Row{}, false
	}
	return best, true
}
