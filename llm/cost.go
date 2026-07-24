package llm

func calculateCost(m *Model, u *Usage) {
	rates := PricingTier{
		Input:      m.Cost.Input,
		Output:     m.Cost.Output,
		CacheRead:  m.Cost.CacheRead,
		CacheWrite: m.Cost.CacheWrite,
	}
	inputTokens := u.Input + u.CacheRead + u.CacheWrite
	matched := -1
	for _, tier := range m.Cost.Tiers {
		if inputTokens > tier.InputTokensAbove && tier.InputTokensAbove > matched {
			rates = tier
			matched = tier.InputTokensAbove
		}
	}

	// Anthropic charges 2x base input for 1h cache writes.
	longWrite := u.CacheWrite1h
	shortWrite := u.CacheWrite - longWrite
	u.Cost.Input = rates.Input / 1_000_000 * float64(u.Input)
	u.Cost.Output = rates.Output / 1_000_000 * float64(u.Output)
	u.Cost.CacheRead = rates.CacheRead / 1_000_000 * float64(u.CacheRead)
	u.Cost.CacheWrite = (rates.CacheWrite*float64(shortWrite) + rates.Input*2*float64(longWrite)) / 1_000_000
	u.Cost.Total = u.Cost.Input + u.Cost.Output + u.Cost.CacheRead + u.Cost.CacheWrite
}

var thinkingOrder = []ThinkingLevel{
	ThinkingOff, ThinkingMinimal, ThinkingLow, ThinkingMedium, ThinkingHigh, ThinkingXHigh, ThinkingMax,
}

// SupportedThinkingLevels returns the thinking levels a model accepts.
func SupportedThinkingLevels(m *Model) []ThinkingLevel {
	if !m.Reasoning {
		return []ThinkingLevel{ThinkingOff}
	}
	var out []ThinkingLevel
	for _, level := range thinkingOrder {
		if m.NullLevels[level] {
			continue
		}
		if level == ThinkingXHigh || level == ThinkingMax {
			if _, ok := m.ThinkingLevelMap[level]; !ok {
				continue
			}
		}
		out = append(out, level)
	}
	return out
}

func levelIndex(level ThinkingLevel) int {
	for i, l := range thinkingOrder {
		if l == level {
			return i
		}
	}
	return -1
}

// ClampThinkingLevel snaps a requested level to the nearest supported one,
// preferring a higher level, then a lower one, matching pi's clampThinkingLevel.
func ClampThinkingLevel(m *Model, level ThinkingLevel) ThinkingLevel {
	available := SupportedThinkingLevels(m)
	contains := func(l ThinkingLevel) bool {
		for _, a := range available {
			if a == l {
				return true
			}
		}
		return false
	}
	fallback := func() ThinkingLevel {
		if len(available) > 0 {
			return available[0]
		}
		return ThinkingOff
	}

	if contains(level) {
		return level
	}
	idx := levelIndex(level)
	if idx == -1 {
		return fallback()
	}
	for i := idx; i < len(thinkingOrder); i++ {
		if contains(thinkingOrder[i]) {
			return thinkingOrder[i]
		}
	}
	for i := idx - 1; i >= 0; i-- {
		if contains(thinkingOrder[i]) {
			return thinkingOrder[i]
		}
	}
	return fallback()
}
