package engine

import "math/rand"

// Rank 表示点数:2..14,其中 14=A。
type Rank int8

// Suit 表示花色:0=黑桃 ♠, 1=红心 ♥, 2=方块 ♦, 3=梅花 ♣。
type Suit int8

// Card 是一张扑克牌,值类型,可比较、可作 map key。
type Card struct {
	Rank Rank
	Suit Suit
}

// Rand 是 math/rand.Rand 的别名,用于随机源注入。
type Rand = rand.Rand

var rankChars = [...]byte{'2', '3', '4', '5', '6', '7', '8', '9', 'T', 'J', 'Q', 'K', 'A'}
var suitChars = [...]byte{'s', 'h', 'd', 'c'}

// String 返回两张牌的常见文本表示,如 "As" / "Th" / "2d" / "Kc"。
func (c Card) String() string {
	return string([]byte{rankChars[c.Rank-2], suitChars[c.Suit]})
}

// DeckOption 用于在 NewDeck 注入随机源等配置。
type DeckOption func(*Deck)

// WithRand 注入一个已 seed 化的 *Rand,便于复现某一局。
func WithRand(r *Rand) DeckOption {
	return func(d *Deck) { d.rng = r }
}

// Deck 是一副牌。
type Deck struct {
	cards []Card
	rng   *Rand
}

// NewDeck 返回一张未洗、按花色与点数顺序排列的 52 张完整牌。
// 默认随机源为包级全局源;测试请用 WithRand 注入固定 seed。
func NewDeck(opts ...DeckOption) *Deck {
	d := &Deck{cards: make([]Card, 0, 52)}
	for s := Suit(0); s < 4; s++ {
		for r := Rank(2); r <= 14; r++ {
			d.cards = append(d.cards, Card{Rank: r, Suit: s})
		}
	}
	for _, o := range opts {
		o(d)
	}
	return d
}

// Remaining 返回牌堆中剩余牌数。
func (d *Deck) Remaining() int { return len(d.cards) }

// Draw 取出牌堆顶一张。ok=false 表示已发完。
func (d *Deck) Draw() (Card, bool) {
	if len(d.cards) == 0 {
		return Card{}, false
	}
	c := d.cards[len(d.cards)-1]
	d.cards = d.cards[:len(d.cards)-1]
	return c, true
}

// rngOrDefault 返回注入的随机源;未注入则用全局源。
func (d *Deck) rngOrDefault() *Rand {
	if d.rng != nil {
		return d.rng
	}
	return globalRand
}

var globalRand = rand.New(rand.NewSource(1)) // 默认 seed=1,行为可预测

// Shuffle 用注入或默认随机源洗牌(Fisher-Yates)。
func (d *Deck) Shuffle() {
	r := d.rngOrDefault()
	for i := len(d.cards) - 1; i > 0; i-- {
		j := r.Intn(i + 1)
		d.cards[i], d.cards[j] = d.cards[j], d.cards[i]
	}
}
