package skill

import "testing"

// 候选集由调用方按编号升序传入，路由只做"取最高分、同分取靠前"这一件事。
func TestRoutePicksHighestScore(t *testing.T) {
	candidates := []Row{
		{ID: 1, Name: "语文解题引导", TriggerText: "春晓,默写,阅读"},
		{ID: 2, Name: "数学解题引导", TriggerText: "方程,去括号,分数"},
	}
	picked, ok := Route("这道方程的分数怎么去括号？", candidates)
	if !ok {
		t.Fatal("expected a skill to be selected")
	}
	// "方程""去括号""分数"三个词都命中，语文一个都没命中。
	if picked.ID != 2 {
		t.Fatalf("picked skill %d, want 2", picked.ID)
	}
}

// 同分时取编号较小者——这正是候选集必须按编号升序传入的原因。
func TestRouteTieKeepsEarlierCandidate(t *testing.T) {
	candidates := []Row{
		{ID: 7, Name: "先注册的技能", TriggerText: "方程"},
		{ID: 9, Name: "后注册的技能", TriggerText: "通分"},
	}
	picked, ok := Route("方程要不要先通分？", candidates)
	if !ok {
		t.Fatal("expected a skill to be selected")
	}
	if picked.ID != 7 {
		t.Fatalf("picked skill %d, want the earlier one (7)", picked.ID)
	}
}

// 一个词都没命中时不选技能：宁可不注入规程，也不注入可能冲突的规程。
func TestRouteReturnsNothingOnZeroScore(t *testing.T) {
	candidates := []Row{
		{ID: 1, Name: "语文解题引导", TriggerText: "春晓,默写"},
		{ID: 2, Name: "数学解题引导", TriggerText: "方程,通分"},
	}
	if _, ok := Route("今天天气怎么样？", candidates); ok {
		t.Fatal("zero-score question must not select a skill")
	}
}

// 空候选集是最常见的状态（教师还没绑定技能），必须安全返回。
func TestRouteHandlesEmptyCandidateSet(t *testing.T) {
	if _, ok := Route("任意问题", nil); ok {
		t.Fatal("empty candidate set must select nothing")
	}
}

// 大小写折叠：英文触发词不该因为提问里的写法不同而漏判。
func TestScoreIsCaseInsensitive(t *testing.T) {
	if Score("怎么用 Docker 启动？", "docker,compose") != 1 {
		t.Fatal("lowercased matching should hit exactly once")
	}
	if Score("怎么用 DOCKER 启动？", "docker") != 1 {
		t.Fatal("uppercase question should still match")
	}
}

// 命中词数就是分数，逐个累加。
func TestScoreCountsEveryHit(t *testing.T) {
	score := Score("分数通分之后去括号", "方程,分数,通分,去括号,面积")
	if score != 3 {
		t.Fatalf("score = %d, want 3", score)
	}
}
