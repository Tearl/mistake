package perf

import (
	"sort"
	"time"
)

// RouteStat 是单个路由在窗口内的聚合。
type RouteStat struct {
	Route     string  `json:"route"`
	Count     int     `json:"count"`
	Errors    int     `json:"errors"`
	ErrorRate float64 `json:"errorRate"`
	P50       int64   `json:"p50"`
	P90       int64   `json:"p90"`
	P99       int64   `json:"p99"`
	Max       int64   `json:"max"`
	Avg       int64   `json:"avg"`
}

// Bucket 是按分钟的请求量/错误量时序点。
type Bucket struct {
	Minute int64 `json:"minute"` // 分钟对齐的毫秒时间戳
	Count  int   `json:"count"`
	Errors int   `json:"errors"`
}

// Summary 是清洗任务的产物，供 /ops 页面直接渲染。
type Summary struct {
	WindowStart   int64       `json:"windowStart"`
	WindowEnd     int64       `json:"windowEnd"`
	GeneratedAt   int64       `json:"generatedAt"`
	TotalRequests int         `json:"totalRequests"`
	TotalErrors   int         `json:"totalErrors"`
	ErrorRate     float64     `json:"errorRate"`
	OverallP95    int64       `json:"overallP95"`
	Routes        []RouteStat `json:"routes"`
	Timeline      []Bucket    `json:"timeline"`
}

// Aggregate 把窗口内事件聚合成 Summary。状态码 >=500 记为错误。
func Aggregate(events []Event, start, end int64) Summary {
	s := Summary{WindowStart: start, WindowEnd: end, GeneratedAt: time.Now().UnixMilli()}
	byRoute := map[string][]int64{}
	errByRoute := map[string]int{}
	buckets := map[int64]*Bucket{}
	var allLat []int64

	for _, e := range events {
		if e.TS < start || e.TS > end {
			continue
		}
		s.TotalRequests++
		byRoute[e.Route] = append(byRoute[e.Route], e.LatencyMs)
		allLat = append(allLat, e.LatencyMs)
		isErr := e.Status >= 500
		if isErr {
			s.TotalErrors++
			errByRoute[e.Route]++
		}
		m := e.TS - e.TS%60000
		b := buckets[m]
		if b == nil {
			b = &Bucket{Minute: m}
			buckets[m] = b
		}
		b.Count++
		if isErr {
			b.Errors++
		}
	}

	for route, lats := range byRoute {
		sort.Slice(lats, func(i, j int) bool { return lats[i] < lats[j] })
		st := RouteStat{
			Route: route, Count: len(lats), Errors: errByRoute[route],
			P50: percentile(lats, 50), P90: percentile(lats, 90),
			P99: percentile(lats, 99), Max: lats[len(lats)-1], Avg: mean(lats),
		}
		if st.Count > 0 {
			st.ErrorRate = round2(float64(st.Errors) / float64(st.Count))
		}
		s.Routes = append(s.Routes, st)
	}
	// 请求量降序，方便页面展示热点路由
	sort.Slice(s.Routes, func(i, j int) bool { return s.Routes[i].Count > s.Routes[j].Count })

	if s.TotalRequests > 0 {
		s.ErrorRate = round2(float64(s.TotalErrors) / float64(s.TotalRequests))
	}
	sort.Slice(allLat, func(i, j int) bool { return allLat[i] < allLat[j] })
	s.OverallP95 = percentile(allLat, 95)

	for _, b := range buckets {
		s.Timeline = append(s.Timeline, *b)
	}
	sort.Slice(s.Timeline, func(i, j int) bool { return s.Timeline[i].Minute < s.Timeline[j].Minute })
	return s
}

// percentile 取已排序切片的 p 分位（最近秩法）。
func percentile(sorted []int64, p int) int64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := (p*len(sorted) + 99) / 100 // ceil(p/100 * n)
	if idx < 1 {
		idx = 1
	}
	if idx > len(sorted) {
		idx = len(sorted)
	}
	return sorted[idx-1]
}

func mean(v []int64) int64 {
	if len(v) == 0 {
		return 0
	}
	var sum int64
	for _, x := range v {
		sum += x
	}
	return sum / int64(len(v))
}

func round2(f float64) float64 {
	return float64(int64(f*10000+0.5)) / 10000
}
