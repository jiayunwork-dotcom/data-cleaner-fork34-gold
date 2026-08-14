package monitor

import (
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/data-cleaner/internal/config"
	"github.com/robfig/cron/v3"
)

type TaskStatus struct {
	Name           string
	Running        bool
	LastRunTime    *time.Time
	NextRunTime    *time.Time
	LastDQI        float64
	ActiveAlerts   int
	LastScanResult *ScanResult
}

type Scheduler struct {
	cfg           *config.Config
	scanner       *Scanner
	stateMgr      *StateManager
	notifier      *Notifier
	aggregator    *Aggregator
	baselineMgr   *BaselineManager
	inhibitMgr    *InhibitManager
	escalationMgr *EscalationManager
	cron          *cron.Cron
	status        map[string]*TaskStatus
	running       map[string]bool
	entryIDs      map[string]cron.EntryID
	sourceMu      map[string]*sync.Mutex
	mu            sync.Mutex
	stopCh        chan struct{}
}

func NewScheduler(cfg *config.Config, cacheDir string) *Scheduler {
	poolSize := cfg.Monitor.ConnectionPool
	if poolSize <= 0 {
		poolSize = 5
	}

	scanner := NewScanner(cfg, poolSize, cacheDir)
	stateMgr := NewStateManager(cacheDir)
	notifier := NewNotifier(cfg, cacheDir)
	aggregator := NewAggregator(cfg, notifier)
	baselineMgr := NewBaselineManager(cacheDir)
	inhibitMgr := NewInhibitManager(cfg)
	escalationMgr := NewEscalationManager(cfg)

	status := make(map[string]*TaskStatus)
	running := make(map[string]bool)
	entryIDs := make(map[string]cron.EntryID)
	sourceMu := make(map[string]*sync.Mutex)

	for _, task := range cfg.Monitor.Tasks {
		status[task.Name] = &TaskStatus{Name: task.Name}
		running[task.Name] = false
		if _, exists := sourceMu[task.Source]; !exists {
			sourceMu[task.Source] = &sync.Mutex{}
		}
	}

	return &Scheduler{
		cfg:           cfg,
		scanner:       scanner,
		stateMgr:      stateMgr,
		notifier:      notifier,
		aggregator:    aggregator,
		baselineMgr:   baselineMgr,
		inhibitMgr:    inhibitMgr,
		escalationMgr: escalationMgr,
		status:        status,
		running:       running,
		entryIDs:      entryIDs,
		sourceMu:      sourceMu,
		stopCh:        make(chan struct{}),
	}
}

func (s *Scheduler) Start() error {
	s.cron = cron.New()

	for _, task := range s.cfg.Monitor.Tasks {
		taskCopy := task
		schedule := taskCopy.Schedule

		entryID, err := s.cron.AddFunc(schedule, func() {
			s.RunTask(taskCopy.Name)
		})
		if err != nil {
			return fmt.Errorf("invalid cron schedule '%s' for task '%s': %w", schedule, taskCopy.Name, err)
		}

		s.entryIDs[taskCopy.Name] = entryID
	}

	s.cron.Start()
	s.refreshAllNextRunTimes()
	log.Println("[monitor] scheduler started")

	go s.probeLoop()

	return nil
}

func (s *Scheduler) refreshAllNextRunTimes() {
	if s.cron == nil {
		return
	}
	entries := s.cron.Entries()
	entryMap := make(map[cron.EntryID]time.Time)
	for _, e := range entries {
		entryMap[e.ID] = e.Next
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for taskName, entryID := range s.entryIDs {
		if next, ok := entryMap[entryID]; ok {
			if st, ok := s.status[taskName]; ok {
				st.NextRunTime = &next
			}
		}
	}
}

func (s *Scheduler) probeLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			s.notifier.ProbeRecovery()
		}
	}
}

func (s *Scheduler) RunTask(taskName string) (*ScanResult, error) {
	s.mu.Lock()
	if s.running[taskName] {
		s.mu.Unlock()
		log.Printf("[monitor] task '%s' already running, skipping", taskName)
		return nil, nil
	}
	s.running[taskName] = true
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.running[taskName] = false
		s.mu.Unlock()
	}()

	var task *config.MonitorTask
	for _, t := range s.cfg.Monitor.Tasks {
		if t.Name == taskName {
			task = &t
			break
		}
	}
	if task == nil {
		return nil, fmt.Errorf("task '%s' not found", taskName)
	}

	sourceMutex := s.sourceMu[task.Source]
	sourceMutex.Lock()
	defer sourceMutex.Unlock()

	result, err := s.scanner.Scan(taskName)
	if err != nil {
		log.Printf("[monitor] scan failed for task '%s': %v", taskName, err)
		return nil, err
	}

	now := time.Now()
	s.mu.Lock()
	if st, ok := s.status[taskName]; ok {
		st.LastRunTime = &now
		st.LastDQI = result.DQI
		st.LastScanResult = result
	}
	s.mu.Unlock()

	s.evaluateRules(task, result)

	s.baselineMgr.Persist()

	s.refreshAllNextRunTimes()

	return result, nil
}

func (s *Scheduler) evaluateRules(task *config.MonitorTask, result *ScanResult) {
	for _, rule := range task.Rules {
		value, err := s.scanner.GetMetricValue(result, rule.Metric)
		if err != nil {
			log.Printf("[monitor] metric '%s' not found for task '%s': %v", rule.Metric, task.Name, err)
			continue
		}

		if rule.Mode == "dynamic_baseline" {
			bl := s.baselineMgr.GetOrCreateBaseline(task.Name, rule.Name, rule.Metric, rule)
			bl.AddPoint(value, time.Now())

			blResult := bl.Evaluate(value, rule.Operator, rule.Threshold)
			s.stateMgr.UpdateBaseline(task.Name, rule.Name, blResult.Mean, blResult.Std, blResult.Deviation, blResult.Fallback)

			if !blResult.Triggered {
				previousState, currentState := s.stateMgr.Evaluate(task.Name, rule.Name, value, rule)
				if currentState == StateRESOLVED && (previousState == StateFIRING || previousState == StateSUPPRESSED) {
					s.handleResolved(task, rule, value)
				}
				continue
			}
		}

		previousState, currentState := s.stateMgr.Evaluate(task.Name, rule.Name, value, rule)

		if currentState == StateFIRING {
			suppressed, suppressedBy := s.inhibitMgr.IsSuppressed(rule.Name, rule.Labels, s.stateMgr)
			if suppressed {
				s.stateMgr.SetSuppressed(task.Name, rule.Name, suppressedBy)
				currentState = StateSUPPRESSED
			}
		}

		if currentState == StateFIRING {
			s.handleFiring(task, rule, value, previousState)
		}

		if currentState == StateSUPPRESSED {
			log.Printf("[monitor] rule '%s' in task '%s' is SUPPRESSED by '%s'", rule.Name, task.Name, s.stateMgr.GetState(task.Name, rule.Name).SuppressedBy)
		}

		if currentState == StateRESOLVED && (previousState == StateFIRING || previousState == StateSUPPRESSED) {
			s.handleResolved(task, rule, value)
		}
	}

	s.checkInhibitRecovery(task)

	s.mu.Lock()
	if st, ok := s.status[task.Name]; ok {
		alerts := s.stateMgr.GetFiringAndPendingForTask(task.Name)
		st.ActiveAlerts = len(alerts)
	}
	s.mu.Unlock()
}

func (s *Scheduler) handleFiring(task *config.MonitorTask, rule config.AlertRule, value float64, previousState AlertState) {
	st := s.stateMgr.GetState(task.Name, rule.Name)
	if st == nil {
		return
	}

	escInfo := &EscalationInfo{
		CurrentLevel: st.EscalationLevel,
		NotifyCount:  st.NotifyCount,
	}
	if st.FiredAt != nil {
		escInfo.FiredAt = *st.FiredAt
	}
	if st.LastEscalatedAt != nil {
		escInfo.LastEscalatedAt = *st.LastEscalatedAt
	}

	now := time.Now()
	newLevel := s.escalationMgr.ComputeEscalationLevel(rule.Name, escInfo, now)

	shouldNotify := s.escalationMgr.ShouldNotify(escInfo, newLevel, st.SilencedUntil, now)
	if newLevel > st.EscalationLevel {
		shouldNotify = true
	}

	if shouldNotify {
		channels := s.escalationMgr.GetChannelsForLevel(rule.Name, newLevel, task.Channels)

		ctx := AlertContext{
			RuleName:        rule.Name,
			CurrentValue:    value,
			Threshold:       rule.Threshold,
			Source:          task.Source,
			TaskName:        task.Name,
			Operator:        rule.Operator,
			State:           string(StateFIRING),
			PreviousState:   string(previousState),
			Timestamp:       now.Format(time.RFC3339),
			Template:        rule.Template,
			EscalationLevel: newLevel,
			NotifyCount:     st.NotifyCount + 1,
		}

		if st.FiredAt != nil {
			ctx.FiredAt = st.FiredAt.Format(time.RFC3339)
			ctx.Duration = now.Sub(*st.FiredAt).Round(time.Second).String()
		}

		if st.BaselineMean != 0 || st.BaselineStd != 0 {
			ctx.BaselineMean = st.BaselineMean
			ctx.BaselineStd = st.BaselineStd
			ctx.BaselineDev = st.BaselineDev
		}

		s.notifier.Notify(ctx, channels)

		s.stateMgr.IncrementNotifyCount(task.Name, rule.Name)

		if newLevel > st.EscalationLevel {
			s.stateMgr.UpdateEscalation(task.Name, rule.Name, newLevel)
		}

		silenceDur := 1 * time.Hour
		if rule.Silence != "" {
			if d, err := time.ParseDuration(rule.Silence); err == nil {
				silenceDur = d
			}
		}
		s.stateMgr.SetSilenced(task.Name, rule.Name, now.Add(silenceDur))
	}
}

func (s *Scheduler) handleResolved(task *config.MonitorTask, rule config.AlertRule, value float64) {
	st := s.stateMgr.GetState(task.Name, rule.Name)
	if st == nil {
		return
	}

	wasSuppressed := st.State == StateSUPPRESSED || st.SuppressedBy != ""

	ctx := AlertContext{
		RuleName:         rule.Name,
		CurrentValue:     value,
		Threshold:        rule.Threshold,
		Source:           task.Source,
		TaskName:         task.Name,
		Operator:         rule.Operator,
		State:            string(StateRESOLVED),
		PreviousState:    string(st.State),
		Timestamp:        time.Now().Format(time.RFC3339),
		Template:         rule.Template,
		SuppressionLifted: wasSuppressed,
	}

	if st.FiredAt != nil {
		ctx.FiredAt = st.FiredAt.Format(time.RFC3339)
	}

	s.aggregator.Add(ctx, task.Name, task.Source)
}

func (s *Scheduler) checkInhibitRecovery(task *config.MonitorTask) {
	allStates := s.stateMgr.AllStates()
	for _, st := range allStates {
		if st.State != StateSUPPRESSED {
			continue
		}

		suppressedBy := st.SuppressedBy
		if suppressedBy == "" {
			continue
		}

		parentState := s.stateMgr.GetStateByRuleName(suppressedBy)
		if parentState == nil || parentState.State != StateFIRING {
			oldState := s.stateMgr.ClearSuppressed(st.TaskName, st.RuleName)
			if oldState == StateFIRING {
				var rule *config.AlertRule
				for _, t := range s.cfg.Monitor.Tasks {
					for _, r := range t.Rules {
						if r.Name == st.RuleName {
							rule = &r
							break
						}
					}
				}
				if rule != nil {
					now := time.Now()
					ctx := AlertContext{
						RuleName:         st.RuleName,
						CurrentValue:     st.LastValue,
						Threshold:        rule.Threshold,
						Source:           task.Source,
						TaskName:         st.TaskName,
						Operator:         rule.Operator,
						State:            string(StateFIRING),
						PreviousState:    string(StateSUPPRESSED),
						Timestamp:        now.Format(time.RFC3339),
						Template:         rule.Template,
						SuppressionLifted: true,
					}
					if st.FiredAt != nil {
						ctx.FiredAt = st.FiredAt.Format(time.RFC3339)
						ctx.Duration = now.Sub(*st.FiredAt).Round(time.Second).String()
					}

					var taskChannels []string
					for _, t := range s.cfg.Monitor.Tasks {
						if t.Name == st.TaskName {
							taskChannels = t.Channels
							break
						}
					}
					s.notifier.Notify(ctx, taskChannels)
					s.stateMgr.IncrementNotifyCount(st.TaskName, st.RuleName)
				}
			}
		}
	}
}

func (s *Scheduler) Stop() {
	if s.cron != nil {
		ctx := s.cron.Stop()
		<-ctx.Done()
	}

	close(s.stopCh)

	s.baselineMgr.Persist()
	s.aggregator.Stop()
	s.notifier.Close()
	log.Println("[monitor] scheduler stopped")
}

func (s *Scheduler) GracefulStop(timeout time.Duration) {
	done := make(chan struct{})
	go func() {
		s.Stop()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(timeout):
		log.Println("[monitor] graceful stop timeout, forcing exit")
	}
}

func (s *Scheduler) GetStatus() []*TaskStatus {
	s.mu.Lock()
	defer s.mu.Unlock()

	var result []*TaskStatus
	for _, task := range s.cfg.Monitor.Tasks {
		if st, ok := s.status[task.Name]; ok {
			result = append(result, st)
		}
	}
	return result
}

func (s *Scheduler) GetHistory(taskName string, n int) []*ScanResult {
	return s.scanner.GetHistory(taskName, n)
}

func (s *Scheduler) GetAlerts() []*RuleState {
	return s.stateMgr.GetFiringAndPending()
}

func (s *Scheduler) GetAlertsForTask(taskName string) []*RuleState {
	return s.stateMgr.GetFiringAndPendingForTask(taskName)
}

func (s *Scheduler) GetNotifier() *Notifier {
	return s.notifier
}

func (s *Scheduler) GetStateManager() *StateManager {
	return s.stateMgr
}

func (s *Scheduler) GetBaselineManager() *BaselineManager {
	return s.baselineMgr
}
