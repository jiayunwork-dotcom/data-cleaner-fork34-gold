package monitor

import (
	"github.com/data-cleaner/internal/config"
)

type InhibitManager struct {
	rules   []config.InhibitRule
	ruleMap map[string]*config.AlertRule
}

func NewInhibitManager(cfg *config.Config) *InhibitManager {
	ruleMap := make(map[string]*config.AlertRule)
	for i := range cfg.Monitor.Tasks {
		for j := range cfg.Monitor.Tasks[i].Rules {
			r := &cfg.Monitor.Tasks[i].Rules[j]
			ruleMap[r.Name] = r
		}
	}

	return &InhibitManager{
		rules:   cfg.Monitor.InhibitRules,
		ruleMap: ruleMap,
	}
}

func (im *InhibitManager) IsSuppressed(ruleName string, ruleLabels map[string]string, stateMgr *StateManager) (bool, string) {
	for _, ir := range im.rules {
		for _, target := range ir.TargetRules {
			if target != ruleName {
				continue
			}

			sourceRule, ok := im.ruleMap[ir.SourceRule]
			if !ok {
				continue
			}

			if !labelsMatch(ir.EqualLabels, sourceRule.Labels, ruleLabels) {
				continue
			}

			sourceState := stateMgr.GetStateByRuleName(ir.SourceRule)
			if sourceState != nil && sourceState.State == StateFIRING {
				return true, ir.SourceRule
			}

			if im.isTransitivelySuppressed(ir.SourceRule, ruleLabels, stateMgr, make(map[string]bool)) {
				return true, ir.SourceRule
			}
		}
	}
	return false, ""
}

func (im *InhibitManager) isTransitivelySuppressed(ruleName string, targetLabels map[string]string, stateMgr *StateManager, visited map[string]bool) bool {
	if visited[ruleName] {
		return false
	}
	visited[ruleName] = true

	for _, ir := range im.rules {
		if ir.SourceRule != ruleName {
			continue
		}
		for _, target := range ir.TargetRules {
			sourceRule, ok := im.ruleMap[ir.SourceRule]
			if !ok {
				continue
			}
			if !labelsMatch(ir.EqualLabels, sourceRule.Labels, targetLabels) {
				continue
			}

			sourceState := stateMgr.GetStateByRuleName(ir.SourceRule)
			if sourceState != nil && sourceState.State == StateFIRING {
				return true
			}

			if im.isTransitivelySuppressed(target, targetLabels, stateMgr, visited) {
				return true
			}
		}
	}
	return false
}

func (im *InhibitManager) GetSuppressedBySource(sourceRuleName string, ruleLabels map[string]string, stateMgr *StateManager) []string {
	var suppressed []string
	for _, ir := range im.rules {
		if ir.SourceRule != sourceRuleName {
			continue
		}
		for _, target := range ir.TargetRules {
			targetRule, ok := im.ruleMap[target]
			if !ok {
				continue
			}
			if !labelsMatch(ir.EqualLabels, targetRule.Labels, ruleLabels) {
				continue
			}

			targetState := stateMgr.GetStateByRuleName(target)
			if targetState != nil && targetState.State == StateFIRING {
				suppressed = append(suppressed, target)
			}
		}
	}
	return suppressed
}

func labelsMatch(equalLabels []string, sourceLabels, targetLabels map[string]string) bool {
	for _, label := range equalLabels {
		sv := sourceLabels[label]
		tv := targetLabels[label]
		if sv != tv {
			return false
		}
	}
	return true
}
