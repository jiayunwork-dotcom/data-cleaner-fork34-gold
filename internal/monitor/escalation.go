package monitor

import (
	"fmt"
	"time"

	"github.com/data-cleaner/internal/config"
)

type EscalationInfo struct {
	CurrentLevel    int       `json:"current_level"`
	LastEscalatedAt time.Time `json:"last_escalated_at,omitempty"`
	NotifyCount     int       `json:"notify_count"`
	FiredAt         time.Time `json:"fired_at,omitempty"`
}

type EscalationManager struct {
	cfg *config.Config
}

func NewEscalationManager(cfg *config.Config) *EscalationManager {
	return &EscalationManager{cfg: cfg}
}

func (em *EscalationManager) GetRuleEscalation(ruleName string) []config.EscalationLevel {
	for _, task := range em.cfg.Monitor.Tasks {
		for _, rule := range task.Rules {
			if rule.Name == ruleName {
				return rule.Escalation
			}
		}
	}
	return nil
}

func (em *EscalationManager) ComputeEscalationLevel(ruleName string, info *EscalationInfo, now time.Time) int {
	if info == nil || info.FiredAt.IsZero() {
		return 0
	}

	levels := em.getSortedLevelsForRule(ruleName)
	if len(levels) == 0 {
		return 0
	}

	elapsed := now.Sub(info.FiredAt)
	currentLevel := 0

	for i, lvl := range levels {
		if elapsed >= lvl.duration {
			currentLevel = i + 1
		}
	}

	return currentLevel
}

func (em *EscalationManager) ShouldNotify(info *EscalationInfo, newLevel int, silencedUntil time.Time, now time.Time) bool {
	if info == nil {
		return newLevel == 0
	}

	if newLevel > info.CurrentLevel {
		return true
	}

	if now.After(silencedUntil) {
		return true
	}

	return false
}

func (em *EscalationManager) GetChannelsForLevel(ruleName string, level int, baseChannels []string) []string {
	levels := em.getSortedLevelsForRule(ruleName)

	channels := make([]string, len(baseChannels))
	copy(channels, baseChannels)

	if level > 0 && level <= len(levels) {
		channels = append(channels, levels[level-1].channels...)
	}

	return dedupeStrings(channels)
}

func (em *EscalationManager) getSortedLevelsForRule(ruleName string) []escalationDuration {
	for _, task := range em.cfg.Monitor.Tasks {
		for _, rule := range task.Rules {
			if rule.Name != ruleName {
				continue
			}
			var levels []escalationDuration
			for _, lvl := range rule.Escalation {
				d, err := time.ParseDuration(lvl.After)
				if err != nil {
					continue
				}
				levels = append(levels, escalationDuration{
					after:     lvl.After,
					duration:  d,
					channels:  lvl.Channels,
				})
			}
			return levels
		}
	}
	return nil
}

type escalationDuration struct {
	after    string
	duration time.Duration
	channels []string
}

func dedupeStrings(ss []string) []string {
	seen := make(map[string]bool)
	var result []string
	for _, s := range ss {
		if !seen[s] {
			seen[s] = true
			result = append(result, s)
		}
	}
	return result
}

func FormatEscalationPayload(info *EscalationInfo, level int) map[string]interface{} {
	payload := map[string]interface{}{
		"escalation_level": level,
	}

	if info != nil {
		if !info.FiredAt.IsZero() {
			duration := time.Since(info.FiredAt)
			payload["fired_at"] = info.FiredAt.Format(time.RFC3339)
			payload["duration"] = fmt.Sprintf("%s", duration.Round(time.Second))
		}
		payload["notify_count"] = info.NotifyCount
	}

	return payload
}
