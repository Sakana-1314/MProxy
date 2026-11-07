package api

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"cdnproxy/internal/store"
)

type dashboardPoint struct {
	TS       int64 `json:"ts"`
	Requests int64 `json:"requests"`
	Bytes    int64 `json:"bytes"`
}

func (a *API) dashboard(c *gin.Context) {
	rangeKey := c.Query("range")
	if rangeKey == "" {
		rangeKey = "24h"
	}
	now := time.Now()
	local := now.In(time.Local)

	var (
		start         time.Time
		bucketCount   int
		bucketSeconds int
	)
	switch rangeKey {
	case "24h":
		end := time.Date(local.Year(), local.Month(), local.Day(), local.Hour(), 0, 0, 0, time.Local)
		bucketCount = 24
		bucketSeconds = 3600
		start = end.Add(-time.Duration(bucketCount-1) * time.Hour)
	case "7d":
		end := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.Local)
		bucketCount = 7
		bucketSeconds = 86400
		start = end.AddDate(0, 0, -(bucketCount - 1))
	case "30d":
		end := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.Local)
		bucketCount = 30
		bucketSeconds = 86400
		start = end.AddDate(0, 0, -(bucketCount - 1))
	default:
		fail(c, http.StatusBadRequest, "range 仅支持 24h、7d、30d")
		return
	}

	rows, err := a.store.TrafficBucketsSince(start.Unix())
	if err != nil {
		a.logger.Printf("[api] 查询流量统计失败: %v", err)
		fail(c, http.StatusInternalServerError, "查询统计数据失败")
		return
	}

	series := make([]dashboardPoint, bucketCount)
	if bucketSeconds == 3600 {
		byBucket := make(map[int64]store.TrafficBucket, len(rows))
		for _, row := range rows {
			byBucket[row.Bucket] = row
		}
		for index := 0; index < bucketCount; index++ {
			ts := start.Add(time.Duration(index) * time.Hour).Unix()
			row := byBucket[ts]
			series[index] = dashboardPoint{TS: ts, Requests: row.Requests, Bytes: row.Bytes}
		}
	} else {
		byDay := make(map[int64]dashboardPoint, bucketCount)
		for _, row := range rows {
			day := time.Unix(row.Bucket, 0).In(time.Local)
			dayStart := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.Local).Unix()
			point := byDay[dayStart]
			point.TS = dayStart
			point.Requests += row.Requests
			point.Bytes += row.Bytes
			byDay[dayStart] = point
		}
		for index := 0; index < bucketCount; index++ {
			day := start.AddDate(0, 0, index)
			point := byDay[day.Unix()]
			point.TS = day.Unix()
			series[index] = point
		}
	}

	todayTotals, err := a.store.TrafficTotalsSince(store.StartOfDay(now).Unix())
	if err != nil {
		a.logger.Printf("[api] 查询今日流量失败: %v", err)
		fail(c, http.StatusInternalServerError, "查询统计数据失败")
		return
	}
	totalUsers, err := a.store.CountUsers()
	if err != nil {
		a.logger.Printf("[api] 查询用户数量失败: %v", err)
		fail(c, http.StatusInternalServerError, "查询统计数据失败")
		return
	}
	enabledRules, err := a.store.CountEnabledRules()
	if err != nil {
		a.logger.Printf("[api] 查询规则数量失败: %v", err)
		fail(c, http.StatusInternalServerError, "查询统计数据失败")
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"range": rangeKey,
		"summary": gin.H{
			"today_requests": todayTotals.Requests,
			"today_bytes":    todayTotals.Bytes,
			"total_users":    totalUsers,
			"enabled_rules":  enabledRules,
		},
		"series": series,
	})
}
