package monitor

import (
	"sync"
	"time"

	"github.com/data-cleaner/internal/config"
)

type pendingAlert struct {
	ctx      AlertContext
	taskName string
	source   string
}

type Aggregator struct {
	window   time.Duration
	pending  map[string][]pendingAlert
	timer    map[string]*time.Timer
	notifier *Notifier
	mu       sync.Mutex
	channels map[string][]string
}

func NewAggregator(cfg *config.Config, notifier *Notifier) *Aggregator {
	window := 30 * time.Second
	if cfg.Monitor.AggregateWindow != "" {
		if d, err := time.ParseDuration(cfg.Monitor.AggregateWindow); err == nil {
			window = d
		}
	}

	chMap := make(map[string][]string)
	for _, task := range cfg.Monitor.Tasks {
		chMap[task.Name] = task.Channels
	}

	return &Aggregator{
		window:   window,
		pending:  make(map[string][]pendingAlert),
		timer:    make(map[string]*time.Timer),
		notifier: notifier,
		channels: chMap,
	}
}

func (ag *Aggregator) Add(ctx AlertContext, taskName, source string) {
	ag.mu.Lock()
	defer ag.mu.Unlock()

	key := taskName + "|" + source

	ag.pending[key] = append(ag.pending[key], pendingAlert{
		ctx:      ctx,
		taskName: taskName,
		source:   source,
	})

	if _, exists := ag.timer[key]; !exists {
		t := time.AfterFunc(ag.window, func() {
			ag.flush(key)
		})
		ag.timer[key] = t
	}
}

func (ag *Aggregator) flush(key string) {
	ag.mu.Lock()
	alerts := ag.pending[key]
	delete(ag.pending, key)
	delete(ag.timer, key)
	ag.mu.Unlock()

	if len(alerts) == 0 {
		return
	}

	if len(alerts) == 1 {
		pa := alerts[0]
		chNames := ag.channels[pa.taskName]
		ag.notifier.Notify(pa.ctx, chNames)
		return
	}

	agg := AggregatedAlert{
		TaskName:  alerts[0].taskName,
		Source:    alerts[0].source,
		Timestamp: time.Now().Format(time.RFC3339),
		Alerts:    make([]AlertContext, len(alerts)),
	}
	for i, pa := range alerts {
		agg.Alerts[i] = pa.ctx
	}

	chNames := ag.channels[alerts[0].taskName]
	ag.notifier.NotifyAggregated(agg, chNames)
}

func (ag *Aggregator) Stop() {
	ag.mu.Lock()
	defer ag.mu.Unlock()

	for key, t := range ag.timer {
		t.Stop()
		ag.flush(key)
	}
}
