package elo

import (
	"math"
	"testing"
)

func TestExpectedSymmetric(t *testing.T) {
	// 同分时,预期胜率 0.5
	if got := Expected(1500, 1500); math.Abs(got-0.5) > 1e-9 {
		t.Fatalf("Expected(1500,1500) = %v, want 0.5", got)
	}
	// A 比 B 高 400 分,A 胜率约 0.909
	got := Expected(1900, 1500)
	if math.Abs(got-0.9091) > 0.001 {
		t.Fatalf("Expected(1900,1500) = %v, want ~0.909", got)
	}
	// 对称性:Expected(A,B) + Expected(B,A) = 1
	if a, b := Expected(1500, 1700), Expected(1700, 1500); math.Abs(a+b-1.0) > 1e-9 {
		t.Fatalf("symmetry broken: %v + %v = %v", a, b, a+b)
	}
}

func TestUpdateWin(t *testing.T) {
	// 平分平局双方不变;一方赢分,赢家涨、输家跌,且和=守恒
	newA, newB := Update(1500, 1500, Win, 32)
	if newA <= 1500 {
		t.Fatalf("winner rating %v should increase", newA)
	}
	if newB >= 1500 {
		t.Fatalf("loser rating %v should decrease", newB)
	}
	// 守恒:delta 量级相同方向相反
	if math.Abs((newA-1500)+(newB-1500)) > 1e-9 {
		t.Fatalf("rating changes not conserved: %+v / %+v", newA-1500, newB-1500)
	}
}

func TestUpdateDraw(t *testing.T) {
	newA, newB := Update(1500, 1500, Draw, 32)
	if math.Abs(newA-1500) > 1e-9 || math.Abs(newB-1500) > 1e-9 {
		t.Fatalf("equal-rating draw should leave ratings unchanged: %v %v", newA, newB)
	}
}

func TestUpdateAsymmetricRatings(t *testing.T) {
	// 强者(1900)赢弱者(1500):因为预期胜率 0.909,实际赢的增益小
	newA, _ := Update(1900, 1500, Win, 32)
	gain := newA - 1900
	if gain > 5 {
		t.Fatalf("favorite's win gain %v should be small (<5)", gain)
	}
	// 弱者爆冷赢:增益应该大
	newA2, _ := Update(1500, 1900, Win, 32)
	if newA2-1500 < 20 {
		t.Fatalf("underdog's win gain %v should be large (>20)", newA2-1500)
	}
}

func TestUpdateUsesDefaultKWhenZero(t *testing.T) {
	// k=0 应使用 DefaultK=32,结果与显式 k=32 相同
	a1, b1 := Update(1500, 1500, Win, 0)
	a2, b2 := Update(1500, 1500, Win, 32)
	if math.Abs(a1-a2) > 1e-9 || math.Abs(b1-b2) > 1e-9 {
		t.Fatalf("k=0 should equal k=32: (%v,%v) vs (%v,%v)", a1, b1, a2, b2)
	}
}
