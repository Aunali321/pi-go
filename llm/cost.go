package llm

func calculateCost(m *Model, u *Usage) {
	u.Cost.Input = m.Cost.Input / 1_000_000 * float64(u.Input)
	u.Cost.Output = m.Cost.Output / 1_000_000 * float64(u.Output)
	u.Cost.CacheRead = m.Cost.CacheRead / 1_000_000 * float64(u.CacheRead)
	u.Cost.CacheWrite = m.Cost.CacheWrite / 1_000_000 * float64(u.CacheWrite)
	u.Cost.Total = u.Cost.Input + u.Cost.Output + u.Cost.CacheRead + u.Cost.CacheWrite
}

var thinkingOrder = []ThinkingLevel{
	ThinkingOff, ThinkingMinimal, ThinkingLow, ThinkingMedium, ThinkingHigh, ThinkingXHigh,
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
		if level == ThinkingXHigh {
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
