package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// admin_wrongbook.go — 错题本 admin 观测 API(TODO.md P0)。给 /admin/wrong-book
// 页提供聚合统计。每个聚合独立 try + 降级到零值(对齐 DashboardStats 范式),
// 单点失败不拖垮整页。

// wrongBookStatsDTO 是 /admin/api/wrong-book/stats 的响应。snake_case 给 SPA。
type wrongBookStatsDTO struct {
	Total      int64 `json:"total"`
	Unmastered int64 `json:"unmastered"`
	ThisWeek   int64 `json:"this_week"`
	// 已掌握转化率 = (total - unmastered) / total。前端也可自己算,但后端算好省心,
	// 且 total=0 时返回 0 而非除零。
	MasterRate float64 `json:"master_rate"`
	// TopFrequentWrong 高频错题榜(错得最多的 N 题)。每个 admin 页请求都带,
	// 避免前端再发一次请求(观测页是低频访问,可接受)。
	TopFrequent []wrongBookFrequentDTO `json:"top_frequent"`
	// BySubject 按科目分组的错题量(弱点分布图)。
	BySubject []wrongBookSubjectDTO `json:"by_subject"`
}

type wrongBookFrequentDTO struct {
	QuestionID    uint   `json:"question_id"`
	Stem          string `json:"stem"`
	OccurCount    int64  `json:"occur_count"`
	TotalAttempts int64  `json:"total_attempts"`
}

type wrongBookSubjectDTO struct {
	SubjectKey   string `json:"subject_key"`
	SubjectLabel string `json:"subject_label"`
	Count        int64  `json:"count"`
}

// WrongBookStats GET /admin/api/wrong-book/stats
// 返回错题本全局统计 + 高频错题榜 + 科目分布。AI 未配置(aiService nil)时返回零值。
func (h *adminHandler) WrongBookStats(c *gin.Context) {
	if h.aiService == nil {
		c.JSON(http.StatusOK, wrongBookStatsDTO{TopFrequent: []wrongBookFrequentDTO{}, BySubject: []wrongBookSubjectDTO{}})
		return
	}
	var dto wrongBookStatsDTO

	// 三个聚合各自独立 try + 降级(对齐 DashboardStats 范式)。
	stats, err := h.aiService.WrongBookStats()
	if err != nil {
		// 降级:零值,不 500(观测页宁可显示 0 也不能整页挂)。
		dto.Total, dto.Unmastered, dto.ThisWeek = 0, 0, 0
	} else {
		dto.Total = stats.Total
		dto.Unmastered = stats.Unmastered
		dto.ThisWeek = stats.ThisWeek
		if stats.Total > 0 {
			dto.MasterRate = float64(stats.Total-stats.Unmastered) / float64(stats.Total)
		}
	}

	top, err := h.aiService.WrongBookTopFrequent(10)
	dto.TopFrequent = []wrongBookFrequentDTO{}
	if err == nil {
		for _, r := range top {
			dto.TopFrequent = append(dto.TopFrequent, wrongBookFrequentDTO{
				QuestionID: r.QuestionID, Stem: r.Stem,
				OccurCount: r.OccurCount, TotalAttempts: r.TotalAttempts,
			})
		}
	}

	dist, err := h.aiService.WrongBookSubjectDistribution()
	dto.BySubject = []wrongBookSubjectDTO{}
	if err == nil {
		for _, r := range dist {
			dto.BySubject = append(dto.BySubject, wrongBookSubjectDTO{
				SubjectKey: r.SubjectKey, SubjectLabel: r.SubjectLabel, Count: r.Count,
			})
		}
	}

	c.JSON(http.StatusOK, dto)
}

// ── 课程考试观测 ──

// examStatsDTO 是 /admin/api/exam/stats 的响应。
type examStatsDTO struct {
	Total      int64                     `json:"total"`
	Submitted  int64                     `json:"submitted"`
	AvgScore   float64                   `json:"avg_score"`
	ThisWeek   int64                     `json:"this_week"`
	// SourceQuality 对比 pool(题库抽)vs generated(agent 新出)题的正确率。
	SourceQuality []examSourceQualityDTO `json:"source_quality"`
}

type examSourceQualityDTO struct {
	Source  string  `json:"source"` // pool | generated
	Total   int64   `json:"total"`
	Correct int64   `json:"correct"`
	Rate    float64 `json:"rate"`
}

// ExamStats GET /admin/api/exam/stats。AI 未配置时返回零值。
func (h *adminHandler) ExamStats(c *gin.Context) {
	if h.aiService == nil {
		c.JSON(http.StatusOK, examStatsDTO{SourceQuality: []examSourceQualityDTO{}})
		return
	}
	stats, err := h.aiService.ExamStats()
	dto := examStatsDTO{SourceQuality: []examSourceQualityDTO{}}
	if err == nil {
		dto.Total = stats.Total
		dto.Submitted = stats.Submitted
		dto.AvgScore = stats.AvgScore
		dto.ThisWeek = stats.ThisWeek
	}
	srcQ, err := h.aiService.ExamSourceQuality()
	if err == nil {
		for _, r := range srcQ {
			dto.SourceQuality = append(dto.SourceQuality, examSourceQualityDTO{
				Source: r.Source, Total: r.Total, Correct: r.Correct, Rate: r.Rate,
			})
		}
	}
	c.JSON(http.StatusOK, dto)
}
