package monitor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/data-cleaner/internal/config"
)

type AlertState string

const (
	StateOK        AlertState = "OK"
	StatePENDING   AlertState = "PENDING"
	StateFIRING    AlertState = "FIRING"
	StateRESOLVED  AlertState = "RESOLVED"
	StateSUPPRESSED AlertState = "SUPPRESSED"
)

type RuleState struct {
	RuleName         string     `json:"rule_name"`
	TaskName         string     `json:"task_name"`
	State            AlertState `json:"state"`
	ConsecutiveCount int        `json:"consecutive_count"`
	LastValue        float64    `json:"last_value"`
	LastEvalTime     time.Time  `json:"last_eval_time"`
	FiredAt          *time.Time `json:"fired_at,omitempty"`
	ResolvedAt       *time.Time `json:"resolved_at,omitempty"`
	SilencedUntil    time.Time  `json:"silenced_until,omitempty"`
	SuppressedBy     string     `json:"suppressed_by,omitempty"`
	EscalationLevel  int        `json:"escalation_level"`
	NotifyCount      int        `json:"notify_count"`
	LastEscalatedAt  *time.Time `json:"last_escalated_at,omitempty"`
	BaselineMean     float64    `json:"baseline_mean,omitempty"`
	BaselineStd      float64    `json:"baseline_std,omitempty"`
	BaselineDev      float64    `json:"baseline_dev,omitempty"`
	BaselineFallback bool       `json:"baseline_fallback,omitempty"`
}

type StateManager struct {
	mu       sync.Mutex
	states   map[string]*RuleState
	filePath string
}

func stateKey(taskName, ruleName string) string {
	return taskName + "|" + ruleName
}

func NewStateManager(cacheDir string) *StateManager {
	dir := filepath.Join(cacheDir, "monitor")
	os.MkdirAll(dir, 0755)
	fp := filepath.Join(dir, "state.json")

	sm := &StateManager{
		states:   make(map[string]*RuleState),
		filePath: fp,
	}
	sm.load()
	return sm
}

func (sm *StateManager) load() {
	data, err := os.ReadFile(sm.filePath)
	if err != nil {
		return
	}
	var states []*RuleState
	if err := json.Unmarshal(data, &states); err != nil {
		return
	}
	for _, s := range states {
		sm.states[stateKey(s.TaskName, s.RuleName)] = s
	}
}

func (sm *StateManager) save() {
	var states []*RuleState
	for _, s := range sm.states {
		states = append(states, s)
	}
	data, err := json.MarshalIndent(states, "", "  ")
	if err != nil {
		return
	}
	os.WriteFile(sm.filePath, data, 0644)
}

func (sm *StateManager) GetState(taskName, ruleName string) *RuleState {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	key := stateKey(taskName, ruleName)
	if s, ok := sm.states[key]; ok {
		return s
	}
	return &RuleState{
		RuleName: ruleName,
		TaskName: taskName,
		State:    StateOK,
	}
}

func (sm *StateManager) GetStateByRuleName(ruleName string) *RuleState {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	for _, s := range sm.states {
		if s.RuleName == ruleName {
			return s
		}
	}
	return nil
}

func (sm *StateManager) Evaluate(taskName, ruleName string, value float64, rule config.AlertRule) (AlertState, AlertState) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	key := stateKey(taskName, ruleName)
	current, ok := sm.states[key]
	if !ok {
		current = &RuleState{
			RuleName: ruleName,
			TaskName: taskName,
			State:    StateOK,
		}
		sm.states[key] = current
	}

	previousState := current.State
	current.LastValue = value
	current.LastEvalTime = time.Now()

	var triggered bool
	if rule.Mode == "dynamic_baseline" {
		triggered = true
	} else {
		triggered = evaluateCondition(value, rule.Operator, rule.Threshold)
	}

	if triggered {
		current.ConsecutiveCount++
		if current.ConsecutiveCount >= rule.ForCount {
			if current.State != StateFIRING && current.State != StateSUPPRESSED {
				now := time.Now()
				current.FiredAt = &now
				current.EscalationLevel = 0
				current.NotifyCount = 0
				current.LastEscalatedAt = nil
			}
			current.State = StateFIRING
		} else {
			current.State = StatePENDING
		}
	} else {
		if current.State == StateFIRING || current.State == StateSUPPRESSED {
			now := time.Now()
			current.State = StateRESOLVED
			current.ResolvedAt = &now
			current.SuppressedBy = ""
			current.EscalationLevel = 0
			current.NotifyCount = 0
			current.LastEscalatedAt = nil
		} else {
			current.State = StateOK
		}
		current.ConsecutiveCount = 0
	}

	sm.save()
	return previousState, current.State
}

func (sm *StateManager) SetSuppressed(taskName, ruleName, suppressedBy string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	key := stateKey(taskName, ruleName)
	if s, ok := sm.states[key]; ok {
		s.State = StateSUPPRESSED
		s.SuppressedBy = suppressedBy
		sm.save()
	}
}

func (sm *StateManager) ClearSuppressed(taskName, ruleName string) AlertState {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	key := stateKey(taskName, ruleName)
	if s, ok := sm.states[key]; ok {
		if s.State == StateSUPPRESSED {
			s.State = StateFIRING
			s.SuppressedBy = ""
			sm.save()
			return StateFIRING
		}
	}
	return StateOK
}

func (sm *StateManager) UpdateBaseline(taskName, ruleName string, mean, std, dev float64, fallback bool) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	key := stateKey(taskName, ruleName)
	if s, ok := sm.states[key]; ok {
		s.BaselineMean = mean
		s.BaselineStd = std
		s.BaselineDev = dev
		s.BaselineFallback = fallback
		sm.save()
	}
}

func (sm *StateManager) UpdateEscalation(taskName, ruleName string, level int) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	key := stateKey(taskName, ruleName)
	if s, ok := sm.states[key]; ok {
		s.EscalationLevel = level
		now := time.Now()
		s.LastEscalatedAt = &now
		sm.save()
	}
}

func (sm *StateManager) IncrementNotifyCount(taskName, ruleName string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	key := stateKey(taskName, ruleName)
	if s, ok := sm.states[key]; ok {
		s.NotifyCount++
		sm.save()
	}
}

func (sm *StateManager) IsSilenced(taskName, ruleName string) bool {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	key := stateKey(taskName, ruleName)
	if s, ok := sm.states[key]; ok {
		return time.Now().Before(s.SilencedUntil)
	}
	return false
}

func (sm *StateManager) SetSilenced(taskName, ruleName string, until time.Time) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	key := stateKey(taskName, ruleName)
	if s, ok := sm.states[key]; ok {
		s.SilencedUntil = until
		sm.save()
	}
}

func (sm *StateManager) GetFiringAndPending() []*RuleState {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	var result []*RuleState
	for _, s := range sm.states {
		if s.State == StateFIRING || s.State == StatePENDING || s.State == StateSUPPRESSED {
			result = append(result, s)
		}
	}
	return result
}

func (sm *StateManager) GetFiringAndPendingForTask(taskName string) []*RuleState {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	var result []*RuleState
	for _, s := range sm.states {
		if s.TaskName == taskName && (s.State == StateFIRING || s.State == StatePENDING || s.State == StateSUPPRESSED) {
			result = append(result, s)
		}
	}
	return result
}

func (sm *StateManager) AllStates() []*RuleState {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	var result []*RuleState
	for _, s := range sm.states {
		result = append(result, s)
	}
	return result
}

func evaluateCondition(value float64, operator string, threshold float64) bool {
	switch operator {
	case "<":
		return value < threshold
	case ">":
		return value > threshold
	case "<=":
		return value <= threshold
	case ">=":
		return value >= threshold
	case "==":
		return value == threshold
	default:
		return false
	}
}
