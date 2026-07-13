package handlers

import (
	"net/http"
	"sort"
	"time"

	"mistakeserver/internal/db"
)

const cnOffsetMs = 8 * 3600 * 1000 // 东八区

var weekLabels = []string{"日", "一", "二", "三", "四", "五", "六"} // 0=周日

// 把毫秒时间戳折算成北京时间的「天序号」（自 epoch 起的天数）
func cnDayIndex(ms int64) int64 {
	return (ms + cnOffsetMs) / 86400000
}

type subjectCount struct {
	Subject string `json:"subject"`
	Count   int32  `json:"count"`
}

type weekBar struct {
	Label string `json:"label"`
	Count int    `json:"count"`
}

// GET /api/stats
func (s *Server) Stats(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := s.user()

	counts, err := s.Q.StatsCounts(ctx, uid)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	total := counts.Total
	mastered := counts.Mastered
	reviewing := counts.Reviewing
	unmastered := total - mastered - reviewing

	subjRows, err := s.Q.StatsBySubject(ctx, uid)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	bySubject := make([]subjectCount, 0, len(subjRows))
	for _, x := range subjRows {
		bySubject = append(bySubject, subjectCount{Subject: x.Subject, Count: x.Count})
	}

	weekly, streak := s.weeklyAndStreak(r)

	writeJSON(w, http.StatusOK, map[string]any{
		"total":      total,
		"mastered":   mastered,
		"reviewing":  reviewing,
		"unmastered": unmastered,
		"pending":    unmastered + reviewing,
		"streak":     streak,
		"bySubject":  bySubject,
		"weekly":     weekly,
	})
}

// 本周 7 天柱状图 + 连续复习天数（移植原 statsData 逻辑）
func (s *Server) weeklyAndStreak(r *http.Request) ([]weekBar, int) {
	since := pgTimestamp(time.Now().Add(-60 * 24 * time.Hour))
	logs, err := s.Q.ReviewLogsSince(r.Context(), db.ReviewLogsSinceParams{UserID: s.user(), Since: since})

	weekly := make([]weekBar, 0, 7)
	if err != nil {
		// reviewLogs 不可用时降级为 0，不影响其它数据
		for i := 6; i >= 0; i-- {
			weekly = append(weekly, weekBar{Label: weekLabels[0], Count: 0})
		}
		return weekly, 0
	}

	dayCount := map[int64]int{}
	for _, ts := range logs {
		if !ts.Valid {
			continue
		}
		dayCount[cnDayIndex(ts.Time.UnixMilli())]++
	}

	todayIdx := cnDayIndex(time.Now().UnixMilli())
	for i := int64(6); i >= 0; i-- {
		idx := todayIdx - i
		weekday := ((idx%7)+7+4)%7 // epoch(1970-01-01) 是周四
		weekly = append(weekly, weekBar{Label: weekLabels[weekday], Count: dayCount[idx]})
	}

	streak := 0
	cursor := todayIdx
	if dayCount[cursor] == 0 {
		cursor-- // 今天还没复习，从昨天起算也算连续
	}
	for dayCount[cursor] > 0 {
		streak++
		cursor--
	}
	return weekly, streak
}

type adminUser struct {
	Openid   string `json:"openid"`
	Short    string `json:"short"`
	Count    int32  `json:"count"`
	Mastered int32  `json:"mastered"`
}

// GET /api/admin
func (s *Server) Admin(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// 单用户模式默认 dev 用户就是 admin，这里不再额外校验角色
	userCount, err := s.Q.CountUsers(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	mistakeCount, err := s.Q.CountAllMistakes(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	masteredCount, err := s.Q.CountAllMastered(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	rows, err := s.Q.AdminPerUser(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	users := make([]adminUser, 0, len(rows))
	for _, u := range rows {
		users = append(users, adminUser{
			Openid: u.UserID, Short: lastN(u.UserID, 6), Count: u.Count, Mastered: u.Mastered,
		})
	}
	sort.SliceStable(users, func(i, j int) bool { return users[i].Count > users[j].Count })

	writeJSON(w, http.StatusOK, map[string]any{
		"userCount":     userCount,
		"mistakeCount":  mistakeCount,
		"masteredCount": masteredCount,
		"users":         users,
	})
}

func lastN(s string, n int) string {
	rs := []rune(s)
	if len(rs) <= n {
		return s
	}
	return string(rs[len(rs)-n:])
}
