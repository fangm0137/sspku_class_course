package rag

import "sort"

// FusionK 是倒数排名融合（RRF）的常数，取自第 4 课规约的 k = 60。
const FusionK = 60

// Fuse 按名次融合若干有序候选列表并取前 limit 条，返回切片 id。
// 只传一条路径时结果就是该路径的顺序（RRF 分数随名次严格递减），因此调用方不必分支处理。
func Fuse(paths [][]uint64, limit int) []uint64 {
	if limit <= 0 {
		return nil
	}
	scores := make(map[uint64]float64)
	order := make([]uint64, 0)
	for _, path := range paths {
		for rank, id := range path {
			if _, seen := scores[id]; !seen {
				order = append(order, id)
			}
			scores[id] += 1 / float64(FusionK+rank+1)
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
	return order
}
