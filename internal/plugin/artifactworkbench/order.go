package artifactworkbench

import (
	"container/heap"
	"sort"

	"github.com/z-chenhao/assistant/internal/plugin/artifactworkbench/contract"
)

type ranked struct {
	index int
	stamp int64
}
type selectionHeap struct {
	rows   []ranked
	newest bool
}

func before(a, b ranked, newest bool) bool {
	if a.stamp == b.stamp {
		return a.index < b.index
	}
	if newest {
		return a.stamp > b.stamp
	}
	return a.stamp < b.stamp
}
func (h selectionHeap) Len() int           { return len(h.rows) }
func (h selectionHeap) Less(i, j int) bool { return before(h.rows[j], h.rows[i], h.newest) }
func (h selectionHeap) Swap(i, j int)      { h.rows[i], h.rows[j] = h.rows[j], h.rows[i] }
func (h *selectionHeap) Push(value any)    { h.rows = append(h.rows, value.(ranked)) }
func (h *selectionHeap) Pop() any {
	n := len(h.rows) - 1
	value := h.rows[n]
	h.rows = h.rows[:n]
	return value
}
func (h *selectionHeap) add(row ranked, limit int) {
	if len(h.rows) < limit {
		heap.Push(h, row)
	} else if before(row, h.rows[0], h.newest) {
		h.rows[0] = row
		heap.Fix(h, 0)
	}
}
func (h *selectionHeap) plan(limit int) contract.Selection {
	sort.Slice(h.rows, func(i, j int) bool { return before(h.rows[i], h.rows[j], h.newest) })
	plan := contract.Selection{Indices: []int{}, Limit: limit}
	for _, row := range h.rows {
		plan.Indices = append(plan.Indices, row.index)
	}
	return plan
}
