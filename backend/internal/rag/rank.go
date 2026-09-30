package rag

import "sort"

// FusionK 是倒数排名融合（RRF）的常数，取自第 4 课规约的 k = 60。
const FusionK = 60

// Candidate 是一条检索候选：ID 是切片主键，Score 是该路径给出的排序分。
// 关键字路径为全文相关度，向量路径为余弦相似度，融合后为 RRF 名次分。
type Candidate struct {
	ID    uint64
	Score float64
}

// FuseScored 按名次融合若干有序候选列表并取前 limit 条。
// score = Σ 1/(k + rank)，rank 从 1 开始；缺席的一路不贡献分数。
// 名次分只用于排序，不是相关性阈值，因此不对外承诺可比性。
func FuseScored(paths [][]Candidate, limit int) []Candidate {
	if limit <= 0 {
		return nil
	}
	scores := make(map[uint64]float64)
	order := make([]uint64, 0)
	for _, path := range paths {
		for rank, candidate := range path {
			if _, seen := scores[candidate.ID]; !seen {
				order = append(order, candidate.ID)
			}
			scores[candidate.ID] += 1 / float64(FusionK+rank+1)
		}
	}
	sort.SliceStable(order, func(i, j int) bool {
		if scores[order[i]] != scores[order[j]] {
			return scores[order[i]] > scores[order[j]]
		}
		return order[i] < order[j]
	})
	if len(order) > limit {
		order = order[:limit]
	}
	result := make([]Candidate, len(order))
	for index, id := range order {
		result[index] = Candidate{ID: id, Score: scores[id]}
	}
	return result
}

// Fuse 只返回融合后的切片 id，按名次排列。
func Fuse(paths [][]uint64, limit int) []uint64 {
	candidates := make([][]Candidate, len(paths))
	for index, path := range paths {
		candidates[index] = make([]Candidate, len(path))
		for rank, id := range path {
			candidates[index][rank] = Candidate{ID: id}
		}
	}
	fused := FuseScored(candidates, limit)
	ids := make([]uint64, len(fused))
	for index, candidate := range fused {
		ids[index] = candidate.ID
	}
	return ids
}
