// Package elo 实现标准 ELO rating 算法。
// 用于跨局累积每个 LLM 模型的相对强弱。
package elo

import "math"

// DefaultK 是默认 K 因子(控制单局 rating 波动幅度)。
const DefaultK = 32.0

// DefaultInitial 是新玩家的初始 rating。
const DefaultInitial = 1500.0

// Expected 返回 A 对 B 的预期胜率(0-1)。
// 公式:E_A = 1 / (1 + 10^((R_B - R_A)/400))
func Expected(ratingA, ratingB float64) float64 {
	return 1.0 / (1.0 + math.Pow(10, (ratingB-ratingA)/400.0))
}

// Score 是实际结局,A 的视角。
type Score float64

const (
	Win  Score = 1.0
	Loss Score = 0.0
	Draw Score = 0.5
)

// Update 按一局结果更新双方 rating,返回新 rating。
// K <= 0 时用 DefaultK。
func Update(ratingA, ratingB float64, score Score, k float64) (newA, newB float64) {
	if k <= 0 {
		k = DefaultK
	}
	eA := Expected(ratingA, ratingB)
	eB := 1.0 - eA
	deltaA := k * (float64(score) - eA)
	deltaB := k * (float64(1.0-score) - eB)
	return ratingA + deltaA, ratingB + deltaB
}
