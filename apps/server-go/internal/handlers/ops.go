package handlers

import "net/http"

// GET /api/ops/summary 返回性能清洗任务的最新聚合，供 /ops 面板渲染。
func (s *Server) OpsSummary(w http.ResponseWriter, r *http.Request) {
	if s.OpsStore == nil {
		writeErr(w, http.StatusServiceUnavailable, "ops summary store is not configured")
		return
	}
	sum, ok, err := s.OpsStore.Get(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		// 还没跑过清洗任务：返回空壳而非 404，前端好显示「暂无数据」
		writeJSON(w, http.StatusOK, map[string]any{
			"available": false,
			"routes":    []any{},
			"timeline":  []any{},
		})
		return
	}
	writeJSON(w, http.StatusOK, sum)
}
